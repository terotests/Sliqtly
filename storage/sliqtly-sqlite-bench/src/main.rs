//! Sliqtly semantic workload: SliqtlyDB engines against SQLite.
//!
//! `sliqtly-sqlite-bench run --engine <statebin|log|sqlite> --durability <sync|nosync>`
//! runs one engine in this process and prints one JSON result line. Run each
//! engine in its own process (see `run.sh`) so RSS and caches do not mix.
//! `BENCH_KEEP=1` keeps the database directory for inspection with `sliqtly-db`.
//!
//! Engines:
//! - `statebin`: the `sliqtly-store` engine as it was (`FjallEngine`: whole
//!   store rewritten per commit, snapshot = full clone, no indexes maintained,
//!   so "documents in room" scans every document). It never fsyncs, so it is
//!   only comparable in the `nosync` column.
//! - `log`: `LogEngine` (WAL + checkpoint, O(1) snapshots) with the maintained
//!   `room_document` index.
//! - `sqlite`: SQLite in WAL mode, `synchronous=FULL` (sync) or `OFF`
//!   (nosync), BLOB primary keys, `WITHOUT ROWID`, an index on
//!   `documents(room_id, updated_at DESC)`, prepared statements.

use chrono::{DateTime, TimeZone, Utc};
use rusqlite::{params, Connection, OptionalExtension};
use sliqtly_store::key::{KeyBuilder, KeyType};
use sliqtly_store::log_engine::{Durability, LogOptions};
use sliqtly_store::record::{Document, Membership, Role, Room};
use sliqtly_store::Database;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};
use uuid::Uuid;

// ---------------------------------------------------------------- data

struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        // xorshift64*
        self.0 ^= self.0 >> 12;
        self.0 ^= self.0 << 25;
        self.0 ^= self.0 >> 27;
        self.0.wrapping_mul(0x2545_f491_4f6c_dd1d)
    }
    fn below(&mut self, n: usize) -> usize {
        (self.next() % n as u64) as usize
    }
    fn uuid(&mut self) -> Uuid {
        Uuid::from_u64_pair(self.next(), self.next())
    }
}

struct Dataset {
    rooms: Vec<Room>,
    docs: Vec<Document>,
    members: Vec<Membership>,
    users: Vec<Uuid>,
}

fn ts(n: i64) -> DateTime<Utc> {
    Utc.timestamp_opt(1_790_000_000 + n, 0).unwrap()
}

fn dataset(rooms: usize, docs_per_room: usize, users: usize, members_per_room: usize) -> Dataset {
    let mut r = Rng(0x5eed_1234_abcd_0001);
    let users: Vec<Uuid> = (0..users).map(|_| r.uuid()).collect();
    let mut out = Dataset {
        rooms: Vec::new(),
        docs: Vec::new(),
        members: Vec::new(),
        users: users.clone(),
    };
    for i in 0..rooms {
        let owner = users[r.below(users.len())];
        let room = Room {
            id: r.uuid(),
            title: format!("Room {i}"),
            description: Some(format!("Room number {i} for the benchmark")),
            created_by: owner,
            created_at: ts(i as i64),
            updated_at: ts(i as i64),
            metadata: serde_json::json!({"kind": "project", "n": i}),
        };
        for d in 0..docs_per_room {
            out.docs.push(Document {
                id: r.uuid(),
                room_id: room.id,
                title: format!("Document {i}.{d}"),
                created_by: users[r.below(users.len())],
                created_at: ts((i * docs_per_room + d) as i64),
                updated_at: ts((i * docs_per_room + d) as i64),
                version: 1,
                metadata: serde_json::json!({"words": r.below(5000), "tags": ["a", "b"]}),
            });
        }
        let mut seen = std::collections::HashSet::new();
        for m in 0..members_per_room {
            let u = if m == 0 {
                owner
            } else {
                users[r.below(users.len())]
            };
            if seen.insert(u) {
                out.members.push(Membership {
                    user_id: u,
                    room_id: room.id,
                    role: if m == 0 { Role::Owner } else { Role::Editor },
                    joined_at: ts(i as i64),
                    metadata: serde_json::json!({}),
                });
            }
        }
        out.rooms.push(room);
    }
    out
}

// ---------------------------------------------------------------- adapters

