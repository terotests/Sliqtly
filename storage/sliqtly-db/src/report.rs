//! `info` and `stats`.

use crate::backend::{Loaded, ADMIN_DIR};
use crate::catalog::{family, Kind, FAMILIES};
use crate::model::Model;
use crate::verify::{self, Status};
use serde::Serialize;
use std::collections::BTreeMap;
use std::fs;

#[derive(Debug, Default, Clone, Serialize)]
pub struct FamilyStats {
    pub name: &'static str,
    pub kind: String,
    pub count: u64,
    pub key_bytes: u64,
    pub value_bytes: u64,
    pub max_value: u64,
}

pub fn family_stats(db: &Loaded) -> (Vec<FamilyStats>, FamilyStats) {
    let mut by_tag: BTreeMap<u8, FamilyStats> = BTreeMap::new();
    let mut unknown = FamilyStats {
        name: "unknown",
        kind: "unknown".into(),
        ..Default::default()
    };
    for (k, v) in &db.entries {
        let s = match family(k) {
            Some(f) => by_tag.entry(f.tag).or_insert_with(|| FamilyStats {
                name: f.name,
                kind: format!("{:?}", f.kind).to_lowercase(),
                ..Default::default()
            }),
            None => &mut unknown,
        };
        s.count += 1;
        s.key_bytes += k.len() as u64;
        s.value_bytes += v.len() as u64;
        s.max_value = s.max_value.max(v.len() as u64);
    }
    // Keep every family listed, in layout order, so empty ones are visible.
    let all = FAMILIES
        .iter()
        .map(|f| {
            by_tag.remove(&f.tag).unwrap_or(FamilyStats {
                name: f.name,
                kind: format!("{:?}", f.kind).to_lowercase(),
                ..Default::default()
            })
        })
        .collect();
    (all, unknown)
}

pub fn stats_text(db: &Loaded) -> String {
    let (fams, unknown) = family_stats(db);
    let mut out = format!(
        "{:<16} {:<8} {:>12} {:>14} {:>14} {:>12}\n",
        "family", "kind", "keys", "key bytes", "value bytes", "max value"
    );
    for f in fams
        .iter()
        .chain(std::iter::once(&unknown))
        .filter(|f| f.count > 0 || f.name != "unknown")
    {
        out += &format!(
            "{:<16} {:<8} {:>12} {:>14} {:>14} {:>12}\n",
            f.name,
            f.kind,
            group(f.count),
            group(f.key_bytes),
            group(f.value_bytes),
            group(f.max_value)
        );
    }
    out += &format!(
        "\nlogical {}  physical {}  ({} keys, CommitSeq {})\n",
        bytes(db.logical_bytes()),
        bytes(db.physical_bytes()),
        group(db.entries.len() as u64),
        group(db.commit_seq)
    );
    out
}

pub fn stats_json(db: &Loaded) -> serde_json::Value {
    let (fams, unknown) = family_stats(db);
    serde_json::json!({
        "path": db.path.display().to_string(),
        "commit_seq": db.commit_seq,
        "families": fams,
        "unknown": unknown,
        "logical_bytes": db.logical_bytes(),
        "physical_bytes": db.physical_bytes(),
    })
}

#[derive(Debug, Serialize, serde::Deserialize, Clone)]
pub struct LastBackup {
    pub commit_seq: u64,
    pub created_at: String,
    pub location: String,
}

pub fn read_last_backup(db: &Loaded) -> Option<LastBackup> {
    let p = db.path.join(ADMIN_DIR).join("last_backup.json");
    serde_json::from_slice(&fs::read(p).ok()?).ok()
}

