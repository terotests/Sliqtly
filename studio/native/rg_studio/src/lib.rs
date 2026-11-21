// SPDX-License-Identifier: AGPL-3.0-or-later
//! The RangerGames engine behind the same C ABI the browser host uses
//! (RangerGames crates/rg_web/src/abi.rs), built as a static library that
//! Sliqtly Game Studio links into its native window. One engine per
//! process, driven from the window's thread.

fn new_engine() -> rg_engine::Engine {
    rg_engine::Engine::new()
}

include!("../../../.deps/RangerGames/crates/rg_web/src/abi.rs");

/// Drops the engine, so the next call starts a fresh one with an empty file
/// system (the Studio's full reload).
#[no_mangle]
pub extern "C" fn rg_studio_reset() {
    ENGINE.with(|e| *e.borrow_mut() = None);
}
