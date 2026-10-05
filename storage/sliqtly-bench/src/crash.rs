//! Crash harness for the Sliqtly kernel.
//!
//! `crash <dir> <cycles> <seed>` runs `cycles` rounds of: start a child
//! process that commits durably, kill it with SIGKILL at a random moment,
//! sometimes damage the log past the last acknowledged commit (a torn or
//! never-synced write after a power cut), reopen the store and compare it with
//! a model of the acknowledged commits.
//!
//! The child acknowledges a commit only after `commit` has returned, which in
//! durable mode is after fdatasync. Each batch is generated from the seed and
//! its sequence number, so the parent can regenerate the one batch that may
//! have been in flight. After recovery the store must hold exactly the
//! acknowledged batches, or those plus the in-flight batch; anything else
//! (a lost acknowledged write, a partial batch, a resurrected write) fails.

use sliqtly_kernel::kernel::{Batch, Kernel};
use std::collections::BTreeMap;
use std::io::{BufRead, BufReader, Write};
use std::os::unix::fs::MetadataExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

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
}

const KEYS: u64 = 400;

/// The batch committed as sequence number `seq`. Its compare-and-set
/// conditions read the current state through `get`, which is the store in
/// the child and the model in the parent; they agree whenever the store is
/// correct.
fn batch_for(seed: u64, seq: i64, get: &dyn Fn(&str) -> Option<String>) -> Batch {
    let mut r = Rng(seed ^ (seq as u64).wrapping_mul(0x2545F4914F6CDD1D));
    let mut b = Batch::new();
    let ops = 1 + r.below(8);
    for i in 0..ops {
        let key = format!("k/{:04}", r.below(KEYS));
        match r.below(10) {
            0 | 1 => b.delete(&key),
            2 => {
                // compare-and-set on the current value
                match get(&key) {
                    Some(v) => b.expect_value(&key, &v),
                    None => b.expect_absent(&key),
                }
                b.put(&key, &format!("cas-{}-{}", seq, i));
            }
            _ => {
                let len = match r.below(10) {
                    0 => 3000 + r.below(5000) as usize,
                    _ => r.below(300) as usize,
                };
                let mut v = format!("{}:{}:", seq, i);
                while v.len() < len {
                    v.push(char::from(b'a' + (r.below(26) as u8)));
                }
                if r.below(20) == 0 {
                    v.push_str("é😀");
                }
                b.put(&key, &v);
            }
        }
    }
    b
}

fn apply(model: &mut BTreeMap<String, String>, b: &Batch) {
    for c in &b.conds {
        let cur = model.get(&c.key);
        let ok = if c.kind == 1 { cur.is_none() } else { cur == Some(&c.value) };
        assert!(ok, "generated condition must hold on the model");
    }
    for op in &b.ops {
        if op.kind == 1 {
            model.insert(op.key.clone(), op.value.clone());
        } else {
            model.remove(&op.key);
        }
    }
}

fn open_kernel(dir: &Path) -> Kernel {
    let mut k = Kernel::open(dir.to_str().unwrap(), true);
    // small, so compaction and its checkpoint run often under the kills
    k.compact_min_bytes = 192 * 1024;
    k.compact_slack_bytes = 32 * 1024;
    k
}

/// The child: commit durably forever, acknowledging each commit on stdout.
pub fn child(dir: &Path, seed: u64) {
    let mut k = open_kernel(dir);
    let out = std::io::stdout();
    let mut out = out.lock();
    let log = dir.join("data.log");
    loop {
        let seq = k.seq() + 1;
        let b = {
            let kr = &k;
            batch_for(seed, seq, &|key| kr.get(key))
        };
        let r = k.commit(&b);
        assert_eq!(r, seq, "commit failed in the child");
        let ino = std::fs::metadata(&log).map(|m| m.ino()).unwrap_or(0);
        writeln!(out, "ack {} {} {}", seq, k.file_bytes(), ino).unwrap();
        out.flush().unwrap();
    }
}

/// Damage past `from` (the end of the last acknowledged commit): what a
/// power cut can leave of writes that were never acknowledged. The log is
/// preallocated with zeros, so the damage is aimed at the unacknowledged
/// bytes actually written (`from..data_end`) and the boundary after them.
fn damage(log: &Path, from: u64, r: &mut Rng) -> &'static str {
    use std::os::unix::fs::FileExt;
    let f = std::fs::OpenOptions::new().read(true).write(true).open(log).unwrap();
    let size = f.metadata().unwrap().len();
    // a frame here is at most a few hundred KB; past it is preallocated zeros
    let window = size.saturating_sub(from).min(1 << 20) as usize;
    let mut buf = vec![0u8; window];
    if window > 0 {
        f.read_exact_at(&mut buf, from).unwrap();
    }
    let data_end = from + buf.iter().rposition(|b| *b != 0).map(|p| p as u64 + 1).unwrap_or(0);
    match r.below(10) {
        0..=3 => "none",
        4 | 5 => {
            let at = from + r.below(data_end - from + 1);
            f.set_len(at).unwrap();
            if at < data_end { "truncate-unacked" } else { "truncate-at-end" }
        }
        6 | 7 => {
            let n = 1 + r.below(200) as usize;
            let mut junk: Vec<u8> = (0..n).map(|_| r.below(256) as u8).collect();
            if r.below(2) == 0 {
                // looks like a frame header
                junk[0] = b'S';
                if n > 1 {
                    junk[1] = b'Q';
                }
            }
            f.write_all_at(&junk, data_end).unwrap();
            "garbage-after-data"
        }
        _ => {
            if data_end > from {
                for _ in 0..1 + r.below(4) {
                    let at = from + r.below(data_end - from);
                    f.write_all_at(&[r.below(256) as u8], at).unwrap();
                }
                "flip-unacked-bytes"
            } else {
                "none(no unacked bytes)"
            }
        }
    }
}

