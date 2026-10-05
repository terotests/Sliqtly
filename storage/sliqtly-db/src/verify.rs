//! `sliqtly-db verify`: integrity checks over one consistent snapshot.
//!
//! The quick pass covers storage, key families, record decoding, metadata and
//! the change feed. `--deep` adds reference integrity, secondary index
//! completeness, edge symmetry and blob content hashes.

use crate::backend::Loaded;
use crate::catalog::{describe_key, family, tag, KeyReader};
use crate::model::Model;
use serde::Serialize;
use sha2::{Digest, Sha256};
use sliqtly_store::key::KeyType;
use std::collections::{BTreeMap, BTreeSet};

/// Highest `meta/format_version` this tool understands.
pub const SUPPORTED_FORMAT_VERSION: u64 = 1;
const MAX_EXAMPLES: usize = 10;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Status {
    Ok,
    Skip,
    Warn,
    Fail,
}

#[derive(Debug, Serialize)]
pub struct Check {
    pub name: &'static str,
    pub status: Status,
    pub summary: String,
    pub problems: usize,
    pub examples: Vec<String>,
}

#[derive(Debug, Serialize)]
pub struct Report {
    pub path: String,
    pub commit_seq: u64,
    pub deep: bool,
    pub checks: Vec<Check>,
}

impl Report {
    pub fn worst(&self) -> Status {
        self.checks
            .iter()
            .map(|c| c.status)
            .max()
            .unwrap_or(Status::Ok)
    }
}

struct Builder {
    name: &'static str,
    problems: usize,
    examples: Vec<String>,
}

impl Builder {
    fn new(name: &'static str) -> Self {
        Builder {
            name,
            problems: 0,
            examples: Vec::new(),
        }
    }

    fn problem(&mut self, msg: impl Into<String>) {
        self.problems += 1;
        if self.examples.len() < MAX_EXAMPLES {
            self.examples.push(msg.into());
        }
    }

    /// Problems found make the check fail; otherwise `ok` with `summary`.
    fn finish(self, summary: impl Into<String>) -> Check {
        self.finish_as(Status::Fail, summary)
    }

    fn finish_as(self, on_problem: Status, summary: impl Into<String>) -> Check {
        let status = if self.problems > 0 {
            on_problem
        } else {
            Status::Ok
        };
        let summary = if self.problems > 0 {
            format!("{} problem(s)", self.problems)
        } else {
            summary.into()
        };
        Check {
            name: self.name,
            status,
            summary,
            problems: self.problems,
            examples: self.examples,
        }
    }
}

fn plain(name: &'static str, status: Status, summary: impl Into<String>) -> Check {
    Check {
        name,
        status,
        summary: summary.into(),
        problems: 0,
        examples: Vec::new(),
    }
}

pub fn run(db: &Loaded, deep: bool) -> Report {
    let model = Model::build(db);
    let mut checks = vec![
        storage(db),
        key_families(db),
        primary_decoding(&model),
        record_revisions(&model),
        format_metadata(&model, db),
        change_feed(db),
        checksums(db),
    ];
    if deep {
        checks.push(references(&model));
        checks.extend(indexes(db, &model));
        checks.push(edge_symmetry(db));
        checks.push(blobs(db));
    }
    Report {
        path: db.path.display().to_string(),
        commit_seq: db.commit_seq,
        deep,
        checks,
    }
}

fn checksums(db: &Loaded) -> Check {
    if db.engine == "log" {
        // recover() rejects a checkpoint with a bad CRC and stops replay at the
        // first WAL record whose CRC fails; anything after that is a torn tail.
        return plain(
            "checksums",
            Status::Ok,
            "checkpoint and every replayed WAL record passed CRC32".to_string(),
        );
    }
    plain(
        "checksums",
        Status::Skip,
        format!(
            "{} stores no per-record checksums; whole-file decode succeeded",
            db.storage_format
        ),
    )
}

fn storage(db: &Loaded) -> Check {
    let mut b = Builder::new("storage");
    for f in &db.leftovers {
        b.problem(format!(
            "{} ({} bytes) left behind: a commit was interrupted or is in flight",
            f.name, f.size
        ));
    }
    b.finish_as(
        Status::Warn,
        format!("{} decoded, {} keys", db.storage_format, db.entries.len()),
    )
}

fn key_families(db: &Loaded) -> Check {
    let mut b = Builder::new("key families");
    for k in db.entries.keys() {
        if family(k).is_none() {
            b.problem(format!("unknown family tag in key {}", describe_key(k)));
        }
    }
    b.finish("every key belongs to a known family")
}

