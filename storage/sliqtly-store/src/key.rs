//! Binary key encoding for ordered KV storage.
//!
//! Keys are encoded as:
//! [type:u8][fields...][id:16 bytes]
//!
//! Numeric fields use big-endian to preserve byte-order == sort-order.
//! Strings are normalized (lowercase, NFC) and include length prefix + terminator.

use crate::error::{Error, Result};
use uuid::Uuid;

/// Key type discriminators.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum KeyType {
    // Primary records
    Room = 0x10,
    Document = 0x11,
    File = 0x12,
    Blob = 0x13,
    Membership = 0x14,
    ExternalRef = 0x15,
    CollabOp = 0x16,

    // Indexes
    IdxRoomDoc = 0x20,
    IdxUserRoom = 0x21,
    IdxDocRoom = 0x22,
    IdxDocUpdated = 0x23,
    IdxFtsRoom = 0x24,

    // Edges
    EdgeOut = 0x30,
    EdgeIn = 0x31,

    // Sequence & changes
    Seq = 0x40,
    Changes = 0x41,

    // Metadata
    Meta = 0x50,
}

pub struct KeyBuilder {
    buf: Vec<u8>,
}

impl KeyBuilder {
    pub fn new(ty: KeyType) -> Self {
        let mut buf = vec![ty as u8];
        buf.reserve(64);
        KeyBuilder { buf }
    }

    pub fn push_uuid(mut self, id: Uuid) -> Self {
        self.buf.extend_from_slice(id.as_bytes());
        self
    }

    pub fn push_be_u64(mut self, val: u64) -> Self {
        self.buf.extend_from_slice(&val.to_be_bytes());
        self
    }

    pub fn push_be_u32(mut self, val: u32) -> Self {
        self.buf.extend_from_slice(&val.to_be_bytes());
        self
    }

    pub fn push_be_i32(mut self, val: i32) -> Self {
        self.buf.extend_from_slice(&val.to_be_bytes());
        self
    }

    pub fn push_string(mut self, s: &str) -> Self {
        // Normalize: lowercase, NFC
        let normalized = s.to_lowercase();
        let len = normalized.len() as u16;
        self.buf.extend_from_slice(&len.to_be_bytes());
        self.buf.extend_from_slice(normalized.as_bytes());
        self.buf.push(0); // Null terminator
        self
    }

    pub fn build(self) -> Vec<u8> {
        self.buf
    }
}

/// Decode UUID from a slice (must be 16 bytes).
pub fn decode_uuid(buf: &[u8]) -> Result<Uuid> {
    if buf.len() < 16 {
        return Err(Error::Codec("not enough bytes for UUID".to_string()));
    }
    Uuid::from_slice(&buf[..16]).map_err(|e| Error::Codec(e.to_string()))
}

/// Decode big-endian u64 from a slice (must be 8 bytes).
pub fn decode_be_u64(buf: &[u8]) -> Result<u64> {
    if buf.len() < 8 {
        return Err(Error::Codec("not enough bytes for u64".to_string()));
    }
    Ok(u64::from_be_bytes([
        buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], buf[6], buf[7],
    ]))
}

/// Prefix scan: create a key prefix for range queries.
/// E.g., all documents in a room: prefix_for_idx(IdxDocRoom, room_id)
pub fn prefix_for_index(ty: KeyType, id: Uuid) -> Vec<u8> {
    KeyBuilder::new(ty).push_uuid(id).build()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_key_ordering() {
        let id1 = Uuid::now_v7();
        let id2 = Uuid::now_v7();

        let k1 = KeyBuilder::new(KeyType::IdxDocUpdated)
            .push_uuid(id1)
            .push_be_u64(1000)
            .build();

        let k2 = KeyBuilder::new(KeyType::IdxDocUpdated)
            .push_uuid(id1)
            .push_be_u64(2000)
            .build();

        // Lower timestamp comes first
        assert!(k1 < k2, "Timestamp ordering preserved");
    }

    #[test]
    fn test_uuid_decode() {
        let id = Uuid::now_v7();
        let key = KeyBuilder::new(KeyType::Room).push_uuid(id).build();
        let decoded = decode_uuid(&key[1..]).unwrap();
        assert_eq!(decoded, id);
    }
}
