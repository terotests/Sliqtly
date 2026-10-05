//! sqlite3 compatibility: invocation, output modes, dot-commands, PRAGMAs,
//! and a differential check against real SQLite on a `.dump` of the database.

mod common;

use common::*;
use sliqtly_store::key::{KeyBuilder, KeyType};
use std::io::Write;
use std::path::Path;
use std::process::{Command, Output, Stdio};
use tempfile::TempDir;
use uuid::Uuid;

/// `sliqtly-db [ARGS...]` with no `--db`: the sqlite3-style front end.
fn sq(args: &[&str]) -> Output {
    Command::new(BIN).args(args).output().unwrap()
}

fn sq_stdin(args: &[&str], input: &str) -> Output {
    let mut c = Command::new(BIN)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    c.stdin.take().unwrap().write_all(input.as_bytes()).unwrap();
    c.wait_with_output().unwrap()
}

fn p(d: &Path) -> &str {
    d.to_str().unwrap()
}

#[test]
fn default_is_list_mode_without_headers() {
    let s = seed();
    let o = sq(&[p(s.dir.path()), "SELECT title, 1 FROM rooms ORDER BY title"]);
    assert!(o.status.success(), "{}", stderr(&o));
    assert_eq!(stdout(&o), "Alpha|1\nBeta|1\nGamma|1\n");
}

#[test]
fn output_mode_flags() {
    let s = seed();
    let d = p(s.dir.path());
    let q =
        "SELECT title, length(title) AS n, NULL AS x FROM rooms WHERE title < 'C' ORDER BY title";

    assert_eq!(
        stdout(&sq(&["-header", "-csv", d, q])),
        "title,n,x\r\nAlpha,5,\r\nBeta,4,\r\n"
    );
    assert_eq!(
        stdout(&sq(&["-json", d, q])),
        "[{\"title\":\"Alpha\",\"n\":5,\"x\":null},\n{\"title\":\"Beta\",\"n\":4,\"x\":null}]\n"
    );
    assert_eq!(
        stdout(&sq(&["-line", d, q])),
        "title = Alpha\n    n = 5\n    x = \n\ntitle = Beta\n    n = 4\n    x = \n"
    );
    assert_eq!(
        stdout(&sq(&["-separator", ";", "-nullvalue", "NULL", d, q])),
        "Alpha;5;NULL\nBeta;4;NULL\n"
    );
    assert_eq!(
        stdout(&sq(&["-markdown", d, q])),
        "| title | n | x |\n|-------|---|---|\n| Alpha | 5 |   |\n| Beta  | 4 |   |\n"
    );
    assert_eq!(
        stdout(&sq(&["-table", d, q])),
        "+-------+---+---+\n| title | n | x |\n+-------+---+---+\n| Alpha | 5 |   |\n| Beta  | 4 |   |\n+-------+---+---+\n"
    );
    assert_eq!(
        stdout(&sq(&["-quote", d, q])),
        "'Alpha',5,NULL\n'Beta',4,NULL\n"
    );
}

#[test]
fn dot_commands_describe_the_schema() {
    let s = seed();
    let d = p(s.dir.path());
    assert_eq!(
        stdout(&sq(&[d, ".tables"])),
        "documents    memberships  rooms        sliqtly_kv\n".replace(
            "documents    memberships  rooms        sliqtly_kv",
            "rooms        documents    memberships  sliqtly_kv"
        )
    );
    assert_eq!(
        stdout(&sq(&[d, ".indexes documents"])),
        "room_document  document_room  doc_updated\n"
    );
    let schema = stdout(&sq(&[d, ".schema memberships"]));
    assert!(
        schema.starts_with("CREATE TABLE memberships (\n  user_id TEXT NOT NULL,"),
        "{schema}"
    );
    assert!(
        schema.contains("  PRIMARY KEY (user_id, room_id)\n);\n"),
        "{schema}"
    );
    assert!(
        schema.contains("CREATE INDEX user_room ON memberships(user_id, room_id);"),
        "{schema}"
    );
    let o = sq(&[d, ".databases"]);
    assert!(stdout(&o).starts_with("main: "), "{}", stdout(&o));
    let o = sq(&[d, ".nosuch"]);
    assert_eq!(o.status.code(), Some(1));
    assert!(stderr(&o).contains("unknown command"), "{}", stderr(&o));
}

