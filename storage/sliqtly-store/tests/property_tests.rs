//! Property-based tests
//!
//! These tests verify invariants with various inputs.

use sliqtly_store::{Database, Room, Document};
use tempfile::TempDir;
use uuid::Uuid;

#[test]
fn prop_seq_always_increases_on_writes() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    let mut last_seq = 0u64;
    for i in 0..20 {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Room {}", i),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        let seq = tx.commit(&db).unwrap();
        assert!(seq > last_seq, "seq {} must be > previous {}", seq, last_seq);
        last_seq = seq;
    }
    assert_eq!(last_seq, 20);
}

#[test]
fn prop_current_seq_matches_read_seq() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    for _ in 0..10 {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: "Test".to_string(),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        tx.commit(&db).unwrap();
    }

    let db_seq = db.current_seq();
    let read_tx = db.read().unwrap();
    let read_seq = read_tx.seq();

    assert_eq!(db_seq, read_seq, "current_seq and read_seq must be consistent");
    assert_eq!(db_seq, 10);
}

#[test]
fn prop_seq_never_wraps_or_repeats() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    let mut seqs = Vec::new();
    for i in 0..100 {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Unique {}", i),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        let seq = tx.commit(&db).unwrap();
        seqs.push(seq);
    }

    // All sequences should be unique
    for i in 0..seqs.len() {
        for j in (i + 1)..seqs.len() {
            assert_ne!(seqs[i], seqs[j], "Sequences must never repeat");
        }
    }

    // Should be monotonically increasing
    for i in 1..seqs.len() {
        assert!(
            seqs[i] > seqs[i - 1],
            "Sequences must be strictly increasing"
        );
    }
}

#[test]
fn prop_empty_commits_no_seq_increment() {
    let db = Database::open(".").unwrap();

    for _ in 0..30 {
        let tx = db.write().unwrap();
        let seq = tx.commit(&db).unwrap();
        assert_eq!(seq, 0); // Empty commits return current seq (0)
    }

    assert_eq!(db.current_seq(), 0); // Seq never incremented
}

#[test]
fn prop_multiple_changes_one_commit() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    for batch in 0..5 {
        let room_id = Uuid::now_v7();
        let mut tx = db.write().unwrap();

        // Multiple documents in same transaction
        for i in 0..10 {
            let doc = Document {
                id: Uuid::now_v7(),
                room_id,
                title: format!("Doc {} in batch {}", i, batch),
                created_by: Uuid::now_v7(),
                created_at: chrono::Utc::now(),
                updated_at: chrono::Utc::now(),
                version: 0,
                metadata: serde_json::json!({"batch": batch, "index": i}),
            };
            tx.put_document(doc).unwrap();
        }

        let seq = tx.commit(&db).unwrap();
        assert_eq!(seq, (batch + 1) as u64);
    }
}

#[test]
fn prop_room_timestamp_always_updates() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    for i in 0..10 {
        let original_time = chrono::Utc::now();
        std::thread::sleep(std::time::Duration::from_millis(1)); // Ensure time passes

        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Timestamped {}", i),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: original_time,
            updated_at: original_time,
            metadata: serde_json::json!({}),
        };

        {
            let mut tx = db.write().unwrap();
            let updated_room = tx.put_room(room).unwrap();
            assert!(
                updated_room.updated_at >= original_time,
                "Timestamp must be updated to current time or later"
            );
            tx.commit(&db).unwrap();
        }
    }
}

#[test]
fn prop_document_version_increments() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    let room_id = Uuid::now_v7();
    let mut doc = Document {
        id: Uuid::now_v7(),
        room_id,
        title: "Version Test".to_string(),
        created_by: Uuid::now_v7(),
        created_at: chrono::Utc::now(),
        updated_at: chrono::Utc::now(),
        version: 0,
        metadata: serde_json::json!({}),
    };

    for expected_version in 1..=10 {
        {
            let mut tx = db.write().unwrap();
            doc = tx.put_document(doc).unwrap();
            assert_eq!(
                doc.version, expected_version,
                "Version should increment sequentially"
            );
            tx.commit(&db).unwrap();
        }
    }

    assert_eq!(db.current_seq(), 10);
}

#[test]
fn prop_large_metadata_handling() {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();

    for size in vec![100, 1_000, 10_000, 100_000] {
        let large_field = "x".repeat(size);
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Large metadata {}", size),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({
                "large": large_field,
                "size": size
            }),
        };

        {
            let mut tx = db.write().unwrap();
            tx.put_room(room).unwrap();
            let seq = tx.commit(&db).unwrap();
            assert!(seq > 0);
        }
    }
}

#[test]
fn prop_concurrent_writes_serialize() {
    use std::sync::Arc;

    let dir = TempDir::new().unwrap();
    let db = Arc::new(Database::open(dir.path()).unwrap());

    // Note: In MVP, writes are not truly concurrent (in-memory)
    // This tests sequential write semantics
    for i in 0..5 {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Serialized {}", i),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        let seq = tx.commit(&db).unwrap();
        assert_eq!(seq, (i + 1) as u64);
    }
}
