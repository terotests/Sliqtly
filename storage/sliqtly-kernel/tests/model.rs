//! The kernel against a BTreeMap model: random puts, deletes, conditional
//! commits, prefix and range scans, inline and phased compaction, and
//! reopening with and without the index checkpoint.

use sliqtly_kernel::kernel::{Batch, Kernel, TOO_LARGE};
use std::collections::BTreeMap;

struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        self.0
    }
    fn below(&mut self, n: u64) -> u64 {
        self.next() % n
    }
}

fn dir(name: &str) -> String {
    let d = std::env::temp_dir().join(format!("sliqtly-kernel-{}-{}", name, std::process::id()));
    let _ = std::fs::remove_dir_all(&d);
    d.to_str().unwrap().to_string()
}

fn key(r: &mut Rng) -> String {
    // shared prefixes and some multibyte keys, to exercise the checkpoint's
    // prefix compression and byte-wise ordering
    match r.below(4) {
        0 => format!("a/{:03}", r.below(300)),
        1 => format!("a/{:03}/x{}", r.below(300), r.below(5)),
        2 => format!("b/é{}", r.below(200)),
        _ => format!("c/{}", r.below(1000)),
    }
}

fn check(k: &Kernel, model: &BTreeMap<String, String>, r: &mut Rng) {
    assert_eq!(k.len() as usize, model.len());
    let all = k.scan("", "", i64::MAX);
    let got: Vec<(String, String)> = all.into_iter().map(|p| (p.key, p.value)).collect();
    let want: Vec<(String, String)> = model.iter().map(|(a, b)| (a.clone(), b.clone())).collect();
    assert_eq!(got, want);
    for _ in 0..50 {
        let start = key(r);
        let prefix = ["", "a/", "a/1", "b/", "c/9"][r.below(5) as usize];
        let limit = 1 + r.below(40) as i64;
        let got: Vec<String> = k.scan_keys(prefix, &start, limit);
        let from = if prefix > start.as_str() { prefix.to_string() } else { start.clone() };
        let want: Vec<String> =
            model.range(from..).map(|(a, _)| a.clone()).take_while(|a| a.starts_with(prefix)).take(limit as usize).collect();
        assert_eq!(got, want, "scan prefix {prefix:?} start {start:?} limit {limit}");
    }
}

#[test]
fn kernel_matches_model() {
    let d = dir("model");
    let mut k = Kernel::open(&d, false);
    k.compact_min_bytes = 64 * 1024;
    k.compact_slack_bytes = 16 * 1024;
    let mut model: BTreeMap<String, String> = BTreeMap::new();
    let mut r = Rng(0x1234_5678_9abc_def1);
    let mut compaction = None;
    let mut phased_done = 0;
    for round in 0..4000 {
        let mut b = Batch::new();
        let mut cond_ok = true;
        if r.below(5) == 0 {
            let ck = key(&mut r);
            match (r.below(2), model.get(&ck)) {
                (0, Some(v)) => b.expect_value(&ck, v),
                (0, None) => b.expect_absent(&ck),
                (_, Some(_)) => {
                    b.expect_absent(&ck);
                    cond_ok = false;
                }
                (_, None) => {
                    b.expect_value(&ck, "nope");
                    cond_ok = false;
                }
            }
        }
        let mut next = model.clone();
        for _ in 0..1 + r.below(6) {
            let kk = key(&mut r);
            if r.below(4) == 0 {
                b.delete(&kk);
                next.remove(&kk);
            } else {
                let v = format!("{}-{}", round, "v".repeat(r.below(120) as usize));
                b.put(&kk, &v);
                next.insert(kk, v);
            }
        }
        let res = k.commit(&b);
        if cond_ok {
            assert!(res > 0, "round {round}: commit refused");
            model = next;
        } else {
            assert_eq!(res, -1, "round {round}: failed condition must refuse the batch");
        }
        // phased compaction interleaved with the commits
        if round % 700 == 350 {
            compaction = Some(k.compact_begin());
        }
        if let Some(c) = compaction.as_mut() {
            if k.compact_step(c, 37) {
                k.compact_catch_up(c);
                c.sync();
                let mut c = compaction.take().unwrap();
                k.compact_finish(&mut c);
                phased_done += 1;
            }
        }
        if round % 500 == 499 && compaction.is_none() {
            check(&k, &model, &mut r);
            if r.below(2) == 0 {
                k.close();
            }
            // without close the checkpoint is stale and the log is replayed
            k = Kernel::open(&d, false);
            k.compact_min_bytes = 64 * 1024;
            k.compact_slack_bytes = 16 * 1024;
            check(&k, &model, &mut r);
        }
    }
    assert!(phased_done >= 5, "phased compactions completed: {phased_done}");
    k.close();
    let _ = std::fs::remove_dir_all(&d);
}

#[test]
fn empty_batch_and_size_limits() {
    let d = dir("limits");
    let mut k = Kernel::open(&d, true);
    assert_eq!(k.commit(&Batch::new()), 0);
    let mut b = Batch::new();
    b.put(&"k".repeat(46656), "v");
    assert_eq!(k.commit(&b), TOO_LARGE);
    let mut b = Batch::new();
    b.put(&"k".repeat(46655), "v");
    assert_eq!(k.commit(&b), 1);
    k.close();
    let k = Kernel::open(&d, true);
    assert_eq!(k.len(), 1);
    let _ = std::fs::remove_dir_all(&d);
}

#[test]
fn torn_tail_is_cut_and_never_resurrected() {
    use std::io::Write;
    let d = dir("torn");
    let mut k = Kernel::open(&d, true);
    for i in 0..20 {
        let mut b = Batch::new();
        b.put(&format!("k{i:02}"), &format!("v{i}"));
        k.commit(&b);
    }
    let good = k.file_bytes();
    k.close();
    // a frame header with a body that never made it, then junk
    let mut f = std::fs::OpenOptions::new().append(true).open(format!("{d}/data.log")).unwrap();
    f.write_all(b"SQ0000000500000000010000000001xyz").unwrap();
    drop(f);
    let mut k = Kernel::open(&d, true);
    assert_eq!(k.len(), 20);
    assert_eq!(k.file_bytes(), good);
    assert!(k.recovered_cut > 0);
    let mut b = Batch::new();
    b.put("after", "ok");
    k.commit(&b);
    k.close();
    let k = Kernel::open(&d, true);
    assert_eq!(k.len(), 21);
    assert_eq!(k.get("after").as_deref(), Some("ok"));
    let _ = std::fs::remove_dir_all(&d);
}
