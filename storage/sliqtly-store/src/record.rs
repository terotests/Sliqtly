//! Typed record definitions for Sliqtly data model.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub type RoomId = Uuid;
pub type DocumentId = Uuid;
pub type UserId = Uuid;
pub type FileId = Uuid;
pub type BlobId = Uuid;

/// A Room is the top-level container for collaborative content.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Room {
    pub id: RoomId,
    pub title: String,
    pub description: Option<String>,
    pub created_by: UserId,
    #[serde(deserialize_with = "fast_time::deserialize")]
    pub created_at: DateTime<Utc>,
    #[serde(deserialize_with = "fast_time::deserialize")]
    pub updated_at: DateTime<Utc>,
    pub metadata: serde_json::Value,
}

/// A Document belongs to a Room.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Document {
    pub id: DocumentId,
    pub room_id: RoomId,
    pub title: String,
    pub created_by: UserId,
    #[serde(deserialize_with = "fast_time::deserialize")]
    pub created_at: DateTime<Utc>,
    #[serde(deserialize_with = "fast_time::deserialize")]
    pub updated_at: DateTime<Utc>,
    pub version: u64,
    pub metadata: serde_json::Value,
}

/// A FileRef points to a blob within a Document.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FileRef {
    pub id: FileId,
    pub document_id: DocumentId,
    pub room_id: RoomId,
    pub blob_id: Option<BlobId>,
    pub name: String,
    pub mime: String,
    pub created_at: DateTime<Utc>,
    pub metadata: serde_json::Value,
}

/// A Blob is immutable stored data (separate from KV values).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Blob {
    pub id: BlobId,
    pub sha256: [u8; 32],
    pub size: u64,
    pub mime: String,
    pub references: u32, // Reference count for GC
    pub created_at: DateTime<Utc>,
}

/// Room Membership: user access to a room.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    Owner,
    Editor,
    Viewer,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Membership {
    pub user_id: UserId,
    pub room_id: RoomId,
    pub role: Role,
    #[serde(deserialize_with = "fast_time::deserialize")]
    pub joined_at: DateTime<Utc>,
    pub metadata: serde_json::Value,
}

/// External reference: link to external system (e.g., Slack, GitHub).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExternalRef {
    pub id: Uuid,
    pub room_id: RoomId,
    pub provider: String, // "slack", "github", etc.
    pub external_id: String,
    pub metadata: serde_json::Value,
    pub created_at: DateTime<Utc>,
}

/// A room-to-room link (graph edge).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RoomLink {
    pub from: RoomId,
    pub to: RoomId,
    pub kind: String, // "references", "parent", "child", etc.
    pub metadata: serde_json::Value,
    pub created_at: DateTime<Utc>,
}

/// Change log entry: describes a single atomic change.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum Change {
    RoomCreated { room: Room },
    RoomUpdated { room: Room, version: u64 },
    RoomDeleted { room_id: RoomId },

    DocumentCreated { doc: Document },
    DocumentUpdated { doc: Document, version: u64 },
    DocumentDeleted { doc_id: DocumentId },

    MembershipAdded { membership: Membership },
    MembershipRemoved { user_id: UserId, room_id: RoomId },

    FileAdded { file: FileRef },
    FileRemoved { file_id: FileId },
}

impl Change {
    pub fn room_id(&self) -> Option<RoomId> {
        match self {
            Self::RoomCreated { room } => Some(room.id),
            Self::RoomUpdated { room, .. } => Some(room.id),
            Self::RoomDeleted { room_id } => Some(*room_id),
            Self::DocumentCreated { doc } => Some(doc.room_id),
            Self::DocumentUpdated { doc, .. } => Some(doc.room_id),
            Self::DocumentDeleted { .. } => None,
            Self::MembershipAdded { membership } => Some(membership.room_id),
            Self::MembershipRemoved { room_id, .. } => Some(*room_id),
            Self::FileAdded { file } => Some(file.room_id),
            Self::FileRemoved { .. } => None,
        }
    }
}

