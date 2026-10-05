//! Storage benchmark: the Sliqtly kernel (Ranger Rust dialect) against redb,
//! Fjall, LMDB (heed), RocksDB and SQLite.
//!
//! sliqtly-bench raw <engine> <value_bytes> <dir>
//! sliqtly-bench semantic <engine> <scale> <ops> <dir>
//!
//! Each run prints JSON lines on stdout, one per measurement. Run one engine
//! per process (scripts/run_bench.sh does) so RSS and I/O counters belong to
//! that engine alone.

mod crash;
mod engines;
mod semantic_db;

use engines::Engine;
use semantic_db::{LoadOp, SemanticDb};
use serde_json::json;
use sliqtly_kernel::kernel::Batch;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

// ------------------------------------------------------------- helpers

struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^ (z >> 31)
    }
    fn below(&mut self, n: u64) -> u64 {
        self.next() % n
    }
    fn text(&mut self, len: usize) -> String {
        const A: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 ";
        let mut s = String::with_capacity(len);
        for _ in 0..len {
            s.push(A[self.below(A.len() as u64) as usize] as char);
        }
        s
    }
    fn shuffle<T>(&mut self, v: &mut [T]) {
        for i in (1..v.len()).rev() {
            let j = self.below(i as u64 + 1) as usize;
            v.swap(i, j);
        }
    }
}

struct Lat(Vec<u64>);

impl Lat {
    fn new() -> Lat {
        Lat(Vec::new())
    }
    fn time<T>(&mut self, f: impl FnOnce() -> T) -> T {
        let t = Instant::now();
        let r = f();
        self.0.push(t.elapsed().as_nanos() as u64);
        r
    }
    fn summary(&mut self, ops_per_sample: f64) -> serde_json::Value {
        self.0.sort_unstable();
        let n = self.0.len();
        let total: u64 = self.0.iter().sum();
        let pct = |p: f64| -> f64 {
            if n == 0 {
                return 0.0;
            }
            let i = ((p / 100.0) * n as f64).ceil() as usize;
            self.0[i.clamp(1, n) - 1] as f64 / 1000.0
        };
        json!({
            "samples": n,
            "ops_per_sec": if total == 0 { 0.0 } else { (n as f64 * ops_per_sample) / (total as f64 / 1e9) },
            "p50_us": pct(50.0), "p95_us": pct(95.0), "p99_us": pct(99.0), "p999_us": pct(99.9),
            "max_us": if n == 0 { 0.0 } else { self.0[n - 1] as f64 / 1000.0 },
        })
    }
}

fn proc_field(file: &str, field: &str) -> u64 {
    let s = std::fs::read_to_string(file).unwrap_or_default();
    for line in s.lines() {
        if let Some(rest) = line.strip_prefix(field) {
            let num: String = rest.chars().filter(|c| c.is_ascii_digit()).collect();
            return num.parse().unwrap_or(0);
        }
    }
    0
}

fn rss_kb() -> u64 {
    proc_field("/proc/self/status", "VmRSS:")
}
fn hwm_kb() -> u64 {
    proc_field("/proc/self/status", "VmHWM:")
}
fn anon_kb() -> u64 {
    proc_field("/proc/self/status", "RssAnon:")
}
/// Bytes this process caused to be written to storage, net of writes
/// cancelled by truncating or deleting dirty pages.
fn disk_writes() -> u64 {
    proc_field("/proc/self/io", "write_bytes:").saturating_sub(proc_field("/proc/self/io", "cancelled_write_bytes:"))
}

/// Bytes on disk (allocated blocks) and apparent file sizes under `dir`.
fn dir_bytes(dir: &Path) -> (u64, u64) {
    use std::os::unix::fs::MetadataExt;
    let (mut alloc, mut apparent) = (0, 0);
    if let Ok(rd) = std::fs::read_dir(dir) {
        for e in rd.flatten() {
            let m = e.metadata().unwrap();
            if m.is_dir() {
                let (a, b) = dir_bytes(&e.path());
                alloc += a;
                apparent += b;
            } else {
                alloc += m.blocks() * 512;
                apparent += m.len();
            }
        }
    }
    (alloc, apparent)
}

