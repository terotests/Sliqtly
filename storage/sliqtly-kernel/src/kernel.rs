//! Sliqtly kernel: an ordered key-value store written in the Ranger Rust
//! dialect. rustc builds it against the `ranger` prelude crate; `rgrc` lowers
//! the same file and writes the other targets from it.
//!
//! Storage is one append-only log. Every commit is one checksummed frame, so a
//! batch is applied entirely or not at all. In durable mode the frame is
//! fdatasync'ed before the commit returns. Recovery replays frames and cuts the
//! log at the first frame that is short or fails its checksum (a torn write).
//!
//! An in-memory B+tree maps each live key to the offset and length of its value
//! in the log. On Rust the log is memory-mapped for reads; other targets use
//! positional reads. When dead bytes outweigh live bytes the log is rewritten
//! (compaction). A checkpoint of the index (`index.hint`) is written on close
//! and after compaction, so opening replays only the frames written after it.
//!
//! Frame: `SQ` + payload length (10 digits) + checksum a (10) + checksum b
//! (10), then the payload: seq (20) + op count (10) + per op `P`/`D` + key
//! length (3 base-36 digits) + value length (5) + key + value. Lengths are
//! UTF-8 bytes, so a key is at most 46655 bytes and a value 60466175.
use ranger::prelude::*;

#[ranger::target(rust)]
mod osfs {
    use std::fs::{File, OpenOptions};
    use std::mem::ManuallyDrop;
    use std::os::unix::fs::FileExt;
    use std::os::unix::io::{FromRawFd, IntoRawFd};

    /// Address space reserved for a log mapping. Pages past the end of the
    /// file are never read: readers stay below the committed length.
    const MAP_LEN: usize = 1 << 40;

    fn file(fd: i64) -> ManuallyDrop<File> {
        ManuallyDrop::new(unsafe { File::from_raw_fd(fd as i32) })
    }
    pub fn mkdirs(dir: &str) {
        std::fs::create_dir_all(dir).unwrap();
    }
    pub fn exists(path: &str) -> bool {
        std::path::Path::new(path).exists()
    }
    pub fn open(path: &str) -> i64 {
        let f = OpenOptions::new().read(true).write(true).create(true).open(path).unwrap();
        f.into_raw_fd() as i64
    }
    pub fn close(fd: i64) {
        let f = unsafe { File::from_raw_fd(fd as i32) };
        drop(f);
    }
    pub fn size(fd: i64) -> i64 {
        file(fd).metadata().unwrap().len() as i64
    }
    /// Writes in 1 MiB pieces: on Linux one multi-megabyte buffered write
    /// was measured an order of magnitude slower than the same bytes in
    /// pieces (45 ms against 4 ms for 8 MiB on ext4).
    pub fn pwrite(fd: i64, off: i64, data: &str) {
        let f = file(fd);
        let bytes = data.as_bytes();
        let mut at = 0;
        while at < bytes.len() {
            let end = (at + (1 << 20)).min(bytes.len());
            f.write_all_at(&bytes[at..end], off as u64 + at as u64).unwrap();
            at = end;
        }
    }
    fn text(bytes: &[u8]) -> String {
        match std::str::from_utf8(bytes) {
            Ok(s) => s.to_string(),
            Err(_) => String::from_utf8_lossy(bytes).into_owned(),
        }
    }
    pub fn pread(fd: i64, off: i64, len: i64) -> String {
        let mut buf = vec![0u8; len as usize];
        file(fd).read_exact_at(&mut buf, off as u64).unwrap();
        match String::from_utf8(buf) {
            Ok(s) => s,
            Err(e) => String::from_utf8_lossy(e.as_bytes()).into_owned(),
        }
    }
    pub fn map(fd: i64) -> i64 {
        let p = unsafe { libc::mmap(std::ptr::null_mut(), MAP_LEN, libc::PROT_READ, libc::MAP_SHARED, fd as i32, 0) };
        if p == libc::MAP_FAILED {
            0
        } else {
            p as i64
        }
    }
    pub fn unmap(addr: i64) {
        if addr != 0 {
            unsafe {
                libc::munmap(addr as *mut libc::c_void, MAP_LEN);
            }
        }
    }
    pub fn read(fd: i64, addr: i64, off: i64, len: i64) -> String {
        if addr == 0 {
            return pread(fd, off, len);
        }
        let bytes = unsafe { std::slice::from_raw_parts((addr + off) as *const u8, len as usize) };
        text(bytes)
    }
    pub fn sync(fd: i64) {
        file(fd).sync_data().unwrap();
    }
    pub fn truncate(fd: i64, len: i64) {
        file(fd).set_len(len as u64).unwrap();
    }
    /// Allocates blocks for [off, off + len) and extends the file over them.
    pub fn preallocate(fd: i64, off: i64, len: i64) {
        let r = unsafe { libc::fallocate(fd as i32, 0, off as libc::off_t, len as libc::off_t) };
        if r != 0 {
            file(fd).set_len((off + len) as u64).unwrap();
        }
    }
    pub fn rename(from: &str, to: &str) {
        std::fs::rename(from, to).unwrap();
    }
    pub fn sync_dir(dir: &str) {
        File::open(dir).unwrap().sync_all().unwrap();
    }
    pub fn remove(path: &str) {
        let _ = std::fs::remove_file(path);
    }
}

fn fs_mkdirs(dir: &str) {
    ranger::native!(
        rust: { osfs::mkdirs(dir) },
        es6: "require('fs').mkdirSync({dir}, {{ recursive: true }});",
    );
}

fn fs_exists(path: &str) -> bool {
    let b: bool = ranger::native!(
        rust: { osfs::exists(path) },
        es6: "require('fs').existsSync({path})",
    );
    b
}

fn fs_open(path: &str) -> i64 {
    let fd: i64 = ranger::native!(
        rust: { osfs::open(path) },
        es6: "require('fs').openSync({path}, require('fs').existsSync({path}) ? 'r+' : 'w+')",
    );
    fd
}

fn fs_close(fd: i64) {
    ranger::native!(
        rust: { osfs::close(fd) },
        es6: "require('fs').closeSync({fd});",
    );
}

fn fs_size(fd: i64) -> i64 {
    let n: i64 = ranger::native!(
        rust: { osfs::size(fd) },
        es6: "require('fs').fstatSync({fd}).size",
    );
    n
}

fn fs_pwrite(fd: i64, off: i64, data: &str) {
    ranger::native!(
        rust: { osfs::pwrite(fd, off, data) },
        es6: "(() => {{ const b = Buffer.from({data}, 'utf8'); require('fs').writeSync({fd}, b, 0, b.length, {off}); }})();",
    );
}

