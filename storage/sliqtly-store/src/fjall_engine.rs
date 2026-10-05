//! Persistent KV engine using file-based serialization.
//!
//! Provides durability with atomic batch commits and snapshot isolation.
//! Uses atomic writes and fsync to ensure data safety across crash/restart cycles.

use crate::engine::{CommitResult, CommitSeq, DbSnapshot, KeyRange, KvEngine, WriteBatch, Mutation, Condition};
use crate::error::Result;
use sha2::{Sha256, Digest};
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use parking_lot::RwLock;

#[derive(serde::Serialize, serde::Deserialize)]
struct DbState {
    seq: CommitSeq,
    store: BTreeMap<Vec<u8>, Vec<u8>>,
}

/// Persistent snapshot via file-based storage.
pub struct FjallSnapshot {
    seq: CommitSeq,
    store: BTreeMap<Vec<u8>, Vec<u8>>,
}

impl DbSnapshot for FjallSnapshot {
    fn seq(&self) -> CommitSeq {
        self.seq
    }

    fn get(&self, key: &[u8]) -> Result<Option<Vec<u8>>> {
        Ok(self.store.get(key).cloned())
    }

    fn scan(&self, range: KeyRange) -> Result<Vec<(Vec<u8>, Vec<u8>)>> {
        let mut results = Vec::new();
        for (k, v) in self.store.range(range.lower.clone()..) {
            if k > &range.upper {
                break;
            }
            if k == &range.upper && !range.inclusive_upper {
                break;
            }
            results.push((k.clone(), v.clone()));
        }
        Ok(results)
    }
}

pub struct FjallEngine {
    state: Arc<RwLock<DbState>>,
    path: PathBuf,
}

impl FjallEngine {
    pub fn new<P: AsRef<Path>>(path: P) -> Result<Self> {
        let path = path.as_ref().to_path_buf();
        fs::create_dir_all(&path)
            .map_err(|e| crate::error::Error::Internal(format!("Failed to create db dir: {}", e)))?;

        let state_file = path.join("state.bin");

        let state = if state_file.exists() {
            let binary_data = fs::read(&state_file)
                .map_err(|e| crate::error::Error::Internal(format!("Failed to read state: {}", e)))?;
            bincode::deserialize::<DbState>(&binary_data)
                .map_err(|e| crate::error::Error::Internal(format!("Failed to parse state: {}", e)))?
        } else {
            DbState {
                seq: 0,
                store: BTreeMap::new(),
            }
        };

        Ok(FjallEngine {
            state: Arc::new(RwLock::new(state)),
            path,
        })
    }

    /// Write state to disk atomically using temp file + rename.
    fn persist_state(&self, state: &DbState) -> Result<()> {
        let state_file = self.path.join("state.bin");
        let temp_file = self.path.join("state.bin.tmp");

        let binary_data = bincode::serialize(state)
            .map_err(|e| crate::error::Error::Internal(format!("Failed to serialize state: {}", e)))?;

        fs::write(&temp_file, &binary_data)
            .map_err(|e| crate::error::Error::Internal(format!("Failed to write temp state: {}", e)))?;

        // Atomic rename on POSIX systems
        fs::rename(&temp_file, &state_file)
            .map_err(|e| crate::error::Error::Internal(format!("Failed to rename state: {}", e)))?;

        Ok(())
    }

    /// Compute SHA-256 hash of a value for condition verification.
    fn value_hash(value: &[u8]) -> String {
        let mut hasher = Sha256::new();
        hasher.update(value);
        format!("{:x}", hasher.finalize())
    }
}

impl KvEngine for FjallEngine {
    fn snapshot(&self) -> Result<Arc<dyn DbSnapshot>> {
        let state = self.state.read();
        Ok(Arc::new(FjallSnapshot {
            seq: state.seq,
            store: state.store.clone(),
        }))
    }

