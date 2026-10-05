//! Typed record definitions for Sliqtly data model.

use serde::{Deserialize, Serialize};
use uuid::Uuid;
use chrono::{DateTime, Utc};

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
    pub created_at: DateTime<Utc>,
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
    pub created_at: DateTime<Utc>,
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
