//! Persistent KV engine: append-only write-ahead log + periodic checkpoint,
//! with structurally shared in-memory state.
//!
//! Compared with `FjallEngine` (whole store rewritten per commit, snapshot =
//! full clone), a commit here appends one checksummed record and fsyncs it,
//! and a snapshot is an O(1) clone of a persistent B-tree (`imbl::OrdMap`).
//!
//! On disk:
//! - `wal.log`: records `[u32 len][u32 crc32][payload]`, payload =
//!   bincode `WalRecord { seq, mutations }`. Appended, fsynced before the
//!   commit is acknowledged (in `Durability::Sync`).
//! - `checkpoint.bin`: bincode `Checkpoint { seq, entries }` followed by a
//!   crc32 of those bytes. Written to a temp file, fsynced, renamed; the WAL
//!   is then restarted.
//!
//! Recovery loads the checkpoint, replays WAL records with a newer seq, and
//! stops at the first short or corrupt record (a torn tail from a crash),
//! truncating it away. Acknowledged commits were fully fsynced, so they are
//! never part of a torn tail.

use crate::engine::{
    CommitResult, CommitSeq, Condition, DbSnapshot, KeyRange, KvEngine, Mutation, WriteBatch,
};
use crate::error::{Error, Result};
use imbl::OrdMap;
use parking_lot::{Mutex, RwLock};
use sha2::{Digest, Sha256};
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::ops::Bound;
use std::path::{Path, PathBuf};
use std::sync::Arc;

pub const WAL_FILE: &str = "wal.log";
pub const CHECKPOINT_FILE: &str = "checkpoint.bin";
const CHECKPOINT_TMP: &str = "checkpoint.bin.tmp";

/// Keys and values are refcounted so copy-on-write node clones (needed while
/// a snapshot shares a node) bump counters instead of copying bytes.
type Bytes = Arc<[u8]>;
type Map = OrdMap<Bytes, Bytes>;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Durability {
    /// fsync the WAL before acknowledging each commit.
    Sync,
    /// Leave flushing to the OS; a crash can lose recent commits.
    NoSync,
}

#[derive(Debug, Clone)]
pub struct LogOptions {
    pub durability: Durability,
    /// Checkpoint once the WAL grows past this many bytes.
    pub checkpoint_bytes: u64,
}

impl Default for LogOptions {
    fn default() -> Self {
        LogOptions {
            durability: Durability::Sync,
            checkpoint_bytes: 64 << 20,
        }
    }
}

#[derive(serde::Serialize, serde::Deserialize)]
struct WalRecord {
    seq: CommitSeq,
    mutations: Vec<(Vec<u8>, Option<Vec<u8>>)>,
}

#[derive(serde::Serialize, serde::Deserialize)]
struct Checkpoint {
    seq: CommitSeq,
    entries: Vec<(Vec<u8>, Vec<u8>)>,
}

/// What recovery found, for tools that inspect a database without opening it.
#[derive(Debug)]
pub struct Recovered {
    pub seq: CommitSeq,
    pub entries: Vec<(Vec<u8>, Vec<u8>)>,
    pub checkpoint_seq: CommitSeq,
    pub wal_records: u64,
    /// Bytes after the last valid WAL record (0 after a clean shutdown).
    pub torn_tail_bytes: u64,
    /// Length of the valid WAL prefix.
    pub wal_valid_bytes: u64,
}

struct State {
    seq: CommitSeq,
    map: Map,
}

struct Writer {
    wal: File,
    wal_bytes: u64,
}

pub struct LogEngine {
    dir: PathBuf,
    opts: LogOptions,
    state: RwLock<State>,
    writer: Mutex<Writer>,
}

pub struct LogSnapshot {
    seq: CommitSeq,
    map: Map,
}

impl DbSnapshot for LogSnapshot {
    fn seq(&self) -> CommitSeq {
        self.seq
    }

    fn get(&self, key: &[u8]) -> Result<Option<Vec<u8>>> {
        Ok(self.map.get(key).map(|v| v.to_vec()))
    }

