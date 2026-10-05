# Blob store benchmark

`store/blobbench_test.go`, run on a cloud container's disk (ext4, 4 vCPU):

    SLIQTLY_BLOB_BENCH=1 SLIQTLY_BLOB_BENCH_DIR=/some/disk go test ./store -run TestBlobBench -v -timeout 2h

Each size is put N times (random bytes, no dedup), read back in full, read
at 20 random 64 KB offsets per blob, and half of the blobs are deleted.

## Result

Blob stores, 2026-10-05, SQLite chunk 255 KiB. Every write is durable (fsync) when it returns.

| size | store | put MB/s | put ms (p50) | read MB/s | 64 KB range read µs (p50 / p99) | delete ms (p50) |
|---|---|---:|---:|---:|---:|---:|
| 1 KB × 1000 | SQLite blobs.db | 2 | 0.47 | 63 | 0 / 0 | 0.29 |
| 1 KB × 1000 | files | 1 | 1.60 | 319 | 0 / 1 | 0.18 |
| 100 KB × 200 | SQLite blobs.db | 38 | 1.15 | 1219 | 2 / 11 | 0.35 |
| 100 KB × 200 | files | 34 | 2.74 | 3634 | 2 / 10 | 0.29 |
| 1 MB × 50 | SQLite blobs.db | 100 | 6.68 | 767 | 217 / 1390 | 0.52 |
| 1 MB × 50 | files | 110 | 6.22 | 3386 | 4 / 38 | 1.25 |
| 10 MB × 10 | SQLite blobs.db | 102 | 98.63 | 814 | 281 / 1057 | 2.32 |
| 10 MB × 10 | files | 224 | 46.32 | 5177 | 10 / 50 | 6.19 |
| 100 MB × 3 | SQLite blobs.db | 112 | 890.44 | 926 | 289 / 1259 | 16.69 |
| 100 MB × 3 | files | 274 | 343.93 | 4650 | 13 / 22 | 62.27 |
| 1 GB × 1 | SQLite blobs.db | 123 | 8356.83 | 829 | 295 / 750 | – |
| 1 GB × 1 | files | 285 | 3589.33 | 5262 | 11 / 18 | – |

Document writes to sliqtly.db while a 1 GB blob is written to blobs.db (separate files, so the blob does not hold up the documents):

| | doc writes | p50 ms | p99 ms |
|---|---:|---:|---:|
| alone | 6121 | 0.36 | 2.26 |
| during the blob write | 7093 | 0.61 | 21.23 |

Backup and startup with the blobs that were kept:

| store | blobs | bytes kept | on disk | backup | backup method | open |
|---|---:|---:|---:|---:|---|---:|
| SQLite blobs.db | 634 | 2333 MB | 2346 MB | 13.66 s | VACUUM INTO (a copy) | 3.6 ms |
| files | 633 | 1309 MB | 1309 MB | 0.05 s | hard links (no copy) | 0.0 ms |

"On disk" for SQLite is blobs.db's file size after `Shrink` (incremental
vacuum), measured after the run; the test first counted the backup copy in
it and was fixed afterwards. SQLite keeps one more blob than the folder:
the 1 GB written during the document-write test.

## Chunk size

64 KB range read p50 / p99 in µs (quick runs, 1–100 MB):

| chunk | 1 MB | 10 MB | 100 MB | put MB/s (100 MB) |
|---|---:|---:|---:|---:|
| 1 MiB (first full run) | ~1100 | ~1100–1800 | ~1100–1800 | ~120 |
| 256 KiB | 213 / 1743 | 230 / 1095 | 236 / 1874 | 99 |
| 64 KiB | 75 / 572 | 99 / 627 | 92 / 606 | 116 |

255 KiB was chosen: with 64 KiB pages a chunk is one leaf page and three
overflow pages with no waste. 64 KiB chunks are faster for small ranges but
store a row per 64 KiB (16× the rows for a 1 GB file).

## Reading it

- SQLite is faster than a file per blob below about 1 MB (one transaction
  instead of a file, an fsync of its directory and a rename).
- From 1 MB up, files write 2–2.5× and read about 5× faster. SQLite's
  ~120 MB/s put and ~800 MB/s read are still above what one client upload
  or download uses.
- A range read costs about 0.25 ms against 0.01 ms for a file.
- Document writes go on during a 1 GB blob write (p50 0.36 → 0.65 ms,
  p99 2.3 → 20 ms): blobs.db is a separate file with its own lock and WAL.
- The weak point is backup: `VACUUM INTO` copies the whole of blobs.db
  (14 s for 2.3 GB), where hard links of immutable files cost nothing. An
  incremental blob backup (copy the blobs the last backup lacks) is not
  built yet. `FSBlobStore` implements the same interface if that matters
  more than small-file speed.