fn primary_decoding(m: &Model) -> Check {
    let mut b = Builder::new("primary record decoding");
    for u in &m.undecodable {
        b.problem(format!("{}: {}", describe_key(&u.key), u.error));
    }
    for (k, what) in &m.id_mismatch {
        b.problem(format!(
            "{}: key and record disagree ({what})",
            describe_key(k)
        ));
    }
    b.finish(format!(
        "{} rooms, {} documents, {} memberships decoded",
        m.rooms.len(),
        m.documents.len(),
        m.memberships.len()
    ))
}

fn record_revisions(m: &Model) -> Check {
    let mut b = Builder::new("record revisions");
    for d in m.documents.values() {
        let doc = &d.value;
        if doc.version == 0 {
            b.problem(format!(
                "documents/{}: version 0 (never committed through put_document)",
                doc.id
            ));
        }
        if doc.updated_at < doc.created_at {
            b.problem(format!(
                "documents/{}: updated_at before created_at",
                doc.id
            ));
        }
    }
    for r in m.rooms.values() {
        if r.value.updated_at < r.value.created_at {
            b.problem(format!(
                "rooms/{}: updated_at before created_at",
                r.value.id
            ));
        }
    }
    b.finish_as(Status::Warn, "document versions and timestamps consistent")
}

fn format_metadata(m: &Model, db: &Loaded) -> Check {
    let mut b = Builder::new("schema invariants");
    let mut notes = Vec::new();
    match m.meta.get("format_version").map(|v| v.parse::<u64>()) {
        None => notes.push("meta/format_version unset".to_string()),
        Some(Ok(v)) if v > SUPPORTED_FORMAT_VERSION => b.problem(format!(
            "format_version {v} is newer than this tool supports ({SUPPORTED_FORMAT_VERSION})"
        )),
        Some(Ok(v)) => notes.push(format!("format_version {v}")),
        Some(Err(_)) => b.problem("meta/format_version is not an integer"),
    }
    if let Some(v) = m.meta.get("commit_seq") {
        match v.parse::<u64>() {
            Ok(s) if s != db.commit_seq => b.problem(format!(
                "meta/commit_seq {s} differs from engine CommitSeq {}",
                db.commit_seq
            )),
            Ok(_) => {}
            Err(_) => b.problem("meta/commit_seq is not an integer"),
        }
    }
    if b.problems == 0 && !m.meta.contains_key("format_version") {
        // Not corruption, but operators should know the DB predates versioning.
        return plain("schema invariants", Status::Warn, notes.join(", "));
    }
    b.finish(notes.join(", "))
}

/// `changes/<seq:be64>[/<ordinal>]` entries: none newer than CommitSeq, no gaps.
fn change_feed(db: &Loaded) -> Check {
    let mut b = Builder::new("change-feed continuity");
    let mut seqs = BTreeSet::new();
    for k in db
        .entries
        .keys()
        .filter(|k| k.first() == Some(&tag(KeyType::Changes)))
    {
        match KeyReader::new(k).be_u64() {
            Some(s) => {
                if s > db.commit_seq {
                    b.problem(format!(
                        "changes/{s} is newer than CommitSeq {}",
                        db.commit_seq
                    ));
                }
                seqs.insert(s);
            }
            None => b.problem(format!("malformed change key {}", describe_key(k))),
        }
    }
    if seqs.is_empty() && b.problems == 0 {
        let status = if db.commit_seq == 0 {
            Status::Ok
        } else {
            Status::Warn
        };
        return plain(
            "change-feed continuity",
            status,
            "no change-feed entries (the current writer does not record a change feed)",
        );
    }
    let mut prev: Option<u64> = None;
    for &s in &seqs {
        if let Some(p) = prev {
            if s != p + 1 {
                b.problem(format!("gap in change feed: {p} then {s}"));
            }
        }
        prev = Some(s);
    }
    b.finish(format!(
        "{} commits in feed ({}..={})",
        seqs.len(),
        seqs.first().unwrap(),
        seqs.last().unwrap()
    ))
}

fn references(m: &Model) -> Check {
    let mut b = Builder::new("reference integrity");
    for d in m.documents.values() {
        if !m.rooms.contains_key(&d.value.room_id) {
            b.problem(format!(
                "documents/{} points to missing room {}",
                d.value.id, d.value.room_id
            ));
        }
    }
    for (user, room) in m.memberships.keys() {
        if !m.rooms.contains_key(room) {
            b.problem(format!("memberships/{user}/{room}: room does not exist"));
        }
    }
    b.finish("every document and membership points to an existing room")
}

