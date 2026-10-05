//! What each key means: the logical tables and indexes of the key layout in
//! `sliqtly_store::key`, decoded back into Sliqtly concepts.

use sliqtly_store::key::KeyType;
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum Kind {
    Primary,
    Index,
    Edge,
    Feed,
    Meta,
}

pub struct Family {
    pub tag: u8,
    pub name: &'static str,
    pub kind: Kind,
}

const fn fam(ty: KeyType, name: &'static str, kind: Kind) -> Family {
    Family {
        tag: ty as u8,
        name,
        kind,
    }
}

/// Every key family in the layout, in tag order.
pub const FAMILIES: &[Family] = &[
    fam(KeyType::Room, "rooms", Kind::Primary),
    fam(KeyType::Document, "documents", Kind::Primary),
    fam(KeyType::File, "files", Kind::Primary),
    fam(KeyType::Blob, "blobs", Kind::Primary),
    fam(KeyType::Membership, "memberships", Kind::Primary),
    fam(KeyType::ExternalRef, "external_refs", Kind::Primary),
    fam(KeyType::CollabOp, "collab_ops", Kind::Primary),
    fam(KeyType::Tree, "blob_trees", Kind::Primary),
    fam(KeyType::Commit, "blob_commits", Kind::Primary),
    fam(KeyType::BlobIndex, "blob_paths", Kind::Primary),
    fam(KeyType::IdxRoomDoc, "room_document", Kind::Index),
    fam(KeyType::IdxUserRoom, "user_room", Kind::Index),
    fam(KeyType::IdxDocRoom, "document_room", Kind::Index),
    fam(KeyType::IdxDocUpdated, "doc_updated", Kind::Index),
    fam(KeyType::IdxFtsRoom, "fts_room", Kind::Index),
    fam(KeyType::EdgeOut, "edges_out", Kind::Edge),
    fam(KeyType::EdgeIn, "edges_in", Kind::Edge),
    fam(KeyType::Seq, "seq", Kind::Feed),
    fam(KeyType::Changes, "changes", Kind::Feed),
    fam(KeyType::Meta, "meta", Kind::Meta),
];

pub fn family(key: &[u8]) -> Option<&'static Family> {
    let tag = *key.first()?;
    FAMILIES.iter().find(|f| f.tag == tag)
}

pub fn tag(ty: KeyType) -> u8 {
    ty as u8
}

/// Reads the fields `KeyBuilder` appends, in order.
pub struct KeyReader<'a> {
    rest: &'a [u8],
}

impl<'a> KeyReader<'a> {
    /// Starts after the one-byte family tag.
    pub fn new(key: &'a [u8]) -> Self {
        KeyReader {
            rest: key.get(1..).unwrap_or(&[]),
        }
    }

    pub fn uuid(&mut self) -> Option<Uuid> {
        if self.rest.len() < 16 {
            return None;
        }
        let (head, tail) = self.rest.split_at(16);
        self.rest = tail;
        Uuid::from_slice(head).ok()
    }

    pub fn be_u64(&mut self) -> Option<u64> {
        if self.rest.len() < 8 {
            return None;
        }
        let (head, tail) = self.rest.split_at(8);
        self.rest = tail;
        Some(u64::from_be_bytes(head.try_into().ok()?))
    }

    /// `push_string`: u16 big-endian length, bytes, NUL terminator.
    pub fn string(&mut self) -> Option<String> {
        if self.rest.len() < 2 {
            return None;
        }
        let len = u16::from_be_bytes([self.rest[0], self.rest[1]]) as usize;
        if self.rest.len() < 2 + len + 1 || self.rest[2 + len] != 0 {
            return None;
        }
        let s = std::str::from_utf8(&self.rest[2..2 + len])
            .ok()?
            .to_string();
        self.rest = &self.rest[2 + len + 1..];
        Some(s)
    }

    pub fn remaining(&self) -> &'a [u8] {
        self.rest
    }

    pub fn is_empty(&self) -> bool {
        self.rest.is_empty()
    }
}

/// Human-readable rendering of a key, for `verify` findings and `inspect`.
pub fn describe_key(key: &[u8]) -> String {
    let Some(f) = family(key) else {
        return format!("unknown:{}", hex(key));
    };
    let mut r = KeyReader::new(key);
    let tail = match f.kind {
        Kind::Primary | Kind::Index | Kind::Edge => {
            let mut parts = Vec::new();
            while r.remaining().len() >= 16 {
                match r.uuid() {
                    Some(u) => parts.push(u.to_string()),
                    None => break,
                }
            }
            if parts.is_empty() {
                let mut r2 = KeyReader::new(key);
                if let Some(s) = r2.string() {
                    parts.push(format!("{s:?}"));
                    if let Some(s2) = r2.string() {
                        parts.push(format!("{s2:?}"));
                    }
                    r = r2;
                }
            }
            if !r.is_empty() {
                parts.push(hex(r.remaining()));
            }
            parts.join("/")
        }
        Kind::Feed | Kind::Meta => {
            let mut r2 = KeyReader::new(key);
            if let Some(s) = r2.string() {
                s
            } else if let Some(n) = r.be_u64() {
                format!(
                    "{n}{}",
                    if r.is_empty() {
                        String::new()
                    } else {
                        format!("/{}", hex(r.remaining()))
                    }
                )
            } else {
                hex(r.remaining())
            }
        }
    };
    format!("{}/{}", f.name, tail)
}

pub fn hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{x:02x}")).collect()
}
