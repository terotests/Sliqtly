//! `backup` and `restore`.
//!
//! A backup is a directory with two files:
//!
//! - `records.sqkv`: every key/value pair of one snapshot, engine-neutral:
//!   `SQKV\x01\n`, then per record `u32be key_len, key, u32be value_len, value`,
//!   then `END\n` and `u64be record_count`.
//! - `manifest.json`: storage format, schema version, CommitSeq, engine,
//!   creation time, SHA-256 of every file, and the blob manifest.
//!
//! Restore checks the manifest and checksums before touching the target and
//! keeps the original CommitSeq, so change-feed cursors stay valid.

use crate::backend::{self, Loaded, ADMIN_DIR};
use crate::catalog::{tag, KeyReader};
use crate::model::Model;
use crate::report::LastBackup;
use anyhow::{bail, ensure, Context, Result};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sliqtly_store::key::KeyType;
use std::collections::BTreeMap;
use std::fs;
use std::io::Write;
use std::path::Path;

type Entries = BTreeMap<Vec<u8>, Vec<u8>>;

pub const BACKUP_FORMAT: u32 = 1;
const RECORDS: &str = "records.sqkv";
const MANIFEST: &str = "manifest.json";
const MAGIC: &[u8] = b"SQKV\x01\n";
const TRAILER: &[u8] = b"END\n";