    fn scan(&self, range: KeyRange) -> Result<Vec<(Vec<u8>, Vec<u8>)>> {
        let upper = if range.inclusive_upper {
            Bound::Included(range.upper.as_slice())
        } else {
            Bound::Excluded(range.upper.as_slice())
        };
        Ok(self
            .map
            .range::<_, [u8]>((Bound::Included(range.lower.as_slice()), upper))
            .map(|(k, v)| (k.to_vec(), v.to_vec()))
            .collect())
    }
}

fn io(e: std::io::Error, what: &str) -> Error {
    Error::Internal(format!("{what}: {e}"))
}

fn sync_dir(dir: &Path) -> Result<()> {
    #[cfg(unix)]
    File::open(dir)
        .and_then(|d| d.sync_all())
        .map_err(|e| io(e, "fsync dir"))?;
    Ok(())
}

/// Read a database directory without changing it.
pub fn recover(dir: &Path) -> Result<Recovered> {
    let (map, mut r) = recover_map(dir)?;
    r.entries = map.iter().map(|(k, v)| (k.to_vec(), v.to_vec())).collect();
    Ok(r)
}

fn recover_map(dir: &Path) -> Result<(Map, Recovered)> {
    let mut map = Map::new();
    let mut seq = 0;
    let cp_path = dir.join(CHECKPOINT_FILE);
    if cp_path.exists() {
        let bytes = fs::read(&cp_path).map_err(|e| io(e, "read checkpoint"))?;
        if bytes.len() < 4 {
            return Err(Error::Codec("checkpoint.bin truncated".into()));
        }
        let (body, crc) = bytes.split_at(bytes.len() - 4);
        if crc32fast::hash(body) != u32::from_le_bytes(crc.try_into().unwrap()) {
            return Err(Error::Codec("checkpoint.bin checksum mismatch".into()));
        }
        let cp: Checkpoint =
            bincode::deserialize(body).map_err(|e| Error::Codec(format!("checkpoint: {e}")))?;
        seq = cp.seq;
        map = cp
            .entries
            .into_iter()
            .map(|(k, v)| (Bytes::from(k), Bytes::from(v)))
            .collect();
    }
    let checkpoint_seq = seq;

    let mut wal = Vec::new();
    if let Ok(mut f) = File::open(dir.join(WAL_FILE)) {
        f.read_to_end(&mut wal).map_err(|e| io(e, "read wal"))?;
    }
    let mut pos = 0usize;
    let mut records = 0;
    while pos + 8 <= wal.len() {
        let len = u32::from_le_bytes(wal[pos..pos + 4].try_into().unwrap()) as usize;
        let crc = u32::from_le_bytes(wal[pos + 4..pos + 8].try_into().unwrap());
        let Some(payload) = wal.get(pos + 8..pos + 8 + len) else {
            break;
        };
        if crc32fast::hash(payload) != crc {
            break;
        }
        let Ok(rec) = bincode::deserialize::<WalRecord>(payload) else {
            break;
        };
        if rec.seq > seq {
            if rec.seq != seq + 1 {
                return Err(Error::Codec(format!(
                    "wal.log jumps from seq {seq} to {}",
                    rec.seq
                )));
            }
            apply(&mut map, rec.mutations);
            seq = rec.seq;
            records += 1;
        }
        pos += 8 + len;
    }
    let r = Recovered {
        seq,
        entries: Vec::new(),
        checkpoint_seq,
        wal_records: records,
        torn_tail_bytes: (wal.len() - pos) as u64,
        wal_valid_bytes: pos as u64,
    };
    Ok((map, r))
}

fn write_checkpoint_file(
    dir: &Path,
    seq: CommitSeq,
    entries: Vec<(Vec<u8>, Vec<u8>)>,
) -> Result<()> {
    let cp = Checkpoint { seq, entries };
    let mut bytes =
        bincode::serialize(&cp).map_err(|e| Error::Internal(format!("checkpoint: {e}")))?;
    let crc = crc32fast::hash(&bytes);
    bytes.extend_from_slice(&crc.to_le_bytes());
    let tmp = dir.join(CHECKPOINT_TMP);
    {
        let mut f = File::create(&tmp).map_err(|e| io(e, "create checkpoint"))?;
        f.write_all(&bytes).map_err(|e| io(e, "write checkpoint"))?;
        f.sync_all().map_err(|e| io(e, "fsync checkpoint"))?;
    }
    fs::rename(&tmp, dir.join(CHECKPOINT_FILE)).map_err(|e| io(e, "rename checkpoint"))?;
    sync_dir(dir)
}

