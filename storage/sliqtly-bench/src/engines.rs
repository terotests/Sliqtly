//! One adapter per engine. Every adapter commits a `Batch` atomically with
//! its conditions, and `sync` means the commit is on disk (fdatasync or the
//! engine's equivalent) before `commit` returns. Reads go through the
//! engine's normal read path (a read transaction or snapshot where the engine
//! has them).

use redb::{ReadableDatabase, ReadableTable};
use rusqlite::OptionalExtension;
use sliqtly_kernel::kernel::{Batch, Kernel, KvPair};
use std::path::Path;
use std::sync::atomic::{AtomicBool, AtomicI8, Ordering};
use std::sync::{Arc, Condvar, Mutex, RwLock};

pub trait Engine: Send + Sync {
    fn name(&self) -> &'static str;
    /// Applies `b` if its conditions hold; false on a failed condition.
    fn commit(&self, b: &Batch, sync: bool) -> bool;
    /// The value's length, or None.
    fn get(&self, key: &str) -> Option<usize>;
    fn get_value(&self, key: &str) -> Option<String>;
    /// Entries with `prefix` and key >= `start`, at most `limit`; returns
    /// (count, value bytes) after reading each value.
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize);
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String>;
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair>;
    /// `keys` read from one consistent snapshot; total value bytes.
    fn snapshot_get(&self, keys: &[String]) -> usize;
    /// Makes everything committed so far durable.
    fn flush(&self);
}

/// Every engine gets the same cache budget (LMDB and the Sliqtly kernel read
/// through the OS page cache and have no cache of their own).
pub const CACHE_BYTES: usize = 256 << 20;

pub const ENGINES: [&str; 6] = ["sliqtly", "redb", "fjall", "lmdb", "rocksdb", "sqlite"];

pub fn open(name: &str, dir: &Path) -> Box<dyn Engine> {
    std::fs::create_dir_all(dir).unwrap();
    match name {
        "sliqtly" => Box::new(SliqtlyEngine::open(dir)),
        "redb" => Box::new(RedbEngine::open(dir)),
        "fjall" => Box::new(FjallEngine::open(dir)),
        "lmdb" => Box::new(LmdbEngine::open(dir)),
        "rocksdb" => Box::new(RocksEngine::open(dir)),
        "sqlite" => Box::new(SqliteKvEngine::open(dir)),
        _ => panic!("unknown engine {name}"),
    }
}

fn ends_scan(key: &[u8], prefix: &str) -> bool {
    !key.starts_with(prefix.as_bytes())
}

fn from(prefix: &str, start: &str) -> String {
    if prefix > start { prefix.to_string() } else { start.to_string() }
}

// ---------------------------------------------------------------- sliqtly

/// The Ranger-dialect kernel. One writer at a time (`writer`); the frame is
/// written and synced while readers keep the shared lock, and only the index
/// update takes the exclusive lock. Compaction runs the kernel's phased
/// compaction on a background thread: copying and syncing under the shared
/// lock, and only `compact_finish` exclusively (with the writer lock, so no
/// commit is between its write and its apply).
pub struct SliqtlyEngine {
    inner: Arc<SliqInner>,
    bg: Option<std::thread::JoinHandle<()>>,
}

struct SliqInner {
    k: RwLock<Kernel>,
    writer: Mutex<()>,
    wake: Mutex<bool>,
    cv: Condvar,
    compacting: AtomicBool,
    stop: AtomicBool,
}

impl SliqtlyEngine {
    pub fn open(dir: &Path) -> Self {
        let mut k = Kernel::open(dir.to_str().unwrap(), false);
        k.auto_compact = false;
        let inner = Arc::new(SliqInner {
            k: RwLock::new(k),
            writer: Mutex::new(()),
            wake: Mutex::new(false),
            cv: Condvar::new(),
            compacting: AtomicBool::new(false),
            stop: AtomicBool::new(false),
        });
        let i2 = inner.clone();
        let bg = std::thread::spawn(move || compactor(i2));
        SliqtlyEngine { inner, bg: Some(bg) }
    }
}

