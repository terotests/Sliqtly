//! Transaction and database layer.
//!
//! Semantic transactions on top of KV engine abstraction.
//! Engine can be swapped (MemoryEngine, FjallEngine, RedbEngine) without API changes.

use crate::engine::{CommitResult, CommitSeq, DbSnapshot, KeyRange, KvEngine, WriteBatch};
use crate::error::Result;
use crate::fjall_engine::FjallEngine;
use crate::key::{KeyBuilder, KeyType};
use crate::log_engine::{LogEngine, LogOptions};
use crate::record::{Document, DocumentId, Membership, Room, RoomId, UserId};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

/// The main database handle.
pub struct Database {
    engine: Arc<dyn KvEngine>,
}

impl Database {
    /// Open or create a database at the given path.
    /// Uses FjallEngine for persistent storage.
    pub fn open<P: AsRef<Path>>(path: P) -> Result<Self> {
        let engine = FjallEngine::new(path)?;
        Ok(Database {
            engine: Arc::new(engine),
        })
    }

    /// Open or create a database backed by the WAL + checkpoint engine.
    pub fn open_log<P: AsRef<Path>>(path: P) -> Result<Self> {
        Self::open_log_with(path, LogOptions::default())
    }

    pub fn open_log_with<P: AsRef<Path>>(path: P, opts: LogOptions) -> Result<Self> {
        Ok(Database {
            engine: Arc::new(LogEngine::open_with(path, opts)?),
        })
    }

    /// Use any engine (tests, benchmarks).
    pub fn with_engine(engine: Arc<dyn KvEngine>) -> Self {
        Database { engine }
    }

    /// Begin a read-only transaction.
    pub fn read(&self) -> Result<ReadTx> {
        let snapshot = self.engine.snapshot()?;
        Ok(ReadTx { snapshot })
    }

    /// Begin a write transaction.
    pub fn write(&self) -> Result<WriteTx> {
        Ok(WriteTx {
            snapshot: self.engine.snapshot()?,
            mutations: Vec::new(),
            conditions: Vec::new(),
            docs: HashMap::new(),
        })
    }

    /// Get current sequence number.
    pub fn current_seq(&self) -> CommitSeq {
        match self.engine.snapshot() {
            Ok(snap) => snap.seq(),
            Err(_) => 0,
        }
    }

    /// Commit a write transaction.
    pub(crate) fn commit_batch(&self, batch: WriteBatch) -> Result<CommitSeq> {
        match self.engine.commit(batch)? {
            CommitResult::NoChanges => Ok(self.current_seq()),
            CommitResult::Applied { seq } => Ok(seq),
            CommitResult::Conflict { condition_index } => {
                Err(crate::error::Error::Conflict(condition_index))
            }
        }
    }
}

/// Read-only transaction providing a consistent snapshot view.
/// All operations in this transaction see exactly one committed database state.
pub struct ReadTx {
    snapshot: Arc<dyn DbSnapshot>,
}

impl ReadTx {
    /// Get a room by ID.
    pub fn get_room(&self, room_id: RoomId) -> Result<Option<Room>> {
        let key = KeyBuilder::new(KeyType::Room).push_uuid(room_id).build();

        match self.snapshot.get(key.as_slice())? {
            Some(bytes) => {
                let room = serde_json::from_slice(&bytes)?;
                Ok(Some(room))
            }
            None => Ok(None),
        }
    }

    /// Get a document by ID.
    pub fn get_document(&self, doc_id: DocumentId) -> Result<Option<Document>> {
        let key = KeyBuilder::new(KeyType::Document).push_uuid(doc_id).build();

        match self.snapshot.get(key.as_slice())? {
            Some(bytes) => {
                let doc = serde_json::from_slice(&bytes)?;
                Ok(Some(doc))
            }
            None => Ok(None),
        }
    }

    /// Get the commit sequence this snapshot observes.
    pub fn seq(&self) -> CommitSeq {
        self.snapshot.seq()
    }

