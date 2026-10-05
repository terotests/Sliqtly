//! End-to-end tests: databases written through `sliqtly_store`, inspected
//! through the `sliqtly-db` binary.

use chrono::Utc;
use serde_json::Value;
use sha2::{Digest, Sha256};
use sliqtly_store::engine::KvEngine;
use sliqtly_store::key::{KeyBuilder, KeyType};
use sliqtly_store::record::{Document, Membership, Role, Room};
use sliqtly_store::{Database, FjallEngine};
use std::path::Path;
use std::process::{Command, Output};
use tempfile::TempDir;
use uuid::Uuid;

fn cli(db: &Path, args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_sliqtly-db"))
        .arg("--db")
        .arg(db)
        .args(args)
        .output()
        .expect("run sliqtly-db")
}

fn stdout(o: &Output) -> String {
    String::from_utf8_lossy(&o.stdout).into_owned()
}

fn stderr(o: &Output) -> String {
    String::from_utf8_lossy(&o.stderr).into_owned()
}

fn json(o: &Output) -> Value {
    serde_json::from_slice(&o.stdout).unwrap_or_else(|e| panic!("{e}: {}{}", stdout(o), stderr(o)))
}

struct Seeded {
    dir: TempDir,
    rooms: Vec<Uuid>,
    docs: Vec<Uuid>,
}

fn room(title: &str) -> Room {
    let t = Utc::now();
    Room {
        id: Uuid::now_v7(),
        title: title.into(),
        description: None,
        created_by: Uuid::now_v7(),
        created_at: t,
        updated_at: t,
        metadata: serde_json::json!({}),
    }
}

fn doc(room_id: Uuid, title: &str) -> Document {
    Document {
        id: Uuid::now_v7(),
        room_id,
        title: title.into(),
        created_by: Uuid::now_v7(),
        created_at: Utc::now(),
        updated_at: Utc::now(),
        version: 0,
        metadata: serde_json::json!({}),
    }
}

/// 3 rooms, 5 documents, 2 memberships, 1 blob; one commit per write.
fn seed() -> Seeded {
    let dir = TempDir::new().unwrap();
    let db = Database::open(dir.path()).unwrap();
    let mut rooms = Vec::new();
    let mut docs = Vec::new();
    for title in ["Alpha", "Beta", "Gamma"] {
        let r = room(title);
        let mut tx = db.write().unwrap();
        tx.put_room(r.clone()).unwrap();
        tx.commit(&db).unwrap();
        rooms.push(r.id);
    }
    for (i, title) in ["one", "two", "three", "four", "five"].iter().enumerate() {
        let d = doc(rooms[i % 2], title);
        let mut tx = db.write().unwrap();
        tx.put_document(d.clone()).unwrap();
        tx.commit(&db).unwrap();
        docs.push(d.id);
    }
    for (user, r) in [(Uuid::now_v7(), rooms[0]), (Uuid::now_v7(), rooms[1])] {
        let mut tx = db.write().unwrap();
        tx.add_membership(Membership {
            user_id: user,
            room_id: r,
            role: Role::Editor,
            joined_at: Utc::now(),
            metadata: serde_json::json!({}),
        })
        .unwrap();
        tx.commit(&db).unwrap();
    }
    let data = b"\x89PNG fake image bytes".to_vec();
    let id = format!("{:x}", Sha256::digest(&data));
    put_raw(
        &db,
        KeyBuilder::new(KeyType::Blob).push_str(&id).build(),
        data.clone(),
    );
    let meta = serde_json::json!({"id": id, "size": data.len(), "checksum": adler32(&data)});
    put_raw(
        &db,
        KeyBuilder::new(KeyType::Blob)
            .push_str(&id)
            .push_str("_meta")
            .build(),
        serde_json::to_vec(&meta).unwrap(),
    );
    Seeded { dir, rooms, docs }
}

fn put_raw(db: &Database, k: Vec<u8>, v: Vec<u8>) {
    let mut tx = db.write().unwrap();
    tx.put_raw(k, v).unwrap();
    tx.commit(db).unwrap();
}

fn raw(dir: &Path, k: Vec<u8>, v: Vec<u8>) {
    put_raw(&Database::open(dir).unwrap(), k, v);
}