fn compactor(i: Arc<SliqInner>) {
    loop {
        {
            let mut w = i.wake.lock().unwrap();
            while !*w && !i.stop.load(Ordering::Relaxed) {
                w = i.cv.wait(w).unwrap();
            }
            *w = false;
        }
        if i.stop.load(Ordering::Relaxed) {
            return;
        }
        let t0 = std::time::Instant::now();
        let mut c = i.k.read().unwrap().compact_begin();
        // Sync the new log every 16 MB without holding any lock, so a
        // commit's fdatasync never waits behind a large amount of
        // unsynced compaction data (ext4 ordered mode).
        let mut synced = 0;
        loop {
            let done = i.k.read().unwrap().compact_step(&mut c, 2048);
            if c.written() - synced > 16 << 20 {
                c.sync();
                synced = c.written();
            }
            if done {
                break;
            }
        }
        i.k.read().unwrap().compact_catch_up(&mut c);
        c.sync();
        let t1 = t0.elapsed();
        {
            let _w = i.writer.lock().unwrap();
            let tf = std::time::Instant::now();
            i.k.write().unwrap().compact_finish(&mut c);
            if std::env::var("SLIQ_DEBUG").is_ok() {
                eprintln!("compaction: background {:?}, exclusive finish {:?}", t1, tf.elapsed());
            }
        }
        i.compacting.store(false, Ordering::Relaxed);
    }
}

impl Drop for SliqtlyEngine {
    fn drop(&mut self) {
        self.inner.stop.store(true, Ordering::Relaxed);
        {
            let _w = self.inner.wake.lock().unwrap();
            self.inner.cv.notify_all();
        }
        if let Some(h) = self.bg.take() {
            h.join().unwrap();
        }
        self.inner.k.write().unwrap().close();
    }
}

impl Engine for SliqtlyEngine {
    fn name(&self) -> &'static str {
        "sliqtly"
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        let i = &self.inner;
        let _w = i.writer.lock().unwrap();
        let p = {
            let k = i.k.read().unwrap();
            let p = k.prepare(b);
            if p.status != 1 {
                return p.status == 0;
            }
            k.write_prepared(&p);
            if sync {
                k.sync();
            }
            p
        };
        let mut k = i.k.write().unwrap();
        k.apply_prepared(&p, b);
        if k.needs_compaction() && !i.compacting.swap(true, Ordering::Relaxed) {
            *i.wake.lock().unwrap() = true;
            i.cv.notify_one();
        }
        true
    }
    fn get(&self, key: &str) -> Option<usize> {
        self.inner.k.read().unwrap().get(key).map(|v| v.len())
    }
    fn get_value(&self, key: &str) -> Option<String> {
        self.inner.k.read().unwrap().get(key)
    }
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        let rows = self.inner.k.read().unwrap().scan(prefix, start, limit as i64);
        (rows.len(), rows.iter().map(|r| r.value.len()).sum())
    }
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        self.inner.k.read().unwrap().scan_keys(prefix, start, limit as i64)
    }
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        self.inner.k.read().unwrap().scan(prefix, start, limit as i64)
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        let k = self.inner.k.read().unwrap();
        keys.iter().map(|key| k.get(key).map(|v| v.len()).unwrap_or(0)).sum()
    }
    fn flush(&self) {
        self.inner.k.read().unwrap().sync();
    }
}

// ---------------------------------------------------------------- redb

const REDB_TABLE: redb::TableDefinition<&str, &str> = redb::TableDefinition::new("kv");

pub struct RedbEngine {
    db: redb::Database,
}

impl RedbEngine {
    pub fn open(dir: &Path) -> Self {
        let db = redb::Builder::new().set_cache_size(CACHE_BYTES).create(dir.join("data.redb")).unwrap();
        let w = db.begin_write().unwrap();
        w.open_table(REDB_TABLE).unwrap();
        w.commit().unwrap();
        RedbEngine { db }
    }
}

