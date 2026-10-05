#!/usr/bin/env bash
# Builds src/main.rs (the kernel and semantic modules) with rustc and with
# Ranger for es6, runs both, and checks that they print the same.
# RGRC: path to Ranger's dist/rgrc.js (default ../../../Ranger/dist/rgrc.js).
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
rgrc="${RGRC:-$here/../../../Ranger/dist/rgrc.js}"
out="$(mktemp -d)"
trap 'rm -rf "$out" /tmp/sliqtly-kernel-smoke' EXIT
rm -rf /tmp/sliqtly-kernel-smoke
(cd "$here" && cargo run -q --bin sliqtly-kernel 2>/dev/null) > "$out/rust.txt"
rm -rf /tmp/sliqtly-kernel-smoke
log="$(node "$rgrc" "$here/src/main.rs" -d="$out" -o=smoke.js -nodecli 2>&1)" || true
if grep -q "FAIL" <<<"$log" || [ ! -f "$out/smoke.js" ]; then
  grep -A3 "FAIL" <<<"$log" >&2
  echo "Ranger compile failed" >&2
  exit 1
fi
node "$out/smoke.js" > "$out/es6.txt"
if diff "$out/rust.txt" "$out/es6.txt"; then
  echo "rustc and Ranger es6 output identical ($(wc -l < "$out/rust.txt") lines)"
else
  echo "outputs differ" >&2
  exit 1
fi