fn fs_pread(fd: i64, off: i64, len: i64) -> String {
    let s: String = ranger::native!(
        rust: { osfs::pread(fd, off, len) },
        es6: "(() => {{ const b = Buffer.alloc({len}); require('fs').readSync({fd}, b, 0, {len}, {off}); return b.toString('utf8'); }})()",
    );
    s
}

/// Maps the file for reading; 0 where the target has no mapping.
fn fs_map(fd: i64) -> i64 {
    let a: i64 = ranger::native!(
        rust: { osfs::map(fd) },
        es6: "0",
    );
    a
}

fn fs_unmap(addr: i64) {
    ranger::native!(
        rust: { osfs::unmap(addr) },
        es6: "void 0;",
    );
}

/// Reads through the mapping `addr`, or with a positional read when it is 0.
fn fs_read(fd: i64, addr: i64, off: i64, len: i64) -> String {
    let s: String = ranger::native!(
        rust: { osfs::read(fd, addr, off, len) },
        es6: "(() => {{ const b = Buffer.alloc({len}); require('fs').readSync({fd}, b, 0, {len}, {off}); return b.toString('utf8'); }})()",
    );
    s
}

fn fs_sync(fd: i64) {
    ranger::native!(
        rust: { osfs::sync(fd) },
        es6: "require('fs').fdatasyncSync({fd});",
    );
}

fn fs_truncate(fd: i64, len: i64) {
    ranger::native!(
        rust: { osfs::truncate(fd, len) },
        es6: "require('fs').ftruncateSync({fd}, {len});",
    );
}

/// Reserves [off, off + len) for appends; extends the file with zeros.
fn fs_preallocate(fd: i64, off: i64, len: i64) {
    ranger::native!(
        rust: { osfs::preallocate(fd, off, len) },
        es6: "require('fs').ftruncateSync({fd}, {off} + {len});",
    );
}

fn fs_rename(from: &str, to: &str) {
    ranger::native!(
        rust: { osfs::rename(from, to) },
        es6: "require('fs').renameSync({from}, {to});",
    );
}

fn fs_sync_dir(dir: &str) {
    ranger::native!(
        rust: { osfs::sync_dir(dir) },
        es6: "(() => {{ const d = require('fs').openSync({dir}, 'r'); require('fs').fsyncSync(d); require('fs').closeSync(d); }})();",
    );
}

fn fs_remove(path: &str) {
    ranger::native!(
        rust: { osfs::remove(path) },
        es6: "(() => {{ try {{ require('fs').unlinkSync({path}); }} catch (e) {{}} }})();",
    );
}

/// One key and its value.
pub struct KvPair {
    pub key: String,
    pub value: String,
}

/// A put (`kind` 1) or a delete (`kind` 2).
pub struct Op {
    pub kind: i64,
    pub key: String,
    pub value: String,
}

/// A condition checked against the committed state: key absent (`kind` 1)
/// or key present with exactly `value` (`kind` 2).
pub struct Cond {
    pub kind: i64,
    pub key: String,
    pub value: String,
}

/// Mutations and conditions committed atomically.
pub struct Batch {
    pub ops: Vec<Op>,
    pub conds: Vec<Cond>,
}

impl Batch {
    pub fn new() -> Batch {
        Batch { ops: Vec::new(), conds: Vec::new() }
    }
    pub fn put(&mut self, key: &str, value: &str) {
        self.ops.push(Op { kind: 1, key: key.to_string(), value: value.to_string() });
    }
    pub fn delete(&mut self, key: &str) {
        self.ops.push(Op { kind: 2, key: key.to_string(), value: String::new() });
    }
    pub fn expect_absent(&mut self, key: &str) {
        self.conds.push(Cond { kind: 1, key: key.to_string(), value: String::new() });
    }
    pub fn expect_value(&mut self, key: &str, value: &str) {
        self.conds.push(Cond { kind: 2, key: key.to_string(), value: value.to_string() });
    }
    pub fn size(&self) -> i64 {
        self.ops.len() as i64
    }
}

const FANOUT: usize = 64;

/// A B+tree node. A leaf packs its keys into one string: key i is
/// `packed[ends[i-1]..ends[i]]`, its value at `offs[i]`, `lens[i]` bytes
/// long. An inner node has separator keys and children.
struct BNode {
    leaf: bool,
    packed: String,
    ends: Vec<i64>,
    offs: Vec<i64>,
    lens: Vec<i64>,
    seps: Vec<String>,
    kids: Vec<i64>,
    next: i64,
}

impl BNode {
    fn new(leaf: bool) -> BNode {
        BNode {
            leaf: leaf,
            packed: String::new(),
            ends: Vec::new(),
            offs: Vec::new(),
            lens: Vec::new(),
            seps: Vec::new(),
            kids: Vec::new(),
            next: -1,
        }
    }

    fn n(&self) -> usize {
        self.ends.len()
    }

    fn start(&self, i: usize) -> usize {
        if i == 0 {
            return 0;
        }
        self.ends[i - 1] as usize
    }

    fn key(&self, i: usize) -> &str {
        &self.packed[self.start(i)..self.ends[i] as usize]
    }

    /// First position whose key is >= `k`.
    fn lower(&self, k: &str) -> usize {
        let mut lo: usize = 0;
        let mut hi: usize = self.ends.len();
        while lo < hi {
            let mid = (lo + hi) / 2;
            if self.key(mid) < k {
                lo = mid + 1;
            } else {
                hi = mid;
            }
        }
        lo
    }

    fn insert_key(&mut self, i: usize, k: &str, off: i64, len: i64) {
        let at = self.start(i);
        let kl = k.as_bytes().len() as i64;
        if i == self.ends.len() {
            self.packed.push_str(k);
        } else {
            let mut np = String::from(&self.packed[0..at]);
            np.push_str(k);
            np.push_str(&self.packed[at..self.packed.as_bytes().len()]);
            self.packed = np;
        }
        self.ends.insert(i, (at as i64) + kl);
        let mut j = i + 1;
        while j < self.ends.len() {
            self.ends[j] = self.ends[j] + kl;
            j += 1;
        }
        self.offs.insert(i, off);
        self.lens.insert(i, len);
    }

