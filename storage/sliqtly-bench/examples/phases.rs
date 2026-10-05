//! Where a large-value commit spends its time: prepare (frame build and
//! checksum), write, apply.
use sliqtly_kernel::kernel::{Batch, Kernel};
use std::time::Instant;

fn main() {
    let vsize: usize = std::env::args().nth(1).map(|s| s.parse().unwrap()).unwrap_or(10240);
    let dir = "/tmp/claude-0/phases";
    let _ = std::fs::remove_dir_all(dir);
    let mut k = Kernel::open(dir, false);
    let per = (8 << 20) / vsize;
    let (mut tp, mut tw, mut ta) = (0.0, 0.0, 0.0);
    let v = "x".repeat(vsize);
    for round in 0..20 {
        let mut b = Batch::new();
        for j in 0..per {
            b.put(&format!("key/{:012}", round * per + j), &v);
        }
        let t = Instant::now();
        let p = k.prepare(&b);
        tp += t.elapsed().as_secs_f64();
        let t = Instant::now();
        k.write_prepared(&p);
        tw += t.elapsed().as_secs_f64();
        let t = Instant::now();
        k.apply_prepared(&p, &b);
        ta += t.elapsed().as_secs_f64();
    }
    let n = (20 * per) as f64;
    println!("{} B values: prepare {:.1} µs/op, write {:.1} µs/op, apply {:.1} µs/op", vsize, tp / n * 1e6, tw / n * 1e6, ta / n * 1e6);
}
