//! Shared fixtures: databases written through `sliqtly_store`.
#![allow(dead_code)]

use chrono::Utc;
use serde_json::Value;
use sha2::{Digest, Sha256};
use sliqtly_store::key::{KeyBuilder, KeyType};
use sliqtly_store::record::{Document, Membership, Role, Room};
use sliqtly_store::Database;
use std::path::Path;
use std::process::{Command, Output};
use tempfile::TempDir;
use uuid::Uuid;

pub const BIN: &str = env!("CARGO_BIN_EXE_sliqtly-db");

pub fn cli(db: &Path, args: &[&str]) -> Output {
    Command::new(BIN)
        .arg("--db")
        .arg(db)
        .args(args)
        .output()
        .expect("run sliqtly-db")
}

pub fn stdout(o: &Output) -> String {
    String::from_utf8_lossy(&o.stdout).into_owned()
}

pub fn stderr(o: &Output) -> String {
    String::from_utf8_lossy(&o.stderr).into_owned()
}

pub fn json(o: &Output) -> Value {
    serde_json::from_slice(&o.stdout).unwrap_or_else(|e| panic!("{e}: {}{}", stdout(o), stderr(o)))
}

pub struct Seeded {
    pub dir: TempDir,
    pub rooms: Vec<Uuid>,
    pub docs: Vec<Uuid>,
}

pub fn room(title: &str) -> Room {
    let t = Utc::now();
    Room {
        id: Uuid::now_v7(),
        title: title.into(),
        description: None,
        created_by: Uuid::now_v7(),
        created_at: t,
        updated_at: t,
        metadata: serde_json::json!({}),
    }
}

pub fn doc(room_id: Uuid, title: &str) -> Document {
    Document {
        id: Uuid::now_v7(),
        room_id,
        title: title.into(),
        created_by: Uuid::now_v7(),
        created_at: Utc::now(),
        updated_at: Utc::now(),
        version: 0,
        metadata: serde_json::json!({}),
    }
}

/// 3 rooms, 5 documents, 2 memberships, 1 blob; one commit per write.
pub fn seed() -> Seeded {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();
    let mut rooms = Vec::new();
    let mut docs = Vec::new();
    for title in ["Alpha", "Beta", "Gamma"] {
        let r = room(title);
        let mut tx = db.write().unwrap();
        tx.put_room(r.clone()).unwrap();
        tx.commit(&db).unwrap();
        rooms.push(r.id);
    }
    for (i, title) in ["one", "two", "three", "four", "five"].iter().enumerate() {
        let d = doc(rooms[i % 2], title);
        let mut tx = db.write().unwrap();
        tx.put_document(d.clone()).unwrap();
        tx.commit(&db).unwrap();
        docs.push(d.id);
    }
    for (user, r) in [(Uuid::now_v7(), rooms[0]), (Uuid::now_v7(), rooms[1])] {
        let mut tx = db.write().unwrap();
        tx.add_membership(Membership {
            user_id: user,
            room_id: r,
            role: Role::Editor,
            joined_at: Utc::now(),
            metadata: serde_json::json!({}),
        })
        .unwrap();
        tx.commit(&db).unwrap();
    }
    let data = b"\x89PNG fake image bytes".to_vec();
    let id = format!("{:x}", Sha256::digest(&data));
    put_raw(
        &db,
        KeyBuilder::new(KeyType::Blob).push_str(&id).build(),
        data.clone(),
    );
    let meta = serde_json::json!({"id": id, "size": data.len(), "checksum": adler32(&data)});
    put_raw(
        &db,
        KeyBuilder::new(KeyType::Blob)
            .push_str(&id)
            .push_str("_meta")
            .build(),
        serde_json::to_vec(&meta).unwrap(),
    );
    Seeded { dir, rooms, docs }
}

pub fn put_raw(db: &Database, k: Vec<u8>, v: Vec<u8>) {
    let mut tx = db.write().unwrap();
    tx.put_raw(k, v).unwrap();
    tx.commit(db).unwrap();
}

pub fn raw(dir: &Path, k: Vec<u8>, v: Vec<u8>) {
    put_raw(&Database::open(dir).unwrap(), k, v);
}

pub fn adler32(data: &[u8]) -> String {
    let (mut a, mut b) = (1u32, 0u32);
    for &x in data {
        a = (a + x as u32) % 65521;
        b = (b + a) % 65521;
    }
    format!("{:08x}", (b << 16) | a)
}

/// Seeded commits: 3 rooms + 5 docs + 2 memberships + 2 blob keys.
pub const SEEDED_SEQ: u64 = 12;
