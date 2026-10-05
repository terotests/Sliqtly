# ADR 0002: SQLite for the server's own storage; SliqtlyDB archived

Status: accepted (2026-10-05, Tero). Applies to the folder server
(`SLIQTLY_DATA`, the local app, the `.deb` and Docker images). sliqtly.com
keeps Firestore and Cloud Storage.

## The question

The folder server kept one JSON file per document (`db/`) and one file per
kept file (`files/`), each written to a temporary file, synced and renamed.
Should it move to an engine of our own (SliqtlyDB, `storage/` on the
`claude/vibrant-keller-l32zl3` branch), or to an existing embedded database?

## What the benchmark said

SliqtlyDB was benchmarked against SQLite on Sliqtly's own workload
(terotests/Sliqtly#215, `storage/sliqtly-sqlite-bench/RESULTS.md`). After
that branch's work (WAL engine, room index, faster commits), it beat SQLite
on point reads and membership lookups. It was about even on transactional
writes, and behind on listing a room's documents, open time, disk use and
memory. It keeps the whole store in memory. Being about even with SQLite
does not pay for owning a storage engine: its crash safety, its tooling
and its migrations.

## Decision

**SQLite, two files, behind the store contracts the server already has.**

```
<data>/sliqtly.db   documents (store.Engine), file references, logs
<data>/blobs.db     immutable bytes by SHA-256, in chunks (store.BlobStore)
```

- **Documents:** `store.SQLiteStore` implements `store.Engine`. It passes
  `storetest.Run`, the same contract the folder and memory backends pass,
  the authorization tests included. Documents are the same JSON the folder
  kept.
- **Files:** a path (`shares/{id}/media/x`) is a row in `file_refs`
  (path → hash, size, type). The bytes are in `blobs.db`. Whether someone
  may read a path is decided by what the path belongs to; a hash is not a
  permission.
- **blobs.db is a file of its own**, so gigabytes of attachments do not grow
  the documents' backups, WAL and checkpoints. The benchmark shows document
  writes go on, slower but not blocked, while a 1 GB blob is written.
- **Chunks:**
  - A blob is written in 255 KiB chunks, committed 64 at a time. The blob's
    row is added last and is the commit point.
  - So the WAL holds at most 16 MiB of a large upload, and a crash leaves
    only an upload that the next open removes.
  - A Range request reads only the chunks it covers. The chunk size was
    measured: a 64 KB range read cost 1.1 ms with 1 MiB chunks and about
    0.2 ms with 256 KiB ones (`mcp-go/store/BENCHMARK-blobs.md`).
- **Dedup and collection:**
  - The same bytes under two paths are kept once.
  - Because a blob can have many references, it is never deleted directly.
    `CollectBlobs` removes blobs that no reference names, after an hour's
    grace. That grace covers a blob written just before its reference,
    since the two files cannot share one transaction.
  - The hourly sweep runs it, then gives the space back (`auto_vacuum`
    incremental).
- **The same API elsewhere:** `store.BlobStore` (Put a stream, Open with
  ReadAt and Seek, Stat, Delete, Each) also has `FSBlobStore`. An S3 backend
  for a PostgreSQL deployment would be a third.
- **RangerDiff's versions** use the same SHA-256 ids (RdRepo), so a deck's
  history can keep its objects in `blobs.db`. RdRepo also rewrites an old
  version as a reverse delta under the same id, so a later blobs schema
  migration adds an encoding (raw, or a delta against a base hash). The
  content of a hash never changes, only how it is stored. Deltas pay for
  Markdown, xlsx and PNG (0.3–6 % of the file in RangerDiff's benchmark),
  not for JPEG or video (about 99 %): those rely on dedup alone.

## Migrations

There are two levels, both refusing what is newer than the server knows.

1. **The folder's layout** (`datafmt.go`, `format.json`). Format 4 is the
   move to SQLite:
   - The folder is backed up first (hard links).
   - The databases are built as `*.migrating`.
   - They are checked against the folder: every document's digest, every
     file's SHA-256 and every blob read back.
   - They are renamed into place, `sliqtly.db` last, which is the commit
     point. Only then are `db/` and `files/` removed.
   - A run stopped before the rename starts over; one stopped after it only
     finishes the removal.
   - From format 4 on, the backup before a migration copies `sliqtly.db`
     with `VACUUM INTO`, since a database is written in place and a hard
     link would not keep its old contents.
2. **Each database's schema** (`store/sqlmigrate.go`):
   - The schema is a numbered, append-only list of migrations
     (`SQLiteSchema`, `SQLiteBlobSchema`).
   - `PRAGMA user_version` is the last one applied, and `schema_history` has
     a row per migration (version, note, when, which server).
   - Each migration runs in one transaction with its history row.
   - Before an existing file is migrated, it is copied to `backups/` with
     `VACUUM INTO`.

Going on to PostgreSQL later is `store.Copy` + `store.Verify` (documents
with their revisions) and the blobs copied by hash. The same contracts are
on both sides.

## SliqtlyDB

Archived, not deleted. Its branches and PRs stay as they are
(`claude/vibrant-keller-l32zl3` with `storage/sliqtly-store` and
`storage/sliqtly-kernel`; #212 the `sliqtly-db` admin CLI; #215 the SQLite
benchmark). Things worth keeping from it:
- the benchmark harness;
- the `sliqtly-db` CLI's sqlite3-compatible shell and `verify --deep`
  checks, which have counterparts in SQLite's own `sqlite3` and
  `PRAGMA integrity_check`;
- the prefix-scan bug it found.
