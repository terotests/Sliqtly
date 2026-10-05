#!/bin/sh
# Builds svgraster.wasm from src/ (needs rustup's wasm32-unknown-unknown
# target: rustup target add wasm32-unknown-unknown). Commit the result.
set -e
cd "$(dirname "$0")"
cargo build --release --locked --target wasm32-unknown-unknown
cp target/wasm32-unknown-unknown/release/svgraster.wasm ../svgraster.wasm
ls -l ../svgraster.wasm
