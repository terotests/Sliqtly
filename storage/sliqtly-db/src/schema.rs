//! The relational view of the key layout, as SQLite would describe it.
//!
//! One definition feeds `.schema`, `.dump`, `sqlite_schema`, `PRAGMA table_info`
//! and the query engine, so what the shell shows is what `.dump` produces and
//! what a real `sqlite3` accepts.

pub struct Column {
    pub name: &'static str,
    /// SQLite declared type.
    pub ty: &'static str,
    pub not_null: bool,
}

pub struct Table {
    pub name: &'static str,
    pub columns: &'static [Column],
    pub primary_key: &'static [&'static str],
}

pub struct Index {
    pub name: &'static str,
    pub table: &'static str,
    pub columns: &'static [&'static str],
}

const fn c(name: &'static str, ty: &'static str, not_null: bool) -> Column {
    Column { name, ty, not_null }
}

/// Keys that are not rooms, documents or memberships, byte for byte.
pub const KV_TABLE: &str = "sliqtly_kv";

pub const TABLES: &[Table] = &[
    Table {
        name: "rooms",
        columns: &[
            c("id", "TEXT", true),
            c("title", "TEXT", true),
            c("description", "TEXT", false),
            c("created_by", "TEXT", true),
            c("created_at", "TEXT", true),
            c("updated_at", "TEXT", true),
            c("metadata", "TEXT", false),
        ],
        primary_key: &["id"],
    },
    Table {
        name: "documents",
        columns: &[
            c("id", "TEXT", true),
            c("room_id", "TEXT", true),
            c("title", "TEXT", true),
            c("version", "INTEGER", true),
            c("created_by", "TEXT", true),
            c("created_at", "TEXT", true),
            c("updated_at", "TEXT", true),
            c("metadata", "TEXT", false),
        ],
        primary_key: &["id"],
    },
    Table {
        name: "memberships",
        columns: &[
            c("user_id", "TEXT", true),
            c("room_id", "TEXT", true),
            c("role", "TEXT", true),
            c("joined_at", "TEXT", true),
            c("metadata", "TEXT", false),
        ],
        primary_key: &["user_id", "room_id"],
    },
    Table {
        name: KV_TABLE,
        columns: &[c("key", "BLOB", true), c("value", "BLOB", false)],
        primary_key: &["key"],
    },
];

/// The secondary indexes of the key layout (`sliqtly_store::key`).
pub const INDEXES: &[Index] = &[
    Index {
        name: "room_document",
        table: "documents",
        columns: &["room_id", "id"],
    },
    Index {
        name: "document_room",
        table: "documents",
        columns: &["id", "room_id"],
    },
    Index {
        name: "doc_updated",
        table: "documents",
        columns: &["room_id", "updated_at"],
    },
    Index {
        name: "user_room",
        table: "memberships",
        columns: &["user_id", "room_id"],
    },
];

pub fn table(name: &str) -> Option<&'static Table> {
    let name = name.to_ascii_lowercase();
    TABLES.iter().find(|t| t.name == name)
}

impl Table {
    pub fn column_names(&self) -> Vec<&'static str> {
        self.columns.iter().map(|c| c.name).collect()
    }

    pub fn create_sql(&self) -> String {
        let mut parts: Vec<String> = self
            .columns
            .iter()
            .map(|c| {
                let mut s = format!("  {} {}", c.name, c.ty);
                if self.primary_key == [c.name] {
                    s += " PRIMARY KEY";
                } else if c.not_null {
                    s += " NOT NULL";
                }
                s
            })
            .collect();
        if self.primary_key.len() > 1 {
            parts.push(format!("  PRIMARY KEY ({})", self.primary_key.join(", ")));
        }
        format!("CREATE TABLE {} (\n{}\n)", self.name, parts.join(",\n"))
    }
}

impl Index {
    pub fn create_sql(&self) -> String {
        format!(
            "CREATE INDEX {} ON {}({})",
            self.name,
            self.table,
            self.columns.join(", ")
        )
    }
}