/// Create a database holding exactly `entries` at `seq` (used by restore).
/// Any existing WAL is removed after the checkpoint is durable.
pub fn write_database(dir: &Path, seq: CommitSeq, entries: Vec<(Vec<u8>, Vec<u8>)>) -> Result<()> {
    fs::create_dir_all(dir).map_err(|e| io(e, "create db dir"))?;
    write_checkpoint_file(dir, seq, entries)?;
    match fs::remove_file(dir.join(WAL_FILE)) {
        Ok(()) => sync_dir(dir),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(io(e, "remove wal")),
    }
}

fn apply(map: &mut Map, mutations: Vec<(Vec<u8>, Option<Vec<u8>>)>) {
    for (k, v) in mutations {
        match v {
            Some(v) => {
                map.insert(Bytes::from(k), Bytes::from(v));
            }
            None => {
                map.remove(k.as_slice());
            }
        }
    }
}

impl LogEngine {
    pub fn open<P: AsRef<Path>>(dir: P) -> Result<Self> {
        Self::open_with(dir, LogOptions::default())
    }

    pub fn open_with<P: AsRef<Path>>(dir: P, opts: LogOptions) -> Result<Self> {
        let dir = dir.as_ref().to_path_buf();
        fs::create_dir_all(&dir).map_err(|e| io(e, "create db dir"))?;
        let _ = fs::remove_file(dir.join(CHECKPOINT_TMP));
        let (map, rec) = recover_map(&dir)?;
        let wal_path = dir.join(WAL_FILE);
        let wal = OpenOptions::new()
            .create(true)
            .read(true)
            .write(true)
            .truncate(false)
            .open(&wal_path)
            .map_err(|e| io(e, "open wal"))?;
        if rec.torn_tail_bytes > 0 {
            // Drop the torn tail so new records follow the last valid one.
            wal.set_len(rec.wal_valid_bytes)
                .map_err(|e| io(e, "truncate wal"))?;
            wal.sync_all().map_err(|e| io(e, "fsync wal"))?;
        }
        drop(wal);
        let wal = OpenOptions::new()
            .append(true)
            .open(&wal_path)
            .map_err(|e| io(e, "open wal"))?;
        sync_dir(&dir)?;
        Ok(LogEngine {
            dir,
            opts,
            state: RwLock::new(State { seq: rec.seq, map }),
            writer: Mutex::new(Writer {
                wal,
                wal_bytes: rec.wal_valid_bytes,
            }),
        })
    }

    fn value_hash(value: &[u8]) -> String {
        format!("{:x}", Sha256::digest(value))
    }

    fn check(map: &Map, conditions: &[Condition]) -> Option<usize> {
        for (idx, c) in conditions.iter().enumerate() {
            let ok = match c {
                Condition::KeyAbsent(k) => !map.contains_key(k.as_slice()),
                Condition::ValueEquals(k, h) => map
                    .get(k.as_slice())
                    .is_some_and(|v| Self::value_hash(v) == *h),
                Condition::RecordRevEquals(k, rev) => map
                    .get(k.as_slice())
                    .and_then(|v| serde_json::from_slice::<serde_json::Value>(v).ok())
                    .and_then(|v| v.get("version").and_then(|x| x.as_u64()))
                    .is_some_and(|cur| cur == *rev),
            };
            if !ok {
                return Some(idx);
            }
        }
        None
    }