fn emit(suite: &str, engine: &str, param: &str, phase: &str, mut v: serde_json::Value) {
    let o = v.as_object_mut().unwrap();
    o.insert("suite".into(), json!(suite));
    o.insert("engine".into(), json!(engine));
    o.insert("param".into(), json!(param));
    o.insert("phase".into(), json!(phase));
    println!("{}", v);
}

fn key(i: u64) -> String {
    format!("key/{:012}", i)
}

// ------------------------------------------------------------- raw suite

fn raw(engine_name: &str, vsize: usize, dir: &Path) {
    let param = format!("{}", vsize);
    let n: u64 = match vsize {
        s if s <= 128 => 200_000,
        s if s <= 1024 => 100_000,
        s if s <= 10 * 1024 => 20_000,
        _ => 4_000,
    };
    let batch_n: usize = 1000.min((8 * 1024 * 1024 / vsize).max(1));
    let e = |phase: &str, v: serde_json::Value| emit("raw", engine_name, &param, phase, v);
    let mut rng = Rng(42 ^ vsize as u64);
    let mut logical_written: u64 = 0;
    if vsize > 200 {
        // SQLite's raw table switches to a rowid layout for large rows
        std::env::set_var("SLIQ_LARGE_VALUES", "1");
    }

    // fill_seq in its own directory
    {
        let d = dir.join("seq");
        let eng = engines::open(engine_name, &d);
        let mut lat = Lat::new();
        let mut i = 0;
        while i < n {
            let mut b = Batch::new();
            for j in i..(i + batch_n as u64).min(n) {
                b.put(&key(j), &rng.text(vsize));
            }
            lat.time(|| eng.commit(&b, false));
            i += batch_n as u64;
        }
        let tf = Instant::now();
        eng.flush();
        // throughput over commit time plus the final flush; generating the
        // data is not timed
        let secs = lat.0.iter().sum::<u64>() as f64 / 1e9 + tf.elapsed().as_secs_f64();
        let mut s = lat.summary(batch_n as f64);
        s["ops_per_sec"] = json!(n as f64 / secs);
        e("fill_seq", s);
        drop(eng);
        std::fs::remove_dir_all(&d).unwrap();
    }
    let writes0 = disk_writes();

    let d = dir.join("main");
    let mut eng = engines::open(engine_name, &d);
    let mut order: Vec<u64> = (0..n).collect();
    rng.shuffle(&mut order);
    {
        let mut lat = Lat::new();
        for chunk in order.chunks(batch_n) {
            let mut b = Batch::new();
            for j in chunk {
                b.put(&key(*j), &rng.text(vsize));
            }
            logical_written += (chunk.len() * (16 + vsize)) as u64;
            lat.time(|| eng.commit(&b, false));
        }
        let tf = Instant::now();
        eng.flush();
        let secs = lat.0.iter().sum::<u64>() as f64 / 1e9 + tf.elapsed().as_secs_f64();
        let mut s = lat.summary(batch_n as f64);
        s["ops_per_sec"] = json!(n as f64 / secs);
        e("fill_random", s);
    }
    let logical_live = n * (16 + vsize as u64);

    let reads = 100_000.min(n * 5);
    {
        let keys: Vec<String> = (0..reads).map(|_| key(rng.below(n))).collect();
        let mut lat = Lat::new();
        let mut bytes = 0;
        for k in &keys {
            bytes += lat.time(|| eng.get(k)).unwrap();
        }
        assert!(bytes > 0);
        e("get_random", lat.summary(1.0));
        let missing: Vec<String> = (0..reads / 4).map(|i| format!("key/{:012}x", i)).collect();
        let mut lat = Lat::new();
        for k in &missing {
            assert!(lat.time(|| eng.get(k)).is_none());
        }
        e("get_missing", lat.summary(1.0));
    }
    {
        // keys share their first 13 characters with up to 100 others
        let scans = 10_000.min(n);
        let limit = if vsize > 10 * 1024 { 20 } else { 100 };
        let mut lat = Lat::new();
        for _ in 0..scans {
            let p = format!("key/{:010}", rng.below(n.div_ceil(100)));
            let (c, _) = lat.time(|| eng.scan(&p, "", limit));
            assert!(c > 0);
        }
        e("prefix_scan", json!({"limit": limit}).as_object().unwrap().clone().into_iter().chain(lat.summary(1.0).as_object().unwrap().clone()).collect::<serde_json::Map<_, _>>().into());
    }
    {
        let scans = 2_000.min(n);
        let limit = if vsize >= 10 * 1024 { 100 } else { 1000 };
        let mut lat = Lat::new();
        for _ in 0..scans {
            let s = key(rng.below(n));
            lat.time(|| eng.scan("key/", &s, limit));
        }
        let mut v = lat.summary(1.0);
        v["limit"] = json!(limit);
        v["entries_per_sec"] = json!(v["ops_per_sec"].as_f64().unwrap() * limit as f64);
        e("range_scan", v);
    }
    {
        let mut lat = Lat::new();
        for _ in 0..10_000 {
            let keys: Vec<String> = (0..10).map(|_| key(rng.below(n))).collect();
            lat.time(|| eng.snapshot_get(&keys));
        }
        e("snapshot_read_10", lat.summary(1.0));
    }
    {
        // 1 writer (durable single-put commits) + 3 readers for 5 s
        let stop = std::sync::atomic::AtomicBool::new(false);
        let eng_ref: &dyn Engine = &*eng;
        let (wl, rls) = std::thread::scope(|s| {
            let mut readers = Vec::new();
            for r in 0..3 {
                let stop = &stop;
                readers.push(s.spawn(move || {
                    let mut rng = Rng(1000 + r);
                    let mut lat = Lat::new();
                    while !stop.load(std::sync::atomic::Ordering::Relaxed) {
                        let k = key(rng.below(n));
                        lat.time(|| eng_ref.get(&k));
                    }
                    lat
                }));
            }
            let mut wl = Lat::new();
            let mut wrng = Rng(77);
            let t = Instant::now();
            while t.elapsed() < Duration::from_secs(5) {
                let mut b = Batch::new();
                b.put(&key(wrng.below(n)), &wrng.text(vsize));
                wl.time(|| eng_ref.commit(&b, true));
            }
            stop.store(true, std::sync::atomic::Ordering::Relaxed);
            let rls: Vec<Lat> = readers.into_iter().map(|h| h.join().unwrap()).collect();
            (wl, rls)
        });
        logical_written += wl.0.len() as u64 * (16 + vsize as u64);
        let mut wl = wl;
        let mut all = Lat(rls.into_iter().flat_map(|l| l.0).collect());
        let reads_done = all.0.len();
        let mut rs = all.summary(1.0);
        rs["reads_per_sec_total"] = json!(reads_done as f64 / 5.0);
        e("concurrent_readers_3", rs);
        let mut ws = wl.summary(1.0);
        ws["commits_per_sec"] = json!(wl.0.len() as f64 / 5.0);
        e("concurrent_writer", ws);
    }
    {
        let ow = n / 2;
        let mut lat = Lat::new();
        let mut i = 0;
        while i < ow {
            let mut b = Batch::new();
            for _ in 0..batch_n.min((ow - i) as usize) {
                b.put(&key(rng.below(n)), &rng.text(vsize));
            }
            logical_written += (b.ops.len() * (16 + vsize)) as u64;
            lat.time(|| eng.commit(&b, false));
            i += batch_n as u64;
        }
        let tf = Instant::now();
        eng.flush();
        let secs = lat.0.iter().sum::<u64>() as f64 / 1e9 + tf.elapsed().as_secs_f64();
        let mut s = lat.summary(batch_n as f64);
        s["ops_per_sec"] = json!(ow as f64 / secs);
        e("overwrite", s);
    }
    {
        let per = (100 * 1024 / vsize).clamp(1, 800);
        let mut lat = Lat::new();
        for _ in 0..300 {
            let mut b = Batch::new();
            for _ in 0..per {
                b.put(&key(rng.below(n)), &rng.text(vsize));
            }
            logical_written += (per * (16 + vsize)) as u64;
            lat.time(|| eng.commit(&b, true));
        }
        let mut s = lat.summary(per as f64);
        s["ops_per_commit"] = json!(per);
        s["commits_per_sec"] = json!(s["ops_per_sec"].as_f64().unwrap() / per as f64);
        e("batch_commit_durable", s);
    }
    {
        let mut lat = Lat::new();
        for _ in 0..1000 {
            let mut b = Batch::new();
            b.put(&key(rng.below(n)), &rng.text(vsize));
            logical_written += 16 + vsize as u64;
            lat.time(|| eng.commit(&b, true));
        }
        e("sync_commit_1", lat.summary(1.0));
    }
    {
        let del = n / 4;
        let mut victims: Vec<u64> = (0..n).collect();
        rng.shuffle(&mut victims);
        let mut lat = Lat::new();
        for chunk in victims[..del as usize].chunks(batch_n) {
            let mut b = Batch::new();
            for j in chunk {
                b.delete(&key(*j));
            }
            lat.time(|| eng.commit(&b, false));
        }
        let tf = Instant::now();
        eng.flush();
        let secs = lat.0.iter().sum::<u64>() as f64 / 1e9 + tf.elapsed().as_secs_f64();
        let mut s = lat.summary(batch_n as f64);
        s["ops_per_sec"] = json!(del as f64 / secs);
        e("delete", s);
    }
    let live_after = (n - n / 4) * (16 + vsize as u64);
    let rss = rss_kb();
    let hwm = hwm_kb();
    let anon = anon_kb();
    eng.flush();
    drop(eng);
    let (alloc, apparent) = dir_bytes(&d);
    let writes = disk_writes() - writes0;
    {
        let t = Instant::now();
        eng = engines::open(engine_name, &d);
        let _ = eng.get(&key(order[order.len() - 1]));
        let open_ms = t.elapsed().as_secs_f64() * 1000.0;
        e("restart", json!({"open_ms": open_ms}));
    }
    e(
        "footprint",
        json!({
            "keys": n, "value_bytes": vsize,
            "logical_live_bytes_at_load": logical_live,
            "logical_live_bytes_end": live_after,
            "disk_alloc_bytes": alloc, "disk_apparent_bytes": apparent,
            "space_amp": alloc as f64 / live_after as f64,
            "rss_kb": rss, "peak_rss_kb": hwm, "anon_rss_kb": anon,
            "disk_write_bytes": writes, "logical_write_bytes": logical_written,
            "write_amp": writes as f64 / logical_written as f64,
        }),
    );
    drop(eng);
    std::fs::remove_dir_all(dir).ok();
}

