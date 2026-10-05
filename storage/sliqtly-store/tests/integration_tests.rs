//! Integration tests for sliqtly-store
//!
//! Tests complete workflows, crash recovery simulation, and stress scenarios.

use sliqtly_store::{open, Document, Membership, Room};
use std::sync::Arc;
use std::thread;
use tempfile::TempDir;
use uuid::Uuid;

#[test]
fn test_complete_workflow() {
    let dir = TempDir::new().unwrap();
    let db = open(dir.path()).unwrap();

    // Step 1: Create a room
    let room_id = Uuid::now_v7();
    let room = Room {
        id: room_id,
        title: "Collaboration Room".to_string(),
        description: Some("Test collaboration space".to_string()),
        created_by: Uuid::now_v7(),
        created_at: chrono::Utc::now(),
        updated_at: chrono::Utc::now(),
        metadata: serde_json::json!({}),
    };

    {
        let mut tx = db.write().unwrap();
        tx.put_room(room.clone()).unwrap();
        let seq = tx.commit(&db).unwrap();
        assert_eq!(seq, 1);
    }

    // Step 2: Add documents to the room
    for i in 0..5 {
        let doc = Document {
            id: Uuid::now_v7(),
            room_id,
            title: format!("Document {}", i),
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            version: 0,
            metadata: serde_json::json!({"index": i}),
        };

        {
            let mut tx = db.write().unwrap();
            tx.put_document(doc).unwrap();
            let seq = tx.commit(&db).unwrap();
            assert_eq!(seq, 1 + i as u64 + 1);
        }
    }

    // Step 3: Add members to the room
    for i in 0..3 {
        let membership = Membership {
            user_id: Uuid::now_v7(),
            room_id,
            role: if i == 0 {
                sliqtly_store::record::Role::Owner
            } else {
                sliqtly_store::record::Role::Editor
            },
            joined_at: chrono::Utc::now(),
            metadata: serde_json::json!({"order": i}),
        };

        {
            let mut tx = db.write().unwrap();
            tx.add_membership(membership).unwrap();
            tx.commit(&db).unwrap();
        }
    }

    // Verify final sequence number
    assert_eq!(db.current_seq(), 9); // 1 room + 5 docs + 3 members
}

#[test]
fn test_crash_recovery_simulation() {
    let dir = TempDir::new().unwrap();

    // Create and write data
    {
        let db = open(dir.path()).unwrap();

        for i in 0..10 {
            let mut tx = db.write().unwrap();
            let room = Room {
                id: Uuid::now_v7(),
                title: format!("Crash Test Room {}", i),
                description: None,
                created_by: Uuid::now_v7(),
                created_at: chrono::Utc::now(),
                updated_at: chrono::Utc::now(),
                metadata: serde_json::json!({}),
            };
            tx.put_room(room).unwrap();
            tx.commit(&db).unwrap();
        }

        assert_eq!(db.current_seq(), 10);
    }
    // Simulate crash: db is dropped

    // Reopen and verify consistency
    {
        let db = open(dir.path()).unwrap();
        // Storage is persistent: every committed write survives the reopen.
        assert_eq!(db.current_seq(), 10);
    }
}

#[test]
fn test_high_write_throughput() {
    let dir = TempDir::new().unwrap();
    let db = Arc::new(open(dir.path()).unwrap());

    let start = std::time::Instant::now();
    let write_count = 100;

    for i in 0..write_count {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Throughput Test {}", i),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        tx.commit(&db).unwrap();
    }

    let elapsed = start.elapsed();
    let ops_per_sec = write_count as f64 / elapsed.as_secs_f64();

    println!(
        "Wrote {} rooms in {:?} ({:.0} ops/sec)",
        write_count, elapsed, ops_per_sec
    );

    assert!(ops_per_sec > 100.0, "Should handle at least 100 ops/sec");
    assert_eq!(db.current_seq(), write_count as u64);
}