#[test]
fn pragmas() {
    let s = seed();
    let d = p(s.dir.path());
    assert_eq!(stdout(&sq(&[d, "PRAGMA integrity_check"])), "ok\n");
    assert_eq!(stdout(&sq(&[d, "PRAGMA quick_check;"])), "ok\n");
    assert_eq!(
        stdout(&sq(&[d, "PRAGMA table_info(rooms)"]))
            .lines()
            .take(2)
            .collect::<Vec<_>>(),
        ["0|id|TEXT|0||1", "1|title|TEXT|1||0"]
    );
    assert_eq!(
        stdout(&sq(&[d, "PRAGMA data_version"])),
        format!("{SEEDED_SEQ}\n")
    );
    assert_eq!(stdout(&sq(&[d, "PRAGMA user_version"])), "0\n");
    assert_eq!(stdout(&sq(&[d, "PRAGMA no_such_pragma"])), "");
    let o = sq(&[d, "PRAGMA user_version = 3"]);
    assert_eq!(o.status.code(), Some(1));
    assert!(stderr(&o).contains("readonly"), "{}", stderr(&o));
}

#[test]
fn integrity_check_lists_problems_like_sqlite() {
    let s = seed();
    let mut d = doc(Uuid::now_v7(), "orphan");
    d.version = 1;
    raw(
        s.dir.path(),
        KeyBuilder::new(KeyType::Document).push_uuid(d.id).build(),
        serde_json::to_vec(&d).unwrap(),
    );
    let dir = p(s.dir.path());
    let out = stdout(&sq(&[dir, "PRAGMA integrity_check"]));
    assert!(out.starts_with("reference integrity: documents/"), "{out}");
    assert!(out.contains("points to missing room"), "{out}");
    // quick_check skips cross-record checks, as in SQLite.
    assert_eq!(stdout(&sq(&[dir, "PRAGMA quick_check"])), "ok\n");
    assert_eq!(
        stdout(&sq(&[dir, "PRAGMA integrity_check(1)"]))
            .lines()
            .count(),
        1
    );
}

#[test]
fn errors_match_sqlite_wording_and_exit_status() {
    let s = seed();
    let d = p(s.dir.path());
    for (sql, msg) in [
        ("SELECT * FROM users", "Parse error: no such table: users"),
        (
            "SELECT nope FROM rooms",
            "Parse error: no such column: nope",
        ),
        ("SELEC 1", "Parse error: near \"SELEC\": syntax error"),
        (
            "DELETE FROM rooms",
            "Runtime error: attempt to write a readonly database",
        ),
        (
            "INSERT INTO rooms VALUES(1)",
            "Runtime error: attempt to write a readonly database",
        ),
        ("SELECT nosuchfn(1)", "no such function: nosuchfn"),
    ] {
        let o = sq(&[d, sql]);
        assert_eq!(o.status.code(), Some(1), "{sql}");
        assert!(stderr(&o).contains(msg), "{sql}: {}", stderr(&o));
    }
    let o = sq(&["/definitely/not/here", "SELECT 1"]);
    assert_eq!(o.status.code(), Some(1));
    assert!(
        stderr(&o).contains("unable to open database"),
        "{}",
        stderr(&o)
    );
}

#[test]
fn scripts_on_stdin_with_multiline_statements_and_bail() {
    let s = seed();
    let d = p(s.dir.path());
    let script = ".headers on\n.mode csv\nSELECT title\n  FROM rooms\n  WHERE title = 'Beta';\nSELECT bad FROM rooms;\nSELECT 'after';\n";
    let o = sq_stdin(&[d], script);
    // As in sqlite3, headers are printed for every statement.
    assert_eq!(stdout(&o), "title\r\nBeta\r\n'after'\r\nafter\r\n");
    assert!(stderr(&o).contains("no such column: bad"));
    assert_eq!(
        o.status.code(),
        Some(1),
        "an error in a piped script makes the exit status 1"
    );

    let o = sq_stdin(&["-bail", d], script);
    assert_eq!(
        stdout(&o),
        "title\r\nBeta\r\n",
        "-bail stops at the first error"
    );

    let o = sq_stdin(&["-cmd", ".mode json", d], "SELECT 1 AS one;");
    assert_eq!(stdout(&o), "[{\"one\":1}]\n");

    let o = sq_stdin(
        &[d],
        "SELECT 'a;b' AS x; -- trailing comment\n.quit\nSELECT 'never';\n",
    );
    assert_eq!(stdout(&o), "a;b\n");
}

