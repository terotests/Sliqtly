# SliqtlyDB vs SQLite: Sliqtly workload

Run with `./run.sh` (`REPEAT=3` by default). Each engine runs in its own process and directory.
Host: 4 vCPU Xeon 2.1 GHz cloud VM, local disk, Linux 6.18. The VM is noisy: the same
configuration varies by up to ±20% between runs, so differences smaller than that are not
meaningful.

## Workload

Same data and the same operations for every engine:

- **Data:** 1000 rooms × 20 documents, 2000 users, 5 memberships per room. Loaded in
  transactions of 1000 rows, then the database is reopened.
- **get document:** point read by id.
- **documents in room:** all 20 documents of a room, decoded.
- **rooms for user:** room ids of a user's memberships.
- **update document:** read, change the title, write back in one transaction. The version is
  bumped and the room index kept consistent.
- **create room + owner:** room plus membership in one transaction.

Durability:

- **sync:** the commit is on disk when acknowledged. SQLite uses WAL with
  `synchronous=FULL`; Sliqtly log uses an fsync per commit.
- **nosync:** the OS flushes. SQLite uses `synchronous=OFF`.

The before engine (state-bin) never fsyncs, so it only appears in the nosync column.

SQLite setup: BLOB primary keys, `WITHOUT ROWID`, an index on
`documents(room_id, updated_at DESC)`, prepared statements, rusqlite 0.37 (bundled SQLite).

<!-- BEGIN TABLES -->
Throughput (ops/s, median of 3 runs; higher is better)

| | SQLite sync | Sliqtly log sync | SQLite nosync | Sliqtly log nosync | Sliqtly state-bin (before) nosync |
|---|---|---|---|---|---|
| get document | 222.2k | 407.7k | 276.1k | 459.1k | 196 |
| documents in room (20) | 20.2k | 19.1k | 23.1k | 20.8k | 44 |
| rooms for user | 382.3k | 871.3k | 430.8k | 848.1k | 208 |
| update document (read-modify-write tx) | 3.5k | 4.3k | 48.6k | 51.7k | 34 |
| create room + owner (tx) | 5.3k | 4.9k | 72.5k | 77.7k | 39 |
| bulk load (rows/s, batches of 1000) | 98.2k | 152.2k | 207.1k | 189.7k | 49.9k |

Latency p50 / p99 (µs; lower is better)

| | SQLite sync | Sliqtly log sync | SQLite nosync | Sliqtly log nosync | Sliqtly state-bin (before) nosync |
|---|---|---|---|---|---|
| get document | 3.9 / 12.5 | 2.2 / 4.6 | 2.9 / 10.6 | 1.8 / 4.1 | 4890.7 / 10603.5 |
| documents in room (20) | 38.3 / 113.3 | 46.0 / 142.1 | 39.6 / 93.3 | 44.8 / 86.7 | 21735.5 / 36805.3 |
| rooms for user | 2.2 / 6.0 | 1.0 / 2.6 | 2.0 / 4.3 | 1.0 / 2.7 | 3823.3 / 9682.2 |
| update document (read-modify-write tx) | 236.1 / 1143.8 | 183.0 / 631.8 | 15.5 / 62.0 | 17.1 / 58.3 | 28404.1 / 55051.7 |
| create room + owner (tx) | 160.7 / 485.2 | 172.9 / 527.1 | 9.1 / 49.7 | 11.5 / 38.6 | 25521.2 / 40309.8 |

Resources

| | SQLite sync | Sliqtly log sync | SQLite nosync | Sliqtly log nosync | Sliqtly state-bin (before) nosync |
|---|---|---|---|---|---|
| open after load (ms) | 7.9 | 73.9 | 1.5 | 60.1 | 20.8 |
| disk (MB) | 4.6 | 10.2 | 4.6 | 10.2 | 8.3 |
| peak RSS (MB) | 29 | 47 | 29 | 47 | 67 |

Dataset: 1000 rooms, 20000 documents, 4990 memberships, 2000 users.
<!-- END TABLES -->

## What was found and changed

1. **The before engine is O(store) per operation.** `FjallEngine` (state-bin) clones the whole
   map for every snapshot, including every read, and rewrites the whole file on every commit.
   At 26k rows that is 4–5 ms per read and 25 ms per write, 1000–2000× slower than SQLite. It
   also never fsyncs.
   Replaced by `LogEngine`:
   - append-only `wal.log` with CRC32 per record and an fsync per commit;
   - periodic `checkpoint.bin`, written as tmp, fsync, rename;
   - torn-tail truncation on recovery;
   - O(1) snapshots on a persistent B-tree (`imbl::OrdMap`).
2. **No secondary index.** "Documents in room" scanned every document. `put_document` now
   maintains `room_document`. It removes the stale entry when a document moves and guards the
   read-modify-write with a compare-and-set condition on the document.
3. **Prefix scans dropped keys (correctness bug).** `KeyRange::prefix` used `prefix ++ 0xFF` as
   an exclusive upper bound. Any key whose next byte is 0xFF was skipped, which is about 1 in
   256 ids. Rooms appeared to be missing documents, and users missing rooms. The benchmark's
   own assertion caught it. The bound is now the prefix's successor, and there is a
   regression test.
4. **Commit copied B-tree nodes.** A commit cloned the map while the published state still
   shared it. imbl's copy-on-write then deep-copied every `Vec<u8>` key and value in each node
   on the path. Keys and values are now `Arc<[u8]>`, and the commit mutates the published map
   in place, so nodes are copied only while a reader snapshot holds them.
   Nosync update went from 35k to about 50k ops/s; room create from 34k to about 75k ops/s.
5. **Timestamp parsing was 30% of record decoding.** A fast path parses the format the store
   writes (`…Z`) and falls back to chrono for anything else. Get document went from about 500k
   to about 600k ops/s in a back-to-back run; documents in room from about 17k to about
   22k ops/s.

## Where SQLite still wins, and why

- **documents in room:** about 10–20% behind. 87% of read time is serde_json decoding of the
  record (`Document` with RFC 3339 strings and a `metadata` JSON value). SQLite reads typed
  columns. The next step is a binary record codec. It changes the on-disk record format, so it
  needs a `format_version` bump and matching decoders in `sliqtly-db`. It was not done here.
- **open:** 60–75 ms vs 2–8 ms. The log engine loads the whole store into memory: checkpoint
  decode plus WAL replay. SQLite pages data in lazily. Open is O(data) by design for this
  engine. A larger-than-memory engine (the kernel work in the other session) is the fix.
- **disk:** 10 MB vs 4.6 MB. Records are JSON, so the logical data alone is 8.5 MB
  (`sliqtly-db info`). The WAL adds framing on top. A binary record codec is the lever here
  too.
- **memory:** 47 MB vs 29 MB. The whole dataset is resident.
- **durable commits** (sync column) are fsync-bound for both engines and within noise.

## Where Sliqtly is ahead

- **Point reads:** about 1.7× SQLite.
- **Membership lookups:** about 2× SQLite.
- **Durable bulk load:** about 1.5× SQLite.
