# Archived

SliqtlyDB (`sliqtly-store`, `sliqtly-kernel`, the `sliqtly-db` CLI and the
SQLite benchmark here) is archived as of 2026-10-05. Nothing was deleted;
the branches and PRs (#212, #215) stay unmerged for reference.

The Go server uses SQLite instead: documents and file references in
`sliqtly.db`, file bytes in `blobs.db`. The reasons and the benchmark
results are in `docs/adr/0002-sqlite-storage.md` (terotests/Sliqtly#219).
In short, SliqtlyDB was about even with SQLite on Sliqtly's workload,
which does not pay for owning a storage engine.
