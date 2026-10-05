//! Sliqtly's semantic layer: rooms, documents, memberships, room links and a
//! change feed, kept as ordered keys so every query is one prefix scan.
//! Written in the Ranger Rust dialect over the `Kv` trait, so the same code
//! runs on the Sliqtly kernel and, in the benchmark, on other KV engines.
//!
//! Key layout (ids are 7 base-36 digits, so string order is numeric order):
//! - `r/{room}`                       title, owner, created
//! - `d/{doc}`                        room, version, updated, title, body
//! - `rd/{room}/{max - updated}/{doc}` room's documents, newest first
//! - `mu/{user}/{room}`, `mr/{room}/{user}`   role
//! - `lf/{from}/{kind}/{to}`, `lt/{to}/{kind}/{from}`
//! - `ch/{seq}`                       change feed entry; `meta/chg` the last seq
use ranger::prelude::*;
use std::collections::HashSet;

use crate::kernel::{b36, unb36, Batch, Kernel, KvPair};

/// What the semantic layer needs from a key-value engine.
pub trait Kv {
    fn kv_get(&self, key: &str) -> Option<String>;
    /// Keys starting with `prefix` and >= `start`, in order, at most `limit`.
    fn kv_scan_keys(&self, prefix: &str, start: &str, limit: i64) -> Vec<String>;
    /// As `kv_scan_keys`, with the values.
    fn kv_scan(&self, prefix: &str, start: &str, limit: i64) -> Vec<KvPair>;
    /// Applies the batch atomically if its conditions hold: > 0 on success,
    /// < 0 when a condition failed.
    fn kv_commit(&mut self, b: &Batch) -> i64;
}

impl Kv for Kernel {
    fn kv_get(&self, key: &str) -> Option<String> {
        self.get(key)
    }
    fn kv_scan_keys(&self, prefix: &str, start: &str, limit: i64) -> Vec<String> {
        self.scan_keys(prefix, start, limit)
    }
    fn kv_scan(&self, prefix: &str, start: &str, limit: i64) -> Vec<KvPair> {
        self.scan(prefix, start, limit)
    }
    fn kv_commit(&mut self, b: &Batch) -> i64 {
        self.commit(b)
    }
}

const TS_MAX: i64 = 1000000000000000;

pub fn id7(n: i64) -> String {
    b36(n, 7)
}

fn tail_id(key: &str) -> i64 {
    let n = key.as_bytes().len();
    unb36(&key[n - 7..n])
}

fn fields(v: &str) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for part in v.split('\t') {
        out.push(part.to_string());
    }
    out
}

fn to_int(s: &str) -> i64 {
    match s.parse::<i64>() {
        Ok(v) => v,
        Err(_) => -1,
    }
}

fn room_doc_key(room: i64, updated: i64, doc: i64) -> String {
    format!("rd/{}/{}/{}", id7(room), b36(TS_MAX - updated, 10), id7(doc))
}

/// Ops for the bulk loader, which groups many into one commit.
pub fn room_ops(b: &mut Batch, room: i64, title: &str, owner: i64, ts: i64) {
    b.put(&format!("r/{}", id7(room)), &format!("{}\t{}\t{}", title, owner, ts));
}

pub fn member_ops(b: &mut Batch, room: i64, user: i64, role: &str) {
    b.put(&format!("mu/{}/{}", id7(user), id7(room)), role);
    b.put(&format!("mr/{}/{}", id7(room), id7(user)), role);
}

pub fn document_ops(b: &mut Batch, doc: i64, room: i64, title: &str, body: &str, ts: i64) {
    b.put(&format!("d/{}", id7(doc)), &format!("{}\t1\t{}\t{}\t{}", room, ts, title, body));
    b.put(&room_doc_key(room, ts, doc), "");
}

pub fn link_ops(b: &mut Batch, from: i64, to: i64, kind: &str) {
    b.put(&format!("lf/{}/{}/{}", id7(from), kind, id7(to)), "");
    b.put(&format!("lt/{}/{}/{}", id7(to), kind, id7(from)), "");
}

/// A Sliqtly database over any `Kv` engine.
pub struct Sliqtly {
    pub kv: Box<dyn Kv>,
    change: i64,
}

impl Sliqtly {
    pub fn new(kv: Box<dyn Kv>) -> Sliqtly {
        let mut change: i64 = 0;
        match kv.kv_get("meta/chg") {
            Some(v) => change = to_int(&v),
            None => {}
        }
        Sliqtly { kv: kv, change: change }
    }

    pub fn last_change(&self) -> i64 {
        self.change
    }

    fn note(&mut self, b: &mut Batch, what: &str) {
        let n = self.change + 1;
        b.put(&format!("ch/{}", b36(n, 8)), what);
        b.put("meta/chg", &format!("{}", n));
    }

    fn commit_noted(&mut self, b: &Batch) -> i64 {
        let r = self.kv.kv_commit(b);
        if r > 0 {
            self.change += 1;
        }
        r
    }