/// An index with no entries at all is reported as not maintained (warn);
/// an index with some entries must be complete and have no dangling entries.
fn indexes(db: &Loaded, m: &Model) -> Vec<Check> {
    let entries = |ty: KeyType| {
        db.entries
            .keys()
            .filter(move |k| k.first() == Some(&tag(ty)))
            .collect::<Vec<_>>()
    };

    let mut out = Vec::new();

    // room_document: room | doc
    let keys = entries(KeyType::IdxRoomDoc);
    let mut b = Builder::new("index room_document");
    let mut seen = BTreeSet::new();
    for k in &keys {
        let mut r = KeyReader::new(k);
        match (r.uuid(), r.uuid()) {
            (Some(room), Some(doc)) => {
                match m.documents.get(&doc) {
                    None => b.problem(format!("dangling entry room_document/{room}/{doc}")),
                    Some(d) if d.value.room_id != room => b.problem(format!(
                        "room_document/{room}/{doc}: document is in room {}",
                        d.value.room_id
                    )),
                    _ => {}
                }
                seen.insert(doc);
            }
            _ => b.problem(format!("malformed key {}", describe_key(k))),
        }
    }
    out.push(completeness(
        b,
        keys.len(),
        m.documents
            .keys()
            .filter(|id| !seen.contains(id))
            .map(|id| format!("documents/{id} has no room_document entry")),
    ));

    // user_room: user | room
    let keys = entries(KeyType::IdxUserRoom);
    let mut b = Builder::new("index user_room");
    let mut seen = BTreeSet::new();
    for k in &keys {
        let mut r = KeyReader::new(k);
        match (r.uuid(), r.uuid()) {
            (Some(user), Some(room)) => {
                if !m.memberships.contains_key(&(user, room)) {
                    b.problem(format!("dangling entry user_room/{user}/{room}"));
                }
                seen.insert((user, room));
            }
            _ => b.problem(format!("malformed key {}", describe_key(k))),
        }
    }
    out.push(completeness(
        b,
        keys.len(),
        m.memberships
            .keys()
            .filter(|p| !seen.contains(p))
            .map(|(u, r)| format!("memberships/{u}/{r} has no user_room entry")),
    ));

    // document_room: doc | room
    let keys = entries(KeyType::IdxDocRoom);
    let mut b = Builder::new("index document_room");
    let mut seen = BTreeSet::new();
    for k in &keys {
        let mut r = KeyReader::new(k);
        match (r.uuid(), r.uuid()) {
            (Some(doc), Some(room)) => {
                match m.documents.get(&doc) {
                    None => b.problem(format!("dangling entry document_room/{doc}/{room}")),
                    Some(d) if d.value.room_id != room => b.problem(format!(
                        "document_room/{doc}/{room}: document is in room {}",
                        d.value.room_id
                    )),
                    _ => {}
                }
                seen.insert(doc);
            }
            _ => b.problem(format!("malformed key {}", describe_key(k))),
        }
    }
    out.push(completeness(
        b,
        keys.len(),
        m.documents
            .keys()
            .filter(|id| !seen.contains(id))
            .map(|id| format!("documents/{id} has no document_room entry")),
    ));

    // doc_updated: room | updated_ms | doc  (only dangling entries are checked:
    // the timestamp encoding is not fixed by the writer yet)
    let keys = entries(KeyType::IdxDocUpdated);
    let mut b = Builder::new("index doc_updated");
    for k in &keys {
        let mut r = KeyReader::new(k);
        if let (Some(_room), Some(_ts), Some(doc)) = (r.uuid(), r.be_u64(), r.uuid()) {
            if !m.documents.contains_key(&doc) {
                b.problem(format!("dangling entry {}", describe_key(k)));
            }
        }
    }
    let n = keys.len();
    out.push(if n == 0 {
        plain(
            "index doc_updated",
            Status::Warn,
            "no entries: index not maintained by the writer",
        )
    } else {
        b.finish(format!("{n} entries, none dangling"))
    });

    out
}

fn completeness(mut b: Builder, entries: usize, missing: impl Iterator<Item = String>) -> Check {
    if entries == 0 && b.problems == 0 {
        let name = b.name;
        return plain(
            name,
            Status::Warn,
            "no entries: index not maintained by the writer",
        );
    }
    for m in missing {
        b.problem(m);
    }
    b.finish(format!("{entries} entries, complete and none dangling"))
}

