#!/usr/bin/env bash
# Runs the raw and semantic suites for every engine, one process per run,
# REPS times with the engine order rotated each time, and writes JSON lines
# to $OUT. scripts/report.py turns them into tables (medians over runs).
#
#   REPS=3 SIZES="128 1024 10240 102400" SCALE=1.0 OPS=100000 scripts/run_bench.sh
set -euo pipefail
cd "$(dirname "$0")/../.."
BIN=target/release/sliqtly-bench
OUT=${OUT:-sliqtly-bench/results}
DATA=${DATA:-/tmp/sliqtly-bench-data}
REPS=${REPS:-3}
SIZES=${SIZES:-"128 1024 10240 102400"}
SCALE=${SCALE:-1.0}
OPS=${OPS:-100000}
SUITES=${SUITES:-"raw semantic"}
ENGINES=(sliqtly redb fjall lmdb rocksdb sqlite)
mkdir -p "$OUT" "$DATA"
cargo build --release -p sliqtly-bench >/dev/null
for rep in $(seq 1 "$REPS"); do
  order=("${ENGINES[@]:$(( (rep - 1) % 6 ))}" "${ENGINES[@]:0:$(( (rep - 1) % 6 ))}")
  for suite in $SUITES; do
    if [ "$suite" = raw ]; then
      for size in $SIZES; do
        for e in "${order[@]}"; do
          echo "rep $rep raw $size $e" >&2
          rm -rf "$DATA/run"
          "$BIN" raw "$e" "$size" "$DATA/run" | sed "s/}\$/,\"rep\":$rep}/" >> "$OUT/raw.jsonl"
        done
      done
    else
      for e in "${order[@]}"; do
        echo "rep $rep semantic $SCALE $e" >&2
        rm -rf "$DATA/run"
        "$BIN" semantic "$e" "$SCALE" "$OPS" "$DATA/run" | sed "s/}\$/,\"rep\":$rep}/" >> "$OUT/semantic.jsonl"
      done
    fi
  done
done
rm -rf "$DATA/run"