    /// Creates a room owned by `owner`; false if the id is taken.
    pub fn create_room(&mut self, room: i64, title: &str, owner: i64, ts: i64) -> bool {
        let mut b = Batch::new();
        b.expect_absent(&format!("r/{}", id7(room)));
        room_ops(&mut b, room, title, owner, ts);
        member_ops(&mut b, room, owner, "owner");
        self.note(&mut b, &format!("room {}", room));
        self.commit_noted(&b) > 0
    }

    pub fn add_member(&mut self, room: i64, user: i64, role: &str) -> bool {
        let mut b = Batch::new();
        member_ops(&mut b, room, user, role);
        self.note(&mut b, &format!("member {} {}", room, user));
        self.commit_noted(&b) > 0
    }

    pub fn create_document(&mut self, doc: i64, room: i64, title: &str, body: &str, ts: i64) -> bool {
        let mut b = Batch::new();
        b.expect_absent(&format!("d/{}", id7(doc)));
        document_ops(&mut b, doc, room, title, body, ts);
        self.note(&mut b, &format!("doc {}", doc));
        self.commit_noted(&b) > 0
    }

    /// Replaces the body; the new version, -1 if the document is missing,
    /// -2 if it changed underneath (compare-and-set on the old record).
    pub fn update_document(&mut self, doc: i64, body: &str, ts: i64) -> i64 {
        let key = format!("d/{}", id7(doc));
        let old = match self.kv.kv_get(&key) {
            Some(v) => v,
            None => return -1,
        };
        let f = fields(&old);
        let room = to_int(&f[0]);
        let version = to_int(&f[1]) + 1;
        let updated = to_int(&f[2]);
        let mut b = Batch::new();
        b.expect_value(&key, &old);
        b.put(&key, &format!("{}\t{}\t{}\t{}\t{}", room, version, ts, f[3], body));
        b.delete(&room_doc_key(room, updated, doc));
        b.put(&room_doc_key(room, ts, doc), "");
        self.note(&mut b, &format!("update {} {}", doc, version));
        if self.commit_noted(&b) > 0 {
            return version;
        }
        -2
    }

    pub fn get_document(&self, doc: i64) -> Option<String> {
        self.kv.kv_get(&format!("d/{}", id7(doc)))
    }

    /// The room's document ids, most recently updated first.
    pub fn list_documents(&self, room: i64, limit: i64) -> Vec<i64> {
        let keys = self.kv.kv_scan_keys(&format!("rd/{}/", id7(room)), "", limit);
        let mut out: Vec<i64> = Vec::new();
        for k in keys.iter() {
            out.push(tail_id(k));
        }
        out
    }

    pub fn rooms_for_user(&self, user: i64, limit: i64) -> Vec<i64> {
        let keys = self.kv.kv_scan_keys(&format!("mu/{}/", id7(user)), "", limit);
        let mut out: Vec<i64> = Vec::new();
        for k in keys.iter() {
            out.push(tail_id(k));
        }
        out
    }

    /// The user's role in the room, or "" if not a member.
    pub fn membership(&self, user: i64, room: i64) -> String {
        match self.kv.kv_get(&format!("mu/{}/{}", id7(user), id7(room))) {
            Some(v) => v,
            None => String::new(),
        }
    }

    pub fn link_rooms(&mut self, from: i64, to: i64, kind: &str) -> bool {
        let mut b = Batch::new();
        link_ops(&mut b, from, to, kind);
        self.note(&mut b, &format!("link {} {}", from, to));
        self.commit_noted(&b) > 0
    }

    /// Rooms linked from `room` (any kind), at most `limit`.
    pub fn neighbors(&self, room: i64, limit: i64) -> Vec<i64> {
        let keys = self.kv.kv_scan_keys(&format!("lf/{}/", id7(room)), "", limit);
        let mut out: Vec<i64> = Vec::new();
        for k in keys.iter() {
            out.push(tail_id(k));
        }
        out
    }

    /// Breadth-first walk over outgoing links, `fanout` neighbours per room,
    /// `depth` levels; the visited rooms in visiting order.
    pub fn traverse(&self, room: i64, depth: i64, fanout: i64) -> Vec<i64> {
        let mut seen: HashSet<i64> = HashSet::new();
        let mut order: Vec<i64> = Vec::new();
        let mut frontier: Vec<i64> = Vec::new();
        seen.insert(room);
        order.push(room);
        frontier.push(room);
        let mut level: i64 = 0;
        while level < depth {
            let mut next: Vec<i64> = Vec::new();
            for r in frontier.iter() {
                let ns = self.neighbors(*r, fanout);
                for n in ns.iter() {
                    if seen.insert(*n) {
                        order.push(*n);
                        next.push(*n);
                    }
                }
            }
            frontier = next;
            level += 1;
        }
        order
    }

    /// Change feed entries after `after`, as "seq what".
    pub fn watch(&self, after: i64, limit: i64) -> Vec<String> {
        let rows = self.kv.kv_scan("ch/", &format!("ch/{}", b36(after + 1, 8)), limit);
        let mut out: Vec<String> = Vec::new();
        for r in rows.iter() {
            out.push(format!("{} {}", unb36(&r.key[3..11]), r.value));
        }
        out
    }
}
