#!/bin/sh
# Builds svgraster.wasm from src/ (needs rustup's wasm32-unknown-unknown
# target: rustup target add wasm32-unknown-unknown). Commit the result.
set -e
cd "$(dirname "$0")"
# SIMD: tiny-skia's pipelines run 4–8 pixels at a time; with opt-level 3
# an aurora background with clip paths and a pattern draws in 0.4 s instead
# of 8 s (wazero runs WebAssembly SIMD)
RUSTFLAGS="-C target-feature=+simd128" cargo build --release --locked --target wasm32-unknown-unknown
cp target/wasm32-unknown-unknown/release/svgraster.wasm ../svgraster.wasm
ls -l ../svgraster.wasm