impl Engine for RedbEngine {
    fn name(&self) -> &'static str {
        "redb"
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        let mut w = self.db.begin_write().unwrap();
        w.set_durability(if sync { redb::Durability::Immediate } else { redb::Durability::None }).unwrap();
        {
            let mut t = w.open_table(REDB_TABLE).unwrap();
            for c in &b.conds {
                let cur = t.get(c.key.as_str()).unwrap().map(|v| v.value().to_string());
                let ok = if c.kind == 1 { cur.is_none() } else { cur.as_deref() == Some(c.value.as_str()) };
                if !ok {
                    drop(t);
                    w.abort().unwrap();
                    return false;
                }
            }
            for op in &b.ops {
                if op.kind == 1 {
                    t.insert(op.key.as_str(), op.value.as_str()).unwrap();
                } else {
                    t.remove(op.key.as_str()).unwrap();
                }
            }
        }
        w.commit().unwrap();
        true
    }
    fn get(&self, key: &str) -> Option<usize> {
        let r = self.db.begin_read().unwrap();
        let t = r.open_table(REDB_TABLE).unwrap();
        t.get(key).unwrap().map(|v| v.value().to_string().len())
    }
    fn get_value(&self, key: &str) -> Option<String> {
        let r = self.db.begin_read().unwrap();
        let t = r.open_table(REDB_TABLE).unwrap();
        t.get(key).unwrap().map(|v| v.value().to_string())
    }
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        let r = self.db.begin_read().unwrap();
        let t = r.open_table(REDB_TABLE).unwrap();
        let s = from(prefix, start);
        let (mut n, mut bytes) = (0, 0);
        for item in t.range(s.as_str()..).unwrap() {
            if n >= limit {
                break;
            }
            let (k, v) = item.unwrap();
            if ends_scan(k.value().as_bytes(), prefix) {
                break;
            }
            n += 1;
            bytes += v.value().to_string().len();
        }
        (n, bytes)
    }
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        let r = self.db.begin_read().unwrap();
        let t = r.open_table(REDB_TABLE).unwrap();
        let s = from(prefix, start);
        let mut out = Vec::new();
        for item in t.range(s.as_str()..).unwrap() {
            if out.len() >= limit {
                break;
            }
            let (k, _) = item.unwrap();
            if ends_scan(k.value().as_bytes(), prefix) {
                break;
            }
            out.push(k.value().to_string());
        }
        out
    }
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        let r = self.db.begin_read().unwrap();
        let t = r.open_table(REDB_TABLE).unwrap();
        let s = from(prefix, start);
        let mut out = Vec::new();
        for item in t.range(s.as_str()..).unwrap() {
            if out.len() >= limit {
                break;
            }
            let (k, v) = item.unwrap();
            if ends_scan(k.value().as_bytes(), prefix) {
                break;
            }
            out.push(KvPair { key: k.value().to_string(), value: v.value().to_string() });
        }
        out
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        let r = self.db.begin_read().unwrap();
        let t = r.open_table(REDB_TABLE).unwrap();
        keys.iter().map(|k| t.get(k.as_str()).unwrap().map(|v| v.value().to_string().len()).unwrap_or(0)).sum()
    }
    fn flush(&self) {
        // An empty commit with Immediate durability persists the earlier
        // non-durable commits.
        let mut w = self.db.begin_write().unwrap();
        w.set_durability(redb::Durability::Immediate).unwrap();
        w.commit().unwrap();
    }
}

// ---------------------------------------------------------------- fjall

pub struct FjallEngine {
    db: fjall::Database,
    ks: fjall::Keyspace,
    writer: Mutex<()>,
}

impl FjallEngine {
    pub fn open(dir: &Path) -> Self {
        let db = fjall::Database::builder(dir).cache_size(CACHE_BYTES as u64).open().unwrap();
        let ks = db.keyspace("kv", fjall::KeyspaceCreateOptions::default).unwrap();
        FjallEngine { db, ks, writer: Mutex::new(()) }
    }
}

