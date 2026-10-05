//! Writes a small demo database for trying the CLI:
//! `cargo run -p sliqtly-db --example seed -- /tmp/demo-db`

use chrono::Utc;
use sliqtly_store::record::{Document, Membership, Role, Room};
use sliqtly_store::Database;
use uuid::Uuid;

fn main() {
    let path = std::env::args().nth(1).expect("usage: seed <dir>");
    let db = Database::open(&path).expect("open");
    let user = Uuid::now_v7();
    for r in 0..3 {
        let room = Room {
            id: Uuid::now_v7(),
            title: format!("Room {r}"),
            description: None,
            created_by: user,
            created_at: Utc::now(),
            updated_at: Utc::now(),
            metadata: serde_json::json!({}),
        };
        let mut tx = db.write().unwrap();
        tx.put_room(room.clone()).unwrap();
        tx.add_membership(Membership {
            user_id: user,
            room_id: room.id,
            role: Role::Owner,
            joined_at: Utc::now(),
            metadata: serde_json::json!({}),
        })
        .unwrap();
        for d in 0..4 {
            tx.put_document(Document {
                id: Uuid::now_v7(),
                room_id: room.id,
                title: format!("Doc {r}.{d}"),
                created_by: user,
                created_at: Utc::now(),
                updated_at: Utc::now(),
                version: 0,
                metadata: serde_json::json!({}),
            })
            .unwrap();
        }
        tx.commit(&db).unwrap();
    }
    println!("seeded {path} at CommitSeq {}", db.current_seq());
}