    fn remove_key(&mut self, i: usize) {
        let s = self.start(i);
        let e = self.ends[i] as usize;
        let kl = (e - s) as i64;
        let mut np = String::from(&self.packed[0..s]);
        np.push_str(&self.packed[e..self.packed.as_bytes().len()]);
        self.packed = np;
        self.ends.remove(i);
        let mut j = i;
        while j < self.ends.len() {
            self.ends[j] = self.ends[j] - kl;
            j += 1;
        }
        self.offs.remove(i);
        self.lens.remove(i);
    }
}

/// First position whose separator is > `k`.
fn upper_bound(keys: &Vec<String>, k: &str) -> usize {
    let mut lo: usize = 0;
    let mut hi: usize = keys.len();
    while lo < hi {
        let mid = (lo + hi) / 2;
        if keys[mid].as_str() <= k {
            lo = mid + 1;
        } else {
            hi = mid;
        }
    }
    lo
}

/// Where a value lives in the log; `off` is -1 when the key is absent.
pub struct Loc {
    pub off: i64,
    pub len: i64,
}

/// An index entry: a key and its value location.
pub struct Entry {
    pub key: String,
    pub off: i64,
    pub len: i64,
}

/// A run of index entries in key order, and where the next run starts.
pub struct Chunk {
    pub entries: Vec<Entry>,
    pub next_leaf: i64,
    pub next_pos: i64,
}

/// A B+tree in an arena: key -> (value offset, value length). Leaves are
/// chained for range scans. Deletes leave leaves underfull; compaction
/// rebuilds the tree.
pub struct Index {
    nodes: Vec<BNode>,
    root: i64,
    count: i64,
    split_key: String,
    split_node: i64,
    old_len: i64,
    last_leaf: i64,
}

impl Index {
    pub fn new() -> Index {
        let mut ix = Index { nodes: Vec::new(), root: 0, count: 0, split_key: String::new(), split_node: -1, old_len: -1, last_leaf: 0 };
        ix.nodes.push(BNode::new(true));
        ix
    }

    pub fn len(&self) -> i64 {
        self.count
    }

    fn find_leaf(&self, key: &str) -> i64 {
        let mut n = self.root;
        while !self.nodes[n as usize].leaf {
            let node = &self.nodes[n as usize];
            let i = upper_bound(&node.seps, key);
            n = node.kids[i];
        }
        n
    }

    fn first_leaf(&self) -> i64 {
        let mut n = self.root;
        while !self.nodes[n as usize].leaf {
            n = self.nodes[n as usize].kids[0];
        }
        n
    }

    pub fn contains(&self, key: &str) -> bool {
        let node = &self.nodes[self.find_leaf(key) as usize];
        let i = node.lower(key);
        i < node.n() && node.key(i) == key
    }

    pub fn get_loc(&self, key: &str) -> Loc {
        let node = &self.nodes[self.find_leaf(key) as usize];
        let i = node.lower(key);
        if i < node.n() && node.key(i) == key {
            return Loc { off: node.offs[i], len: node.lens[i] };
        }
        Loc { off: -1, len: 0 }
    }

    /// Inserts or replaces; `old_len` is the replaced value's length or -1.
    pub fn insert(&mut self, key: &str, off: i64, len: i64) {
        self.old_len = -1;
        let root = self.root;
        let split = self.insert_at(root, key, off, len);
        if split {
            let mut nr = BNode::new(false);
            nr.seps.push(self.split_key.clone());
            nr.kids.push(root);
            nr.kids.push(self.split_node);
            self.nodes.push(nr);
            self.root = (self.nodes.len() - 1) as i64;
        }
    }

    fn insert_at(&mut self, n: i64, key: &str, off: i64, len: i64) -> bool {
        let ni = n as usize;
        if self.nodes[ni].leaf {
            let i = self.nodes[ni].lower(key);
            let node = &mut self.nodes[ni];
            if i < node.n() && node.key(i) == key {
                self.old_len = node.lens[i];
                node.offs[i] = off;
                node.lens[i] = len;
                return false;
            }
            let at_end = i == node.n() && node.next < 0;
            node.insert_key(i, key, off, len);
            self.count += 1;
            if self.nodes[ni].n() <= FANOUT {
                return false;
            }
            return self.split_leaf(n, at_end);
        }
        let i = upper_bound(&self.nodes[ni].seps, key);
        let child = self.nodes[ni].kids[i];
        let split = self.insert_at(child, key, off, len);
        if !split {
            return false;
        }
        let sk = self.split_key.clone();
        let sn = self.split_node;
        let node = &mut self.nodes[ni];
        node.seps.insert(i, sk);
        node.kids.insert(i + 1, sn);
        if node.seps.len() <= FANOUT {
            return false;
        }
        self.split_inner(n)
    }

    /// Splits a full leaf. Appending at the right edge (ascending inserts)
    /// leaves the left leaf full instead of half full.
    fn split_leaf(&mut self, n: i64, at_end: bool) -> bool {
        let ni = n as usize;
        let mut right = BNode::new(true);
        let total = self.nodes[ni].n();
        let mut mid = total / 2;
        if at_end {
            mid = total - 1;
        }
        {
            let node = &self.nodes[ni];
            let base = node.start(mid);
            right.packed = node.packed[base..node.packed.as_bytes().len()].to_string();
            for j in mid..total {
                right.ends.push(node.ends[j] - (base as i64));
                right.offs.push(node.offs[j]);
                right.lens.push(node.lens[j]);
            }
        }
        right.next = self.nodes[ni].next;
        let node = &mut self.nodes[ni];
        let cut = node.start(mid);
        node.packed = node.packed[0..cut].to_string();
        node.ends.truncate(mid);
        node.offs.truncate(mid);
        node.lens.truncate(mid);
        self.split_key = right.key(0).to_string();
        self.nodes.push(right);
        let rid = (self.nodes.len() - 1) as i64;
        self.nodes[ni].next = rid;
        self.split_node = rid;
        true
    }

    fn split_inner(&mut self, n: i64) -> bool {
        let ni = n as usize;
        let mut right = BNode::new(false);
        let total = self.nodes[ni].seps.len();
        let mid = total / 2;
        for j in (mid + 1)..total {
            right.seps.push(self.nodes[ni].seps[j].clone());
        }
        for j in (mid + 1)..(total + 1) {
            right.kids.push(self.nodes[ni].kids[j]);
        }
        self.split_key = self.nodes[ni].seps[mid].clone();
        let node = &mut self.nodes[ni];
        node.seps.truncate(mid);
        node.kids.truncate(mid + 1);
        self.nodes.push(right);
        self.split_node = (self.nodes.len() - 1) as i64;
        true
    }