trait Store {
    fn load(&mut self, d: &Dataset, batch: usize);
    fn reopen(&mut self);
    fn get_document(&self, id: Uuid) -> Option<Document>;
    fn documents_in_room(&self, room: Uuid) -> Vec<Document>;
    fn rooms_for_user(&self, user: Uuid) -> Vec<Uuid>;
    /// Read the document, change its title, write it back: one transaction.
    fn update_document(&mut self, id: Uuid, title: &str);
    /// A room and its owner's membership: one transaction.
    fn create_room(&mut self, room: Room, owner: Membership);
}

struct Sliqtly {
    dir: PathBuf,
    db: Option<Database>,
    /// `statebin`: the original engine and write path, no index.
    original: bool,
    durability: Durability,
}

impl Sliqtly {
    fn open(&self) -> Database {
        if self.original {
            Database::open(&self.dir).unwrap()
        } else {
            Database::open_log_with(
                &self.dir,
                LogOptions {
                    durability: self.durability,
                    ..Default::default()
                },
            )
            .unwrap()
        }
    }

    fn db(&self) -> &Database {
        self.db.as_ref().unwrap()
    }

    /// The write path `put_document` had before index maintenance.
    fn put_doc_original(tx: &mut sliqtly_store::transaction::WriteTx, mut doc: Document) {
        doc.updated_at = Utc::now();
        doc.version += 1;
        let key = KeyBuilder::new(KeyType::Document).push_uuid(doc.id).build();
        tx.put_raw(key, serde_json::to_vec(&doc).unwrap()).unwrap();
    }
}

impl Store for Sliqtly {
    fn load(&mut self, d: &Dataset, batch: usize) {
        let db = self.db();
        for chunk in d.rooms.chunks(batch) {
            let mut tx = db.write().unwrap();
            for r in chunk {
                tx.put_room(r.clone()).unwrap();
            }
            tx.commit(db).unwrap();
        }
        for chunk in d.docs.chunks(batch) {
            let mut tx = db.write().unwrap();
            for doc in chunk {
                let mut doc = doc.clone();
                doc.version -= 1; // put_document bumps it back
                if self.original {
                    Self::put_doc_original(&mut tx, doc);
                } else {
                    tx.put_document(doc).unwrap();
                }
            }
            tx.commit(db).unwrap();
        }
        for chunk in d.members.chunks(batch) {
            let mut tx = db.write().unwrap();
            for m in chunk {
                tx.add_membership(m.clone()).unwrap();
            }
            tx.commit(db).unwrap();
        }
    }

    fn reopen(&mut self) {
        self.db = None;
        self.db = Some(self.open());
    }

    fn get_document(&self, id: Uuid) -> Option<Document> {
        self.db().read().unwrap().get_document(id).unwrap()
    }

    fn documents_in_room(&self, room: Uuid) -> Vec<Document> {
        let tx = self.db().read().unwrap();
        if self.original {
            tx.documents_in_room_scan(room).unwrap()
        } else {
            tx.documents_in_room(room).unwrap()
        }
    }

    fn rooms_for_user(&self, user: Uuid) -> Vec<Uuid> {
        self.db().read().unwrap().rooms_for_user(user).unwrap()
    }

    fn update_document(&mut self, id: Uuid, title: &str) {
        let db = self.db();
        let mut doc = db.read().unwrap().get_document(id).unwrap().unwrap();
        doc.title = title.to_string();
        let mut tx = db.write().unwrap();
        if self.original {
            Self::put_doc_original(&mut tx, doc);
        } else {
            tx.put_document(doc).unwrap();
        }
        tx.commit(db).unwrap();
    }

    fn create_room(&mut self, room: Room, owner: Membership) {
        let db = self.db();
        let mut tx = db.write().unwrap();
        tx.put_room(room).unwrap();
        tx.add_membership(owner).unwrap();
        tx.commit(db).unwrap();
    }
}

struct Sqlite {
    path: PathBuf,
    conn: Option<Connection>,
    sync: bool,
}

