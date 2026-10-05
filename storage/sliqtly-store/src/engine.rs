//! Key-value engine abstraction.
//!
//! Defines the interface between semantic DB and storage backend.
//! Allows swapping BTreeMap, Fjall, Redb, etc. without changing public API.

use crate::error::Result;
use std::sync::Arc;

pub type CommitSeq = u64;

/// A snapshot of the database at a specific CommitSeq.
pub trait DbSnapshot: Send + Sync {
    fn seq(&self) -> CommitSeq;
    fn get(&self, key: &[u8]) -> Result<Option<Vec<u8>>>;
    fn scan(&self, range: KeyRange) -> Result<Vec<(Vec<u8>, Vec<u8>)>>;
}

/// Range specification for scans.
#[derive(Clone, Debug)]
pub struct KeyRange {
    pub lower: Vec<u8>,
    pub upper: Vec<u8>,
    pub inclusive_upper: bool,
}

impl KeyRange {
    pub fn prefix(prefix: Vec<u8>) -> Self {
        let mut upper = prefix.clone();
        upper.push(u8::MAX);
        KeyRange {
            lower: prefix,
            upper,
            inclusive_upper: false,
        }
    }

    pub fn between(lower: Vec<u8>, upper: Vec<u8>) -> Self {
        KeyRange {
            lower,
            upper,
            inclusive_upper: false,
        }
    }

    pub fn between_inclusive(lower: Vec<u8>, upper: Vec<u8>) -> Self {
        KeyRange {
            lower,
            upper,
            inclusive_upper: true,
        }
    }
}

/// Mutation to apply in a write batch.
#[derive(Clone, Debug)]
pub enum Mutation {
    Put(Vec<u8>, Vec<u8>),
    Delete(Vec<u8>),
}

/// Precondition for atomic compare-and-set semantics.
/// A transaction commits only if all conditions match current state.
#[derive(Clone, Debug)]
pub enum Condition {
    /// Key must not exist in database.
    KeyAbsent(Vec<u8>),
    /// Key must exist with exactly this value hash (SHA-256 hex).
    /// Prevents blind overwrites of values we didn't read.
    ValueEquals(Vec<u8>, String),
    /// Record must have exactly this revision (RecordRev).
    /// Used for document/room/etc. with version tracking.
    RecordRevEquals(Vec<u8>, u64),
}

/// A batch of mutations to commit atomically.
/// Commit succeeds only if all conditions are met against CURRENT state.
#[derive(Clone, Debug, Default)]
pub struct WriteBatch {
    pub conditions: Vec<Condition>,
    pub mutations: Vec<Mutation>,
}

impl WriteBatch {
    pub fn new() -> Self {
        WriteBatch {
            conditions: Vec::new(),
            mutations: Vec::new(),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.mutations.is_empty()
    }

    /// Require key to not exist. Fails if key is present.
    pub fn expect_absent(&mut self, key: Vec<u8>) {
        self.conditions.push(Condition::KeyAbsent(key));
    }

    /// Require key to have exactly this value. Fails if value changed.
    pub fn expect_value(&mut self, key: Vec<u8>, value_hash: String) {
        self.conditions.push(Condition::ValueEquals(key, value_hash));
    }

    /// Require record to have exactly this revision. Fails if rev changed.
    pub fn expect_record_rev(&mut self, key: Vec<u8>, expected_rev: u64) {
        self.conditions.push(Condition::RecordRevEquals(key, expected_rev));
    }

    pub fn push(&mut self, mutation: Mutation) {
        self.mutations.push(mutation);
    }

    pub fn put(&mut self, key: Vec<u8>, value: Vec<u8>) {
        self.push(Mutation::Put(key, value));
    }

    pub fn delete(&mut self, key: Vec<u8>) {
        self.push(Mutation::Delete(key));
    }
}

/// Commit result indicates success, no-op, or conflict.
#[derive(Debug, Clone)]
pub enum CommitResult {
    /// Batch was empty; state unchanged.
    NoChanges,
    /// Batch was applied; new sequence number.
    Applied { seq: CommitSeq },
    /// Precondition failed; state conflicted. Transaction aborted.
    Conflict { condition_index: usize },
}

/// Abstraction for KV storage backend.
pub trait KvEngine: Send + Sync {
    /// Take a snapshot of the current database state.
    fn snapshot(&self) -> Result<Arc<dyn DbSnapshot>>;

    /// Atomically apply a batch of mutations.
    /// Returns NoChanges if batch is empty, or Applied with new Seq if committed.
    fn commit(&self, batch: WriteBatch) -> Result<CommitResult>;
}