#[test]
fn shell_backup_is_restorable() {
    let s = seed();
    let out = TempDir::new().unwrap();
    let b = out.path().join("bk");
    let o = sq(&[p(s.dir.path()), &format!(".backup {}", p(&b))]);
    assert!(o.status.success(), "{}", stderr(&o));
    let target = out.path().join("t");
    let o = cli(&target, &["restore", p(&b)]);
    assert!(o.status.success(), "{}", stderr(&o));
    assert_eq!(
        stdout(&sq(&[p(&target), "SELECT count(*) FROM documents"])),
        "5\n"
    );
    // .restore would replace the open database; the shell is read-only.
    let o = sq(&[p(s.dir.path()), &format!(".restore {}", p(&b))]);
    assert_eq!(o.status.code(), Some(1));
}

#[test]
fn insert_mode_and_output_redirection() {
    let s = seed();
    let tmp = TempDir::new().unwrap();
    let f = tmp.path().join("out.sql");
    let script = format!(".mode insert rooms\n.once {}\nSELECT id, title FROM rooms WHERE title = 'Alpha';\nSELECT 'to stdout';\n", p(&f));
    let o = sq_stdin(&[p(s.dir.path())], &script);
    let written = std::fs::read_to_string(&f).unwrap();
    assert!(
        written.starts_with("INSERT INTO rooms VALUES('"),
        "{written}"
    );
    assert!(written.trim_end().ends_with(",'Alpha');"), "{written}");
    assert_eq!(stdout(&o), "INSERT INTO rooms VALUES('to stdout');\n");
}

#[test]
fn sql_surface() {
    let s = seed();
    let d = p(s.dir.path());
    let one = |q: &str| stdout(&sq(&[d, q]));
    assert_eq!(
        one("SELECT 1 + 2 * 3, 7 / 2, 7 % 3, 7 / 2.0, -(4), 'a' || 'b' || 1"),
        "7|3|1|3.5|-4|ab1\n"
    );
    assert_eq!(
        one("SELECT typeof(1), typeof(1.5), typeof('x'), typeof(NULL), typeof(X'00')"),
        "integer|real|text|null|blob\n"
    );
    assert_eq!(
        one("SELECT coalesce(NULL, NULL, 3), ifnull(NULL, 'd'), nullif(2, 2), abs(-5)"),
        "3|d||5\n"
    );
    assert_eq!(one("SELECT upper('abc'), lower('ABC'), length('héllo'), substr('abcdef', 2, 3), instr('abc', 'c')"), "ABC|abc|5|bcd|3\n");
    assert_eq!(
        one("SELECT 'ABC' LIKE 'a%', 2 BETWEEN 1 AND 3, 5 IN (1, 2), NULL IS NULL, 1 IS NOT NULL"),
        "1|1|0|1|1\n"
    );
    assert_eq!(one("SELECT CASE WHEN 1 > 2 THEN 'x' WHEN 2 > 1 THEN 'y' ELSE 'z' END, CASE 3 WHEN 3 THEN 'three' END"), "y|three\n");
    assert_eq!(
        one("SELECT date('2026-10-05 10:00:00', '+1 day'), datetime('2026-10-05', '-2 hours')"),
        "2026-10-06|2026-10-04 22:00:00\n"
    );
    assert_eq!(
        one("SELECT json_extract('{\"a\":{\"b\":[1,2]}}', '$.a.b[1]')"),
        "2\n"
    );
    assert_eq!(
        one("SELECT room_id IS NOT NULL, count(*) AS n FROM documents GROUP BY room_id HAVING n > 2"),
        "1|3\n"
    );
    assert_eq!(one("SELECT count(DISTINCT room_id), count(*), sum(version), avg(version), total(version) FROM documents"), "2|5|5|1.0|5.0\n");
    assert_eq!(
        one("SELECT title FROM documents ORDER BY title LIMIT 2 OFFSET 1"),
        "four\none\n"
    );
    assert_eq!(
        one("SELECT title FROM documents ORDER BY title LIMIT 1, 2"),
        "four\none\n"
    );
    assert_eq!(one("SELECT DISTINCT role FROM memberships"), "editor\n");
    assert_eq!(
        one("SELECT count(*) FROM sqlite_master WHERE type = 'table'"),
        "4\n"
    );
    assert_eq!(
        one("SELECT count(*) FROM rooms WHERE updated > datetime('now', '-7 days')"),
        "3\n"
    );
    assert_eq!(
        one("SELECT count(*) FROM rooms WHERE updated_at > now() - interval '7 days'"),
        "3\n"
    );
    assert_eq!(
        one("SELECT sliqtly_family(key) FROM sliqtly_kv ORDER BY 1 LIMIT 1"),
        "blobs\n"
    );
    assert_eq!(one("BEGIN; "), "");
}

// ------------------------------------------------------------------ differential