#[test]
fn test_concurrent_read_load() {
    let dir = TempDir::new().unwrap();
    let db = Arc::new(open(dir.path()).unwrap());

    // Seed with data
    {
        for i in 0..10 {
            let mut tx = db.write().unwrap();
            let room = Room {
                id: Uuid::now_v7(),
                title: format!("Read Load Test {}", i),
                description: None,
                created_by: Uuid::now_v7(),
                created_at: chrono::Utc::now(),
                updated_at: chrono::Utc::now(),
                metadata: serde_json::json!({}),
            };
            tx.put_room(room).unwrap();
            tx.commit(&db).unwrap();
        }
    }

    let start = std::time::Instant::now();
    let reader_count = 20;
    let reads_per_reader = 50;

    // Spawn readers
    let handles: Vec<_> = (0..reader_count)
        .map(|_| {
            let db_clone = db.clone();
            thread::spawn(move || {
                for _ in 0..reads_per_reader {
                    let tx = db_clone.read().unwrap();
                    assert_eq!(tx.seq(), 10);
                }
            })
        })
        .collect();

    for handle in handles {
        handle.join().unwrap();
    }

    let elapsed = start.elapsed();
    let total_reads = reader_count * reads_per_reader;
    let reads_per_sec = total_reads as f64 / elapsed.as_secs_f64();

    println!(
        "Read {} times in {:?} ({:.0} reads/sec)",
        total_reads, elapsed, reads_per_sec
    );

    assert!(
        reads_per_sec > 1000.0,
        "Should handle at least 1000 reads/sec"
    );
}

#[test]
fn test_write_after_many_reads() {
    let dir = TempDir::new().unwrap();
    let db = Arc::new(open(dir.path()).unwrap());

    // Write initial data
    {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: "Mixed Workload".to_string(),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        tx.commit(&db).unwrap();
    }

    // Lots of concurrent reads
    let read_handles: Vec<_> = (0..10)
        .map(|_| {
            let db_clone = db.clone();
            thread::spawn(move || {
                for _ in 0..100 {
                    let _tx = db_clone.read().unwrap();
                }
            })
        })
        .collect();

    // Interleaved writes (will block on read thread barrier in a real impl)
    for i in 0..5 {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Write {}", i),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        tx.commit(&db).unwrap();
    }

    // Wait for readers
    for handle in read_handles {
        handle.join().unwrap();
    }

    assert_eq!(db.current_seq(), 6); // 1 initial + 5 writes
}

#[test]
fn test_seq_never_reused() {
    let dir = TempDir::new().unwrap();
    let db = Arc::new(open(dir.path()).unwrap());

    let mut observed_seqs = Vec::new();

    for _ in 0..50 {
        let mut tx = db.write().unwrap();
        let room = Room {
            id: Uuid::now_v7(),
            title: "Seq Uniqueness Test".to_string(),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
            metadata: serde_json::json!({}),
        };
        tx.put_room(room).unwrap();
        let seq = tx.commit(&db).unwrap();
        observed_seqs.push(seq);
    }

    // Check all sequences are unique
    for i in 0..observed_seqs.len() {
        for j in (i + 1)..observed_seqs.len() {
            assert_ne!(
                observed_seqs[i], observed_seqs[j],
                "Sequence numbers must never be reused"
            );
        }
    }

    // Check monotonically increasing
    for i in 1..observed_seqs.len() {
        assert!(
            observed_seqs[i] > observed_seqs[i - 1],
            "Sequences must be strictly increasing"
        );
    }
}

#[test]
fn test_empty_database_operations() {
    let dir = TempDir::new().unwrap();
    let db = open(dir.path()).unwrap();

    // Multiple reads on empty DB
    for _ in 0..5 {
        let tx = db.read().unwrap();
        assert_eq!(tx.seq(), 0);
    }

    // Empty write (commit without changes) - seq unchanged
    {
        let tx = db.write().unwrap();
        let seq = tx.commit(&db).unwrap();
        assert_eq!(seq, 0); // NoChanges: seq unchanged
    }

    // Verify seq did not increment
    assert_eq!(db.current_seq(), 0);
}