fn adler32(data: &[u8]) -> String {
    let (mut a, mut b) = (1u32, 0u32);
    for &x in data {
        a = (a + x as u32) % 65521;
        b = (b + a) % 65521;
    }
    format!("{:08x}", (b << 16) | a)
}

/// Seeded commits: 3 rooms + 5 docs + 2 memberships + 2 blob keys.
const SEEDED_SEQ: u64 = 12;

fn check<'a>(report: &'a Value, name: &str) -> &'a Value {
    report["checks"]
        .as_array()
        .unwrap()
        .iter()
        .find(|c| c["name"] == name)
        .unwrap_or_else(|| panic!("no check {name}"))
}

#[test]
fn info_reports_seq_counts_and_storage() {
    let s = seed();
    let o = cli(s.dir.path(), &["info", "--json"]);
    assert!(o.status.success(), "{}", stderr(&o));
    let v = json(&o);
    assert_eq!(v["commit_seq"], SEEDED_SEQ);
    assert_eq!(v["engine"], "state-bin");
    assert_eq!(v["records"]["rooms"], 3);
    assert_eq!(v["records"]["documents"], 5);
    assert_eq!(v["records"]["memberships"], 2);
    assert_eq!(v["records"]["blobs"], 2);
    assert!(v["storage"]["physical_bytes"].as_u64().unwrap() > 0);
    assert!(v["last_backup"].is_null());

    let text = stdout(&cli(s.dir.path(), &["info"]));
    assert!(text.contains("CommitSeq:       12"), "{text}");
    assert!(text.contains("documents"), "{text}");
}

#[test]
fn stats_lists_every_family() {
    let s = seed();
    let v = json(&cli(s.dir.path(), &["stats", "--json"]));
    let fams = v["families"].as_array().unwrap();
    let get = |n: &str| fams.iter().find(|f| f["name"] == n).unwrap().clone();
    assert_eq!(get("documents")["count"], 5);
    assert_eq!(get("room_document")["count"], 0);
    assert_eq!(v["unknown"]["count"], 0);
}

#[test]
fn healthy_database_passes_deep_verify_with_index_warnings() {
    let s = seed();
    let o = cli(s.dir.path(), &["verify", "--deep", "--json"]);
    assert!(o.status.success(), "{}", stdout(&o));
    let r = json(&o);
    assert_eq!(check(&r, "primary record decoding")["status"], "ok");
    assert_eq!(check(&r, "reference integrity")["status"], "ok");
    assert_eq!(check(&r, "blob references")["status"], "ok");
    // The current writer does not maintain indexes or a change feed: reported, not hidden.
    assert_eq!(check(&r, "index room_document")["status"], "warn");
    assert_eq!(check(&r, "change-feed continuity")["status"], "warn");
    // --strict turns those warnings into a failing exit status.
    assert_eq!(
        cli(s.dir.path(), &["verify", "--deep", "--strict"])
            .status
            .code(),
        Some(1)
    );
}

#[test]
fn verify_fails_on_undecodable_record() {
    let s = seed();
    let k = KeyBuilder::new(KeyType::Document)
        .push_uuid(Uuid::now_v7())
        .build();
    raw(s.dir.path(), k, b"{not json".to_vec());
    let o = cli(s.dir.path(), &["verify"]);
    assert_eq!(o.status.code(), Some(1), "{}", stdout(&o));
    assert!(
        stdout(&o).contains("[FAIL] primary record decoding"),
        "{}",
        stdout(&o)
    );
}

#[test]
fn verify_fails_on_key_record_id_mismatch_and_unknown_family() {
    let s = seed();
    let d = doc(s.rooms[0], "misfiled");
    let k = KeyBuilder::new(KeyType::Document)
        .push_uuid(Uuid::now_v7())
        .build();
    raw(s.dir.path(), k, serde_json::to_vec(&d).unwrap());
    raw(s.dir.path(), vec![0x7f, 1, 2, 3], b"x".to_vec());
    let r = json(&cli(s.dir.path(), &["verify", "--json"]));
    assert_eq!(check(&r, "primary record decoding")["status"], "fail");
    assert_eq!(check(&r, "key families")["status"], "fail");
}