    /// Adds `key` when it sorts after every key present (loading sorted
    /// entries): straight onto the rightmost leaf until that leaf is full.
    pub fn append_sorted(&mut self, key: &str, off: i64, len: i64) {
        let li = self.last_leaf as usize;
        let n = self.nodes[li].n();
        if self.nodes[li].next < 0 && n > 0 && n < FANOUT && self.nodes[li].key(n - 1) < key {
            let node = &mut self.nodes[li];
            node.packed.push_str(key);
            node.ends.push(node.packed.as_bytes().len() as i64);
            node.offs.push(off);
            node.lens.push(len);
            self.count += 1;
            return;
        }
        self.insert(key, off, len);
        self.last_leaf = self.find_leaf(key);
    }

    /// Removes `key`; returns the removed value's length or -1.
    pub fn remove(&mut self, key: &str) -> i64 {
        let leaf = self.find_leaf(key);
        let node = &mut self.nodes[leaf as usize];
        let i = node.lower(key);
        if i < node.n() && node.key(i) == key {
            let len = node.lens[i];
            node.remove_key(i);
            self.count -= 1;
            return len;
        }
        -1
    }

    /// Up to `limit` entries with `start <= key` and key starting with
    /// `prefix` (all keys when `prefix` is empty), in key order.
    pub fn scan(&self, prefix: &str, start: &str, limit: i64) -> Vec<Entry> {
        let mut out: Vec<Entry> = Vec::new();
        let mut from = start;
        if prefix > start {
            from = prefix;
        }
        let mut leaf = self.find_leaf(from);
        let mut i = self.nodes[leaf as usize].lower(from);
        while leaf >= 0 {
            let node = &self.nodes[leaf as usize];
            while i < node.n() {
                if (out.len() as i64) >= limit || !node.key(i).starts_with(prefix) {
                    return out;
                }
                out.push(Entry { key: node.key(i).to_string(), off: node.offs[i], len: node.lens[i] });
                i += 1;
            }
            leaf = node.next;
            i = 0;
        }
        out
    }

    /// Moves every value to the compacted log: offsets at or past `start`
    /// (committed during the compaction) shift by `delta`; the others take
    /// their copied offset from `keys` / `offs`, which are in key order.
    pub fn remap(&mut self, start: i64, delta: i64, keys: &Vec<String>, offs: &Vec<i64>) {
        let mut leaf = self.first_leaf();
        let mut j: usize = 0;
        while leaf >= 0 {
            let node = &mut self.nodes[leaf as usize];
            let mut i: usize = 0;
            while i < node.n() {
                if node.offs[i] >= start {
                    node.offs[i] = node.offs[i] + delta;
                } else {
                    while j < keys.len() && keys[j].as_str() < node.key(i) {
                        j += 1;
                    }
                    node.offs[i] = offs[j];
                }
                i += 1;
            }
            leaf = node.next;
        }
    }

    /// Up to `max` entries in key order starting at leaf `leaf`, position
    /// `pos` (leaf -2 means the first leaf). `next_leaf` is -1 at the end.
    pub fn chunk(&self, leaf: i64, pos: i64, max: i64) -> Chunk {
        let mut c = Chunk { entries: Vec::new(), next_leaf: -1, next_pos: 0 };
        let mut l = leaf;
        if l == -2 {
            l = self.first_leaf();
        }
        let mut i = pos as usize;
        while l >= 0 {
            let node = &self.nodes[l as usize];
            while i < node.n() {
                if (c.entries.len() as i64) >= max {
                    c.next_leaf = l;
                    c.next_pos = i as i64;
                    return c;
                }
                c.entries.push(Entry { key: node.key(i).to_string(), off: node.offs[i], len: node.lens[i] });
                i += 1;
            }
            l = node.next;
            i = 0;
        }
        c
    }
}

const HEADER: i64 = 32;
/// `prepare` / `commit` status for a key or value over the size limits.
pub const TOO_LARGE: i64 = -1000000000;
const PREALLOC: i64 = 64 * 1024 * 1024;
const STEP_BYTES: i64 = 4 * 1024 * 1024;
const HINT_HEADER: i64 = 124;
const BLOCK_HEADER: i64 = 42;
const P1: i64 = 2147483647;
const P2: i64 = 2147483629;

/// A Fletcher-style checksum: `a` sums the bytes (each plus one, so zero
/// bytes count), `b` sums the running `a`. Reduced every 4096 bytes, which
/// keeps every intermediate below 2^53 on every target.
pub struct Sums {
    a: i64,
    b: i64,
    n: i64,
}

impl Sums {
    pub fn new() -> Sums {
        Sums { a: 1, b: 0, n: 0 }
    }
    pub fn add(&mut self, s: &str) {
        let mut a = self.a;
        let mut b = self.b;
        let mut n = self.n;
        for byte in s.bytes() {
            a = a + (byte as i64) + 1;
            b = b + a;
            n += 1;
            if n == 4096 {
                a = a % P1;
                b = b % P2;
                n = 0;
            }
        }
        self.a = a;
        self.b = b;
        self.n = n;
    }
    pub fn first(&self) -> i64 {
        self.a % P1
    }
    pub fn second(&self) -> i64 {
        self.b % P2
    }
}

fn sums_of(s: &str) -> Sums {
    let mut c = Sums::new();
    c.add(s);
    c
}

/// True when every byte is ASCII, so byte offsets are character offsets.
fn ascii(s: &str) -> bool {
    for b in s.bytes() {
        if (b as i64) >= 128 {
            return false;
        }
    }
    true
}

fn blen(s: &str) -> i64 {
    s.as_bytes().len() as i64
}

fn num(s: &str) -> i64 {
    match s.parse::<i64>() {
        Ok(v) => v,
        Err(_) => -1,
    }
}

const DIGITS36: &str = "0123456789abcdefghijklmnopqrstuvwxyz";
const OP_HEADER: i64 = 9;
const MAX_KEY: i64 = 46655;
const MAX_VALUE: i64 = 60466175;

/// Bytes `a` and `b` share at the start, at most 1295, ending on a
/// character boundary of `b`.
fn shared_prefix(a: &str, b: &str) -> i64 {
    let al = a.as_bytes().len();
    let bl = b.as_bytes().len();
    let mut n: usize = 0;
    while n < al && n < bl && n < 1295 && a.as_bytes()[n] == b.as_bytes()[n] {
        n += 1;
    }
    while n > 0 && n < bl && (b.as_bytes()[n] as i64) >= 128 && (b.as_bytes()[n] as i64) < 192 {
        n -= 1;
    }
    n as i64
}

