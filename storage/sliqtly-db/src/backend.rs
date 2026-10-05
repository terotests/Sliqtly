//! Storage backends the admin tool can open.
//!
//! Every command works on a `Loaded` view: one consistent set of key/value
//! pairs at one CommitSeq, plus facts about the files on disk. Adding the
//! Sliqtly kernel later means adding one more `detect` arm here; the commands
//! above it only see `Loaded`.

use anyhow::{bail, Context, Result};
use sliqtly_store::log_engine;
use std::collections::BTreeMap;
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};

/// File name used by `sliqtly_store::FjallEngine` (which, despite its name,
/// keeps the whole store in one bincode file rewritten on every commit).
pub const STATE_FILE: &str = "state.bin";
pub const STATE_TMP_FILE: &str = "state.bin.tmp";
/// Sidecar directory for admin-tool bookkeeping (last backup). Never read by the engine.
pub const ADMIN_DIR: &str = ".sliqtly-admin";

/// Mirror of the private `DbState` in `sliqtly_store::fjall_engine`.
/// bincode encodes structs field by field, so the layout must stay identical;
/// `tests/state_bin_compat.rs` checks this against the real engine.
#[derive(serde::Serialize, serde::Deserialize)]
pub struct StateBin {
    pub seq: u64,
    pub store: BTreeMap<Vec<u8>, Vec<u8>>,
}

#[derive(Debug, Clone, serde::Serialize)]
pub struct DiskFile {
    pub name: String,
    pub size: u64,
}

/// One consistent view of a database.
pub struct Loaded {
    pub path: PathBuf,
    /// Engine identifier as shown to operators.
    pub engine: &'static str,
    /// On-disk container format, independent of the logical schema.
    pub storage_format: &'static str,
    pub commit_seq: u64,
    pub entries: BTreeMap<Vec<u8>, Vec<u8>>,
    pub files: Vec<DiskFile>,
    /// Files that should not exist in a cleanly shut down database.
    pub leftovers: Vec<DiskFile>,
}

impl Loaded {
    pub fn logical_bytes(&self) -> u64 {
        self.entries
            .iter()
            .map(|(k, v)| (k.len() + v.len()) as u64)
            .sum()
    }

    pub fn physical_bytes(&self) -> u64 {
        self.files.iter().map(|f| f.size).sum::<u64>()
            + self.leftovers.iter().map(|f| f.size).sum::<u64>()
    }
}

/// Open a database directory read-only. Never creates or modifies files.
pub fn open(path: &Path) -> Result<Loaded> {
    if !path.is_dir() {
        bail!("{} is not a directory", path.display());
    }
    if path.join(log_engine::WAL_FILE).is_file() || path.join(log_engine::CHECKPOINT_FILE).is_file()
    {
        return open_log(path);
    }
    let state = path.join(STATE_FILE);
    if !state.is_file() {
        bail!(
            "no recognised database in {} (expected {}, or {} and {})",
            path.display(),
            STATE_FILE,
            log_engine::CHECKPOINT_FILE,
            log_engine::WAL_FILE
        );
    }
    // The engine replaces state.bin by rename, so one read sees one commit.
    let bytes = fs::read(&state).with_context(|| format!("reading {}", state.display()))?;
    let decoded: StateBin = bincode::deserialize(&bytes)
        .with_context(|| format!("{} does not decode as a state-bin store", state.display()))?;

    let mut leftovers = Vec::new();
    let tmp = path.join(STATE_TMP_FILE);
    if let Ok(meta) = fs::metadata(&tmp) {
        leftovers.push(DiskFile {
            name: STATE_TMP_FILE.into(),
            size: meta.len(),
        });
    }

    Ok(Loaded {
        path: path.to_path_buf(),
        engine: "state-bin",
        storage_format: "state-bin/bincode-1",
        commit_seq: decoded.seq,
        entries: decoded.store,
        files: vec![DiskFile {
            name: STATE_FILE.into(),
            size: bytes.len() as u64,
        }],
        leftovers,
    })
}

/// `sliqtly_store::LogEngine`: checkpoint plus WAL. Recovery only reads, so a
/// torn WAL tail is reported here and left for the engine to truncate.
fn open_log(path: &Path) -> Result<Loaded> {
    let rec = log_engine::recover(path)
        .with_context(|| format!("{} does not recover as a log-engine store", path.display()))?;
    let mut files = Vec::new();
    for name in [log_engine::CHECKPOINT_FILE, log_engine::WAL_FILE] {
        if let Ok(meta) = fs::metadata(path.join(name)) {
            let size = if name == log_engine::WAL_FILE {
                rec.wal_valid_bytes
            } else {
                meta.len()
            };
            files.push(DiskFile {
                name: name.into(),
                size,
            });
        }
    }
    let mut leftovers = Vec::new();
    if rec.torn_tail_bytes > 0 {
        leftovers.push(DiskFile {
            name: format!("{} torn tail", log_engine::WAL_FILE),
            size: rec.torn_tail_bytes,
        });
    }
    Ok(Loaded {
        path: path.to_path_buf(),
        engine: "log",
        storage_format: "log/wal-crc32+checkpoint-1",
        commit_seq: rec.seq,
        entries: rec.entries.into_iter().collect(),
        files,
        leftovers,
    })
}

/// Write a state-bin database atomically: temp file, fsync, rename, fsync dir.
pub fn write_state_bin(dir: &Path, seq: u64, entries: BTreeMap<Vec<u8>, Vec<u8>>) -> Result<()> {
    fs::create_dir_all(dir)?;
    let state = StateBin {
        seq,
        store: entries,
    };
    let bytes = bincode::serialize(&state)?;
    let tmp = dir.join(STATE_TMP_FILE);
    {
        let mut f = fs::File::create(&tmp)?;
        f.write_all(&bytes)?;
        f.sync_all()?;
    }
    fs::rename(&tmp, dir.join(STATE_FILE))?;
    sync_dir(dir)?;
    Ok(())
}

pub fn sync_dir(dir: &Path) -> Result<()> {
    #[cfg(unix)]
    fs::File::open(dir)?.sync_all()?;
    Ok(())
}