    /// Get a raw key-value pair (for testing and binary data).
    pub fn get_raw(&self, key: &[u8]) -> Result<Option<Vec<u8>>> {
        self.snapshot.get(key)
    }

    /// Documents in a room, via the `room_document` index (one prefix scan
    /// plus one point read per document).
    pub fn documents_in_room(&self, room_id: RoomId) -> Result<Vec<Document>> {
        let prefix = KeyBuilder::new(KeyType::IdxRoomDoc)
            .push_uuid(room_id)
            .build();
        let mut out = Vec::new();
        for (k, _) in self.snapshot.scan(KeyRange::prefix(prefix))? {
            let doc_id = crate::key::decode_uuid(&k[17..])?;
            if let Some(d) = self.get_document(doc_id)? {
                out.push(d);
            }
        }
        Ok(out)
    }

    /// Documents in a room by scanning every document (no index needed).
    pub fn documents_in_room_scan(&self, room_id: RoomId) -> Result<Vec<Document>> {
        let mut out = Vec::new();
        let prefix = vec![KeyType::Document as u8];
        for (_, v) in self.snapshot.scan(KeyRange::prefix(prefix))? {
            let d: Document = serde_json::from_slice(&v)?;
            if d.room_id == room_id {
                out.push(d);
            }
        }
        Ok(out)
    }

    /// Rooms a user belongs to: a prefix scan of `memberships/<user>/…`.
    pub fn rooms_for_user(&self, user_id: UserId) -> Result<Vec<RoomId>> {
        let prefix = KeyBuilder::new(KeyType::Membership)
            .push_uuid(user_id)
            .build();
        self.snapshot
            .scan(KeyRange::prefix(prefix))?
            .into_iter()
            .map(|(k, _)| crate::key::decode_uuid(&k[17..]))
            .collect()
    }
}

/// Write transaction collecting mutations to commit atomically.
/// Does not mutate database until commit() is called.
pub struct WriteTx {
    /// State this transaction was started against; used to maintain indexes
    /// and to guard the records it rewrites.
    snapshot: Arc<dyn DbSnapshot>,
    mutations: Vec<(Vec<u8>, Option<Vec<u8>>)>, // key, Some(value) | None means delete
    conditions: Vec<crate::engine::Condition>,
    /// Room of each document written in this transaction (latest write wins).
    docs: HashMap<DocumentId, RoomId>,
}

impl WriteTx {
    /// Insert or update a room.
    /// Does not affect database until commit() is called.
    pub fn put_room(&mut self, mut room: Room) -> Result<Room> {
        room.updated_at = chrono::Utc::now();
        let key = KeyBuilder::new(KeyType::Room).push_uuid(room.id).build();
        let value = serde_json::to_vec(&room)?;
        self.mutations.push((key, Some(value)));
        Ok(room)
    }

    /// Insert or update a document.
    /// Does not affect database until commit() is called.
    ///
    /// Also maintains the `room_document` index. The commit is conditional on
    /// the stored document being unchanged since this transaction began, so
    /// a concurrent move to another room can never leave a stale index entry.
    pub fn put_document(&mut self, mut doc: Document) -> Result<Document> {
        doc.updated_at = chrono::Utc::now();
        doc.version += 1;
        let key = KeyBuilder::new(KeyType::Document).push_uuid(doc.id).build();

        let old_room = match self.docs.get(&doc.id) {
            Some(r) => Some(*r),
            None => {
                let stored = self.snapshot.get(&key)?;
                match &stored {
                    Some(bytes) => self.conditions.push(crate::engine::Condition::ValueEquals(
                        key.clone(),
                        format!("{:x}", Sha256::digest(bytes)),
                    )),
                    None => self
                        .conditions
                        .push(crate::engine::Condition::KeyAbsent(key.clone())),
                }
                match stored {
                    Some(bytes) => Some(serde_json::from_slice::<Document>(&bytes)?.room_id),
                    None => None,
                }
            }
        };
        if let Some(old) = old_room.filter(|r| *r != doc.room_id) {
            let stale = KeyBuilder::new(KeyType::IdxRoomDoc)
                .push_uuid(old)
                .push_uuid(doc.id)
                .build();
            self.mutations.push((stale, None));
        }
        let idx = KeyBuilder::new(KeyType::IdxRoomDoc)
            .push_uuid(doc.room_id)
            .push_uuid(doc.id)
            .build();
        self.mutations.push((idx, Some(Vec::new())));
        self.docs.insert(doc.id, doc.room_id);

        let value = serde_json::to_vec(&doc)?;
        self.mutations.push((key, Some(value)));
        Ok(doc)
    }