/// `n` as `width` base-36 digits, so that string order is numeric order.
pub fn b36(n: i64, width: i64) -> String {
    let mut p: i64 = 1;
    let mut k: i64 = 1;
    while k < width {
        p = p * 36;
        k += 1;
    }
    let mut s = String::new();
    while p > 0 {
        let d = ((n / p) % 36) as usize;
        s.push_str(&DIGITS36[d..d + 1]);
        p = p / 36;
    }
    s
}

/// The value of base-36 digits, or -1.
pub fn unb36(s: &str) -> i64 {
    let mut v: i64 = 0;
    for b in s.bytes() {
        let c = b as i64;
        let mut d: i64 = -1;
        if c >= 48 && c <= 57 {
            d = c - 48;
        }
        if c >= 97 && c <= 122 {
            d = c - 87;
        }
        if d < 0 {
            return -1;
        }
        v = v * 36 + d;
    }
    v
}

/// A compaction in progress: the new log, how far the old log had grown
/// when it began, the key cursor, and each copied key's new value offset.
pub struct Compaction {
    tmp: String,
    nfd: i64,
    start_len: i64,
    pos: i64,
    cursor: String,
    started: bool,
    keys: Vec<String>,
    offs: Vec<i64>,
    last_off: i64,
    last_header: String,
    tail_src: i64,
    tail_base: i64,
    old_fd: i64,
    old_map: i64,
}

impl Compaction {
    /// Syncs what has been copied so far. Touches only the new log, so it
    /// needs no access to the kernel.
    pub fn sync(&self) {
        fs_sync(self.nfd);
    }

    /// Bytes written to the new log.
    pub fn written(&self) -> i64 {
        self.pos
    }

    /// Closes the replaced log after `compact_finish`. Its last close frees
    /// all of its blocks, which can take long, so it is kept out of the
    /// exclusive section; nothing reads the old log after the finish.
    pub fn release(&mut self) {
        if self.old_fd >= 0 {
            fs_unmap(self.old_map);
            fs_close(self.old_fd);
            self.old_fd = -1;
            self.old_map = 0;
        }
    }
}

/// A commit ready to be written: its frame header and payload, the log
/// offset it goes to, its sequence number, and the log offset of each op's
/// value.
pub struct Prepared {
    pub status: i64,
    pub seq: i64,
    pub off: i64,
    pub header: String,
    pub payload: String,
    pub value_offs: Vec<i64>,
}

impl Prepared {
    pub fn frame_len(&self) -> i64 {
        HEADER + blen(&self.payload)
    }
}

/// The store. `durable` makes every commit fdatasync before it returns.
pub struct Kernel {
    dir: String,
    path: String,
    fd: i64,
    map: i64,
    file_len: i64,
    alloc_len: i64,
    seq: i64,
    live_bytes: i64,
    last_frame_off: i64,
    last_header: String,
    hint_covered: i64,
    index: Index,
    pub durable: bool,
    pub auto_compact: bool,
    pub compact_min_bytes: i64,
    pub compact_slack_bytes: i64,
    pub compactions: i64,
    pub recovered_cut: i64,
    pub replayed_bytes: i64,
}

impl Kernel {
    /// Opens or creates the store in `dir`: loads the index checkpoint when
    /// it matches the log, then replays the frames after it.
    pub fn open(dir: &str, durable: bool) -> Kernel {
        fs_mkdirs(dir);
        let path = format!("{}/data.log", dir);
        fs_remove(&format!("{}/data.log.compact", dir));
        fs_remove(&format!("{}/index.hint.tmp", dir));
        let fd = fs_open(&path);
        let mut k = Kernel {
            dir: dir.to_string(),
            path: path,
            fd: fd,
            map: fs_map(fd),
            file_len: 0,
            alloc_len: 0,
            seq: 0,
            live_bytes: 0,
            last_frame_off: -1,
            last_header: String::new(),
            hint_covered: -1,
            index: Index::new(),
            durable: durable,
            auto_compact: true,
            compact_min_bytes: 64 * 1024 * 1024,
            compact_slack_bytes: 2 * 1024 * 1024,
            compactions: 0,
            recovered_cut: 0,
            replayed_bytes: 0,
        };
        let mut start: i64 = 0;
        if k.load_hint() {
            start = k.hint_covered;
        }
        k.recover(start);
        k
    }

    fn recover(&mut self, from: i64) {
        let size = fs_size(self.fd);
        let mut pos: i64 = from;
        while pos + HEADER <= size {
            let hdr = fs_read(self.fd, self.map, pos, HEADER);
            if !hdr.starts_with("SQ") || blen(&hdr) != HEADER || !ascii(&hdr) {
                break;
            }
            let len = num(&hdr[2..12]);
            let c1 = num(&hdr[12..22]);
            let c2 = num(&hdr[22..32]);
            if len < 30 || pos + HEADER + len > size {
                break;
            }
            let payload = fs_read(self.fd, self.map, pos + HEADER, len);
            let sums = sums_of(&payload);
            if sums.first() != c1 || sums.second() != c2 {
                break;
            }
            if !self.replay(&payload, pos + HEADER) {
                break;
            }
            self.last_frame_off = pos;
            self.last_header = hdr;
            pos = pos + HEADER + len;
        }
        self.replayed_bytes = pos - from;
        if pos < size {
            // Zeros are preallocated space; anything else is a torn or
            // unacknowledged frame. Either way it is cut, so no stale bytes
            // can follow frames written later.
            let n = if size - pos < HEADER { size - pos } else { HEADER };
            if fs_read(self.fd, self.map, pos, n) != "\u{0}".repeat(n as usize) {
                self.recovered_cut = size - pos;
            }
            fs_truncate(self.fd, pos);
            fs_sync(self.fd);
        }
        self.file_len = pos;
        self.alloc_len = pos;
    }

    fn replay(&mut self, payload: &str, base: i64) -> bool {
        let seq = num(&payload[0..20]);
        let count = num(&payload[20..30]);
        let mut cur: usize = 30;
        let total = payload.as_bytes().len();
        let mut i: i64 = 0;
        while i < count {
            if cur + 9 > total {
                return false;
            }
            let kind = &payload[cur..cur + 1];
            let kl = unb36(&payload[cur + 1..cur + 4]) as usize;
            let vl = unb36(&payload[cur + 4..cur + 9]) as usize;
            let ks = cur + 9;
            if ks + kl + vl > total {
                return false;
            }
            let key = &payload[ks..ks + kl];
            if kind == "P" {
                self.apply_put(key, base + ((ks + kl) as i64), vl as i64);
            } else {
                self.apply_delete(key);
            }
            cur = ks + kl + vl;
            i += 1;
        }
        if seq > self.seq {
            self.seq = seq;
        }
        true
    }

