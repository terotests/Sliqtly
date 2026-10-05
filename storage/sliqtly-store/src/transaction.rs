//! Transaction and database layer with in-memory storage.
//!
//! MVP uses HashMap + JSON serialization. Later can be swapped for
//! a proper KV engine (redb, fjall, rocksdb) without changing the API.

use crate::error::Result;
use crate::key::{KeyBuilder, KeyType};
use crate::record::{Room, Document, Membership, Change, Seq, RoomId, DocumentId};
use parking_lot::RwLock;
use std::collections::BTreeMap;
use std::path::Path;
use std::sync::Arc;

type KvStore = BTreeMap<Vec<u8>, Vec<u8>>;

/// The main database handle.
pub struct Database {
    store: Arc<RwLock<KvStore>>,
    seq: Arc<RwLock<Seq>>,
}

impl Database {
    /// Open or create a database at the given path.
    pub fn open<P: AsRef<Path>>(_path: P) -> Result<Self> {
        Ok(Database {
            store: Arc::new(RwLock::new(BTreeMap::new())),
            seq: Arc::new(RwLock::new(0u64)),
        })
    }

    /// Begin a read-only transaction.
    pub fn read(&self) -> Result<ReadTx> {
        Ok(ReadTx {
            store: self.store.clone(),
            seq: *self.seq.read(),
        })
    }

    /// Begin a write transaction.
    pub fn write(&self) -> Result<WriteTx> {
        Ok(WriteTx {
            store: self.store.clone(),
            seq: self.seq.clone(),
            changes: Vec::new(),
        })
    }

    /// Get current sequence number.
    pub fn current_seq(&self) -> Seq {
        *self.seq.read()
    }
}

/// Read-only transaction.
pub struct ReadTx {
    store: Arc<RwLock<KvStore>>,
    seq: Seq,
}

impl ReadTx {
    /// Get a room by ID.
    pub fn get_room(&self, room_id: RoomId) -> Result<Option<Room>> {
        let key = KeyBuilder::new(KeyType::Room).push_uuid(room_id).build();
        let store = self.store.read();

        match store.get(key.as_slice()) {
            Some(bytes) => {
                let room = serde_json::from_slice(bytes)?;
                Ok(Some(room))
            }
            None => Ok(None),
        }
    }

    /// Get a document by ID.
    pub fn get_document(&self, doc_id: DocumentId) -> Result<Option<Document>> {
        let key = KeyBuilder::new(KeyType::Document).push_uuid(doc_id).build();
        let store = self.store.read();

        match store.get(key.as_slice()) {
            Some(bytes) => {
                let doc = serde_json::from_slice(bytes)?;
                Ok(Some(doc))
            }
            None => Ok(None),
        }
    }

    pub fn seq(&self) -> Seq {
        self.seq
    }
}

/// Write transaction with change log semantics.
pub struct WriteTx {
    store: Arc<RwLock<KvStore>>,
    seq: Arc<RwLock<Seq>>,
    changes: Vec<Change>,
}

impl WriteTx {
    /// Insert or update a room.
    pub fn put_room(&mut self, mut room: Room) -> Result<Room> {
        room.updated_at = chrono::Utc::now();
        self.changes.push(Change::RoomUpdated {
            room: room.clone(),
            version: 1,
        });

        Ok(room)
    }

    /// Insert or update a document.
    pub fn put_document(&mut self, mut doc: Document) -> Result<Document> {
        doc.updated_at = chrono::Utc::now();
        doc.version += 1;

        self.changes.push(Change::DocumentUpdated {
            doc: doc.clone(),
            version: doc.version,
        });

        Ok(doc)
    }

    /// Add a membership.
    pub fn add_membership(&mut self, membership: Membership) -> Result<()> {
        self.changes.push(Change::MembershipAdded {
            membership: membership.clone(),
        });

        Ok(())
    }

    /// Commit all changes atomically.
    pub fn commit(self) -> Result<Seq> {
        let mut store = self.store.write();
        let mut seq = self.seq.write();
        *seq += 1;
        let commit_seq = *seq;

        // Write all changes to log
        for (idx, change) in self.changes.iter().enumerate() {
            let key = KeyBuilder::new(KeyType::Changes)
                .push_be_u64(commit_seq)
                .push_be_u32(idx as u32)
                .build();

            let value = serde_json::to_vec(change)?;
            store.insert(key, value);
        }

        Ok(commit_seq)
    }

    pub fn add_change(&mut self, change: Change) {
        self.changes.push(change);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use std::thread;
    use tempfile::TempDir;
    use uuid::Uuid;

    fn create_test_room(title: &str) -> Room {
        Room {
            id: Uuid::now_v7(),
            title: title.to_string(),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        }
    }

    fn create_test_doc(room_id: RoomId, title: &str) -> Document {
        Document {
            id: Uuid::now_v7(),
            room_id,
            title: title.to_string(),
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            version: 0,
            metadata: serde_json::json!({}),
        }
    }

    #[test]
    fn test_database_creation() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();
        assert_eq!(db.current_seq(), 0);
    }

    #[test]
    fn test_single_room_write_read() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let room = create_test_room("Test Room");

        {
            let mut tx = db.write().unwrap();
            tx.put_room(room).unwrap();
            let seq = tx.commit().unwrap();
            assert_eq!(seq, 1);
        }