#[test]
fn deep_verify_catches_dangling_room_reference() {
    let s = seed();
    let mut d = doc(Uuid::now_v7(), "orphan");
    d.version = 1;
    let k = KeyBuilder::new(KeyType::Document).push_uuid(d.id).build();
    raw(s.dir.path(), k, serde_json::to_vec(&d).unwrap());
    // Quick verify does not look at references; deep does.
    assert!(cli(s.dir.path(), &["verify"]).status.success());
    let o = cli(s.dir.path(), &["verify", "--deep"]);
    assert_eq!(o.status.code(), Some(1));
    assert!(
        stdout(&o).contains("points to missing room"),
        "{}",
        stdout(&o)
    );
}

#[test]
fn partially_populated_index_must_be_complete_and_not_dangle() {
    let s = seed();
    let room_of_doc0 = s.rooms[0];
    let ok = KeyBuilder::new(KeyType::IdxRoomDoc)
        .push_uuid(room_of_doc0)
        .push_uuid(s.docs[0])
        .build();
    raw(s.dir.path(), ok, vec![]);
    let r = json(&cli(s.dir.path(), &["verify", "--deep", "--json"]));
    let c = check(&r, "index room_document");
    assert_eq!(c["status"], "fail");
    assert_eq!(
        c["problems"], 4,
        "the other four documents have no entry: {c}"
    );

    let dangling = KeyBuilder::new(KeyType::IdxRoomDoc)
        .push_uuid(room_of_doc0)
        .push_uuid(Uuid::now_v7())
        .build();
    raw(s.dir.path(), dangling, vec![]);
    let r = json(&cli(s.dir.path(), &["verify", "--deep", "--json"]));
    let ex = check(&r, "index room_document")["examples"].to_string();
    assert!(ex.contains("dangling entry"), "{ex}");
}

#[test]
fn edge_without_reverse_is_reported() {
    let s = seed();
    let k = KeyBuilder::new(KeyType::EdgeOut)
        .push_uuid(s.rooms[0])
        .push_uuid(s.rooms[1])
        .build();
    raw(s.dir.path(), k, vec![]);
    let r = json(&cli(s.dir.path(), &["verify", "--deep", "--json"]));
    assert_eq!(check(&r, "edge forward/reverse symmetry")["status"], "fail");
    let back = KeyBuilder::new(KeyType::EdgeIn)
        .push_uuid(s.rooms[1])
        .push_uuid(s.rooms[0])
        .build();
    raw(s.dir.path(), back, vec![]);
    let r = json(&cli(s.dir.path(), &["verify", "--deep", "--json"]));
    assert_eq!(check(&r, "edge forward/reverse symmetry")["status"], "ok");
}

#[test]
fn blob_content_mismatch_is_reported() {
    let s = seed();
    let fake_id = format!("{:x}", Sha256::digest(b"something else"));
    raw(
        s.dir.path(),
        KeyBuilder::new(KeyType::Blob).push_str(&fake_id).build(),
        b"tampered".to_vec(),
    );
    let r = json(&cli(s.dir.path(), &["verify", "--deep", "--json"]));
    let c = check(&r, "blob references");
    assert_eq!(c["status"], "fail");
    assert!(
        c["examples"].to_string().contains("content hashes to"),
        "{c}"
    );
}

#[test]
fn change_feed_gap_and_newer_format_fail() {
    let s = seed();
    for seq in [1u64, 2, 4] {
        raw(
            s.dir.path(),
            KeyBuilder::new(KeyType::Changes).push_be_u64(seq).build(),
            vec![],
        );
    }
    raw(
        s.dir.path(),
        KeyBuilder::new(KeyType::Meta)
            .push_str("format_version")
            .build(),
        b"99".to_vec(),
    );
    let r = json(&cli(s.dir.path(), &["verify", "--json"]));
    assert_eq!(check(&r, "change-feed continuity")["status"], "fail");
    assert!(check(&r, "change-feed continuity")["examples"]
        .to_string()
        .contains("gap"));
    assert_eq!(check(&r, "schema invariants")["status"], "fail");
}

#[test]
fn leftover_temp_file_is_a_warning() {
    let s = seed();
    std::fs::write(s.dir.path().join("state.bin.tmp"), b"partial").unwrap();
    let r = json(&cli(s.dir.path(), &["verify", "--json"]));
    assert_eq!(check(&r, "storage")["status"], "warn");
}

