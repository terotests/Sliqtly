//! Sliqtly transactional key-value store.
//!
//! A custom storage layer optimized for collaborative document/room operations.
//! Builds on an ordered KV engine (Fjall) with:
//! - Typed records and binary key encoding
//! - Atomic secondary index maintenance
//! - Sequence-based change feed and MVCC
//! - Graph traversal via index prefixes
//! - Storage-level access control
//!
//! Architecture:
//! ```text
//!              SliqtlyDB
//!     ┌──────────────────────┐
//!     │ SQL/query frontend   │
//!     │ query optimizer      │
//!     │ secondary indexes    │
//!     │ change feed          │
//!     ├──────────────────────┤
//!     │ ordered KV kernel    │
//!     │ Fjall → own engine   │
//!     └──────────────────────┘
//! ```

pub mod engine;
pub mod error;
pub mod key;
pub mod memory_engine;
pub mod record;
pub mod transaction;

pub use error::{Error, Result};
pub use record::{Document, Membership, Room, RoomId, UserId};
pub use transaction::Database;

use std::path::Path;

/// Open or create a Sliqtly database at the given path.
pub fn open<P: AsRef<Path>>(path: P) -> Result<Database> {
    Database::open(path)
}