impl Engine for FjallEngine {
    fn name(&self) -> &'static str {
        "fjall"
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        // Conditions are checked under the single-writer lock, so nothing
        // can change between the check and the batch.
        let _w = self.writer.lock().unwrap();
        for c in &b.conds {
            let cur = self.ks.get(c.key.as_bytes()).unwrap();
            let ok = if c.kind == 1 { cur.is_none() } else { cur.as_deref() == Some(c.value.as_bytes()) };
            if !ok {
                return false;
            }
        }
        let mut batch = self.db.batch();
        if sync {
            batch = batch.durability(Some(fjall::PersistMode::SyncData));
        }
        for op in &b.ops {
            if op.kind == 1 {
                batch.insert(&self.ks, op.key.as_bytes(), op.value.as_bytes());
            } else {
                batch.remove(&self.ks, op.key.as_bytes());
            }
        }
        batch.commit().unwrap();
        true
    }
    fn get(&self, key: &str) -> Option<usize> {
        self.ks.get(key.as_bytes()).unwrap().map(|v| v.to_vec().len())
    }
    fn get_value(&self, key: &str) -> Option<String> {
        self.ks.get(key.as_bytes()).unwrap().map(|v| String::from_utf8(v.to_vec()).unwrap())
    }
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        let s = from(prefix, start);
        let (mut n, mut bytes) = (0, 0);
        for g in self.ks.range(s.as_bytes()..) {
            if n >= limit {
                break;
            }
            let (k, v) = g.into_inner().unwrap();
            if ends_scan(&k, prefix) {
                break;
            }
            n += 1;
            bytes += v.to_vec().len();
        }
        (n, bytes)
    }
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        let s = from(prefix, start);
        let mut out = Vec::new();
        for g in self.ks.range(s.as_bytes()..) {
            if out.len() >= limit {
                break;
            }
            let k = g.key().unwrap();
            if ends_scan(&k, prefix) {
                break;
            }
            out.push(String::from_utf8(k.to_vec()).unwrap());
        }
        out
    }
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        let s = from(prefix, start);
        let mut out = Vec::new();
        for g in self.ks.range(s.as_bytes()..) {
            if out.len() >= limit {
                break;
            }
            let (k, v) = g.into_inner().unwrap();
            if ends_scan(&k, prefix) {
                break;
            }
            out.push(KvPair { key: String::from_utf8(k.to_vec()).unwrap(), value: String::from_utf8(v.to_vec()).unwrap() });
        }
        out
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        use fjall::Readable;
        let snap = self.db.snapshot();
        keys.iter().map(|k| snap.get(&self.ks, k.as_bytes()).unwrap().map(|v| v.to_vec().len()).unwrap_or(0)).sum()
    }
    fn flush(&self) {
        self.db.persist(fjall::PersistMode::SyncData).unwrap();
    }
}

// ---------------------------------------------------------------- lmdb

/// LMDB through heed in its default (synced) mode: a durable commit syncs the
/// data pages, then the meta page. A non-durable commit runs with NO_SYNC set
/// for that commit only (set and cleared under the writer lock).
pub struct LmdbEngine {
    env: heed::Env,
    db: heed::Database<heed::types::Str, heed::types::Str>,
    writer: Mutex<()>,
}

impl LmdbEngine {
    pub fn open(dir: &Path) -> Self {
        let env = unsafe {
            let mut o = heed::EnvOpenOptions::new();
            o.map_size(64 * 1024 * 1024 * 1024).max_dbs(4);
            o.open(dir).unwrap()
        };
        let mut w = env.write_txn().unwrap();
        let db = env.create_database(&mut w, Some("kv")).unwrap();
        w.commit().unwrap();
        LmdbEngine { env, db, writer: Mutex::new(()) }
    }
}

impl Engine for LmdbEngine {
    fn name(&self) -> &'static str {
        "lmdb"
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        let _g = self.writer.lock().unwrap();
        if !sync {
            // safety: set_flags runs only under the writer lock
            unsafe { self.env.set_flags(heed::EnvFlags::NO_SYNC, heed::FlagSetMode::Enable).unwrap() };
        }
        let r = self.commit_inner(b);
        if !sync {
            unsafe { self.env.set_flags(heed::EnvFlags::NO_SYNC, heed::FlagSetMode::Disable).unwrap() };
        }
        r
    }
    fn get(&self, key: &str) -> Option<usize> {
        self.get_value(key).map(|v| v.len())
    }
    fn get_value(&self, key: &str) -> Option<String> {
        let r = self.env.read_txn().unwrap();
        self.db.get(&r, key).unwrap().map(|v| v.to_string())
    }
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        self.scan_impl(prefix, start, limit)
    }
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        self.scan_keys_impl(prefix, start, limit)
    }
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        self.scan_pairs_impl(prefix, start, limit)
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        let r = self.env.read_txn().unwrap();
        keys.iter().map(|k| self.db.get(&r, k.as_str()).unwrap().map(|v| v.to_string().len()).unwrap_or(0)).sum()
    }
    fn flush(&self) {
        self.env.force_sync().unwrap();
    }
}