/// Every `edges_out/<from>/<to>/<rest>` has `edges_in/<to>/<from>/<rest>` and vice versa.
fn edge_symmetry(db: &Loaded) -> Check {
    let mut b = Builder::new("edge forward/reverse symmetry");
    let collect = |ty: KeyType, b: &mut Builder| {
        let mut set = BTreeSet::new();
        for k in db.entries.keys().filter(|k| k.first() == Some(&tag(ty))) {
            let mut r = KeyReader::new(k);
            match (r.uuid(), r.uuid()) {
                (Some(a), Some(c)) => {
                    set.insert((a, c, r.remaining().to_vec()));
                }
                _ => b.problem(format!("malformed edge key {}", describe_key(k))),
            }
        }
        set
    };
    let outs = collect(KeyType::EdgeOut, &mut b);
    let ins = collect(KeyType::EdgeIn, &mut b);
    for (from, to, rest) in &outs {
        if !ins.contains(&(*to, *from, rest.clone())) {
            b.problem(format!("edges_out/{from}/{to} has no reverse entry"));
        }
    }
    for (to, from, rest) in &ins {
        if !outs.contains(&(*from, *to, rest.clone())) {
            b.problem(format!("edges_in/{to}/{from} has no forward entry"));
        }
    }
    b.finish(format!("{} edges, symmetric", outs.len()))
}

/// Blob data keys are `blobs/<sha256 hex>`; `blobs/<id>/"_meta"` holds JSON
/// with `size` and an Adler-32 `checksum`. `blob_paths/<path>` names a blob id.
fn blobs(db: &Loaded) -> Check {
    let mut b = Builder::new("blob references");
    let mut data: BTreeMap<String, &Vec<u8>> = BTreeMap::new();
    let mut metas: BTreeMap<String, &Vec<u8>> = BTreeMap::new();
    for (k, v) in db
        .entries
        .iter()
        .filter(|(k, _)| k.first() == Some(&tag(KeyType::Blob)))
    {
        let mut r = KeyReader::new(k);
        match (r.string(), r.string()) {
            (Some(id), None) if r.is_empty() => {
                data.insert(id, v);
            }
            (Some(id), Some(s)) if s == "_meta" && r.is_empty() => {
                metas.insert(id, v);
            }
            _ => b.problem(format!("unrecognised blob key {}", describe_key(k))),
        }
    }
    for (id, bytes) in &data {
        let digest = format!("{:x}", Sha256::digest(bytes));
        if &digest != id {
            b.problem(format!("blobs/{id}: content hashes to {digest}"));
        }
        match metas
            .get(id)
            .map(|m| serde_json::from_slice::<serde_json::Value>(m))
        {
            None => b.problem(format!("blobs/{id}: no _meta record")),
            Some(Err(e)) => b.problem(format!("blobs/{id}/_meta: {e}")),
            Some(Ok(meta)) => {
                if meta["size"].as_u64() != Some(bytes.len() as u64) {
                    b.problem(format!(
                        "blobs/{id}: size {} but _meta says {}",
                        bytes.len(),
                        meta["size"]
                    ));
                }
                if let Some(c) = meta["checksum"].as_str() {
                    if c != adler32(bytes) {
                        b.problem(format!("blobs/{id}: Adler-32 mismatch"));
                    }
                }
            }
        }
    }
    for id in metas.keys().filter(|id| !data.contains_key(*id)) {
        b.problem(format!("blobs/{id}/_meta without blob data"));
    }
    let mut paths = 0;
    for (k, v) in db
        .entries
        .iter()
        .filter(|(k, _)| k.first() == Some(&tag(KeyType::BlobIndex)))
    {
        paths += 1;
        let target = String::from_utf8_lossy(v).to_string();
        if !data.contains_key(&target) {
            b.problem(format!(
                "{} points to missing blob {target}",
                describe_key(k)
            ));
        }
    }
    b.finish(format!(
        "{} blobs hash-verified, {paths} paths resolve",
        data.len()
    ))
}

pub fn adler32(data: &[u8]) -> String {
    let (mut a, mut b) = (1u32, 0u32);
    for &x in data {
        a = (a + x as u32) % 65521;
        b = (b + a) % 65521;
    }
    format!("{:08x}", (b << 16) | a)
}
