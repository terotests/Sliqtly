//! In-memory KV engine for prototyping.
//!
//! Uses BTreeMap for ordered storage. Snapshots clone the entire state.
//! Good for testing; real engines avoid cloning via structural sharing.

use crate::engine::{CommitResult, CommitSeq, DbSnapshot, KeyRange, KvEngine, WriteBatch, Mutation};
use crate::error::Result;
use parking_lot::RwLock;
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
}