// ------------------------------------------------------------- semantic suite

struct Dataset {
    rooms: i64,
    docs: i64,
    links: i64,
    members: i64,
    users: i64,
}

fn dataset(scale: f64) -> Dataset {
    let s = |n: f64| (n * scale).round().max(10.0) as i64;
    Dataset { rooms: s(100_000.0), docs: s(1_000_000.0), links: s(5_000_000.0), members: s(2_000_000.0), users: s(200_000.0) }
}

const KINDS: [&str; 3] = ["child", "dep", "ref"];
const ROLES: [&str; 3] = ["editor", "viewer", "admin"];

fn digest(h: &mut u64, s: &str) {
    for b in s.bytes() {
        *h = (*h ^ b as u64).wrapping_mul(0x100000001b3);
    }
    *h = (*h ^ 0xff).wrapping_mul(0x100000001b3);
}

fn digest_ids(h: &mut u64, ids: &[i64]) {
    for i in ids {
        digest(h, &i.to_string());
    }
    digest(h, "|");
}

fn semantic(engine: &str, scale: f64, ops: u64, dir: &Path) {
    let param = format!("{}", scale);
    let e = |phase: &str, v: serde_json::Value| emit("semantic", engine, &param, phase, v);
    let ds = dataset(scale);
    let mut rng = Rng(7);
    let writes0 = disk_writes();
    let mut db = semantic_db::open(engine, dir);
    db.set_sync(false);

    // ---- load: rooms, memberships, documents, links in commits of 5000 ops
    let t = Instant::now();
    let mut buf: Vec<LoadOp> = Vec::new();
    let mut logical: u64 = 0;
    let mut total_ops: u64 = 0;
    let flush = |db: &mut Box<dyn SemanticDb>, buf: &mut Vec<LoadOp>| {
        if !buf.is_empty() {
            db.load(buf);
            buf.clear();
        }
    };
    for r in 1..=ds.rooms {
        let title = format!("Room {}", r);
        logical += title.len() as u64 + 24;
        buf.push(LoadOp::Room { id: r, title, owner: 1 + rng.below(ds.users as u64) as i64, ts: r });
        if buf.len() >= 5000 {
            flush(&mut db, &mut buf);
        }
    }
    for _ in 0..ds.members {
        logical += 24;
        buf.push(LoadOp::Member {
            room: 1 + rng.below(ds.rooms as u64) as i64,
            user: 1 + rng.below(ds.users as u64) as i64,
            role: ROLES[rng.below(3) as usize],
        });
        if buf.len() >= 5000 {
            flush(&mut db, &mut buf);
        }
    }
    for d in 1..=ds.docs {
        let title = format!("Doc {}", d);
        let body = rng.text(200);
        logical += (title.len() + body.len()) as u64 + 32;
        buf.push(LoadOp::Doc { id: d, room: 1 + rng.below(ds.rooms as u64) as i64, title, body, ts: 1_000_000 + d });
        if buf.len() >= 5000 {
            flush(&mut db, &mut buf);
        }
    }
    for _ in 0..ds.links {
        let from = 1 + rng.below(ds.rooms as u64) as i64;
        let mut to = 1 + rng.below(ds.rooms as u64) as i64;
        if to == from {
            to = to % ds.rooms + 1;
        }
        logical += 24;
        buf.push(LoadOp::Link { from, to, kind: KINDS[rng.below(3) as usize] });
        if buf.len() >= 5000 {
            flush(&mut db, &mut buf);
        }
    }
    flush(&mut db, &mut buf);
    db.flush();
    total_ops += (ds.rooms + ds.members + ds.docs + ds.links) as u64;
    let load_secs = t.elapsed().as_secs_f64();
    e(
        "load",
        json!({"seconds": load_secs, "ops_per_sec": total_ops as f64 / load_secs,
               "rooms": ds.rooms, "documents": ds.docs, "links": ds.links, "memberships": ds.members, "users": ds.users}),
    );
    let load_rss = rss_kb();
    let load_anon = anon_kb();
    let load_hwm = hwm_kb();
    // sizes after a clean close, so open WAL files and preallocation are
    // settled the way each engine leaves them
    drop(db);
    let load_writes = disk_writes() - writes0;
    let (alloc, _) = dir_bytes(dir);
    e("load_footprint", json!({"disk_alloc_bytes": alloc, "logical_bytes": logical, "space_amp": alloc as f64 / logical as f64,
                                "disk_write_bytes": load_writes, "write_amp": load_writes as f64 / logical as f64,
                                "rss_kb": load_rss, "anon_rss_kb": load_anon, "peak_rss_kb": load_hwm}));
    let mut db = semantic_db::open(engine, dir);

    // ---- mixed workload, every write durable
    db.set_sync(true);
    let names = ["get_document", "list_room_documents", "update_document", "membership_lookup", "graph_neighbors", "graph_traverse3", "room_or_link_change"];
    let mut lats: Vec<Lat> = names.iter().map(|_| Lat::new()).collect();
    let mut h: u64 = 0xcbf29ce484222325;
    let mut next_room = ds.rooms + 1;
    let mut ts = 10_000_000i64;
    let t = Instant::now();
    for _ in 0..ops {
        let p = rng.below(100);
        ts += 1;
        if p < 50 {
            let d = 1 + rng.below(ds.docs as u64) as i64;
            let r = lats[0].time(|| db.get_document(d));
            digest(&mut h, r.as_deref().unwrap_or("-"));
        } else if p < 65 {
            let r = 1 + rng.below(ds.rooms as u64) as i64;
            let ids = lats[1].time(|| db.list_documents(r, 20));
            digest_ids(&mut h, &ids);
        } else if p < 75 {
            let d = 1 + rng.below(ds.docs as u64) as i64;
            let body = rng.text(200);
            let v = lats[2].time(|| db.update_document(d, &body, ts));
            digest_ids(&mut h, &[v]);
        } else if p < 85 {
            let u = 1 + rng.below(ds.users as u64) as i64;
            let (rooms, role) = lats[3].time(|| {
                let rooms = db.rooms_for_user(u, 50);
                let role = if let Some(r) = rooms.first() { db.membership(u, *r) } else { db.membership(u, 1) };
                (rooms, role)
            });
            digest_ids(&mut h, &rooms);
            digest(&mut h, &role);
        } else if p < 90 {
            let r = 1 + rng.below(ds.rooms as u64) as i64;
            let ids = lats[4].time(|| db.neighbors(r, 100));
            digest_ids(&mut h, &ids);
        } else if p < 95 {
            let r = 1 + rng.below(ds.rooms as u64) as i64;
            let ids = lats[5].time(|| db.traverse(r, 3, 5));
            digest_ids(&mut h, &ids);
        } else {
            let ok = if rng.below(2) == 0 {
                let id = next_room;
                next_room += 1;
                let owner = 1 + rng.below(ds.users as u64) as i64;
                let title = format!("Room {}", id);
                lats[6].time(|| db.create_room(id, &title, owner, ts))
            } else {
                let from = 1 + rng.below(ds.rooms as u64) as i64;
                let to = 1 + rng.below(ds.rooms as u64) as i64;
                let kind = KINDS[rng.below(3) as usize];
                lats[6].time(|| db.link_rooms(from, to, kind))
            };
            digest(&mut h, if ok { "1" } else { "0" });
        }
    }
    let secs = t.elapsed().as_secs_f64();
    e("mixed", json!({"ops": ops, "seconds": secs, "ops_per_sec": ops as f64 / secs, "digest": format!("{:016x}", h)}));
    for (i, name) in names.iter().enumerate() {
        e(&format!("op_{}", name), lats[i].summary(1.0));
    }

    // ---- watch: tail the change feed from recent points
    {
        let last = db.last_change();
        let mut lat = Lat::new();
        let mut wh: u64 = 0xcbf29ce484222325;
        for _ in 0..5000 {
            let after = rng.below(last.max(1) as u64) as i64;
            let entries = lat.time(|| db.watch(after, 100));
            for s in &entries {
                digest(&mut wh, s);
            }
        }
        let mut v = lat.summary(1.0);
        v["digest"] = json!(format!("{:016x}", wh));
        v["last_change"] = json!(last);
        e("watch_100", v);
    }
    let rss = rss_kb();
    let hwm = hwm_kb();
    let anon = anon_kb();
    db.flush();
    drop(db);
    let (alloc, apparent) = dir_bytes(dir);
    let t = Instant::now();
    let db = semantic_db::open(engine, dir);
    let _ = db.get_document(1);
    let open_ms = t.elapsed().as_secs_f64() * 1000.0;
    e("restart", json!({"open_ms": open_ms, "last_change": db.last_change()}));
    e("footprint", json!({"disk_alloc_bytes": alloc, "disk_apparent_bytes": apparent, "rss_kb": rss, "peak_rss_kb": hwm, "anon_rss_kb": anon,
                          "disk_write_bytes": disk_writes() - writes0}));
    drop(db);
    if std::env::var("KEEP_DIR").is_err() {
        std::fs::remove_dir_all(dir).ok();
    }
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    match a.get(1).map(|s| s.as_str()) {
        Some("raw") => raw(&a[2], a[3].parse().unwrap(), &PathBuf::from(&a[4])),
        Some("crash") => crash::run(&PathBuf::from(&a[2]), a[3].parse().unwrap(), a[4].parse().unwrap()),
        Some("crash-child") => crash::child(&PathBuf::from(&a[2]), a[3].parse().unwrap()),
        Some("open") => {
            let t = Instant::now();
            let k = sliqtly_kernel::kernel::Kernel::open(&a[2], false);
            println!("open {:?} keys {} replayed {} log {} live {} compactions {}", t.elapsed(), k.len(), k.replayed_bytes, k.file_bytes(), k.live_bytes(), k.compactions);
        }
        Some("semantic") => semantic(&a[2], a[3].parse().unwrap(), a[4].parse().unwrap(), &PathBuf::from(&a[5])),
        _ => {
            eprintln!("usage: sliqtly-bench raw <engine> <value_bytes> <dir> | semantic <engine> <scale> <ops> <dir> | crash <dir> <cycles> <seed>");
            eprintln!("engines: {:?}", engines::ENGINES);
            std::process::exit(2);
        }
    }
}