pub fn info_json(db: &Loaded) -> serde_json::Value {
    let model = Model::build(db);
    let (fams, unknown) = family_stats(db);
    let report = verify::run(db, true);
    let index_health: BTreeMap<String, String> = report
        .checks
        .iter()
        .filter_map(|c| {
            let name = c.name.strip_prefix("index ")?;
            Some((name.to_string(), health(c.status, &c.summary)))
        })
        .collect();
    let records: BTreeMap<&str, u64> = fams
        .iter()
        .filter(|f| f.kind == "primary" || f.kind == "edge")
        .map(|f| (f.name, f.count))
        .collect();
    serde_json::json!({
        "database": db.path.display().to_string(),
        "storage_format": db.storage_format,
        "format_version": model.meta.get("format_version"),
        "schema_version": model.meta.get("schema_version"),
        "engine": db.engine,
        "commit_seq": db.commit_seq,
        "records": records,
        "unknown_keys": unknown.count,
        "indexes": index_health,
        "health": format!("{:?}", report.worst()).to_lowercase(),
        "storage": {
            "logical_bytes": db.logical_bytes(),
            "physical_bytes": db.physical_bytes(),
            "reclaimable_bytes": db.leftovers.iter().map(|f| f.size).sum::<u64>(),
            "files": db.files,
            "leftovers": db.leftovers,
        },
        "last_backup": read_last_backup(db),
    })
}

fn health(s: Status, summary: &str) -> String {
    match s {
        Status::Ok => "healthy".into(),
        Status::Skip => "skipped".into(),
        Status::Warn => format!("warning: {summary}"),
        Status::Fail => format!("INCONSISTENT: {summary}"),
    }
}

pub fn info_text(db: &Loaded) -> String {
    let model = Model::build(db);
    let (fams, unknown) = family_stats(db);
    let report = verify::run(db, true);
    let unset = "unset".to_string();
    let mut out = String::new();
    out += &format!("Database:        {}\n", db.path.display());
    out += &format!("Storage format:  {}\n", db.storage_format);
    out += &format!(
        "Format version:  {}\n",
        model.meta.get("format_version").unwrap_or(&unset)
    );
    out += &format!(
        "Schema version:  {}\n",
        model.meta.get("schema_version").unwrap_or(&unset)
    );
    out += &format!("Engine:          {}\n", db.engine);
    out += &format!("CommitSeq:       {}\n", group(db.commit_seq));
    out += &format!(
        "Health:          {} (verify --deep for details)\n",
        format!("{:?}", report.worst()).to_lowercase()
    );

    out += "\nRecords:\n";
    for f in fams
        .iter()
        .filter(|f| matches!(family_kind(f.name), Some(Kind::Primary | Kind::Edge)))
    {
        if f.count > 0 || matches!(f.name, "rooms" | "documents" | "memberships" | "edges_out") {
            out += &format!("  {:<15} {:>12}\n", f.name, group(f.count));
        }
    }
    if unknown.count > 0 {
        out += &format!("  {:<15} {:>12}\n", "UNKNOWN", group(unknown.count));
    }

    out += "\nIndexes:\n";
    for c in &report.checks {
        if let Some(name) = c.name.strip_prefix("index ") {
            out += &format!("  {:<15} {}\n", name, health(c.status, &c.summary));
        }
    }

    let reclaimable: u64 = db.leftovers.iter().map(|f| f.size).sum();
    out += "\nStorage:\n";
    out += &format!("  logical data   {:>10}\n", bytes(db.logical_bytes()));
    out += &format!("  physical data  {:>10}\n", bytes(db.physical_bytes()));
    out += &format!("  reclaimable    {:>10}\n", bytes(reclaimable));

    out += "\nLast backup:\n";
    match read_last_backup(db) {
        Some(b) => {
            out += &format!(
                "  seq {}\n  {}\n  {}\n",
                group(b.commit_seq),
                b.created_at,
                b.location
            )
        }
        None => out += "  none recorded\n",
    }
    out
}

fn family_kind(name: &str) -> Option<Kind> {
    FAMILIES.iter().find(|f| f.name == name).map(|f| f.kind)
}

pub fn group(n: u64) -> String {
    let s = n.to_string();
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if i > 0 && (s.len() - i).is_multiple_of(3) {
            out.push(',');
        }
        out.push(c);
    }
    out
}

pub fn bytes(n: u64) -> String {
    const U: [&str; 5] = ["B", "KB", "MB", "GB", "TB"];
    let mut v = n as f64;
    let mut i = 0;
    while v >= 1024.0 && i < U.len() - 1 {
        v /= 1024.0;
        i += 1;
    }
    if i == 0 {
        format!("{n} B")
    } else {
        format!("{v:.1} {}", U[i])
    }
}