impl LmdbEngine {
    fn commit_inner(&self, b: &Batch) -> bool {
        let mut w = self.env.write_txn().unwrap();
        for c in &b.conds {
            let cur = self.db.get(&w, c.key.as_str()).unwrap();
            let ok = if c.kind == 1 { cur.is_none() } else { cur == Some(c.value.as_str()) };
            if !ok {
                w.abort();
                return false;
            }
        }
        for op in &b.ops {
            if op.kind == 1 {
                self.db.put(&mut w, op.key.as_str(), op.value.as_str()).unwrap();
            } else {
                self.db.delete(&mut w, op.key.as_str()).unwrap();
            }
        }
        w.commit().unwrap();
        true
    }
    fn scan_impl(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        let r = self.env.read_txn().unwrap();
        let s = from(prefix, start);
        let range = (std::ops::Bound::Included(s.as_str()), std::ops::Bound::Unbounded);
        let (mut n, mut bytes) = (0, 0);
        for item in self.db.range(&r, &range).unwrap() {
            if n >= limit {
                break;
            }
            let (k, v) = item.unwrap();
            if ends_scan(k.as_bytes(), prefix) {
                break;
            }
            n += 1;
            bytes += v.to_string().len();
        }
        (n, bytes)
    }
    fn scan_keys_impl(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        let r = self.env.read_txn().unwrap();
        let s = from(prefix, start);
        let range = (std::ops::Bound::Included(s.as_str()), std::ops::Bound::Unbounded);
        let mut out = Vec::new();
        for item in self.db.range(&r, &range).unwrap() {
            if out.len() >= limit {
                break;
            }
            let (k, _) = item.unwrap();
            if ends_scan(k.as_bytes(), prefix) {
                break;
            }
            out.push(k.to_string());
        }
        out
    }
    fn scan_pairs_impl(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        let r = self.env.read_txn().unwrap();
        let s = from(prefix, start);
        let range = (std::ops::Bound::Included(s.as_str()), std::ops::Bound::Unbounded);
        let mut out = Vec::new();
        for item in self.db.range(&r, &range).unwrap() {
            if out.len() >= limit {
                break;
            }
            let (k, v) = item.unwrap();
            if ends_scan(k.as_bytes(), prefix) {
                break;
            }
            out.push(KvPair { key: k.to_string(), value: v.to_string() });
        }
        out
    }
}

// ---------------------------------------------------------------- rocksdb

pub struct RocksEngine {
    db: rocksdb::DB,
    writer: Mutex<()>,
}

impl RocksEngine {
    pub fn open(dir: &Path) -> Self {
        let mut o = rocksdb::Options::default();
        o.create_if_missing(true);
        o.increase_parallelism(4);
        let mut t = rocksdb::BlockBasedOptions::default();
        t.set_block_cache(&rocksdb::Cache::new_lru_cache(CACHE_BYTES));
        t.set_bloom_filter(10.0, false);
        o.set_block_based_table_factory(&t);
        let db = rocksdb::DB::open(&o, dir).unwrap();
        RocksEngine { db, writer: Mutex::new(()) }
    }
}