        {
            let tx = db.read().unwrap();
            assert_eq!(tx.seq(), 1);
        }
    }

    #[test]
    fn test_multiple_writes_increment_seq() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        for i in 0..5 {
            let mut tx = db.write().unwrap();
            let room = create_test_room(&format!("Room {}", i));
            tx.put_room(room).unwrap();
            let seq = tx.commit().unwrap();
            assert_eq!(seq, (i + 1) as u64);
        }

        let tx = db.read().unwrap();
        assert_eq!(tx.seq(), 5);
    }

    #[test]
    fn test_document_in_room() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let room = create_test_room("Parent Room");
        let room_id = room.id;

        {
            let mut tx = db.write().unwrap();
            tx.put_room(room).unwrap();
            tx.commit().unwrap();
        }

        let doc = create_test_doc(room_id, "Doc 1");
        {
            let mut tx = db.write().unwrap();
            tx.put_document(doc.clone()).unwrap();
            let seq = tx.commit().unwrap();
            assert_eq!(seq, 2);
        }

        {
            let tx = db.read().unwrap();
            assert_eq!(tx.seq(), 2);
        }
    }

    #[test]
    fn test_empty_write_increments_seq() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        {
            let tx = db.write().unwrap();
            // Empty transaction - no adds
            let seq = tx.commit().unwrap();
            assert_eq!(seq, 1);
        }

        let tx = db.read().unwrap();
        assert_eq!(tx.seq(), 1);
    }

    #[test]
    fn test_concurrent_readers() {
        let dir = TempDir::new().unwrap();
        let db = Arc::new(Database::open(dir.path()).unwrap());

        {
            let mut tx = db.write().unwrap();
            let room = create_test_room("Concurrent Room");
            tx.put_room(room).unwrap();
            tx.commit().unwrap();
        }

        // Spawn 10 concurrent readers
        let handles: Vec<_> = (0..10)
            .map(|_| {
                let db_clone = db.clone();
                thread::spawn(move || {
                    let tx = db_clone.read().unwrap();
                    assert_eq!(tx.seq(), 1);
                })
            })
            .collect();

        for handle in handles {
            handle.join().unwrap();
        }
    }

    #[test]
    fn test_membership_changes() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let room_id = Uuid::now_v7();
        let user_id = Uuid::now_v7();

        {
            let mut tx = db.write().unwrap();
            let membership = Membership {
                user_id,
                room_id,
                role: crate::record::Role::Owner,
                joined_at: chrono::Utc::now(),
                metadata: serde_json::json!({}),
            };
            tx.add_membership(membership).unwrap();
            let seq = tx.commit().unwrap();
            assert_eq!(seq, 1);
        }

        {
            let tx = db.read().unwrap();
            assert_eq!(tx.seq(), 1);
        }
    }

    #[test]
    fn test_multiple_changes_in_one_commit() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let room_id = Uuid::now_v7();

        {
            let mut tx = db.write().unwrap();

            let room = create_test_room("Multi-Change Room");
            tx.put_room(room).unwrap();

            let doc1 = create_test_doc(room_id, "Doc 1");
            let doc2 = create_test_doc(room_id, "Doc 2");
            tx.put_document(doc1).unwrap();
            tx.put_document(doc2).unwrap();

            let user_id = Uuid::now_v7();
            let membership = Membership {
                user_id,
                room_id,
                role: crate::record::Role::Editor,
                joined_at: chrono::Utc::now(),
                metadata: serde_json::json!({}),
            };
            tx.add_membership(membership).unwrap();

            let seq = tx.commit().unwrap();
            assert_eq!(seq, 1);
        }
    }

    #[test]
    fn test_seq_monotonicity() {
        let dir = TempDir::new().unwrap();
        let db = Arc::new(Database::open(dir.path()).unwrap());

        let mut last_seq = 0u64;

        for _ in 0..20 {
            let mut tx = db.write().unwrap();
            let room = create_test_room("Test");
            tx.put_room(room).unwrap();
            let seq = tx.commit().unwrap();

            assert!(seq > last_seq, "Seq must be strictly increasing");
            last_seq = seq;
        }

        assert_eq!(last_seq, 20);
    }

    #[test]
    fn test_reads_see_committed_seq() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let initial_seq = db.current_seq();
        assert_eq!(initial_seq, 0);

        {
            let mut tx = db.write().unwrap();
            let room = create_test_room("Seq Check");
            tx.put_room(room).unwrap();
            tx.commit().unwrap();
        }

        assert_eq!(db.current_seq(), 1);

        {
            let tx = db.read().unwrap();
            assert_eq!(tx.seq(), 1);
        }
    }

    #[test]
    fn test_large_payload() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let mut room = create_test_room("Large Payload Room");
        room.metadata = serde_json::json!({
            "large_field": "x".repeat(100_000),
            "nested": {
                "data": (0..1000).collect::<Vec<_>>()
            }
        });

        {
            let mut tx = db.write().unwrap();
            tx.put_room(room.clone()).unwrap();
            let seq = tx.commit().unwrap();
            assert_eq!(seq, 1);
        }

        let tx = db.read().unwrap();
        assert_eq!(tx.seq(), 1);
    }

    #[test]
    fn test_document_version_increment() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let room_id = Uuid::now_v7();
        let doc = create_test_doc(room_id, "Versioned Doc");
        let initial_version = doc.version;

        let mut current_doc = doc.clone();
        {
            let mut tx = db.write().unwrap();
            current_doc = tx.put_document(current_doc).unwrap();
            assert_eq!(current_doc.version, initial_version + 1);
            tx.commit().unwrap();
        }

        {
            let mut tx = db.write().unwrap();
            current_doc = tx.put_document(current_doc).unwrap();
            assert_eq!(current_doc.version, initial_version + 2);
            tx.commit().unwrap();
        }
    }

    #[test]
    fn test_room_timestamp_updates() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();

        let room = create_test_room("Timestamp Test");
        let original_timestamp = room.updated_at;

        {
            let mut tx = db.write().unwrap();
            let updated_room = tx.put_room(room).unwrap();
            assert!(updated_room.updated_at >= original_timestamp);
            tx.commit().unwrap();
        }
    }
}