    /// Write the whole state to `checkpoint.bin` and restart the WAL.
    /// Called with the writer lock held, so no commit is in flight.
    fn checkpoint(&self, w: &mut Writer) -> Result<()> {
        let (seq, map) = {
            let s = self.state.read();
            (s.seq, s.map.clone())
        };
        write_checkpoint_file(
            &self.dir,
            seq,
            map.iter().map(|(k, v)| (k.to_vec(), v.to_vec())).collect(),
        )?;
        // The checkpoint now covers every WAL record; records at or below its
        // seq are skipped on replay, so truncating is safe even if we crash here.
        w.wal.set_len(0).map_err(|e| io(e, "truncate wal"))?;
        w.wal.sync_all().map_err(|e| io(e, "fsync wal"))?;
        w.wal_bytes = 0;
        Ok(())
    }

    /// Force a checkpoint now (used by tools and tests).
    pub fn checkpoint_now(&self) -> Result<()> {
        let mut w = self.writer.lock();
        self.checkpoint(&mut w)
    }
}

impl KvEngine for LogEngine {
    fn snapshot(&self) -> Result<Arc<dyn DbSnapshot>> {
        let s = self.state.read();
        Ok(Arc::new(LogSnapshot {
            seq: s.seq,
            map: s.map.clone(),
        }))
    }

