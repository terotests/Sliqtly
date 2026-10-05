# sliqtly-db

Operator tool for SliqtlyDB databases. Every command except `restore` opens
the database read-only and works on one consistent snapshot.

```text
sliqtly-db --db <dir> info                 format, versions, CommitSeq, counts, index health, storage, last backup
sliqtly-db --db <dir> stats                keys / key bytes / value bytes per key family
sliqtly-db --db <dir> verify [--deep]      integrity checks; exit 1 on failure (--strict: also on warnings)
sliqtly-db --db <dir> query [SQL]          read-only SQL over rooms, documents, memberships; stdin shell without SQL
sliqtly-db --db <dir> backup <dest>        checksummed snapshot + manifest into an empty directory
sliqtly-db --db <dir> restore <src>        validate, restore (keeps CommitSeq), then verify --deep
```

`--db` can also come from `SLIQTLY_DB`. `--json` gives machine-readable output
for `info`, `stats`, `verify`, `query` and `backup`. Exit codes: 0 ok,
1 verification failed, 2 usage or I/O error.

## verify

Quick pass: storage files, key families, primary record decoding (key id
matches record id), record revisions, format metadata, change-feed continuity.
`--deep` adds reference integrity, secondary index completeness and dangling
entries, edge forward/reverse symmetry, and blob SHA-256 / Adler-32 checks.

An index with no entries at all is reported as a warning ("not maintained by
the writer"), because the current `sliqtly-store` writer does not maintain
indexes or a change feed yet. Once an index has entries it must be complete.

## query

```sql
SELECT id, title FROM rooms
WHERE updated > now() - interval '7 days'
ORDER BY updated DESC LIMIT 20;
```

`SELECT * | COUNT(*) | cols`, `WHERE` with `AND OR NOT`, comparisons, `LIKE`,
`IS [NOT] NULL`, `now()`, `interval '<n> <unit>'`, `ORDER BY`, `LIMIT`.
`updated`, `created`, `joined` are accepted for the `*_at` columns.

## backup format

`records.sqkv` (engine-neutral framed key/value records) and `manifest.json`
(storage format, schema version, CommitSeq, engine, created_at, SHA-256 per
file, blob manifest). Backups are read back and verified before the command
succeeds; `restore` checks checksums before writing anything and replaces the
target atomically.

## Engines

`src/backend.rs` reads the `state.bin` format written by
`sliqtly_store::FjallEngine`. The commands only see `backend::Loaded`, so the
Sliqtly kernel is one more `open` arm.

Try it: `cargo run -p sliqtly-db --example seed -- /tmp/demo && cargo run -p sliqtly-db -- --db /tmp/demo info`
