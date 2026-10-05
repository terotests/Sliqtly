//! Decoded view of the semantic records, shared by info, verify and query.

use crate::backend::Loaded;
use crate::catalog::{tag, KeyReader};
use sliqtly_store::key::KeyType;
use sliqtly_store::record::{Document, Membership, Room};
use std::collections::BTreeMap;
use uuid::Uuid;

pub struct Decoded<T> {
    pub value: T,
}

pub struct Undecodable {
    pub key: Vec<u8>,
    pub error: String,
}

#[derive(Default)]
pub struct Model {
    pub rooms: BTreeMap<Uuid, Decoded<Room>>,
    pub documents: BTreeMap<Uuid, Decoded<Document>>,
    /// Keyed by (user_id, room_id), as in the primary key.
    pub memberships: BTreeMap<(Uuid, Uuid), Decoded<Membership>>,
    pub undecodable: Vec<Undecodable>,
    /// Key id differs from the id inside the record.
    pub id_mismatch: Vec<(Vec<u8>, String)>,
    /// `meta/<name>` entries, value as UTF-8 text.
    pub meta: BTreeMap<String, String>,
    /// Every pair not decoded into rooms, documents or memberships: the
    /// `sliqtly_kv` table, so `.dump` loses nothing.
    pub other: Vec<(Vec<u8>, Vec<u8>)>,
}

impl Model {
    pub fn build(db: &Loaded) -> Model {
        let mut m = Model::default();
        for (k, v) in &db.entries {
            let t = k.first().copied().unwrap_or(0);
            let before = m.undecodable.len();
            let semantic = if t == tag(KeyType::Room) {
                m.room(k, v);
                true
            } else if t == tag(KeyType::Document) {
                m.document(k, v);
                true
            } else if t == tag(KeyType::Membership) {
                m.membership(k, v);
                true
            } else {
                if t == tag(KeyType::Meta) {
                    if let Some(name) = KeyReader::new(k).string() {
                        m.meta.insert(
                            name,
                            String::from_utf8_lossy(v).trim_matches('"').to_string(),
                        );
                    }
                }
                false
            };
            // Anything not cleanly decoded stays visible, byte for byte.
            if !semantic || m.undecodable.len() > before {
                m.other.push((k.clone(), v.clone()));
            }
        }
        m
    }

    fn bad(&mut self, k: &[u8], e: impl ToString) {
        self.undecodable.push(Undecodable {
            key: k.to_vec(),
            error: e.to_string(),
        });
    }

    fn room(&mut self, k: &[u8], v: &[u8]) {
        let mut r = KeyReader::new(k);
        let Some(id) = r.uuid().filter(|_| r.is_empty()) else {
            return self.bad(k, "key is not rooms/<uuid>");
        };
        match serde_json::from_slice::<Room>(v) {
            Ok(room) => {
                if room.id != id {
                    self.id_mismatch
                        .push((k.to_vec(), format!("record id {}", room.id)));
                }
                self.rooms.insert(id, Decoded { value: room });
            }
            Err(e) => self.bad(k, e),
        }
    }

    fn document(&mut self, k: &[u8], v: &[u8]) {
        let mut r = KeyReader::new(k);
        let Some(id) = r.uuid().filter(|_| r.is_empty()) else {
            return self.bad(k, "key is not documents/<uuid>");
        };
        match serde_json::from_slice::<Document>(v) {
            Ok(doc) => {
                if doc.id != id {
                    self.id_mismatch
                        .push((k.to_vec(), format!("record id {}", doc.id)));
                }
                self.documents.insert(id, Decoded { value: doc });
            }
            Err(e) => self.bad(k, e),
        }
    }

    fn membership(&mut self, k: &[u8], v: &[u8]) {
        let mut r = KeyReader::new(k);
        let (Some(user), Some(room)) = (r.uuid(), r.uuid()) else {
            return self.bad(k, "key is not memberships/<user>/<room>");
        };
        if !r.is_empty() {
            return self.bad(k, "trailing bytes after memberships/<user>/<room>");
        }
        match serde_json::from_slice::<Membership>(v) {
            Ok(mem) => {
                if mem.user_id != user || mem.room_id != room {
                    self.id_mismatch.push((
                        k.to_vec(),
                        format!("record user {} room {}", mem.user_id, mem.room_id),
                    ));
                }
                self.memberships
                    .insert((user, room), Decoded { value: mem });
            }
            Err(e) => self.bad(k, e),
        }
    }
}