/// Sequence number: unique ordering for all changes.
/// Used for MVCC, change feed, replication, etc.
pub type Seq = u64;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_room_creation() {
        let room = Room {
            id: Uuid::now_v7(),
            title: "Test Room".to_string(),
            description: None,
            created_by: Uuid::now_v7(),
            created_at: Utc::now(),
            updated_at: Utc::now(),
            metadata: serde_json::json!({}),
        };
        assert!(!room.title.is_empty());
    }
}

/// Timestamp decoding on the read path. chrono's RFC 3339 parser is general
/// (offsets, lowercase, spaces) and was ~30% of `Document` decode time; the
/// store always writes `YYYY-MM-DDTHH:MM:SS[.fraction]Z`, which is parsed
/// directly here. Anything else falls back to chrono, so accepted input is
/// unchanged.
pub mod fast_time {
    use chrono::{DateTime, FixedOffset, NaiveDate, Utc};
    use serde::de::{self, Deserializer, Visitor};
    use std::fmt;

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<DateTime<Utc>, D::Error> {
        struct V;
        impl Visitor<'_> for V {
            type Value = DateTime<Utc>;
            fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
                f.write_str("an RFC 3339 timestamp")
            }
            fn visit_str<E: de::Error>(self, s: &str) -> Result<Self::Value, E> {
                parse(s).ok_or_else(|| E::custom(format!("invalid timestamp {s:?}")))
            }
        }
        d.deserialize_str(V)
    }

    pub fn parse(s: &str) -> Option<DateTime<Utc>> {
        parse_utc_z(s.as_bytes()).or_else(|| {
            s.parse::<DateTime<FixedOffset>>()
                .ok()
                .map(|t| t.with_timezone(&Utc))
        })
    }

    fn parse_utc_z(b: &[u8]) -> Option<DateTime<Utc>> {
        if b.len() < 20
            || b[4] != b'-'
            || b[7] != b'-'
            || b[10] != b'T'
            || b[13] != b':'
            || b[16] != b':'
        {
            return None;
        }
        let num = |r: std::ops::Range<usize>| -> Option<u32> {
            b[r].iter().try_fold(0u32, |n, c| {
                c.is_ascii_digit().then(|| n * 10 + (c - b'0') as u32)
            })
        };
        let (y, mo, d) = (num(0..4)?, num(5..7)?, num(8..10)?);
        let (h, mi, sec) = (num(11..13)?, num(14..16)?, num(17..19)?);
        let mut i = 19;
        let mut nanos = 0u32;
        if b[i] == b'.' {
            i += 1;
            let start = i;
            while i < b.len() && b[i].is_ascii_digit() {
                i += 1;
            }
            let digits = i - start;
            if digits == 0 || digits > 9 {
                return None;
            }
            nanos = num(start..i)? * 10u32.pow((9 - digits) as u32);
        }
        if i + 1 != b.len() || b[i] != b'Z' {
            return None;
        }
        // Leap seconds (sec == 60) go through chrono.
        if sec >= 60 {
            return None;
        }
        let t = NaiveDate::from_ymd_opt(y as i32, mo, d)?.and_hms_nano_opt(h, mi, sec, nanos)?;
        Some(t.and_utc())
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn matches_chrono_on_written_and_foreign_formats() {
            let now = Utc::now();
            for s in [
                serde_json::to_string(&now)
                    .unwrap()
                    .trim_matches('"')
                    .to_string(),
                "2026-10-05T17:46:53Z".into(),
                "2026-10-05T17:46:53.1Z".into(),
                "2026-10-05T17:46:53.123456789Z".into(),
                "2026-10-05T19:46:53.5+02:00".into(),
                "2026-10-05t17:46:53z".into(),
            ] {
                let want = s
                    .parse::<DateTime<FixedOffset>>()
                    .unwrap()
                    .with_timezone(&Utc);
                assert_eq!(parse(&s), Some(want), "{s}");
            }
            for bad in [
                "2026-13-05T17:46:53Z",
                "2026-10-05T17:46:53.Z",
                "2026-10-05T17:46:53.1234567891Z",
                "x",
            ] {
                assert_eq!(
                    parse(bad),
                    bad.parse::<DateTime<FixedOffset>>()
                        .ok()
                        .map(|t| t.with_timezone(&Utc)),
                    "{bad}"
                );
            }
        }
    }
}