impl Engine for RocksEngine {
    fn name(&self) -> &'static str {
        "rocksdb"
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        let _g = self.writer.lock().unwrap();
        for c in &b.conds {
            let cur = self.db.get_pinned(c.key.as_bytes()).unwrap();
            let ok = if c.kind == 1 { cur.is_none() } else { cur.as_deref() == Some(c.value.as_bytes()) };
            if !ok {
                return false;
            }
        }
        let mut wb = rocksdb::WriteBatch::default();
        for op in &b.ops {
            if op.kind == 1 {
                wb.put(op.key.as_bytes(), op.value.as_bytes());
            } else {
                wb.delete(op.key.as_bytes());
            }
        }
        let mut wo = rocksdb::WriteOptions::default();
        wo.set_sync(sync);
        self.db.write_opt(wb, &wo).unwrap();
        true
    }
    fn get(&self, key: &str) -> Option<usize> {
        self.db.get_pinned(key.as_bytes()).unwrap().map(|v| v.to_vec().len())
    }
    fn get_value(&self, key: &str) -> Option<String> {
        self.db.get_pinned(key.as_bytes()).unwrap().map(|v| String::from_utf8(v.to_vec()).unwrap())
    }
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        let s = from(prefix, start);
        let it = self.db.iterator(rocksdb::IteratorMode::From(s.as_bytes(), rocksdb::Direction::Forward));
        let (mut n, mut bytes) = (0, 0);
        for item in it {
            if n >= limit {
                break;
            }
            let (k, v) = item.unwrap();
            if ends_scan(&k, prefix) {
                break;
            }
            n += 1;
            bytes += v.len();
        }
        (n, bytes)
    }
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        let s = from(prefix, start);
        let mut it = self.db.raw_iterator();
        it.seek(s.as_bytes());
        let mut out = Vec::new();
        while it.valid() && out.len() < limit {
            let k = it.key().unwrap();
            if ends_scan(k, prefix) {
                break;
            }
            out.push(String::from_utf8(k.to_vec()).unwrap());
            it.next();
        }
        out
    }
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        let s = from(prefix, start);
        let mut it = self.db.raw_iterator();
        it.seek(s.as_bytes());
        let mut out = Vec::new();
        while it.valid() && out.len() < limit {
            let k = it.key().unwrap();
            if ends_scan(k, prefix) {
                break;
            }
            out.push(KvPair { key: String::from_utf8(k.to_vec()).unwrap(), value: String::from_utf8(it.value().unwrap().to_vec()).unwrap() });
            it.next();
        }
        out
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        let snap = self.db.snapshot();
        keys.iter().map(|k| snap.get(k.as_bytes()).unwrap().map(|v| v.to_vec().len()).unwrap_or(0)).sum()
    }
    fn flush(&self) {
        self.db.flush_wal(true).unwrap();
    }
}

// ---------------------------------------------------------------- sqlite as KV

/// SQLite holding the same key/value pairs, for the raw suite. The semantic
/// suite uses real tables instead (`sqlite_semantic.rs`). WAL mode;
/// `synchronous` is FULL for a durable commit (the WAL is synced at commit)
/// and OFF otherwise.
pub struct SqliteKvEngine {
    path: std::path::PathBuf,
    writer: Mutex<rusqlite::Connection>,
    readers: Mutex<Vec<rusqlite::Connection>>,
    sync_mode: AtomicI8,
}

pub fn sqlite_connect(path: &Path) -> rusqlite::Connection {
    let c = rusqlite::Connection::open(path).unwrap();
    c.pragma_update(None, "journal_mode", "WAL").unwrap();
    c.pragma_update(None, "synchronous", "FULL").unwrap();
    c.pragma_update(None, "cache_size", -((CACHE_BYTES / 1024) as i64)).unwrap();
    c.pragma_update(None, "mmap_size", 4i64 << 30).unwrap();
    c.set_prepared_statement_cache_capacity(64);
    c
}

impl SqliteKvEngine {
    pub fn open(dir: &Path) -> Self {
        let path = dir.join("data.sqlite");
        let c = sqlite_connect(&path);
        // WITHOUT ROWID suits rows under ~1/20 of a page; larger values go in
        // a rowid table with the key in its own index (SQLite's guidance)
        let large = std::env::var("SLIQ_LARGE_VALUES").is_ok();
        let ddl = if large {
            "CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL)"
        } else {
            "CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL) WITHOUT ROWID"
        };
        c.execute_batch(ddl).unwrap();
        SqliteKvEngine { path, writer: Mutex::new(c), readers: Mutex::new(Vec::new()), sync_mode: AtomicI8::new(1) }
    }
    fn with_reader<T>(&self, f: impl FnOnce(&rusqlite::Connection) -> T) -> T {
        let c = self.readers.lock().unwrap().pop().unwrap_or_else(|| sqlite_connect(&self.path));
        let out = f(&c);
        self.readers.lock().unwrap().push(c);
        out
    }
}

/// Sets `synchronous` when it differs from `current` (1 FULL, 0 OFF).
pub fn sqlite_set_sync(c: &rusqlite::Connection, current: &AtomicI8, sync: bool) {
    let want = if sync { 1 } else { 0 };
    if current.swap(want, Ordering::Relaxed) != want {
        c.pragma_update(None, "synchronous", if sync { "FULL" } else { "OFF" }).unwrap();
    }
}