    // ------------------------------------------------ index checkpoint

    /// Writes `index.hint`: the index entries, the log length they cover and
    /// the header of the last covered frame, which ties the hint to this log.
    pub fn write_hint(&self) {
        let tmp = format!("{}/index.hint.tmp", self.dir);
        fs_remove(&tmp);
        let hfd = fs_open(&tmp);
        let mut pos: i64 = HINT_HEADER;
        let mut blocks: i64 = 0;
        let mut leaf: i64 = -2;
        let mut at: i64 = 0;
        while leaf != -1 {
            let c = self.index.chunk(leaf, at, 8192);
            leaf = c.next_leaf;
            at = c.next_pos;
            if c.entries.len() == 0 {
                break;
            }
            // each entry: bytes shared with the previous key (2 base-36
            // digits), suffix length (3), value offset (8), value length (5),
            // then the suffix; the first key of a block shares nothing
            let mut body = String::new();
            let mut prev = String::new();
            for e in c.entries.iter() {
                let shared = shared_prefix(&prev, &e.key);
                let suffix = &e.key[shared as usize..];
                body.push_str(&b36(shared, 2));
                body.push_str(&b36(blen(suffix), 3));
                body.push_str(&b36(e.off, 8));
                body.push_str(&b36(e.len, 5));
                body.push_str(suffix);
                prev = e.key.clone();
            }
            let s = sums_of(&body);
            let head = format!("HB{:010}{:010}{:010}{:010}", blen(&body), s.first(), s.second(), c.entries.len());
            fs_pwrite(hfd, pos, &head);
            fs_pwrite(hfd, pos + BLOCK_HEADER, &body);
            pos = pos + BLOCK_HEADER + blen(&body);
            blocks += 1;
        }
        let mut last = self.last_header.clone();
        if blen(&last) != HEADER {
            last = "-".repeat(32);
        }
        let head = format!("SH{:020}{:020}{:020}{:010}{:020}{}", self.file_len, self.seq, self.last_frame_off, blocks, self.live_bytes, last);
        fs_pwrite(hfd, 0, &head);
        fs_sync(hfd);
        fs_close(hfd);
        fs_rename(&tmp, &format!("{}/index.hint", self.dir));
        fs_sync_dir(&self.dir);
    }

    /// Loads `index.hint` if it is intact and matches the log.
    fn load_hint(&mut self) -> bool {
        let hpath = format!("{}/index.hint", self.dir);
        if !fs_exists(&hpath) {
            return false;
        }
        let hfd = fs_open(&hpath);
        let ok = self.read_hint(hfd);
        fs_close(hfd);
        if !ok {
            self.index = Index::new();
            self.seq = 0;
            self.live_bytes = 0;
            self.last_frame_off = -1;
            self.last_header = String::new();
            self.hint_covered = -1;
        }
        ok
    }

    fn read_hint(&mut self, hfd: i64) -> bool {
        let hsize = fs_size(hfd);
        let lsize = fs_size(self.fd);
        if hsize < HINT_HEADER {
            return false;
        }
        let head = fs_pread(hfd, 0, HINT_HEADER);
        if !head.starts_with("SH") || blen(&head) != HINT_HEADER || !ascii(&head) {
            return false;
        }
        let covered = num(&head[2..22]);
        let seq = num(&head[22..42]);
        let last_off = num(&head[42..62]);
        let blocks = num(&head[62..72]);
        let live = num(&head[72..92]);
        let last = &head[92..124];
        if covered < 0 || covered > lsize || seq < 0 || blocks < 0 {
            return false;
        }
        if covered > 0 {
            if last_off < 0 || last_off + HEADER > covered {
                return false;
            }
            if fs_read(self.fd, self.map, last_off, HEADER) != last {
                return false;
            }
        }
        let mut pos = HINT_HEADER;
        let mut b: i64 = 0;
        while b < blocks {
            if pos + BLOCK_HEADER > hsize {
                return false;
            }
            let bh = fs_pread(hfd, pos, BLOCK_HEADER);
            if !bh.starts_with("HB") || blen(&bh) != BLOCK_HEADER || !ascii(&bh) {
                return false;
            }
            let len = num(&bh[2..12]);
            let count = num(&bh[32..42]);
            if len < 0 || pos + BLOCK_HEADER + len > hsize {
                return false;
            }
            let body = fs_pread(hfd, pos + BLOCK_HEADER, len);
            let s = sums_of(&body);
            if s.first() != num(&bh[12..22]) || s.second() != num(&bh[22..32]) {
                return false;
            }
            let mut cur: usize = 0;
            let total = body.as_bytes().len();
            let mut prev = String::new();
            let mut i: i64 = 0;
            while i < count {
                if cur + 18 > total {
                    return false;
                }
                let shared = unb36(&body[cur..cur + 2]);
                let sl = unb36(&body[cur + 2..cur + 5]);
                let off = unb36(&body[cur + 5..cur + 13]);
                let vl = unb36(&body[cur + 13..cur + 18]);
                if shared < 0 || sl < 0 || off < 0 || vl < 0 || off + vl > covered || shared > blen(&prev) {
                    return false;
                }
                let end = cur + 18 + (sl as usize);
                if end > total {
                    return false;
                }
                let mut key = String::from(&prev[0..shared as usize]);
                key.push_str(&body[cur + 18..end]);
                self.index.append_sorted(&key, off, vl);
                prev = key;
                cur = end;
                i += 1;
            }
            pos = pos + BLOCK_HEADER + len;
            b += 1;
        }
        self.seq = seq;
        self.live_bytes = live;
        self.last_frame_off = last_off;
        self.last_header = last.to_string();
        self.hint_covered = covered;
        true
    }

    // ------------------------------------------------ state

    fn apply_put(&mut self, key: &str, off: i64, len: i64) {
        self.index.insert(key, off, len);
        let old = self.index.old_len;
        if old >= 0 {
            self.live_bytes = self.live_bytes - old;
        } else {
            self.live_bytes = self.live_bytes + blen(key);
        }
        self.live_bytes = self.live_bytes + len;
    }

    fn apply_delete(&mut self, key: &str) {
        let old = self.index.remove(key);
        if old >= 0 {
            self.live_bytes = self.live_bytes - old - blen(key);
        }
    }

    pub fn seq(&self) -> i64 {
        self.seq
    }

    pub fn len(&self) -> i64 {
        self.index.len()
    }