#[test]
fn missing_database_is_an_error_and_creates_nothing() {
    let dir = TempDir::new().unwrap();
    let missing = dir.path().join("nope");
    let o = cli(&missing, &["info"]);
    assert_eq!(o.status.code(), Some(2));
    assert!(!missing.exists());
    let o = cli(dir.path(), &["info"]);
    assert_eq!(o.status.code(), Some(2));
    assert!(
        stderr(&o).contains("no recognised database"),
        "{}",
        stderr(&o)
    );
}

// ------------------------------------------------------------------ query

fn query(dir: &Path, sql: &str) -> Value {
    let o = cli(dir, &["query", "--json", sql]);
    assert!(o.status.success(), "{sql}: {}", stderr(&o));
    json(&o)
}

#[test]
fn query_filters_orders_and_limits() {
    let s = seed();
    let v = query(s.dir.path(), "SELECT title FROM rooms ORDER BY title DESC");
    let titles: Vec<&str> = v
        .as_array()
        .unwrap()
        .iter()
        .map(|r| r["title"].as_str().unwrap())
        .collect();
    assert_eq!(titles, ["Gamma", "Beta", "Alpha"]);

    let v = query(
        s.dir.path(),
        &format!(
            "SELECT id, title FROM documents WHERE room_id = '{}' ORDER BY title LIMIT 2",
            s.rooms[0]
        ),
    );
    let titles: Vec<&str> = v
        .as_array()
        .unwrap()
        .iter()
        .map(|r| r["title"].as_str().unwrap())
        .collect();
    assert_eq!(titles, ["five", "one"]);

    let v = query(
        s.dir.path(),
        "select count(*) from documents where title like 't%' or version > 5",
    );
    assert_eq!(v[0]["count"], 2.0);

    let v = query(
        s.dir.path(),
        "SELECT id FROM rooms WHERE updated > now() - interval '7 days'",
    );
    assert_eq!(v.as_array().unwrap().len(), 3);
    let v = query(
        s.dir.path(),
        "SELECT id FROM rooms WHERE updated < now() - interval '1 day'",
    );
    assert_eq!(v.as_array().unwrap().len(), 0);

    let v = query(
        s.dir.path(),
        "SELECT * FROM memberships WHERE role = 'editor' AND NOT metadata IS NULL",
    );
    assert_eq!(v.as_array().unwrap().len(), 2);
    assert!(v[0].get("joined_at").is_some());
}

#[test]
fn query_rejects_writes_and_unknown_names() {
    let s = seed();
    for (sql, msg) in [
        ("DELETE FROM rooms", "expected select"),
        ("SELECT * FROM rooms; DELETE FROM rooms", "read-only"),
        ("SELECT nope FROM rooms", "unknown column nope"),
        ("SELECT * FROM users", "unknown table users"),
        ("SELECT * FROM rooms WHERE title = 'x", "unterminated"),
    ] {
        let o = cli(s.dir.path(), &["query", sql]);
        assert_eq!(o.status.code(), Some(2), "{sql}");
        assert!(stderr(&o).contains(msg), "{sql}: {}", stderr(&o));
    }
}

#[test]
fn query_shell_reads_statements_from_stdin() {
    use std::io::Write;
    let s = seed();
    let mut child = Command::new(env!("CARGO_BIN_EXE_sliqtly-db"))
        .arg("--db")
        .arg(s.dir.path())
        .arg("query")
        .stdin(std::process::Stdio::piped())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(b"SELECT count(*)\nFROM rooms;\nSELECT bad FROM rooms;\nSELECT title FROM rooms WHERE title = 'Beta';\n")
        .unwrap();
    let o = child.wait_with_output().unwrap();
    let out = stdout(&o);
    assert!(out.contains("3\n"), "{out}");
    assert!(out.contains("Beta"), "{out}");
    assert!(stderr(&o).contains("unknown column bad"), "{}", stderr(&o));
}

// ------------------------------------------------------------------ backup / restore