#[derive(Debug, Serialize, Deserialize)]
pub struct FileSum {
    pub name: String,
    pub size: u64,
    pub sha256: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct BlobEntry {
    pub id: String,
    pub size: u64,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct Manifest {
    pub backup_format: u32,
    pub tool_version: String,
    pub storage_format: String,
    pub format_version: Option<String>,
    pub schema_version: Option<String>,
    pub engine: String,
    pub commit_seq: u64,
    pub created_at: String,
    pub source: String,
    pub records: u64,
    pub logical_bytes: u64,
    pub files: Vec<FileSum>,
    pub blobs: Vec<BlobEntry>,
}

fn encode(entries: &BTreeMap<Vec<u8>, Vec<u8>>) -> Vec<u8> {
    let mut out = MAGIC.to_vec();
    for (k, v) in entries {
        out.extend_from_slice(&(k.len() as u32).to_be_bytes());
        out.extend_from_slice(k);
        out.extend_from_slice(&(v.len() as u32).to_be_bytes());
        out.extend_from_slice(v);
    }
    out.extend_from_slice(TRAILER);
    out.extend_from_slice(&(entries.len() as u64).to_be_bytes());
    out
}

fn decode(buf: &[u8]) -> Result<BTreeMap<Vec<u8>, Vec<u8>>> {
    ensure!(buf.starts_with(MAGIC), "{RECORDS}: bad magic header");
    let end = buf
        .len()
        .checked_sub(TRAILER.len() + 8)
        .context("truncated")?;
    ensure!(
        &buf[end..end + TRAILER.len()] == TRAILER,
        "{RECORDS}: missing trailer (truncated?)"
    );
    let count = u64::from_be_bytes(buf[end + TRAILER.len()..].try_into()?);
    let mut pos = MAGIC.len();
    let take = |n: usize, pos: &mut usize| -> Result<Vec<u8>> {
        ensure!(*pos + n <= end, "{RECORDS}: record runs past end of data");
        let v = buf[*pos..*pos + n].to_vec();
        *pos += n;
        Ok(v)
    };
    let mut out = BTreeMap::new();
    while pos < end {
        let kl = u32::from_be_bytes(take(4, &mut pos)?.try_into().unwrap()) as usize;
        let k = take(kl, &mut pos)?;
        let vl = u32::from_be_bytes(take(4, &mut pos)?.try_into().unwrap()) as usize;
        let v = take(vl, &mut pos)?;
        ensure!(out.insert(k, v).is_none(), "{RECORDS}: duplicate key");
    }
    ensure!(
        out.len() as u64 == count,
        "{RECORDS}: trailer says {count} records, found {}",
        out.len()
    );
    Ok(out)
}

fn sha(b: &[u8]) -> String {
    format!("{:x}", Sha256::digest(b))
}

fn write_synced(path: &Path, bytes: &[u8]) -> Result<()> {
    let mut f = fs::File::create(path).with_context(|| format!("creating {}", path.display()))?;
    f.write_all(bytes)?;
    f.sync_all()?;
    Ok(())
}

fn blob_manifest(db: &Loaded) -> Vec<BlobEntry> {
    db.entries
        .iter()
        .filter(|(k, _)| k.first() == Some(&tag(KeyType::Blob)))
        .filter_map(|(k, v)| {
            let mut r = KeyReader::new(k);
            let id = r.string()?;
            r.is_empty().then_some(BlobEntry {
                id,
                size: v.len() as u64,
            })
        })
        .collect()
}

pub struct BackupOutcome {
    pub manifest: Manifest,
    /// Set when the source DB's `.sliqtly-admin/last_backup.json` could not be written.
    pub bookkeeping_error: Option<String>,
}

pub fn backup(db: &Loaded, dest: &Path) -> Result<BackupOutcome> {
    if dest.exists() {
        ensure!(
            dest.is_dir(),
            "{} exists and is not a directory",
            dest.display()
        );
        ensure!(
            fs::read_dir(dest)?.next().is_none(),
            "{} is not empty; refusing to overwrite",
            dest.display()
        );
    }
    fs::create_dir_all(dest)?;

    let data = encode(&db.entries);
    write_synced(&dest.join(RECORDS), &data)?;

    let model = Model::build(db);
    let manifest = Manifest {
        backup_format: BACKUP_FORMAT,
        tool_version: env!("CARGO_PKG_VERSION").into(),
        storage_format: db.storage_format.into(),
        format_version: model.meta.get("format_version").cloned(),
        schema_version: model.meta.get("schema_version").cloned(),
        engine: db.engine.into(),
        commit_seq: db.commit_seq,
        created_at: chrono::Utc::now().to_rfc3339(),
        source: db.path.display().to_string(),
        records: db.entries.len() as u64,
        logical_bytes: db.logical_bytes(),
        files: vec![FileSum {
            name: RECORDS.into(),
            size: data.len() as u64,
            sha256: sha(&data),
        }],
        blobs: blob_manifest(db),
    };
    write_synced(&dest.join(MANIFEST), &serde_json::to_vec_pretty(&manifest)?)?;
    backend::sync_dir(dest)?;

    // Read back what reached the disk before calling the backup good.
    check(dest).context("backup failed read-back verification")?;

    let bookkeeping_error = record_last_backup(db, dest, &manifest)
        .err()
        .map(|e| e.to_string());
    Ok(BackupOutcome {
        manifest,
        bookkeeping_error,
    })
}

fn record_last_backup(db: &Loaded, dest: &Path, m: &Manifest) -> Result<()> {
    let dir = db.path.join(ADMIN_DIR);
    fs::create_dir_all(&dir)?;
    let last = LastBackup {
        commit_seq: m.commit_seq,
        created_at: m.created_at.clone(),
        location: fs::canonicalize(dest)?.display().to_string(),
    };
    let tmp = dir.join("last_backup.json.tmp");
    write_synced(&tmp, &serde_json::to_vec_pretty(&last)?)?;
    fs::rename(&tmp, dir.join("last_backup.json"))?;
    Ok(())
}

/// Validate a backup directory: manifest, sizes, checksums, record framing.
pub fn check(dir: &Path) -> Result<(Manifest, Entries)> {
    let manifest: Manifest = serde_json::from_slice(
        &fs::read(dir.join(MANIFEST))
            .with_context(|| format!("reading {}/{MANIFEST}", dir.display()))?,
    )
    .context("manifest.json does not parse")?;
    if manifest.backup_format > BACKUP_FORMAT {
        bail!(
            "backup format {} is newer than this tool supports ({BACKUP_FORMAT})",
            manifest.backup_format
        );
    }
    let mut records = None;
    for f in &manifest.files {
        let bytes = fs::read(dir.join(&f.name)).with_context(|| format!("reading {}", f.name))?;
        ensure!(
            bytes.len() as u64 == f.size,
            "{}: size {} but manifest says {}",
            f.name,
            bytes.len(),
            f.size
        );
        let got = sha(&bytes);
        ensure!(
            got == f.sha256,
            "{}: SHA-256 mismatch (manifest {}, file {got})",
            f.name,
            f.sha256
        );
        if f.name == RECORDS {
            records = Some(decode(&bytes)?);
        }
    }
    let records = records.context("manifest lists no records.sqkv")?;
    ensure!(
        records.len() as u64 == manifest.records,
        "manifest says {} records, data has {}",
        manifest.records,
        records.len()
    );
    Ok((manifest, records))
}

pub struct RestoreOutcome {
    pub manifest: Manifest,
    pub replaced_existing: bool,
}

pub fn restore(src: &Path, target: &Path, force: bool, dry_run: bool) -> Result<RestoreOutcome> {
    let (manifest, records) = check(src)?;
    ensure!(
        manifest.storage_format == "state-bin/bincode-1",
        "backup was taken from storage format {}, which this build cannot restore into",
        manifest.storage_format
    );
    let existing = target.join(backend::STATE_FILE).exists();
    if existing && !force {
        bail!(
            "{} already holds a database; pass --force to replace it",
            target.display()
        );
    }
    if !dry_run {
        backend::write_state_bin(target, manifest.commit_seq, records)?;
    }
    Ok(RestoreOutcome {
        manifest,
        replaced_existing: existing,
    })
}
