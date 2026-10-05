//! The semantic workload's interface, implemented by Sliqtly's semantic layer
//! (the Ranger-dialect `semantic.rs`) over any `Engine`, and by SQLite tables
//! with indexes for each query. Both answer every query identically, which
//! the benchmark checks with a digest of all answers.

use crate::engines::{self, sqlite_connect, sqlite_set_sync, Engine};
use rusqlite::{params, OptionalExtension};
use sliqtly_kernel::kernel::{Batch, KvPair};
use sliqtly_kernel::semantic::{self, Kv, Sliqtly};
use std::collections::HashSet;
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

pub enum LoadOp {
    Room { id: i64, title: String, owner: i64, ts: i64 },
    Member { room: i64, user: i64, role: &'static str },
    Doc { id: i64, room: i64, title: String, body: String, ts: i64 },
    Link { from: i64, to: i64, kind: &'static str },
}

pub trait SemanticDb {
    fn load(&mut self, ops: &[LoadOp]);
    fn set_sync(&mut self, sync: bool);
    fn flush(&mut self);
    fn create_room(&mut self, room: i64, title: &str, owner: i64, ts: i64) -> bool;
    fn add_member(&mut self, room: i64, user: i64, role: &str) -> bool;
    fn create_document(&mut self, doc: i64, room: i64, title: &str, body: &str, ts: i64) -> bool;
    fn update_document(&mut self, doc: i64, body: &str, ts: i64) -> i64;
    fn get_document(&self, doc: i64) -> Option<String>;
    fn list_documents(&self, room: i64, limit: i64) -> Vec<i64>;
    fn rooms_for_user(&self, user: i64, limit: i64) -> Vec<i64>;
    fn membership(&self, user: i64, room: i64) -> String;
    fn link_rooms(&mut self, from: i64, to: i64, kind: &str) -> bool;
    fn neighbors(&self, room: i64, limit: i64) -> Vec<i64>;
    fn traverse(&self, room: i64, depth: i64, fanout: i64) -> Vec<i64>;
    fn watch(&self, after: i64, limit: i64) -> Vec<String>;
    fn last_change(&self) -> i64;
}

// ------------------------------------------------ Sliqtly semantic layer

/// Any `Engine` seen through the dialect's `Kv` trait.
struct EngineKv {
    e: Box<dyn Engine>,
    sync: Arc<AtomicBool>,
}

impl Kv for EngineKv {
    fn kv_get(&self, key: &str) -> Option<String> {
        self.e.get_value(key)
    }
    fn kv_scan_keys(&self, prefix: &str, start: &str, limit: i64) -> Vec<String> {
        self.e.scan_keys(prefix, start, limit as usize)
    }
    fn kv_scan(&self, prefix: &str, start: &str, limit: i64) -> Vec<KvPair> {
        self.e.scan_pairs(prefix, start, limit as usize)
    }
    fn kv_commit(&mut self, b: &Batch) -> i64 {
        if self.e.commit(b, self.sync.load(Ordering::Relaxed)) { 1 } else { -1 }
    }
}

pub struct SliqtlyLayer {
    db: Sliqtly,
    sync: Arc<AtomicBool>,
    flusher: Box<dyn Fn()>,
}

impl SliqtlyLayer {
    pub fn open(engine: &str, dir: &Path) -> SliqtlyLayer {
        let sync = Arc::new(AtomicBool::new(false));
        let e = engines::open(engine, dir);
        let e: Arc<Box<dyn Engine>> = Arc::new(e);
        let kv = EngineKv { e: Box::new(ArcEngine(e.clone())), sync: sync.clone() };
        let flusher = Box::new(move || e.flush());
        SliqtlyLayer { db: Sliqtly::new(Box::new(kv)), sync, flusher }
    }
}

struct ArcEngine(Arc<Box<dyn Engine>>);

impl Engine for ArcEngine {
    fn name(&self) -> &'static str {
        self.0.name()
    }
    fn commit(&self, b: &Batch, sync: bool) -> bool {
        self.0.commit(b, sync)
    }
    fn get(&self, key: &str) -> Option<usize> {
        self.0.get(key)
    }
    fn get_value(&self, key: &str) -> Option<String> {
        self.0.get_value(key)
    }
    fn scan(&self, p: &str, s: &str, l: usize) -> (usize, usize) {
        self.0.scan(p, s, l)
    }
    fn scan_keys(&self, p: &str, s: &str, l: usize) -> Vec<String> {
        self.0.scan_keys(p, s, l)
    }
    fn scan_pairs(&self, p: &str, s: &str, l: usize) -> Vec<KvPair> {
        self.0.scan_pairs(p, s, l)
    }
    fn snapshot_get(&self, keys: &[String]) -> usize {
        self.0.snapshot_get(keys)
    }
    fn flush(&self) {
        self.0.flush()
    }
}

impl SemanticDb for SliqtlyLayer {
    fn load(&mut self, ops: &[LoadOp]) {
        let mut b = Batch::new();
        for op in ops {
            match op {
                LoadOp::Room { id, title, owner, ts } => {
                    semantic::room_ops(&mut b, *id, title, *owner, *ts);
                    semantic::member_ops(&mut b, *id, *owner, "owner");
                }
                LoadOp::Member { room, user, role } => semantic::member_ops(&mut b, *room, *user, role),
                LoadOp::Doc { id, room, title, body, ts } => semantic::document_ops(&mut b, *id, *room, title, body, *ts),
                LoadOp::Link { from, to, kind } => semantic::link_ops(&mut b, *from, *to, kind),
            }
        }
        assert!(self.db.kv.kv_commit(&b) > 0);
    }
    fn set_sync(&mut self, sync: bool) {
        self.sync.store(sync, Ordering::Relaxed);
    }
    fn flush(&mut self) {
        (self.flusher)();
    }
    fn create_room(&mut self, room: i64, title: &str, owner: i64, ts: i64) -> bool {
        self.db.create_room(room, title, owner, ts)
    }
    fn add_member(&mut self, room: i64, user: i64, role: &str) -> bool {
        self.db.add_member(room, user, role)
    }
    fn create_document(&mut self, doc: i64, room: i64, title: &str, body: &str, ts: i64) -> bool {
        self.db.create_document(doc, room, title, body, ts)
    }
    fn update_document(&mut self, doc: i64, body: &str, ts: i64) -> i64 {
        self.db.update_document(doc, body, ts)
    }
    fn get_document(&self, doc: i64) -> Option<String> {
        self.db.get_document(doc)
    }
    fn list_documents(&self, room: i64, limit: i64) -> Vec<i64> {
        self.db.list_documents(room, limit)
    }
    fn rooms_for_user(&self, user: i64, limit: i64) -> Vec<i64> {
        self.db.rooms_for_user(user, limit)
    }
    fn membership(&self, user: i64, room: i64) -> String {
        self.db.membership(user, room)
    }
    fn link_rooms(&mut self, from: i64, to: i64, kind: &str) -> bool {
        self.db.link_rooms(from, to, kind)
    }
    fn neighbors(&self, room: i64, limit: i64) -> Vec<i64> {
        self.db.neighbors(room, limit)
    }
    fn traverse(&self, room: i64, depth: i64, fanout: i64) -> Vec<i64> {
        self.db.traverse(room, depth, fanout)
    }
    fn watch(&self, after: i64, limit: i64) -> Vec<String> {
        self.db.watch(after, limit)
    }
    fn last_change(&self) -> i64 {
        self.db.last_change()
    }
}

// ------------------------------------------------ SQLite tables

const SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS rooms (id INTEGER PRIMARY KEY, title TEXT NOT NULL, owner INTEGER NOT NULL, created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS documents (id INTEGER PRIMARY KEY, room_id INTEGER NOT NULL, version INTEGER NOT NULL,
  updated_at INTEGER NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS documents_room_updated ON documents(room_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS memberships (user_id INTEGER NOT NULL, room_id INTEGER NOT NULL, role TEXT NOT NULL,
  PRIMARY KEY (user_id, room_id)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS memberships_room_user ON memberships(room_id, user_id);
CREATE TABLE IF NOT EXISTS room_links (from_room INTEGER NOT NULL, kind TEXT NOT NULL, to_room INTEGER NOT NULL,
  PRIMARY KEY (from_room, kind, to_room)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS links_to_kind_from ON room_links(to_room, kind, from_room);
CREATE TABLE IF NOT EXISTS changes (seq INTEGER PRIMARY KEY, what TEXT NOT NULL);
";

pub struct SqliteSemantic {
    c: rusqlite::Connection,
    change: i64,
}

impl SqliteSemantic {
    pub fn open(dir: &Path) -> SqliteSemantic {
        std::fs::create_dir_all(dir).unwrap();
        let c = sqlite_connect(&dir.join("semantic.sqlite"));
        c.execute_batch(SCHEMA).unwrap();
        let change: i64 = c.query_row("SELECT COALESCE(MAX(seq), 0) FROM changes", [], |r| r.get(0)).unwrap();
        SqliteSemantic { c, change }
    }

    fn write<T>(&mut self, what: String, f: impl FnOnce(&rusqlite::Transaction) -> Option<T>) -> Option<T> {
        let tx = self.c.transaction_with_behavior(rusqlite::TransactionBehavior::Immediate).unwrap();
        let out = f(&tx);
        if out.is_none() {
            return None;
        }
        let n = self.change + 1;
        tx.prepare_cached("INSERT INTO changes (seq, what) VALUES (?1, ?2)").unwrap().execute(params![n, what]).unwrap();
        tx.commit().unwrap();
        self.change = n;
        out
    }

    fn ids(&self, sql: &str, a: i64, limit: i64) -> Vec<i64> {
        let mut st = self.c.prepare_cached(sql).unwrap();
        let rows = st.query_map(params![a, limit], |r| r.get::<_, i64>(0)).unwrap();
        rows.map(|r| r.unwrap()).collect()
    }
}

impl SemanticDb for SqliteSemantic {
    fn load(&mut self, ops: &[LoadOp]) {
        let tx = self.c.transaction().unwrap();
        {
            let mut room = tx.prepare_cached("INSERT INTO rooms (id, title, owner, created) VALUES (?1, ?2, ?3, ?4)").unwrap();
            let mut mem = tx.prepare_cached("INSERT OR REPLACE INTO memberships (user_id, room_id, role) VALUES (?1, ?2, ?3)").unwrap();
            let mut doc = tx
                .prepare_cached("INSERT INTO documents (id, room_id, version, updated_at, title, body) VALUES (?1, ?2, 1, ?3, ?4, ?5)")
                .unwrap();
            let mut link = tx.prepare_cached("INSERT OR REPLACE INTO room_links (from_room, kind, to_room) VALUES (?1, ?2, ?3)").unwrap();
            for op in ops {
                match op {
                    LoadOp::Room { id, title, owner, ts } => {
                        room.execute(params![id, title, owner, ts]).unwrap();
                        mem.execute(params![owner, id, "owner"]).unwrap();
                    }
                    LoadOp::Member { room, user, role } => {
                        mem.execute(params![user, room, role]).unwrap();
                    }
                    LoadOp::Doc { id, room, title, body, ts } => {
                        doc.execute(params![id, room, ts, title, body]).unwrap();
                    }
                    LoadOp::Link { from, to, kind } => {
                        link.execute(params![from, kind, to]).unwrap();
                    }
                }
            }
        }
        tx.commit().unwrap();
    }
    fn set_sync(&mut self, sync: bool) {
        sqlite_set_sync(&self.c, sync);
    }
    fn flush(&mut self) {
        self.c.execute_batch("PRAGMA wal_checkpoint(FULL)").unwrap();
    }
    fn create_room(&mut self, room: i64, title: &str, owner: i64, ts: i64) -> bool {
        self.write(format!("room {}", room), |tx| {
            let n = tx
                .prepare_cached("INSERT OR IGNORE INTO rooms (id, title, owner, created) VALUES (?1, ?2, ?3, ?4)")
                .unwrap()
                .execute(params![room, title, owner, ts])
                .unwrap();
            if n == 0 {
                return None;
            }
            tx.prepare_cached("INSERT OR REPLACE INTO memberships (user_id, room_id, role) VALUES (?1, ?2, 'owner')")
                .unwrap()
                .execute(params![owner, room])
                .unwrap();
            Some(())
        })
        .is_some()
    }
    fn add_member(&mut self, room: i64, user: i64, role: &str) -> bool {
        self.write(format!("member {} {}", room, user), |tx| {
            tx.prepare_cached("INSERT OR REPLACE INTO memberships (user_id, room_id, role) VALUES (?1, ?2, ?3)")
                .unwrap()
                .execute(params![user, room, role])
                .unwrap();
            Some(())
        })
        .is_some()
    }
    fn create_document(&mut self, doc: i64, room: i64, title: &str, body: &str, ts: i64) -> bool {
        self.write(format!("doc {}", doc), |tx| {
            let n = tx
                .prepare_cached(
                    "INSERT OR IGNORE INTO documents (id, room_id, version, updated_at, title, body) VALUES (?1, ?2, 1, ?3, ?4, ?5)",
                )
                .unwrap()
                .execute(params![doc, room, ts, title, body])
                .unwrap();
            if n == 0 { None } else { Some(()) }
        })
        .is_some()
    }
    fn update_document(&mut self, doc: i64, body: &str, ts: i64) -> i64 {
        let cur: Option<i64> = self
            .c
            .prepare_cached("SELECT version FROM documents WHERE id = ?1")
            .unwrap()
            .query_row([doc], |r| r.get(0))
            .optional()
            .unwrap();
        let Some(v) = cur else { return -1 };
        let version = v + 1;
        let r = self.write(format!("update {} {}", doc, version), |tx| {
            let n = tx
                .prepare_cached("UPDATE documents SET version = ?1, updated_at = ?2, body = ?3 WHERE id = ?4 AND version = ?5")
                .unwrap()
                .execute(params![version, ts, body, doc, v])
                .unwrap();
            if n == 0 { None } else { Some(version) }
        });
        r.unwrap_or(-2)
    }
    fn get_document(&self, doc: i64) -> Option<String> {
        self.c
            .prepare_cached("SELECT room_id, version, updated_at, title, body FROM documents WHERE id = ?1")
            .unwrap()
            .query_row([doc], |r| {
                Ok(format!(
                    "{}\t{}\t{}\t{}\t{}",
                    r.get::<_, i64>(0)?,
                    r.get::<_, i64>(1)?,
                    r.get::<_, i64>(2)?,
                    r.get::<_, String>(3)?,
                    r.get::<_, String>(4)?
                ))
            })
            .optional()
            .unwrap()
    }
    fn list_documents(&self, room: i64, limit: i64) -> Vec<i64> {
        self.ids("SELECT id FROM documents WHERE room_id = ?1 ORDER BY updated_at DESC, id ASC LIMIT ?2", room, limit)
    }
    fn rooms_for_user(&self, user: i64, limit: i64) -> Vec<i64> {
        self.ids("SELECT room_id FROM memberships WHERE user_id = ?1 ORDER BY room_id LIMIT ?2", user, limit)
    }
    fn membership(&self, user: i64, room: i64) -> String {
        self.c
            .prepare_cached("SELECT role FROM memberships WHERE user_id = ?1 AND room_id = ?2")
            .unwrap()
            .query_row([user, room], |r| r.get(0))
            .optional()
            .unwrap()
            .unwrap_or_default()
    }
    fn link_rooms(&mut self, from: i64, to: i64, kind: &str) -> bool {
        self.write(format!("link {} {}", from, to), |tx| {
            tx.prepare_cached("INSERT OR REPLACE INTO room_links (from_room, kind, to_room) VALUES (?1, ?2, ?3)")
                .unwrap()
                .execute(params![from, kind, to])
                .unwrap();
            Some(())
        })
        .is_some()
    }
    fn neighbors(&self, room: i64, limit: i64) -> Vec<i64> {
        self.ids("SELECT to_room FROM room_links WHERE from_room = ?1 ORDER BY kind, to_room LIMIT ?2", room, limit)
    }
    fn traverse(&self, room: i64, depth: i64, fanout: i64) -> Vec<i64> {
        let mut seen = HashSet::new();
        let mut order = vec![room];
        let mut frontier = vec![room];
        seen.insert(room);
        for _ in 0..depth {
            let mut next = Vec::new();
            for r in &frontier {
                for n in self.neighbors(*r, fanout) {
                    if seen.insert(n) {
                        order.push(n);
                        next.push(n);
                    }
                }
            }
            frontier = next;
        }
        order
    }
    fn watch(&self, after: i64, limit: i64) -> Vec<String> {
        let mut st = self.c.prepare_cached("SELECT seq, what FROM changes WHERE seq > ?1 ORDER BY seq LIMIT ?2").unwrap();
        let rows = st
            .query_map(params![after, limit], |r| Ok(format!("{} {}", r.get::<_, i64>(0)?, r.get::<_, String>(1)?)))
            .unwrap();
        rows.map(|r| r.unwrap()).collect()
    }
    fn last_change(&self) -> i64 {
        self.change
    }
}

pub fn open(engine: &str, dir: &Path) -> Box<dyn SemanticDb> {
    if engine == "sqlite" {
        Box::new(SqliteSemantic::open(dir))
    } else {
        Box::new(SliqtlyLayer::open(engine, dir))
    }
}