#[test]
fn backup_restore_round_trip_keeps_seq_and_data() {
    let s = seed();
    let out = TempDir::new().unwrap();
    let bdir = out.path().join("b1");
    let o = cli(s.dir.path(), &["backup", bdir.to_str().unwrap(), "--json"]);
    assert!(o.status.success(), "{}", stderr(&o));
    let m = json(&o);
    assert_eq!(m["commit_seq"], SEEDED_SEQ);
    assert_eq!(m["blobs"].as_array().unwrap().len(), 1);

    // info now knows about the backup.
    let info = json(&cli(s.dir.path(), &["info", "--json"]));
    assert_eq!(info["last_backup"]["commit_seq"], SEEDED_SEQ);

    let target = out.path().join("restored");
    let o = cli(&target, &["restore", bdir.to_str().unwrap()]);
    assert!(o.status.success(), "{}{}", stdout(&o), stderr(&o));
    assert!(stdout(&o).contains("verify passed"), "{}", stdout(&o));

    // The real engine opens the restored database at the same CommitSeq and contents.
    let a = FjallEngine::new(s.dir.path()).unwrap().snapshot().unwrap();
    let b = FjallEngine::new(&target).unwrap().snapshot().unwrap();
    assert_eq!(b.seq(), SEEDED_SEQ);
    let all = sliqtly_store::engine::KeyRange::between(vec![], vec![0xff]);
    assert_eq!(a.scan(all.clone()).unwrap(), b.scan(all).unwrap());

    // And it keeps working: the next commit continues the sequence.
    let db = Database::open(&target).unwrap();
    let mut tx = db.write().unwrap();
    tx.put_room(room("after restore")).unwrap();
    assert_eq!(tx.commit(&db).unwrap(), SEEDED_SEQ + 1);
}

#[test]
fn backup_refuses_non_empty_destination() {
    let s = seed();
    let out = TempDir::new().unwrap();
    std::fs::write(out.path().join("x"), b"x").unwrap();
    let o = cli(s.dir.path(), &["backup", out.path().to_str().unwrap()]);
    assert_eq!(o.status.code(), Some(2));
    assert!(stderr(&o).contains("not empty"));
}

#[test]
fn restore_rejects_tampered_backup_and_writes_nothing() {
    let s = seed();
    let out = TempDir::new().unwrap();
    let bdir = out.path().join("b");
    assert!(cli(s.dir.path(), &["backup", bdir.to_str().unwrap()])
        .status
        .success());
    let rec = bdir.join("records.sqkv");
    let mut bytes = std::fs::read(&rec).unwrap();
    let mid = bytes.len() / 2;
    bytes[mid] ^= 0xff;
    std::fs::write(&rec, &bytes).unwrap();

    let target = out.path().join("t");
    let o = cli(&target, &["restore", bdir.to_str().unwrap()]);
    assert_eq!(o.status.code(), Some(2));
    assert!(stderr(&o).contains("SHA-256 mismatch"), "{}", stderr(&o));
    assert!(!target.join("state.bin").exists());
}

#[test]
fn restore_needs_force_to_replace_and_dry_run_writes_nothing() {
    let s = seed();
    let out = TempDir::new().unwrap();
    let bdir = out.path().join("b");
    assert!(cli(s.dir.path(), &["backup", bdir.to_str().unwrap()])
        .status
        .success());

    let other = seed();
    let before = std::fs::read(other.dir.path().join("state.bin")).unwrap();
    let o = cli(other.dir.path(), &["restore", bdir.to_str().unwrap()]);
    assert_eq!(o.status.code(), Some(2));
    assert!(stderr(&o).contains("--force"));
    let o = cli(
        other.dir.path(),
        &["restore", "--dry-run", "--force", bdir.to_str().unwrap()],
    );
    assert!(o.status.success());
    assert_eq!(
        std::fs::read(other.dir.path().join("state.bin")).unwrap(),
        before
    );

    let o = cli(
        other.dir.path(),
        &["restore", "--force", bdir.to_str().unwrap()],
    );
    assert!(o.status.success(), "{}", stderr(&o));
    let v = query(other.dir.path(), "SELECT id FROM documents");
    let ids: Vec<String> = v
        .as_array()
        .unwrap()
        .iter()
        .map(|r| r["id"].as_str().unwrap().to_string())
        .collect();
    let mut want: Vec<String> = s.docs.iter().map(|d| d.to_string()).collect();
    want.sort();
    assert_eq!(ids, want);
}
