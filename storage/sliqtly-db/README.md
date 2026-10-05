# sliqtly-db

Operator tool for SliqtlyDB databases. It has two front ends over the same
read-only snapshot:

**sqlite3-compatible shell.** Same invocation, options, dot-commands, output
modes, error wording and exit status as the `sqlite3` program, with the
database directory in place of the file:

```text
sliqtly-db [OPTIONS] DBDIR [SQL|.COMMAND ...]

sliqtly-db /var/lib/sliqtly/db "SELECT id, title FROM rooms ORDER BY updated_at DESC LIMIT 5"
sliqtly-db -header -csv /var/lib/sliqtly/db "SELECT * FROM documents" > docs.csv
sliqtly-db /var/lib/sliqtly/db "PRAGMA integrity_check"
sliqtly-db /var/lib/sliqtly/db .dump | sqlite3 copy.db      # loads into real SQLite
sliqtly-db /var/lib/sliqtly/db                              # interactive shell
```

- Options: `-bail -batch -box -cmd -column -csv -echo -header -noheader -help
  -init -json -line -list -markdown -newline -nullvalue -quote -readonly
  -separator -table -tabs -version`.
- Dot-commands: `.backup .bail .databases .dbinfo .dump .echo .exit .headers
  .help .indexes .mode .nullvalue .once .open .output .print .quit .read
  .schema .separator .show .tables .timer`.
- PRAGMAs: `integrity_check[(N)]` (= `verify --deep`, prints `ok` or one line
  per problem), `quick_check` (= quick `verify`), `table_info`, `table_xinfo`,
  `table_list`, `index_list`, `index_info`, `database_list`, `user_version`,
  `data_version` (= CommitSeq), `commit_seq`, `encoding`, `query_only`.
- Writes (`INSERT`, `UPDATE`, `DELETE`, `CREATE`, `PRAGMA x = y`, `.restore`,
  `.import`) fail with `attempt to write a readonly database`, as with
  `sqlite3 -readonly`. Restore goes through the `restore` subcommand.

**Admin subcommands.** For checks and backups with machine-readable output:

```text
sliqtly-db --db <dir> info                 format, versions, CommitSeq, counts, index health, storage, last backup
sliqtly-db --db <dir> stats                keys / key bytes / value bytes per key family
sliqtly-db --db <dir> verify [--deep]      integrity checks; exit 1 on failure (--strict: also on warnings)
sliqtly-db --db <dir> query [SQL]          the SQL dialect below, table output
sliqtly-db --db <dir> backup <dest>        checksummed snapshot + manifest into an empty directory
sliqtly-db --db <dir> restore <src>        validate, restore (keeps CommitSeq), then verify --deep
```

`--db` can also come from `SLIQTLY_DB`. `--json` gives machine-readable output
for `info`, `stats`, `verify`, `query` and `backup`. Exit codes: 0 ok,
1 verification failed, 2 usage or I/O error.

## Tables

`rooms`, `documents`, `memberships` hold the decoded records; `sliqtly_kv(key
BLOB, value BLOB)` holds every other key/value pair byte for byte, so `.dump`
loses nothing. `sqlite_schema` / `sqlite_master` describe them, plus the
layout's secondary indexes as `CREATE INDEX` statements. `.schema` shows the
same SQL. `created`/`updated`/`joined` work as short names for the `*_at`
columns.

## SQL

SQLite dialect, `SELECT` only: `DISTINCT`, expressions and aliases, `WHERE`,
`GROUP BY` (expressions, aliases, ordinals), `HAVING`, `ORDER BY`, `LIMIT/OFFSET`,
`CASE`, `IN`, `BETWEEN`, `LIKE` (ASCII case-insensitive), `IS [NOT]`, `||`, the
aggregates `count sum total avg min max group_concat`, and the functions
`lower upper length substr instr replace trim hex typeof abs round coalesce
ifnull nullif date time datetime unixepoch strftime json_extract json_valid`.
`now() - interval '7 days'` also works. Joins and subqueries are not
supported and fail with an error rather than returning wrong rows.
Extensions: `sliqtly_family(key)` and `sliqtly_key(key)` decode `sliqtly_kv` keys.

Timestamps are compared as instants when both sides read as dates, so the
RFC 3339 text the store writes compares correctly with `datetime()` output.
SQLite compares them as plain text.

`tests/sqlite_compat.rs` loads `.dump` into SQLite and checks that a set of
queries returns identical output in both.

## verify

Quick pass: storage files, key families, primary record decoding (key id
matches record id), record revisions, format metadata, change-feed continuity.
`--deep` adds reference integrity, secondary index completeness and dangling
entries, edge forward/reverse symmetry, and blob SHA-256 / Adler-32 checks.

An index with no entries at all is reported as a warning ("not maintained by
the writer"), because the current `sliqtly-store` writer does not maintain
indexes or a change feed yet. Once an index has entries it must be complete.

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