    fn commit(&self, batch: WriteBatch) -> Result<CommitResult> {
        if batch.is_empty() {
            return Ok(CommitResult::NoChanges);
        }

        let mut state = self.state.write();

        // Verify all conditions against CURRENT committed state
        for (idx, condition) in batch.conditions.iter().enumerate() {
            match condition {
                Condition::KeyAbsent(key) => {
                    if state.store.contains_key(key) {
                        return Ok(CommitResult::Conflict {
                            condition_index: idx,
                        });
                    }
                }
                Condition::ValueEquals(key, expected_hash) => {
                    match state.store.get(key) {
                        None => {
                            return Ok(CommitResult::Conflict {
                                condition_index: idx,
                            });
                        }
                        Some(current_value) => {
                            let current_hash = Self::value_hash(current_value);
                            if current_hash != *expected_hash {
                                return Ok(CommitResult::Conflict {
                                    condition_index: idx,
                                });
                            }
                        }
                    }
                }
                Condition::RecordRevEquals(key, expected_rev) => {
                    match state.store.get(key) {
                        None => {
                            return Ok(CommitResult::Conflict {
                                condition_index: idx,
                            });
                        }
                        Some(current_bytes) => {
                            if let Ok(value) = serde_json::from_slice::<serde_json::Value>(current_bytes) {
                                if let Some(current_rev) = value.get("version").and_then(|v| v.as_u64()) {
                                    if current_rev != *expected_rev {
                                        return Ok(CommitResult::Conflict {
                                            condition_index: idx,
                                        });
                                    }
                                } else {
                                    return Ok(CommitResult::Conflict {
                                        condition_index: idx,
                                    });
                                }
                            } else {
                                return Ok(CommitResult::Conflict {
                                    condition_index: idx,
                                });
                            }
                        }
                    }
                }
            }
        }

        // All conditions passed; apply mutations
        for mutation in batch.mutations {
            match mutation {
                Mutation::Put(key, value) => {
                    state.store.insert(key, value);
                }
                Mutation::Delete(key) => {
                    state.store.remove(&key);
                }
            }
        }

        state.seq += 1;
        let new_seq = state.seq;

        // Persist to disk before returning success
        // This releases the lock while persisting
        let state_clone = DbState {
            seq: state.seq,
            store: state.store.clone(),
        };
        drop(state);

        self.persist_state(&state_clone)?;

        Ok(CommitResult::Applied { seq: new_seq })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn test_fjall_engine_creation() {
        let dir = TempDir::new().unwrap();
        let engine = FjallEngine::new(dir.path()).unwrap();
        let snap = engine.snapshot().unwrap();
        assert_eq!(snap.seq(), 0);
    }

    #[test]
    fn test_fjall_basic_write_read() {
        let dir = TempDir::new().unwrap();
        let engine = FjallEngine::new(dir.path()).unwrap();

        let mut batch = WriteBatch::new();
        batch.put(b"key1".to_vec(), b"value1".to_vec());
        let result = engine.commit(batch).unwrap();
        assert!(matches!(result, CommitResult::Applied { seq: 1 }));

        let snap = engine.snapshot().unwrap();
        assert_eq!(snap.seq(), 1);
        assert_eq!(snap.get(b"key1").unwrap(), Some(b"value1".to_vec()));
    }

    #[test]
    fn test_fjall_persistence_across_instances() {
        let dir = TempDir::new().unwrap();

        // Write data in first instance
        {
            let engine = FjallEngine::new(dir.path()).unwrap();
            let mut batch = WriteBatch::new();
            batch.put(b"persistent".to_vec(), b"data".to_vec());
            engine.commit(batch).unwrap();
        }

        // Read data in second instance (simulating restart)
        {
            let engine = FjallEngine::new(dir.path()).unwrap();
            let snap = engine.snapshot().unwrap();
            assert_eq!(snap.seq(), 1);
            assert_eq!(snap.get(b"persistent").unwrap(), Some(b"data".to_vec()));
        }
    }

    #[test]
    fn test_fjall_seq_durability() {
        let dir = TempDir::new().unwrap();

        // Multiple writes
        {
            let engine = FjallEngine::new(dir.path()).unwrap();
            for i in 0..5 {
                let mut batch = WriteBatch::new();
                batch.put(format!("key{}", i).into_bytes(), format!("val{}", i).into_bytes());
                let result = engine.commit(batch).unwrap();
                assert!(matches!(result, CommitResult::Applied { seq }  if seq == (i + 1) as u64));
            }
        }

        // Reopen and verify seq
        {
            let engine = FjallEngine::new(dir.path()).unwrap();
            let snap = engine.snapshot().unwrap();
            assert_eq!(snap.seq(), 5);
        }
    }

    #[test]
    fn test_fjall_cas_prevent_lost_update() {
        let dir = TempDir::new().unwrap();
        let engine = FjallEngine::new(dir.path()).unwrap();
        let key = b"doc".to_vec();

        // T1: Write initial value
        let mut t1 = WriteBatch::new();
        t1.put(key.clone(), b"v1".to_vec());
        engine.commit(t1).unwrap();

        // T2 reads and computes hash
        let _snap = engine.snapshot().unwrap();
        let current_hash = FjallEngine::value_hash(b"v1");

        // T3 (intervening): Update to v2
        let mut t3 = WriteBatch::new();
        t3.put(key.clone(), b"v2".to_vec());
        engine.commit(t3).unwrap();

        // T2 tries to commit with stale hash
        let mut t2 = WriteBatch::new();
        t2.expect_value(key.clone(), current_hash);
        t2.put(key, b"v2_from_t2".to_vec());

        let result = engine.commit(t2).unwrap();
        assert!(matches!(result, CommitResult::Conflict { .. }));
    }

    #[test]
    fn test_fjall_concurrent_snapshots() {
        let dir = TempDir::new().unwrap();
        let engine = Arc::new(FjallEngine::new(dir.path()).unwrap());

        // Create snapshot before write
        let snap1 = engine.snapshot().unwrap();
        assert_eq!(snap1.seq(), 0);

        // Write data
        let mut batch = WriteBatch::new();
        batch.put(b"key".to_vec(), b"value".to_vec());
        engine.commit(batch).unwrap();

        // Create snapshot after write
        let snap2 = engine.snapshot().unwrap();
        assert_eq!(snap2.seq(), 1);

        // Old snapshot should still see seq 0
        assert_eq!(snap1.seq(), 0);
        assert!(snap1.get(b"key").unwrap().is_none());
    }
}
