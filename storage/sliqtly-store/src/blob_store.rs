//! Binary blob storage with RangerDiff delta compression and FS backup.
//!
//! Stores binary objects (blobs) identified by SHA-256, organized into trees
//! (path → blob mapping), and commits (git-like version history).
//! Dual-writes for reliability: KV store primary, filesystem backup secondary.

use crate::error::Result;
use serde::{Deserialize, Serialize};

pub type BlobId = String; // SHA-256 hex
pub type CommitId = String;
pub type TreeId = String;

/// A binary blob with metadata.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Blob {
    pub id: BlobId,
    pub size: u64,
    pub mime_type: String,
    pub checksum: String, // Adler-32 hex
    pub created_at: chrono::DateTime<chrono::Utc>,
    /// Optional recipe for derived blobs (crop, brightness, etc.)
    pub recipe: Option<serde_json::Value>,
}

/// Tree entry mapping a path to a blob.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct TreeEntry {
    pub path: String,
    pub blob_id: BlobId,
    pub size: u64,
    pub recipe: Option<serde_json::Value>,
    pub recipe_only: bool, // true = blob not stored, compute from recipe
}

/// A tree: snapshot of file paths → blobs at a point in time.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Tree {
    pub id: TreeId,
    pub entries: Vec<TreeEntry>,
    pub checksum: String, // SHA-256 hex
}

/// A commit: tree + ancestry + metadata.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Commit {
    pub id: CommitId,
    pub tree_id: TreeId,
    pub parents: Vec<CommitId>, // 0-2 parents
    pub author: String,
    pub message: String,
    pub timestamp: chrono::DateTime<chrono::Utc>,
    pub checksum: String, // SHA-256 hex
}

/// Write options for binary data.
#[derive(Clone, Debug)]
pub struct WriteOptions {
    pub author: String,
    pub message: String,
    pub recipe: Option<serde_json::Value>, // for re-encoded photos, crops, etc.
}

/// Integrity verification result.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct IntegrityCheckResult {
    pub valid: bool,
    pub errors: Vec<String>,
    pub checked_blobs: usize,
}

/// Backup recovery report.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct RecoveryReport {
    pub recovered_blobs: usize,
    pub recovered_trees: usize,
    pub recovered_commits: usize,
    pub errors: Vec<String>,
}

/// Trait for blob storage operations.
pub trait BlobStore: Send + Sync {
    /// Write binary data to a path.
    /// Performs dual-write: KV store + FS backup.
    /// Returns the commit ID and new blob ID.
    fn write_binary(
        &self,
        path: &str,
        data: Vec<u8>,
        opts: WriteOptions,
    ) -> Result<(CommitId, BlobId)>;

    /// Read binary data from a path.
    /// Returns the blob data and its metadata.
    fn read_binary(&self, path: &str) -> Result<Option<(Vec<u8>, Blob)>>;

    /// Get blob by ID (used for accessing specific versions).
    fn get_blob(&self, blob_id: &BlobId) -> Result<Option<(Vec<u8>, Blob)>>;

    /// List all paths in the current tree.
    fn list_paths(&self) -> Result<Vec<String>>;

    /// Get the current tree.
    fn current_tree(&self) -> Result<Option<Tree>>;

    /// Get a commit by ID.
    fn get_commit(&self, commit_id: &CommitId) -> Result<Option<Commit>>;

    /// Get commit history for a path.
    fn path_history(&self, path: &str) -> Result<Vec<Commit>>;

    /// Verify integrity of all stored objects.
    fn verify_integrity(&self) -> Result<IntegrityCheckResult>;

    /// Recover missing objects from filesystem backup.
    fn recover_from_backup(&self) -> Result<RecoveryReport>;

    /// Compute and store RangerDiff delta (async-friendly).
    /// Returns delta size or None if full blob is more efficient.
    fn compress_delta(&self, prev_blob_id: &BlobId, new_blob_id: &BlobId)
        -> Result<Option<u64>>;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_blob_structure() {
        let blob = Blob {
            id: "abc123".to_string(),
            size: 1024,
            mime_type: "image/png".to_string(),
            checksum: "deadbeef".to_string(),
            created_at: chrono::Utc::now(),
            recipe: None,
        };
        assert_eq!(blob.size, 1024);
    }

    #[test]
    fn test_commit_structure() {
        let commit = Commit {
            id: "commit1".to_string(),
            tree_id: "tree1".to_string(),
            parents: vec![],
            author: "alice".to_string(),
            message: "Initial commit".to_string(),
            timestamp: chrono::Utc::now(),
            checksum: "sha256hash".to_string(),
        };
        assert!(commit.parents.is_empty());
    }
}