    /// Add a membership.
    /// Does not affect database until commit() is called.
    pub fn add_membership(&mut self, membership: Membership) -> Result<()> {
        // Key: Membership | user_id | room_id
        let key = KeyBuilder::new(KeyType::Membership)
            .push_uuid(membership.user_id)
            .push_uuid(membership.room_id)
            .build();
        let value = serde_json::to_vec(&membership)?;
        self.mutations.push((key, Some(value)));
        Ok(())
    }

    /// Put raw key-value pair (for binary blobs and metadata).
    /// Does not affect database until commit() is called.
    pub fn put_raw(&mut self, key: Vec<u8>, value: Vec<u8>) -> Result<()> {
        self.mutations.push((key, Some(value)));
        Ok(())
    }

    /// Delete a raw key.
    /// Does not affect database until commit() is called.
    pub fn delete_raw(&mut self, key: Vec<u8>) -> Result<()> {
        self.mutations.push((key, None));
        Ok(())
    }

    /// Commit all collected mutations atomically.
    /// Empty transactions return NoChanges and do not increment sequence.
    pub fn commit(self, db: &Database) -> Result<CommitSeq> {
        let mut batch = WriteBatch::new();
        batch.conditions = self.conditions;

        for (key, value_opt) in self.mutations {
            match value_opt {
                Some(value) => batch.put(key, value),
                None => batch.delete(key),
            }
        }

        db.commit_batch(batch)
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
            let seq = tx.commit(&db).unwrap();
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
            let seq = tx.commit(&db).unwrap();
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
            tx.commit(&db).unwrap();
        }

        let doc = create_test_doc(room_id, "Doc 1");
        {
            let mut tx = db.write().unwrap();
            tx.put_document(doc.clone()).unwrap();
            let seq = tx.commit(&db).unwrap();
            assert_eq!(seq, 2);
        }

        {
            let tx = db.read().unwrap();
            assert_eq!(tx.seq(), 2);
        }
    }

    #[test]
    fn test_empty_write_no_seq_increment() {
        let dir = TempDir::new().unwrap();
        let db = Database::open(dir.path()).unwrap();
        assert_eq!(db.current_seq(), 0);

        {
            let tx = db.write().unwrap();
            // Empty transaction - no mutations
            let seq = tx.commit(&db).unwrap();
            assert_eq!(seq, 0); // NoChanges, seq unchanged
        }

        // Verify seq did not increment
        assert_eq!(db.current_seq(), 0);
    }

    #[test]
    fn test_concurrent_readers() {
        let dir = TempDir::new().unwrap();
        let db = Arc::new(Database::open(dir.path()).unwrap());

        {
            let mut tx = db.write().unwrap();
            let room = create_test_room("Concurrent Room");
            tx.put_room(room).unwrap();
            tx.commit(&db).unwrap();
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
            let seq = tx.commit(&db).unwrap();
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

            let seq = tx.commit(&db).unwrap();
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
            let seq = tx.commit(&db).unwrap();

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
            tx.commit(&db).unwrap();
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
            let seq = tx.commit(&db).unwrap();
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
            tx.commit(&db).unwrap();
        }

        {
            let mut tx = db.write().unwrap();
            current_doc = tx.put_document(current_doc).unwrap();
            assert_eq!(current_doc.version, initial_version + 2);
            tx.commit(&db).unwrap();
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
            tx.commit(&db).unwrap();
        }
    }
}