    /// Bytes in the log, live and dead.
    pub fn file_bytes(&self) -> i64 {
        self.file_len
    }

    /// Key and value bytes of the live entries.
    pub fn live_bytes(&self) -> i64 {
        self.live_bytes
    }

    pub fn get(&self, key: &str) -> Option<String> {
        let loc = self.index.get_loc(key);
        if loc.off < 0 {
            return None;
        }
        Some(fs_read(self.fd, self.map, loc.off, loc.len))
    }

    pub fn contains(&self, key: &str) -> bool {
        self.index.contains(key)
    }

    /// Up to `limit` pairs whose key starts with `prefix` and is >= `start`.
    pub fn scan(&self, prefix: &str, start: &str, limit: i64) -> Vec<KvPair> {
        let mut out: Vec<KvPair> = Vec::new();
        let ix = &self.index;
        let mut from = start;
        if prefix > start {
            from = prefix;
        }
        let mut leaf = ix.find_leaf(from);
        let mut i = ix.nodes[leaf as usize].lower(from);
        while leaf >= 0 {
            let node = &ix.nodes[leaf as usize];
            while i < node.n() {
                if (out.len() as i64) >= limit || !node.key(i).starts_with(prefix) {
                    return out;
                }
                out.push(KvPair { key: node.key(i).to_string(), value: fs_read(self.fd, self.map, node.offs[i], node.lens[i]) });
                i += 1;
            }
            leaf = node.next;
            i = 0;
        }
        out
    }

    /// Keys only, as `scan` without reading values.
    pub fn scan_keys(&self, prefix: &str, start: &str, limit: i64) -> Vec<String> {
        let mut out: Vec<String> = Vec::new();
        let ix = &self.index;
        let mut from = start;
        if prefix > start {
            from = prefix;
        }
        let mut leaf = ix.find_leaf(from);
        let mut i = ix.nodes[leaf as usize].lower(from);
        while leaf >= 0 {
            let node = &ix.nodes[leaf as usize];
            while i < node.n() {
                if (out.len() as i64) >= limit || !node.key(i).starts_with(prefix) {
                    return out;
                }
                out.push(node.key(i).to_string());
                i += 1;
            }
            leaf = node.next;
            i = 0;
        }
        out
    }

    // ------------------------------------------------ commits

    /// Checks the conditions and builds the frame. `status` is 1 when the
    /// commit can be written, 0 for an empty batch, and -(i+1) when
    /// condition i failed.
    pub fn prepare(&self, b: &Batch) -> Prepared {
        let mut p = Prepared {
            status: 1,
            seq: self.seq + 1,
            off: self.file_len,
            header: String::new(),
            payload: String::new(),
            value_offs: Vec::new(),
        };
        let mut ci: i64 = 0;
        for c in b.conds.iter() {
            let ok = if c.kind == 1 {
                !self.contains(&c.key)
            } else {
                match self.get(&c.key) {
                    Some(v) => v == c.value,
                    None => false,
                }
            };
            if !ok {
                p.status = -(ci + 1);
                return p;
            }
            ci += 1;
        }
        if b.ops.len() == 0 {
            p.status = 0;
            return p;
        }
        for op in b.ops.iter() {
            if blen(&op.key) > MAX_KEY || blen(&op.value) > MAX_VALUE {
                p.status = TOO_LARGE;
                return p;
            }
        }
        self.build_frame(&mut p, b);
        p
    }

    fn build_frame(&self, p: &mut Prepared, b: &Batch) {
        let mut payload = format!("{:020}{:010}", p.seq, b.ops.len());
        let mut cur: i64 = 30;
        for op in b.ops.iter() {
            let kl = blen(&op.key);
            let vl = blen(&op.value);
            if op.kind == 1 {
                payload.push_str("P");
            } else {
                payload.push_str("D");
            }
            payload.push_str(&b36(kl, 3));
            payload.push_str(&b36(vl, 5));
            payload.push_str(&op.key);
            payload.push_str(&op.value);
            p.value_offs.push(p.off + HEADER + cur + OP_HEADER + kl);
            cur = cur + OP_HEADER + kl + vl;
        }
        let s = sums_of(&payload);
        p.header = format!("SQ{:010}{:010}{:010}", cur, s.first(), s.second());
        p.payload = payload;
    }

    /// Writes a prepared frame (and syncs it in durable mode). Needs only
    /// shared access, so readers can keep reading the index meanwhile.
    pub fn write_prepared(&self, p: &Prepared) {
        fs_pwrite(self.fd, p.off + HEADER, &p.payload);
        fs_pwrite(self.fd, p.off, &p.header);
        if self.durable {
            fs_sync(self.fd);
        }
    }

    /// Makes a written frame visible.
    pub fn apply_prepared(&mut self, p: &Prepared, b: &Batch) {
        let mut i: usize = 0;
        for op in b.ops.iter() {
            if op.kind == 1 {
                self.apply_put(&op.key, p.value_offs[i], blen(&op.value));
            } else {
                self.apply_delete(&op.key);
            }
            i += 1;
        }
        self.file_len = p.off + p.frame_len();
        if self.file_len > self.alloc_len {
            self.alloc_len = self.file_len;
        }
        if self.alloc_len - self.file_len < PREALLOC / 4 {
            // Extending the file ahead of the writes keeps a commit's
            // fdatasync from also persisting a new file size.
            let from = self.alloc_len;
            self.alloc_len = self.file_len + PREALLOC;
            fs_preallocate(self.fd, from, self.alloc_len - from);
        }
        self.seq = p.seq;
        self.last_frame_off = p.off;
        self.last_header = p.header.clone();
        self.maybe_compact();
    }

    /// Commits a batch: the new sequence number, 0 for an empty batch, or
    /// -(i+1) when condition i failed.
    pub fn commit(&mut self, b: &Batch) -> i64 {
        let p = self.prepare(b);
        if p.status != 1 {
            return p.status;
        }
        self.write_prepared(&p);
        self.apply_prepared(&p, b);
        p.seq
    }

    /// Makes everything written so far durable (for non-durable mode).
    pub fn sync(&self) {
        fs_sync(self.fd);
    }

    /// True when dead bytes outweigh live bytes (plus `compact_slack_bytes`)
    /// in a log of at least `compact_min_bytes`.
    pub fn needs_compaction(&self) -> bool {
        self.file_len >= self.compact_min_bytes && self.file_len > 2 * self.live_bytes + self.compact_slack_bytes
    }

    fn maybe_compact(&mut self) {
        if self.auto_compact && self.needs_compaction() {
            self.compact();
        }
    }

