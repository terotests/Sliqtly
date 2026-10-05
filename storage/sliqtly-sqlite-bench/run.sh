#!/usr/bin/env bash
# Runs every engine × durability combination REPEAT times (each in a fresh
# process and directory), writes results.jsonl and refreshes the tables in
# RESULTS.md.
set -euo pipefail
cd "$(dirname "$0")/.."
REPEAT=${REPEAT:-3}
OUT=${OUT:-sliqtly-sqlite-bench/results.jsonl}
cargo build --release -q -p sliqtly-sqlite-bench
BIN=target/release/sliqtly-sqlite-bench
: > "$OUT"
for i in $(seq "$REPEAT"); do
  for e in sqlite log; do
    for d in sync nosync; do
      "$BIN" run --engine "$e" --durability "$d" >> "$OUT"
    done
  done
  # state-bin never fsyncs and costs O(store) per operation: fewer ops.
  "$BIN" run --engine statebin --durability nosync --reads 2000 --writes 100 >> "$OUT"
done
python3 sliqtly-sqlite-bench/summarize.py "$OUT" sliqtly-sqlite-bench/RESULTS.md
python3 sliqtly-sqlite-bench/summarize.py "$OUT"