const SQLITE_SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS rooms (
  id BLOB PRIMARY KEY, title TEXT NOT NULL, description TEXT, created_by BLOB NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, metadata TEXT
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS documents (
  id BLOB PRIMARY KEY, room_id BLOB NOT NULL, title TEXT NOT NULL, version INTEGER NOT NULL,
  created_by BLOB NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, metadata TEXT
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS memberships (
  user_id BLOB NOT NULL, room_id BLOB NOT NULL, role TEXT NOT NULL, joined_at INTEGER NOT NULL,
  metadata TEXT, PRIMARY KEY (user_id, room_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS documents_room_updated ON documents(room_id, updated_at DESC);
";

fn micros(t: DateTime<Utc>) -> i64 {
    t.timestamp_micros()
}

fn from_micros(n: i64) -> DateTime<Utc> {
    DateTime::from_timestamp_micros(n).unwrap()
}

fn uuid_col(row: &rusqlite::Row, i: usize) -> Uuid {
    Uuid::from_slice(&row.get::<_, Vec<u8>>(i).unwrap()).unwrap()
}

fn doc_from_row(row: &rusqlite::Row) -> rusqlite::Result<Document> {
    Ok(Document {
        id: uuid_col(row, 0),
        room_id: uuid_col(row, 1),
        title: row.get(2)?,
        version: row.get::<_, i64>(3)? as u64,
        created_by: uuid_col(row, 4),
        created_at: from_micros(row.get(5)?),
        updated_at: from_micros(row.get(6)?),
        metadata: serde_json::from_str(&row.get::<_, String>(7)?).unwrap(),
    })
}

const DOC_COLS: &str = "id, room_id, title, version, created_by, created_at, updated_at, metadata";

impl Sqlite {
    fn connect(&self) -> Connection {
        let c = Connection::open(&self.path).unwrap();
        c.pragma_update(None, "journal_mode", "WAL").unwrap();
        c.pragma_update(None, "synchronous", if self.sync { "FULL" } else { "OFF" })
            .unwrap();
        c.execute_batch(SQLITE_SCHEMA).unwrap();
        c.set_prepared_statement_cache_capacity(64);
        c
    }

    fn c(&self) -> &Connection {
        self.conn.as_ref().unwrap()
    }

    fn insert_room(c: &Connection, r: &Room) {
        c.prepare_cached("INSERT INTO rooms VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)")
            .unwrap()
            .execute(params![
                r.id.as_bytes().as_slice(),
                r.title,
                r.description,
                r.created_by.as_bytes().as_slice(),
                micros(r.created_at),
                micros(r.updated_at),
                r.metadata.to_string()
            ])
            .unwrap();
    }

    fn insert_member(c: &Connection, m: &Membership) {
        c.prepare_cached("INSERT INTO memberships VALUES (?1, ?2, ?3, ?4, ?5)")
            .unwrap()
            .execute(params![
                m.user_id.as_bytes().as_slice(),
                m.room_id.as_bytes().as_slice(),
                format!("{:?}", m.role).to_lowercase(),
                micros(m.joined_at),
                m.metadata.to_string()
            ])
            .unwrap();
    }
}

impl Store for Sqlite {
    fn load(&mut self, d: &Dataset, batch: usize) {
        let c = self.conn.as_mut().unwrap();
        for chunk in d.rooms.chunks(batch) {
            let tx = c.transaction().unwrap();
            for r in chunk {
                Self::insert_room(&tx, r);
            }
            tx.commit().unwrap();
        }
        for chunk in d.docs.chunks(batch) {
            let tx = c.transaction().unwrap();
            for doc in chunk {
                tx.prepare_cached("INSERT INTO documents VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)")
                    .unwrap()
                    .execute(params![
                        doc.id.as_bytes().as_slice(),
                        doc.room_id.as_bytes().as_slice(),
                        doc.title,
                        doc.version as i64,
                        doc.created_by.as_bytes().as_slice(),
                        micros(doc.created_at),
                        micros(doc.updated_at),
                        doc.metadata.to_string()
                    ])
                    .unwrap();
            }
            tx.commit().unwrap();
        }
        for chunk in d.members.chunks(batch) {
            let tx = c.transaction().unwrap();
            for m in chunk {
                Self::insert_member(&tx, m);
            }
            tx.commit().unwrap();
        }
    }

    fn reopen(&mut self) {
        self.conn = None;
        self.conn = Some(self.connect());
        // Touch the schema so the open cost includes reading it, as a first query would.
        let _: i64 = self
            .c()
            .query_row("SELECT count(*) FROM sqlite_schema", [], |r| r.get(0))
            .unwrap();
    }

    fn get_document(&self, id: Uuid) -> Option<Document> {
        self.c()
            .prepare_cached(&format!("SELECT {DOC_COLS} FROM documents WHERE id = ?1"))
            .unwrap()
            .query_row([id.as_bytes().as_slice()], doc_from_row)
            .optional()
            .unwrap()
    }

    fn documents_in_room(&self, room: Uuid) -> Vec<Document> {
        self.c()
            .prepare_cached(&format!(
                "SELECT {DOC_COLS} FROM documents WHERE room_id = ?1"
            ))
            .unwrap()
            .query_map([room.as_bytes().as_slice()], doc_from_row)
            .unwrap()
            .map(|r| r.unwrap())
            .collect()
    }

    fn rooms_for_user(&self, user: Uuid) -> Vec<Uuid> {
        self.c()
            .prepare_cached("SELECT room_id FROM memberships WHERE user_id = ?1")
            .unwrap()
            .query_map([user.as_bytes().as_slice()], |r| Ok(uuid_col(r, 0)))
            .unwrap()
            .map(|r| r.unwrap())
            .collect()
    }

    fn update_document(&mut self, id: Uuid, title: &str) {
        let c = self.conn.as_mut().unwrap();
        let tx = c
            .transaction_with_behavior(rusqlite::TransactionBehavior::Immediate)
            .unwrap();
        let mut doc = tx
            .prepare_cached(&format!("SELECT {DOC_COLS} FROM documents WHERE id = ?1"))
            .unwrap()
            .query_row([id.as_bytes().as_slice()], doc_from_row)
            .unwrap();
        doc.title = title.to_string();
        doc.version += 1;
        doc.updated_at = Utc::now();
        tx.prepare_cached(
            "UPDATE documents SET title = ?2, version = ?3, updated_at = ?4 WHERE id = ?1",
        )
        .unwrap()
        .execute(params![
            id.as_bytes().as_slice(),
            doc.title,
            doc.version as i64,
            micros(doc.updated_at)
        ])
        .unwrap();
        tx.commit().unwrap();
    }

    fn create_room(&mut self, room: Room, owner: Membership) {
        let c = self.conn.as_mut().unwrap();
        let tx = c.transaction().unwrap();
        Self::insert_room(&tx, &room);
        Self::insert_member(&tx, &owner);
        tx.commit().unwrap();
    }
}

// ---------------------------------------------------------------- measurement

#[derive(serde::Serialize)]
struct OpStats {
    ops: usize,
    ops_per_sec: f64,
    p50_us: f64,
    p95_us: f64,
    p99_us: f64,
    max_us: f64,
}

fn measure(n: usize, mut f: impl FnMut(usize)) -> OpStats {
    let mut lat = Vec::with_capacity(n);
    let start = Instant::now();
    for i in 0..n {
        let t = Instant::now();
        f(i);
        lat.push(t.elapsed());
    }
    let total = start.elapsed();
    lat.sort();
    let pct = |p: f64| -> f64 {
        if lat.is_empty() {
            return 0.0;
        }
        let i = ((lat.len() as f64 * p).ceil() as usize).clamp(1, lat.len()) - 1;
        lat[i].as_secs_f64() * 1e6
    };
    OpStats {
        ops: n,
        ops_per_sec: n as f64 / total.as_secs_f64().max(1e-9),
        p50_us: pct(0.50),
        p95_us: pct(0.95),
        p99_us: pct(0.99),
        max_us: lat.last().copied().unwrap_or(Duration::ZERO).as_secs_f64() * 1e6,
    }
}

fn dir_bytes(p: &Path) -> u64 {
    std::fs::read_dir(p)
        .map(|it| {
            it.filter_map(|e| e.ok())
                .map(|e| {
                    let m = e.metadata().unwrap();
                    if m.is_dir() {
                        dir_bytes(&e.path())
                    } else {
                        m.len()
                    }
                })
                .sum()
        })
        .unwrap_or(0)
}

fn peak_rss_kb() -> u64 {
    std::fs::read_to_string("/proc/self/status")
        .ok()
        .and_then(|s| {
            s.lines()
                .find(|l| l.starts_with("VmHWM:"))
                .and_then(|l| l.split_whitespace().nth(1)?.parse().ok())
        })
        .unwrap_or(0)
}

fn arg(args: &[String], name: &str, default: &str) -> String {
    args.iter()
        .position(|a| a == name)
        .and_then(|i| args.get(i + 1).cloned())
        .unwrap_or_else(|| default.to_string())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.get(1).map(String::as_str) != Some("run") {
        eprintln!("usage: sliqtly-sqlite-bench run --engine statebin|log|sqlite --durability sync|nosync [--rooms N] [--docs-per-room N] [--users N] [--members-per-room N] [--reads N] [--writes N] [--dir DIR]");
        std::process::exit(2);
    }
    let engine = arg(&args, "--engine", "log");
    let durability = arg(&args, "--durability", "sync");
    let rooms: usize = arg(&args, "--rooms", "1000").parse().unwrap();
    let dpr: usize = arg(&args, "--docs-per-room", "20").parse().unwrap();
    let users: usize = arg(&args, "--users", "2000").parse().unwrap();
    let mpr: usize = arg(&args, "--members-per-room", "5").parse().unwrap();
    let reads: usize = arg(&args, "--reads", "20000").parse().unwrap();
    let writes: usize = arg(&args, "--writes", "1000").parse().unwrap();
    let base = PathBuf::from(arg(&args, "--dir", "/tmp/sliqtly-bench"));
    let dir = base.join(format!("{engine}-{durability}"));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).unwrap();
    let sync = durability == "sync";

    let data = dataset(rooms, dpr, users, mpr);
    let mut store: Box<dyn Store> = match engine.as_str() {
        "sqlite" => {
            let mut s = Sqlite {
                path: dir.join("bench.sqlite"),
                conn: None,
                sync,
            };
            s.conn = Some(s.connect());
            Box::new(s)
        }
        "statebin" | "log" => {
            let mut s = Sliqtly {
                dir: dir.clone(),
                db: None,
                original: engine == "statebin",
                durability: if sync {
                    Durability::Sync
                } else {
                    Durability::NoSync
                },
            };
            s.db = Some(s.open());
            Box::new(s)
        }
        other => panic!("unknown engine {other}"),
    };

    let rows = data.rooms.len() + data.docs.len() + data.members.len();
    let t = Instant::now();
    store.load(&data, 1000);
    let load_s = t.elapsed().as_secs_f64();

    let t = Instant::now();
    store.reopen();
    let open_ms = t.elapsed().as_secs_f64() * 1e3;

    let mut r = Rng(42);
    let doc_ids: Vec<Uuid> = (0..reads)
        .map(|_| data.docs[r.below(data.docs.len())].id)
        .collect();
    let get = measure(reads, |i| {
        assert!(store.get_document(doc_ids[i]).is_some());
    });

    let n_list = (reads / 10).max(1);
    let room_ids: Vec<Uuid> = (0..n_list)
        .map(|_| data.rooms[r.below(data.rooms.len())].id)
        .collect();
    let list = measure(n_list, |i| {
        assert_eq!(store.documents_in_room(room_ids[i]).len(), dpr);
    });

    let user_ids: Vec<Uuid> = (0..reads)
        .map(|_| data.users[r.below(data.users.len())])
        .collect();
    let member = measure(reads, |i| {
        std::hint::black_box(store.rooms_for_user(user_ids[i]));
    });

    let upd_ids: Vec<Uuid> = (0..writes)
        .map(|_| data.docs[r.below(data.docs.len())].id)
        .collect();
    let update = measure(writes, |i| {
        store.update_document(upd_ids[i], &format!("edited {i}"))
    });

    let new_rooms: Vec<(Room, Membership)> = (0..writes)
        .map(|i| {
            let owner = data.users[r.below(data.users.len())];
            let room = Room {
                id: r.uuid(),
                title: format!("New room {i}"),
                description: None,
                created_by: owner,
                created_at: Utc::now(),
                updated_at: Utc::now(),
                metadata: serde_json::json!({}),
            };
            let m = Membership {
                user_id: owner,
                room_id: room.id,
                role: Role::Owner,
                joined_at: Utc::now(),
                metadata: serde_json::json!({}),
            };
            (room, m)
        })
        .collect();
    let mut it = new_rooms.into_iter();
    let create = measure(writes, |_| {
        let (room, m) = it.next().unwrap();
        store.create_room(room, m)
    });

    // Correctness spot check: an updated document reads back.
    let last = upd_ids[writes - 1];
    assert!(store
        .get_document(last)
        .unwrap()
        .title
        .starts_with("edited"));

    drop(store);
    let disk = dir_bytes(&dir);
    let result = serde_json::json!({
        "engine": engine,
        "durability": durability,
        "dataset": {"rooms": rooms, "documents": data.docs.len(), "memberships": data.members.len(), "users": users},
        "load": {"rows": rows, "seconds": load_s, "rows_per_sec": rows as f64 / load_s},
        "open_ms": open_ms,
        "get_document": get,
        "documents_in_room": list,
        "rooms_for_user": member,
        "update_document": update,
        "create_room": create,
        "disk_bytes": disk,
        "peak_rss_kb": peak_rss_kb(),
    });
    println!("{result}");
    if std::env::var_os("BENCH_KEEP").is_none() {
        let _ = std::fs::remove_dir_all(&dir);
    }
}