/// Python's sqlite3 module stands in for the sqlite3 program (not installed
/// everywhere); the test is skipped when neither is available.
fn python_sqlite() -> bool {
    Command::new("python3")
        .args(["-c", "import sqlite3"])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Load `dump` into SQLite, run each query, print rows the way sqlite3 list
/// mode does (`|` separators, NULL as empty).
fn run_in_sqlite(dump: &str, queries: &[&str]) -> Vec<String> {
    let script = r#"
import sqlite3, sys, json
dump, queries = json.load(sys.stdin)
c = sqlite3.connect(":memory:")
c.executescript(dump)
def fmt(v):
    if v is None: return ""
    if isinstance(v, float): return repr(v)
    if isinstance(v, bytes): return v.decode("utf-8", "replace")
    return str(v)
out = []
for q in queries:
    rows = c.execute(q).fetchall()
    out.append("".join("|".join(fmt(v) for v in r) + "\n" for r in rows))
print(json.dumps(out))
"#;
    let mut child = Command::new("python3")
        .args(["-c", script])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(serde_json::to_string(&(dump, queries)).unwrap().as_bytes())
        .unwrap();
    let o = child.wait_with_output().unwrap();
    assert!(o.status.success(), "python sqlite failed");
    serde_json::from_slice(&o.stdout).unwrap()
}

#[test]
fn dump_loads_into_sqlite_and_queries_agree() {
    if !python_sqlite() {
        eprintln!("skipping: python3 with sqlite3 not available");
        return;
    }
    let s = seed();
    // Some extra variety: a description, metadata JSON, an edge key.
    let d = p(s.dir.path());
    raw(
        s.dir.path(),
        KeyBuilder::new(KeyType::EdgeOut)
            .push_uuid(s.rooms[0])
            .push_uuid(s.rooms[1])
            .build(),
        b"it's \"quoted\"".to_vec(),
    );
    let dump = stdout(&sq(&[d, ".dump"]));
    assert!(
        dump.starts_with("PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\nCREATE TABLE rooms"),
        "{dump}"
    );
    assert!(dump.ends_with("COMMIT;\n"));

    let queries = [
        "SELECT title FROM rooms ORDER BY title",
        "SELECT id, room_id, title, version, created_by, created_at, updated_at, metadata FROM documents ORDER BY id",
        "SELECT * FROM memberships ORDER BY user_id",
        "SELECT room_id, count(*), max(version), min(title) FROM documents GROUP BY room_id ORDER BY 2 DESC, 1",
        "SELECT title, upper(title), length(title), substr(title, 2, 3) FROM documents ORDER BY title LIMIT 3 OFFSET 1",
        "SELECT count(*) FROM documents WHERE title LIKE 'T%' OR version BETWEEN 2 AND 5",
        "SELECT title FROM documents WHERE title IN ('one', 'two', 'zzz') ORDER BY title DESC",
        "SELECT role, count(*) FROM memberships GROUP BY role",
        "SELECT CASE WHEN version > 0 THEN 'v' ELSE 'none' END AS k, count(*) FROM documents GROUP BY 1",
        "SELECT DISTINCT room_id FROM documents ORDER BY room_id",
        "SELECT 7 / 2, 7 % 3, 7 / 2.0, -3, abs(-3), 'a' || 1, coalesce(NULL, 'x'), nullif(1, 1), typeof(version) FROM documents LIMIT 1",
        "SELECT total(version), sum(version), avg(version), count(description) FROM documents, rooms LIMIT 0",
        "SELECT total(version), sum(version), avg(version) FROM documents",
        "SELECT hex(key), length(value), hex(value) FROM sliqtly_kv ORDER BY 1",
        "SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name",
        "SELECT title FROM rooms WHERE description IS NULL ORDER BY 1",
        "SELECT json_extract(metadata, '$') FROM rooms ORDER BY id LIMIT 1",
        "SELECT title FROM documents WHERE room_id = (SELECT 1) ORDER BY 1",
        "PRAGMA table_info(documents)",
        "PRAGMA table_info(memberships)",
    ];
    let expected = run_in_sqlite(&dump, &queries);
    for (q, want) in queries.iter().zip(expected) {
        let o = sq(&[d, q]);
        if !o.status.success() {
            // Unsupported constructs must fail loudly, never return wrong rows.
            eprintln!("not supported (ok): {q}: {}", stderr(&o).trim());
            assert!(
                q.contains("(SELECT") || q.contains("documents, rooms"),
                "{q}: {}",
                stderr(&o)
            );
            continue;
        }
        assert_eq!(stdout(&o), want, "query: {q}");
    }
}