impl Engine for SqliteKvEngine {
    fn name(&self) -> &'static str {
        "sqlite"
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        let mut c = self.writer.lock().unwrap();
        sqlite_set_sync(&c, &self.sync_mode, sync);
        let tx = c.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate).unwrap();
        for cond in &b.conds {
            let cur: Option<String> = tx
                .prepare_cached("SELECT v FROM kv WHERE k = ?1")
                .unwrap()
                .query_row([&cond.key], |r| r.get(0))
                .optional()
                .unwrap();
            let ok = if cond.kind == 1 { cur.is_none() } else { cur.as_deref() == Some(cond.value.as_str()) };
            if !ok {
                return false;
            }
        }
        {
            let mut put = tx.prepare_cached("INSERT OR REPLACE INTO kv (k, v) VALUES (?1, ?2)").unwrap();
            let mut del = tx.prepare_cached("DELETE FROM kv WHERE k = ?1").unwrap();
            for op in &b.ops {
                if op.kind == 1 {
                    put.execute([&op.key, &op.value]).unwrap();
                } else {
                    del.execute([&op.key]).unwrap();
                }
            }
        }
        tx.commit().unwrap();
        true
    }
    fn get(&self, key: &str) -> Option<usize> {
        self.get_value(key).map(|v| v.len())
    }
    fn get_value(&self, key: &str) -> Option<String> {
        self.with_reader(|c| {
            c.prepare_cached("SELECT v FROM kv WHERE k = ?1").unwrap().query_row([key], |r| r.get(0)).optional().unwrap()
        })
    }
    fn scan(&self, prefix: &str, start: &str, limit: usize) -> (usize, usize) {
        let s = from(prefix, start);
        self.with_reader(|c| {
            let mut st = c.prepare_cached("SELECT k, v FROM kv WHERE k >= ?1 ORDER BY k LIMIT ?2").unwrap();
            let mut rows = st.query(rusqlite::params![s, limit as i64]).unwrap();
            let (mut n, mut bytes) = (0, 0);
            while let Some(r) = rows.next().unwrap() {
                let k: String = r.get(0).unwrap();
                if ends_scan(k.as_bytes(), prefix) {
                    break;
                }
                let v: String = r.get(1).unwrap();
                n += 1;
                bytes += v.len();
            }
            (n, bytes)
        })
    }
    fn scan_keys(&self, prefix: &str, start: &str, limit: usize) -> Vec<String> {
        let s = from(prefix, start);
        self.with_reader(|c| {
            let mut st = c.prepare_cached("SELECT k FROM kv WHERE k >= ?1 ORDER BY k LIMIT ?2").unwrap();
            let mut rows = st.query(rusqlite::params![s, limit as i64]).unwrap();
            let mut out = Vec::new();
            while let Some(r) = rows.next().unwrap() {
                let k: String = r.get(0).unwrap();
                if ends_scan(k.as_bytes(), prefix) {
                    break;
                }
                out.push(k);
            }
            out
        })
    }
    fn scan_pairs(&self, prefix: &str, start: &str, limit: usize) -> Vec<KvPair> {
        let s = from(prefix, start);
        self.with_reader(|c| {
            let mut st = c.prepare_cached("SELECT k, v FROM kv WHERE k >= ?1 ORDER BY k LIMIT ?2").unwrap();
            let mut rows = st.query(rusqlite::params![s, limit as i64]).unwrap();
            let mut out = Vec::new();
            while let Some(r) = rows.next().unwrap() {
                let k: String = r.get(0).unwrap();
                if ends_scan(k.as_bytes(), prefix) {
                    break;
                }
                out.push(KvPair { key: k, value: r.get(1).unwrap() });
            }
            out
        })
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        self.with_reader(|c| {
            c.execute_batch("BEGIN").unwrap();
            let mut total = 0;
            {
                let mut st = c.prepare_cached("SELECT v FROM kv WHERE k = ?1").unwrap();
                for k in keys {
                    let v: Option<String> = st.query_row([k], |r| r.get(0)).optional().unwrap();
                    total += v.map(|v| v.len()).unwrap_or(0);
                }
            }
            c.execute_batch("COMMIT").unwrap();
            total
        })
    }
    fn flush(&self) {
        let c = self.writer.lock().unwrap();
        // the checkpoint syncs only when synchronous is on
        sqlite_set_sync(&c, &self.sync_mode, true);
        c.execute_batch("PRAGMA wal_checkpoint(TRUNCATE)").unwrap();
    }
}
