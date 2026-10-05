//! Smoke and recovery check, run the same on rustc and on Ranger targets.
use ranger::prelude::*;
mod kernel;
mod semantic;

use kernel::{Batch, Kernel};
use semantic::Sliqtly;

fn main() {
    let dir = "/tmp/sliqtly-kernel-smoke";
    let mut k = Kernel::open(dir, true);
    k.compact_min_bytes = 4096;
    let mut i: i64 = 0;
    while i < 300 {
        let mut b = Batch::new();
        b.put(&format!("k/{:05}", i), &format!("value-{}-é", i));
        if i % 3 == 0 && i > 0 {
            b.delete(&format!("k/{:05}", i - 1));
        }
        k.commit(&b);
        i += 1;
    }
    let mut uni = Batch::new();
    uni.put("ä/éa", "1");
    uni.put("ä/éb", "2");
    uni.put("ä/ö", "3");
    uni.put("ä/öö", "4");
    k.commit(&uni);
    let mut cas = Batch::new();
    cas.expect_value("k/00010", "wrong");
    cas.put("k/00010", "x");
    println!("cas {}", k.commit(&cas));
    let mut ok = Batch::new();
    ok.expect_absent("new/1");
    ok.put("new/1", "fresh");
    println!("absent {}", k.commit(&ok));
    println!("len {} seq {} compactions {}", k.len(), k.seq(), k.compactions);
    let rows = k.scan("k/0010", "", 5);
    for r in rows.iter() {
        println!("{} = {}", r.key, r.value);
    }
    k.close();
    let mut k2 = Kernel::open(dir, true);
    println!("reopen len {} seq {} cut {}", k2.len(), k2.seq(), k2.recovered_cut);
    let urows = k2.scan("ä/", "", 10);
    for r in urows.iter() {
        println!("uni {} = {}", r.key, r.value);
    }
    match k2.get("k/00299") {
        Some(v) => println!("last {}", v),
        None => println!("last missing"),
    }
    k2.compact();
    k2.close();
    let mut k3 = Kernel::open(dir, true);
    println!("after compact len {} seq {} file {} live {} replayed {}", k3.len(), k3.seq(), k3.file_bytes(), k3.live_bytes(), k3.replayed_bytes);
    let mut tail = Batch::new();
    tail.put("k/zzz", "after-checkpoint");
    tail.delete("k/00100");
    k3.commit(&tail);
    // no close: the checkpoint is older than the log
    let k4 = Kernel::open(dir, true);
    println!("unclean reopen len {} seq {} replayed {} zzz {} k100 {}", k4.len(), k4.seq(), k4.replayed_bytes, k4.contains("k/zzz"), k4.contains("k/00100"));

    // compaction in phases with commits in between
    let cdir = "/tmp/sliqtly-kernel-smoke/phased";
    let mut k5 = Kernel::open(cdir, false);
    k5.auto_compact = false;
    let mut j: i64 = 0;
    while j < 200 {
        let mut b = Batch::new();
        b.put(&format!("p/{:04}", j), &format!("v1-{}", j));
        k5.commit(&b);
        j += 1;
    }
    j = 0;
    while j < 200 {
        let mut b = Batch::new();
        b.put(&format!("p/{:04}", j), &format!("v2-{}", j));
        k5.commit(&b);
        j += 2;
    }
    let mut c = k5.compact_begin();
    let mut done = k5.compact_step(&mut c, 50);
    let mut mid = Batch::new();
    mid.put("p/0001", "changed-during");
    mid.put("p/0000", "early-key-changed");
    mid.delete("p/0199");
    mid.put("p/new", "inserted-during");
    k5.commit(&mid);
    while !done {
        done = k5.compact_step(&mut c, 50);
    }
    let mut late = Batch::new();
    late.put("p/0150", "changed-late");
    k5.commit(&late);
    c.sync();
    k5.compact_catch_up(&mut c);
    let mut later = Batch::new();
    later.put("p/0151", "after-catch-up");
    k5.commit(&later);
    k5.compact_finish(&mut c);
    c.release();
    let keys5 = ["p/0000", "p/0001", "p/0002", "p/0003", "p/0150", "p/0151", "p/0198", "p/0199", "p/new"];
    for kk in keys5.iter() {
        match k5.get(kk) {
            Some(v) => println!("{} -> {}", kk, v),
            None => println!("{} -> none", kk),
        }
    }
    println!("phased len {} file {} live {}", k5.len(), k5.file_bytes(), k5.live_bytes());
    k5.close();
    let k6 = Kernel::open(cdir, false);
    println!("phased reopen len {} seq {} p0001 {}", k6.len(), k6.seq(), k6.get("p/0001").unwrap_or(String::from("none")));

    let sdir = "/tmp/sliqtly-kernel-smoke/semantic";
    let mut db = Sliqtly::new(Box::new(Kernel::open(sdir, true)));
    db.create_room(1, "Lobby", 100, 1000);
    db.create_room(2, "Design", 100, 1001);
    db.create_room(3, "Ops", 101, 1002);
    println!("dup room {}", db.create_room(1, "Again", 100, 1003));
    db.add_member(2, 101, "editor");
    db.create_document(10, 2, "Spec", "first", 2000);
    db.create_document(11, 2, "Notes", "n", 2001);
    db.create_document(12, 2, "Plan", "p", 2002);
    println!("v {}", db.update_document(10, "second", 3000));
    println!("missing {}", db.update_document(99, "x", 3001));
    println!("docs {:?}", db.list_documents(2, 10));
    println!("rooms of 101 {:?}", db.rooms_for_user(101, 10));
    println!("role {} / {}", db.membership(101, 2), db.membership(102, 2));
    db.link_rooms(1, 2, "child");
    db.link_rooms(1, 3, "child");
    db.link_rooms(2, 3, "ref");
    db.link_rooms(3, 1, "ref");
    println!("neighbors {:?}", db.neighbors(1, 10));
    println!("traverse {:?}", db.traverse(2, 3, 5));
    let feed = db.watch(8, 3);
    for e in feed.iter() {
        println!("feed {}", e);
    }
    match db.get_document(10) {
        Some(v) => println!("doc10 {}", v),
        None => println!("doc10 missing"),
    }
    println!("changes {}", db.last_change());
    let db2 = Sliqtly::new(Box::new(Kernel::open(sdir, true)));
    println!("reopened changes {}", db2.last_change());
}