pub fn run(dir: &Path, cycles: u64, seed: u64) {
    let _ = std::fs::remove_dir_all(dir);
    std::fs::create_dir_all(dir).unwrap();
    let exe = std::env::current_exe().unwrap();
    let log = dir.join("data.log");
    let mut model: BTreeMap<String, String> = BTreeMap::new();
    let mut acked_seq: i64 = 0;
    let mut acked_len: u64 = 0;
    let mut acked_ino: u64 = 0;
    let mut r = Rng(seed);
    let mut stats: BTreeMap<String, u64> = BTreeMap::new();
    let mut in_flight_applied: u64 = 0;
    let mut compactions_seen: u64 = 0;
    let mut last_ino: u64 = 0;
    let started = Instant::now();
    for cycle in 0..cycles {
        let mut child = Command::new(&exe)
            .arg("crash-child")
            .arg(dir)
            .arg(seed.to_string())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let stdout = child.stdout.take().unwrap();
        let mut lines = BufReader::new(stdout).lines();
        // kill after 0..12 acknowledgements, then after a random delay that
        // lands anywhere in the next commit, compaction or checkpoint
        let want = r.below(13);
        let mut got = 0;
        let mut acks: Vec<(i64, u64, u64)> = Vec::new();
        while got < want {
            match lines.next() {
                Some(Ok(l)) => {
                    acks.push(parse_ack(&l));
                    got += 1;
                }
                _ => break,
            }
        }
        if r.below(3) > 0 {
            std::thread::sleep(Duration::from_micros(r.below(400)));
        }
        child.kill().unwrap();
        child.wait().unwrap();
        for l in lines.map_while(Result::ok) {
            acks.push(parse_ack(&l));
        }
        for (seq, len, ino) in acks {
            assert_eq!(seq, acked_seq + 1, "acknowledgements out of order");
            let b = batch_for(seed, seq, &|k| model.get(k).cloned());
            apply(&mut model, &b);
            acked_seq = seq;
            acked_len = len;
            acked_ino = ino;
        }
        // simulate a power cut only while the log is the file the last
        // acknowledgement was written to (a compaction may have replaced it)
        let ino_now = std::fs::metadata(&log).map(|m| m.ino()).unwrap_or(0);
        if ino_now != last_ino {
            compactions_seen += 1;
            last_ino = ino_now;
        }
        let what = if ino_now == acked_ino && acked_ino != 0 { damage(&log, acked_len, &mut r) } else { "none(new log)" };
        *stats.entry(what.to_string()).or_default() += 1;

        let mut k = open_kernel(dir);
        let rec = k.seq();
        if rec == acked_seq + 1 {
            // the in-flight batch was complete on disk
            let b = batch_for(seed, rec, &|key| model.get(key).cloned());
            apply(&mut model, &b);
            acked_seq = rec;
            in_flight_applied += 1;
        } else if rec != acked_seq {
            panic!("cycle {}: recovered seq {} but {} acknowledged ({})", cycle, rec, acked_seq, what);
        }
        let rows = k.scan("", "", i64::MAX);
        let got: BTreeMap<String, String> = rows.into_iter().map(|p| (p.key, p.value)).collect();
        if got != model {
            let missing: Vec<_> = model.keys().filter(|k| !got.contains_key(*k)).take(5).collect();
            let extra: Vec<_> = got.keys().filter(|k| !model.contains_key(*k)).take(5).collect();
            let wrong: Vec<_> = model.iter().filter(|(k, v)| got.get(*k).map(|g| g != *v).unwrap_or(false)).map(|(k, _)| k).take(5).collect();
            panic!("cycle {}: state differs after {} (missing {:?}, extra {:?}, wrong {:?})", cycle, what, missing, extra, wrong);
        }
        // the next writer must start from a clean frame boundary either way
        if r.below(2) == 0 {
            k.close();
            *stats.entry("verify-close".to_string()).or_default() += 1;
        }
        drop(k);
        // the parent's recovery may have cut the file; the cut point is the
        // new acknowledged end
        acked_len = std::fs::metadata(&log).map(|m| m.len()).unwrap_or(0);
        acked_ino = std::fs::metadata(&log).map(|m| m.ino()).unwrap_or(0);
        if (cycle + 1) % 1000 == 0 {
            eprintln!(
                "{} cycles, {} acknowledged commits, {} keys, {:.0}s",
                cycle + 1,
                acked_seq,
                model.len(),
                started.elapsed().as_secs_f64()
            );
        }
    }
    println!(
        "{}",
        serde_json::json!({
            "suite": "crash", "cycles": cycles, "acknowledged_commits": acked_seq,
            "in_flight_commits_found_complete": in_flight_applied, "keys_at_end": model.len(),
            "faults": stats, "compactions_seen": compactions_seen, "seconds": started.elapsed().as_secs_f64(), "acknowledged_writes_lost": 0,
        })
    );
}

fn parse_ack(l: &str) -> (i64, u64, u64) {
    let p: Vec<&str> = l.split(' ').collect();
    assert_eq!(p[0], "ack", "unexpected child output {l}");
    (p[1].parse().unwrap(), p[2].parse().unwrap(), p[3].parse().unwrap())
}