    /// Rewrites the log with only the live entries and swaps it in, all in
    /// this call. `compact_begin`, `compact_step`, `compact_catch_up`,
    /// `Compaction::sync`, `compact_finish` and `Compaction::release` do the
    /// same in phases: everything but `compact_finish` needs only shared
    /// access (or none), so another thread can run it while commits continue;
    /// `compact_finish` needs exclusive access, with no commit between its
    /// `prepare` and `apply`.
    pub fn compact(&mut self) {
        let mut c = self.compact_begin();
        let mut done = false;
        while !done {
            done = self.compact_step(&mut c, 4096);
        }
        c.sync();
        self.compact_finish(&mut c);
        c.release();
        self.write_hint();
    }

    pub fn compact_begin(&self) -> Compaction {
        let tmp = format!("{}/data.log.compact", self.dir);
        fs_remove(&tmp);
        let nfd = fs_open(&tmp);
        Compaction {
            tmp: tmp,
            nfd: nfd,
            start_len: self.file_len,
            pos: 0,
            cursor: String::new(),
            started: false,
            keys: Vec::new(),
            offs: Vec::new(),
            last_off: -1,
            last_header: String::new(),
            tail_src: self.file_len,
            tail_base: -1,
            old_fd: -1,
            old_map: 0,
        }
    }

    /// Copies up to `max` live entries (and at most about 4 MiB of values)
    /// that were committed before `compact_begin`; true when every key has
    /// been visited. The caps keep each step short for whoever holds the
    /// shared lock around it.
    pub fn compact_step(&self, c: &mut Compaction, max: i64) -> bool {
        let entries = self.index.scan("", &c.cursor, max + 1);
        let mut b = Batch::new();
        let mut seen: i64 = 0;
        let mut bytes: i64 = 0;
        let mut last = String::new();
        let mut capped = false;
        for e in entries.iter() {
            if c.started && e.key.as_str() == c.cursor.as_str() {
                continue;
            }
            if seen >= max || bytes >= STEP_BYTES {
                capped = true;
                break;
            }
            seen += 1;
            last = e.key.clone();
            if e.off < c.start_len {
                let v = fs_read(self.fd, self.map, e.off, e.len);
                b.put(&e.key, &v);
                bytes = bytes + e.len;
            }
        }
        if seen > 0 {
            c.cursor = last;
        }
        c.started = true;
        if b.ops.len() > 0 {
            let mut p = Prepared { status: 1, seq: self.seq, off: c.pos, header: String::new(), payload: String::new(), value_offs: Vec::new() };
            self.build_frame(&mut p, &b);
            fs_pwrite(c.nfd, c.pos + HEADER, &p.payload);
            fs_pwrite(c.nfd, c.pos, &p.header);
            let mut i: usize = 0;
            for op in b.ops.iter() {
                c.keys.push(op.key.clone());
                c.offs.push(p.value_offs[i]);
                i += 1;
            }
            c.last_off = c.pos;
            c.last_header = p.header.clone();
            c.pos = c.pos + p.frame_len();
        }
        // done once a scan came back short and every entry of it was copied
        !capped && (entries.len() as i64) < max + 1
    }

    /// Appends to the new log the frames committed since `compact_begin`
    /// that it does not have yet. Call after the last step; shared access is
    /// enough, and `compact_finish` copies whatever arrives after.
    pub fn compact_catch_up(&self, c: &mut Compaction) {
        if c.tail_base < 0 {
            c.tail_base = c.pos;
        }
        while c.tail_src < self.file_len {
            let hdr = fs_read(self.fd, self.map, c.tail_src, HEADER);
            let len = num(&hdr[2..12]);
            let payload = fs_read(self.fd, self.map, c.tail_src + HEADER, len);
            fs_pwrite(c.nfd, c.pos + HEADER, &payload);
            fs_pwrite(c.nfd, c.pos, &hdr);
            c.last_off = c.pos;
            c.last_header = hdr;
            c.pos = c.pos + HEADER + len;
            c.tail_src = c.tail_src + HEADER + len;
        }
    }

    /// Bytes committed to the old log that the new one does not have yet.
    pub fn compact_lag(&self, c: &Compaction) -> i64 {
        self.file_len - c.tail_src
    }

    /// Appends the remaining frames committed since `compact_begin`, swaps
    /// the new log in and moves the index to it.
    pub fn compact_finish(&mut self, c: &mut Compaction) {
        self.compact_catch_up(c);
        let base = c.tail_base;
        if c.pos == 0 {
            // an empty store still carries its sequence number
            let mut b = Batch::new();
            b.delete("\u{1}seq");
            let mut p = Prepared { status: 1, seq: self.seq, off: 0, header: String::new(), payload: String::new(), value_offs: Vec::new() };
            self.build_frame(&mut p, &b);
            fs_pwrite(c.nfd, HEADER, &p.payload);
            fs_pwrite(c.nfd, 0, &p.header);
            c.last_off = 0;
            c.last_header = p.header.clone();
            c.pos = p.frame_len();
        }
        fs_sync(c.nfd);
        fs_rename(&c.tmp, &self.path);
        fs_sync_dir(&self.dir);
        self.index.remap(c.start_len, base - c.start_len, &c.keys, &c.offs);
        c.old_fd = self.fd;
        c.old_map = self.map;
        self.fd = c.nfd;
        self.map = fs_map(c.nfd);
        self.file_len = c.pos;
        self.alloc_len = c.pos;
        self.last_frame_off = c.last_off;
        self.last_header = c.last_header.clone();
        self.compactions += 1;
    }

    /// Releases the file and mapping without syncing or writing the
    /// checkpoint: what a crash leaves behind. `close` is the clean way.
    pub fn abandon(&mut self) {
        if self.fd >= 0 {
            fs_unmap(self.map);
            self.map = 0;
            fs_close(self.fd);
            self.fd = -1;
        }
    }

    /// Syncs the log, writes the index checkpoint and closes the files.
    pub fn close(&mut self) {
        if self.fd >= 0 {
            if self.alloc_len > self.file_len {
                fs_truncate(self.fd, self.file_len);
                self.alloc_len = self.file_len;
            }
            fs_sync(self.fd);
            self.write_hint();
            fs_unmap(self.map);
            self.map = 0;
            fs_close(self.fd);
            self.fd = -1;
        }
    }
}

/// Dropping a kernel that was not closed releases its file like a crash
/// would; call `close` to sync and checkpoint.
#[ranger::target(rust)]
impl Drop for Kernel {
    fn drop(&mut self) {
        self.abandon();
    }
}