    fn commit(&self, batch: WriteBatch) -> Result<CommitResult> {
        if batch.is_empty() {
            return Ok(CommitResult::NoChanges);
        }
        // One writer at a time; readers keep using the published state.
        let mut w = self.writer.lock();
        // Only this writer changes the state, so reading it here and applying
        // below cannot race with another commit.
        let seq = {
            let s = self.state.read();
            if let Some(i) = Self::check(&s.map, &batch.conditions) {
                return Ok(CommitResult::Conflict { condition_index: i });
            }
            s.seq
        };
        let new_seq = seq + 1;
        let rec = WalRecord {
            seq: new_seq,
            mutations: batch
                .mutations
                .into_iter()
                .map(|m| match m {
                    Mutation::Put(k, v) => (k, Some(v)),
                    Mutation::Delete(k) => (k, None),
                })
                .collect(),
        };
        let payload =
            bincode::serialize(&rec).map_err(|e| Error::Internal(format!("wal encode: {e}")))?;
        let mut frame = Vec::with_capacity(payload.len() + 8);
        frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
        frame.extend_from_slice(&crc32fast::hash(&payload).to_le_bytes());
        frame.extend_from_slice(&payload);
        w.wal.write_all(&frame).map_err(|e| io(e, "append wal"))?;
        if self.opts.durability == Durability::Sync {
            w.wal.sync_data().map_err(|e| io(e, "fsync wal"))?;
        }
        w.wal_bytes += frame.len() as u64;

        {
            // In place: nodes still shared with open snapshots are copied by
            // imbl, the rest are mutated directly.
            let mut s = self.state.write();
            apply(&mut s.map, rec.mutations);
            s.seq = new_seq;
        }
        if w.wal_bytes > self.opts.checkpoint_bytes {
            self.checkpoint(&mut w)?;
        }
        Ok(CommitResult::Applied { seq: new_seq })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn put(e: &LogEngine, k: &str, v: &str) -> CommitSeq {
        let mut b = WriteBatch::new();
        b.put(k.as_bytes().to_vec(), v.as_bytes().to_vec());
        match e.commit(b).unwrap() {
            CommitResult::Applied { seq } => seq,
            other => panic!("{other:?}"),
        }
    }

    #[test]
    fn persists_across_reopen_and_checkpoint() {
        let dir = TempDir::new().unwrap();
        {
            let e = LogEngine::open(dir.path()).unwrap();
            for i in 0..10 {
                put(&e, &format!("k{i}"), &format!("v{i}"));
            }
            e.checkpoint_now().unwrap();
            put(&e, "after", "cp");
        }
        let e = LogEngine::open(dir.path()).unwrap();
        let s = e.snapshot().unwrap();
        assert_eq!(s.seq(), 11);
        assert_eq!(s.get(b"k3").unwrap(), Some(b"v3".to_vec()));
        assert_eq!(s.get(b"after").unwrap(), Some(b"cp".to_vec()));
    }

    #[test]
    fn snapshot_is_isolated_from_later_commits() {
        let dir = TempDir::new().unwrap();
        let e = LogEngine::open(dir.path()).unwrap();
        put(&e, "a", "1");
        let s = e.snapshot().unwrap();
        put(&e, "a", "2");
        put(&e, "b", "x");
        assert_eq!(s.get(b"a").unwrap(), Some(b"1".to_vec()));
        assert_eq!(s.scan(KeyRange::prefix(vec![])).unwrap().len(), 1);
        assert_eq!(e.snapshot().unwrap().seq(), 3);
    }

    #[test]
    fn torn_tail_at_every_offset_recovers_a_commit_prefix() {
        let dir = TempDir::new().unwrap();
        {
            let e = LogEngine::open(dir.path()).unwrap();
            for i in 0..5 {
                put(&e, &format!("k{i}"), &"x".repeat(i * 7));
            }
        }
        let full = fs::read(dir.path().join(WAL_FILE)).unwrap();
        for cut in 0..full.len() {
            let d2 = TempDir::new().unwrap();
            fs::write(d2.path().join(WAL_FILE), &full[..cut]).unwrap();
            let e = LogEngine::open(d2.path()).unwrap();
            let s = e.snapshot().unwrap();
            let n = s.seq() as usize;
            for i in 0..5 {
                assert_eq!(
                    s.get(format!("k{i}").as_bytes()).unwrap().is_some(),
                    i < n,
                    "cut {cut}"
                );
            }
            // And it keeps working after truncating the torn tail.
            assert_eq!(put(&e, "next", "v"), n as u64 + 1);
            drop(e);
            assert_eq!(
                LogEngine::open(d2.path())
                    .unwrap()
                    .snapshot()
                    .unwrap()
                    .seq(),
                n as u64 + 1
            );
        }
    }

    #[test]
    fn corrupt_record_is_treated_as_end_of_log() {
        let dir = TempDir::new().unwrap();
        {
            let e = LogEngine::open(dir.path()).unwrap();
            put(&e, "a", "1");
            put(&e, "b", "2");
        }
        let p = dir.path().join(WAL_FILE);
        let mut bytes = fs::read(&p).unwrap();
        let last = bytes.len() - 1;
        bytes[last] ^= 0xff;
        fs::write(&p, bytes).unwrap();
        let r = recover(dir.path()).unwrap();
        assert_eq!(r.seq, 1);
        assert!(r.torn_tail_bytes > 0);
    }

    #[test]
    fn cas_conditions_and_empty_batches() {
        let dir = TempDir::new().unwrap();
        let e = LogEngine::open(dir.path()).unwrap();
        assert!(matches!(
            e.commit(WriteBatch::new()).unwrap(),
            CommitResult::NoChanges
        ));
        put(&e, "k", "v1");
        let mut b = WriteBatch::new();
        b.expect_absent(b"k".to_vec());
        b.put(b"k".to_vec(), b"v2".to_vec());
        assert!(matches!(
            e.commit(b).unwrap(),
            CommitResult::Conflict { condition_index: 0 }
        ));
        let mut b = WriteBatch::new();
        b.expect_value(b"k".to_vec(), LogEngine::value_hash(b"v1"));
        b.put(b"k".to_vec(), b"v2".to_vec());
        assert!(matches!(
            e.commit(b).unwrap(),
            CommitResult::Applied { seq: 2 }
        ));
    }

    #[test]
    fn automatic_checkpoint_keeps_wal_small() {
        let dir = TempDir::new().unwrap();
        let e = LogEngine::open_with(
            dir.path(),
            LogOptions {
                durability: Durability::NoSync,
                checkpoint_bytes: 4096,
            },
        )
        .unwrap();
        for i in 0..500 {
            put(&e, &format!("k{i:04}"), &"v".repeat(50));
        }
        assert!(fs::metadata(dir.path().join(WAL_FILE)).unwrap().len() <= 4096 + 200);
        drop(e);
        let r = recover(dir.path()).unwrap();
        assert_eq!(r.seq, 500);
        assert_eq!(r.entries.len(), 500);
        assert!(r.checkpoint_seq > 0);
    }
}
