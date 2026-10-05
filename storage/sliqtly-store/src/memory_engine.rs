//! In-memory KV engine for prototyping.
//!
//! Uses BTreeMap for ordered storage. Snapshots clone the entire state.
//! Good for testing; real engines avoid cloning via structural sharing.

use crate::engine::{CommitResult, CommitSeq, DbSnapshot, KeyRange, KvEngine, WriteBatch, Mutation, Condition};
use crate::error::Result;
use parking_lot::RwLock;
use sha2::{Sha256, Digest};
use std::collections::BTreeMap;
use std::sync::Arc;

struct DbState {
    seq: CommitSeq,
    store: BTreeMap<Vec<u8>, Vec<u8>>,
}

pub struct MemorySnapshot {
    seq: CommitSeq,
    store: BTreeMap<Vec<u8>, Vec<u8>>,
}

impl DbSnapshot for MemorySnapshot {
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

pub struct MemoryEngine {
    state: Arc<RwLock<DbState>>,
}

impl MemoryEngine {
    pub fn new() -> Self {
        MemoryEngine {
            state: Arc::new(RwLock::new(DbState {
                seq: 0,
                store: BTreeMap::new(),
            })),
        }
    }
}

impl Default for MemoryEngine {
    fn default() -> Self {
        Self::new()
    }
}

impl KvEngine for MemoryEngine {
    fn snapshot(&self) -> Result<Arc<dyn DbSnapshot>> {
        let state = self.state.read();
        Ok(Arc::new(MemorySnapshot {
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
                            // Try to extract rev from JSON-encoded record
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
        Ok(CommitResult::Applied { seq: state.seq })
    }
}

impl MemoryEngine {
    /// Compute SHA-256 hash of a value for condition verification.
    fn value_hash(value: &[u8]) -> String {
        let mut hasher = Sha256::new();
        hasher.update(value);
        format!("{:x}", hasher.finalize())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_memory_engine_snapshot() {
        let engine = MemoryEngine::new();
        let snap1 = engine.snapshot().unwrap();
        assert_eq!(snap1.seq(), 0);

        let mut batch = WriteBatch::new();
        batch.put(b"key1".to_vec(), b"value1".to_vec());
        engine.commit(batch).unwrap();

        let snap2 = engine.snapshot().unwrap();
        assert_eq!(snap2.seq(), 1);
        assert_eq!(snap2.get(b"key1").unwrap(), Some(b"value1".to_vec()));

        // Old snapshot unchanged
        assert_eq!(snap1.seq(), 0);
        assert!(snap1.get(b"key1").unwrap().is_none());
    }

    #[test]
    fn test_empty_batch_no_seq_increment() {
        let engine = MemoryEngine::new();
        let snap1 = engine.snapshot().unwrap();
        assert_eq!(snap1.seq(), 0);

        let empty_batch = WriteBatch::new();
        let result = engine.commit(empty_batch).unwrap();
        assert!(matches!(result, CommitResult::NoChanges));

        let snap2 = engine.snapshot().unwrap();
        assert_eq!(snap2.seq(), 0); // Unchanged
    }

    #[test]
    fn test_scan_range() {
        let engine = MemoryEngine::new();

        let mut batch = WriteBatch::new();
        batch.put(b"a".to_vec(), b"1".to_vec());
        batch.put(b"b".to_vec(), b"2".to_vec());
        batch.put(b"c".to_vec(), b"3".to_vec());
        batch.put(b"d".to_vec(), b"4".to_vec());
        engine.commit(batch).unwrap();

        let snap = engine.snapshot().unwrap();
        let range = KeyRange::between_inclusive(b"b".to_vec(), b"c".to_vec());
        let results = snap.scan(range).unwrap();
        assert_eq!(results.len(), 2);
        assert_eq!(results[0].0, b"b");
        assert_eq!(results[1].0, b"c");
    }

    #[test]
    fn test_scan_prefix() {
        let engine = MemoryEngine::new();

        let mut batch = WriteBatch::new();
        batch.put(b"prefix_1".to_vec(), b"v1".to_vec());
        batch.put(b"prefix_2".to_vec(), b"v2".to_vec());
        batch.put(b"other".to_vec(), b"v3".to_vec());
        engine.commit(batch).unwrap();

        let snap = engine.snapshot().unwrap();
        let range = KeyRange::prefix(b"prefix".to_vec());
        let results = snap.scan(range).unwrap();
        assert_eq!(results.len(), 2);
    }

    #[test]
    fn test_cas_prevent_lost_update() {
        let engine = MemoryEngine::new();
        let key = b"doc".to_vec();

        // T1: Write initial value
        let mut t1 = WriteBatch::new();
        t1.put(key.clone(), b"v1".to_vec());
        let result = engine.commit(t1).unwrap();
        assert!(matches!(result, CommitResult::Applied { .. }));

        // T2 reads current value
        let snap = engine.snapshot().unwrap();
        let current = snap.get(&key).unwrap();
        assert_eq!(current, Some(b"v1".to_vec()));
        let current_hash = MemoryEngine::value_hash(b"v1");

        // T3 (intervening write): Update to v2
        let mut t3 = WriteBatch::new();
        t3.put(key.clone(), b"v2".to_vec());
        let result = engine.commit(t3).unwrap();
        assert!(matches!(result, CommitResult::Applied { .. }));

        // T2 tries to commit with stale expect_value
        let mut t2 = WriteBatch::new();
        t2.expect_value(key.clone(), current_hash); // T2 still thinks it's v1
        t2.put(key.clone(), b"v2_from_t2".to_vec());

        // Should fail: value changed to v2, not v1
        let result = engine.commit(t2).unwrap();
        assert!(matches!(result, CommitResult::Conflict { .. }));

        // Verify data wasn't overwritten
        let snap = engine.snapshot().unwrap();
        let final_val = snap.get(&key).unwrap();
        assert_eq!(final_val, Some(b"v2".to_vec()));
    }

    #[test]
    fn test_cas_key_absent() {
        let engine = MemoryEngine::new();
        let key = b"new_key".to_vec();

        // Precondition: key must not exist
        let mut batch = WriteBatch::new();
        batch.expect_absent(key.clone());
        batch.put(key.clone(), b"value".to_vec());

        let result = engine.commit(batch).unwrap();
        assert!(matches!(result, CommitResult::Applied { .. }));

        // Second attempt should fail (key now exists)
        let mut batch2 = WriteBatch::new();
        batch2.expect_absent(key.clone());
        batch2.put(key, b"other".to_vec());

        let result = engine.commit(batch2).unwrap();
        assert!(matches!(result, CommitResult::Conflict { .. }));
    }

    #[test]
    fn test_cas_multiple_conditions() {
        let engine = MemoryEngine::new();

        // Set up initial state
        let mut setup = WriteBatch::new();
        setup.put(b"k1".to_vec(), b"v1".to_vec());
        setup.put(b"k2".to_vec(), b"v2".to_vec());
        engine.commit(setup).unwrap();

        // Try to update both with correct conditions
        let mut tx = WriteBatch::new();
        tx.expect_value(b"k1".to_vec(), MemoryEngine::value_hash(b"v1"));
        tx.expect_value(b"k2".to_vec(), MemoryEngine::value_hash(b"v2"));
        tx.put(b"k1".to_vec(), b"v1_new".to_vec());
        tx.put(b"k2".to_vec(), b"v2_new".to_vec());

        let result = engine.commit(tx).unwrap();
        assert!(matches!(result, CommitResult::Applied { .. }));

        // Try again with one stale condition
        let mut tx2 = WriteBatch::new();
        tx2.expect_value(b"k1".to_vec(), MemoryEngine::value_hash(b"v1")); // Wrong: now v1_new
        tx2.expect_value(b"k2".to_vec(), MemoryEngine::value_hash(b"v2_new")); // Correct
        tx2.put(b"k1".to_vec(), b"v1_fail".to_vec());

        let result = engine.commit(tx2).unwrap();
        assert!(matches!(result, CommitResult::Conflict { .. }));
    }
}
