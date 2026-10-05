//! The sqlite3-compatible shell: invocation, dot-commands, PRAGMAs, output modes.
//!
//! `sliqtly-db DBDIR "SELECT ..."`, `sliqtly-db -json DBDIR ".tables"` and an
//! interactive or piped session behave like the `sqlite3` program, with the
//! database opened read-only. `.dump` output loads into a real `sqlite3`.

use crate::backend::{self, Loaded};
use crate::backup;
use crate::model::Model;
use crate::query::{self, ResultSet, Row, Stmt, Val};
use crate::report;
use crate::schema;
use crate::verify::{self, Status};
use anyhow::{anyhow, bail, Context, Result};
use std::fs;
use std::io::{self, BufRead, IsTerminal, Write};
use std::path::{Path, PathBuf};
use std::time::Instant;

#[derive(Debug, Clone, PartialEq)]
pub enum Mode {
    List,
    Csv,
    Tabs,
    Json,
    Line,
    Column,
    Table,
    Box,
    Markdown,
    Quote,
    Insert(String),
}

impl Mode {
    fn parse(name: &str, arg: Option<&str>) -> Option<Mode> {
        Some(match name {
            "list" => Mode::List,
            "csv" => Mode::Csv,
            "tabs" => Mode::Tabs,
            "json" => Mode::Json,
            "line" => Mode::Line,
            "column" => Mode::Column,
            "table" => Mode::Table,
            "box" => Mode::Box,
            "markdown" => Mode::Markdown,
            "quote" => Mode::Quote,
            "insert" => Mode::Insert(arg.unwrap_or("\"table\"").to_string()),
            _ => return None,
        })
    }

    fn name(&self) -> &'static str {
        match self {
            Mode::List => "list",
            Mode::Csv => "csv",
            Mode::Tabs => "tabs",
            Mode::Json => "json",
            Mode::Line => "line",
            Mode::Column => "column",
            Mode::Table => "table",
            Mode::Box => "box",
            Mode::Markdown => "markdown",
            Mode::Quote => "quote",
            Mode::Insert(_) => "insert",
        }
    }
}

pub struct Settings {
    pub mode: Mode,
    pub headers: bool,
    pub separator: String,
    pub newline: String,
    pub nullvalue: String,
    pub echo: bool,
    pub bail: bool,
    pub timer: bool,
}

impl Default for Settings {
    fn default() -> Self {
        Settings {
            mode: Mode::List,
            headers: false,
            separator: "|".into(),
            newline: "\n".into(),
            nullvalue: String::new(),
            echo: false,
            bail: false,
            timer: false,
        }
    }
}

enum Out {
    Stdout,
    File(fs::File),
}

pub struct Shell {
    pub s: Settings,
    db: Option<(Loaded, Model)>,
    out: Out,
    /// `.once`: switch back to stdout after the next statement.
    once: bool,
    pub errors: usize,
    quit: Option<i32>,
}

/// What a line of input asked for.
pub enum Flow {
    Continue,
    Quit(i32),
}

impl query::Source for Model {
    fn rows(&self, table: &str) -> Vec<Row> {
        let t = schema::table(table).expect("known table");
        match table {
            "rooms" => self.rooms.values().map(|d| row_of(t, &d.value)).collect(),
            "documents" => self
                .documents
                .values()
                .map(|d| row_of(t, &d.value))
                .collect(),
            "memberships" => self
                .memberships
                .values()
                .map(|d| row_of(t, &d.value))
                .collect(),
            schema::KV_TABLE => self
                .other
                .iter()
                .map(|(k, v)| query::kv_row(k, v))
                .collect(),
            _ => Vec::new(),
        }
    }
}

fn row_of<T: serde::Serialize>(t: &schema::Table, v: &T) -> Row {
    query::row_from_json(t, &serde_json::to_value(v).unwrap_or_default())
}

impl Shell {
    pub fn new(s: Settings) -> Shell {
        Shell {
            s,
            db: None,
            out: Out::Stdout,
            once: false,
            errors: 0,
            quit: None,
        }
    }

    pub fn open(&mut self, path: &Path) -> Result<()> {
        let loaded = backend::open(path)?;
        let model = Model::build(&loaded);
        self.db = Some((loaded, model));
        Ok(())
    }

    fn db(&self) -> Result<&(Loaded, Model)> {
        self.db
            .as_ref()
            .ok_or_else(|| anyhow!("no database is open; use .open DIR"))
    }

    fn write(&mut self, s: &str) -> Result<()> {
        match &mut self.out {
            Out::Stdout => {
                let mut o = io::stdout().lock();
                o.write_all(s.as_bytes())?;
                o.flush()?;
            }
            Out::File(f) => f.write_all(s.as_bytes())?,
        }
        Ok(())
    }

    fn report_error(&mut self, kind: &str, e: &anyhow::Error) {
        self.errors += 1;
        eprintln!("{kind}: {e:#}");
    }

    /// Run one complete SQL statement or dot-command.
    pub fn run(&mut self, input: &str) -> Flow {
        if self.s.echo {
            println!("{}", input.trim_end());
        }
        let trimmed = input.trim_start();
        let res = if trimmed.starts_with('.') {
            self.dot(trimmed.trim_end()).map_err(|e| ("Error", e))
        } else {
            self.sql(input)
        };
        if let Err((kind, e)) = res {
            self.report_error(kind, &e);
            if self.s.bail {
                return Flow::Quit(1);
            }
        }
        // `.once FILE` applies to the next SQL statement, not to itself.
        if self.once && !trimmed.starts_with('.') {
            self.once = false;
            self.out = Out::Stdout;
        }
        match self.quit.take() {
            Some(c) => Flow::Quit(c),
            None => Flow::Continue,
        }
    }

    fn sql(&mut self, sql: &str) -> std::result::Result<(), (&'static str, anyhow::Error)> {
        // Same split as sqlite3: name resolution fails at prepare time (Parse
        // error), a write against the read-only database at run time.
        let classify = |e: anyhow::Error| {
            let m = e.to_string();
            if m.starts_with("attempt to write") {
                ("Runtime error", e)
            } else if m.starts_with("no such") {
                ("Parse error", e)
            } else {
                ("Runtime error", e)
            }
        };
        let stmt = query::parse(sql).map_err(|e| {
            if e.to_string().starts_with("attempt to write") {
                ("Runtime error", e)
            } else {
                ("Parse error", e)
            }
        })?;
        let started = Instant::now();
        let rs = match stmt {
            Stmt::Nothing => None,
            Stmt::Select(q) => {
                let (_, model) = self.db().map_err(|e| ("Error", e))?;
                Some(query::execute(model, &q, chrono::Utc::now()).map_err(classify)?)
            }
            Stmt::Pragma(name, arg) => self.pragma(&name, arg.as_deref()).map_err(classify)?,
        };
        if let Some(rs) = rs {
            let text = render(&rs, &self.s);
            self.write(&text).map_err(|e| ("Error", e))?;
        }
        if self.s.timer {
            let el = started.elapsed().as_secs_f64();
            let _ = self.write(&format!(
                "Run Time: real {el:.3} user {el:.6} sys 0.000000\n"
            ));
        }
        Ok(())
    }

    // ------------------------------------------------------------ PRAGMA

    fn pragma(&mut self, name: &str, arg: Option<&str>) -> Result<Option<ResultSet>> {
        let one = |col: &str, v: Val| ResultSet {
            columns: vec![col.into()],
            rows: vec![vec![v]],
        };
        let name = name.to_ascii_lowercase();
        // Settings that scripts (including .dump output) set: accept the harmless ones.
        if let Some(a) = arg {
            if !matches!(
                name.as_str(),
                "integrity_check"
                    | "quick_check"
                    | "table_info"
                    | "table_xinfo"
                    | "index_list"
                    | "index_info"
                    | "foreign_key_list"
            ) {
                if name == "foreign_keys"
                    && matches!(a.to_ascii_lowercase().as_str(), "off" | "0" | "false")
                {
                    return Ok(None);
                }
                bail!("attempt to write a readonly database");
            }
        }
        let (loaded, model) = self.db()?;
        let rs = match name.as_str() {
            "integrity_check" | "quick_check" => {
                let limit = arg.and_then(|a| a.parse::<usize>().ok()).unwrap_or(100);
                let r = verify::run(loaded, name == "integrity_check");
                let mut rows = Vec::new();
                for c in r.checks.iter().filter(|c| c.status == Status::Fail) {
                    if c.examples.is_empty() {
                        rows.push(format!("{}: {}", c.name, c.summary));
                    }
                    for e in &c.examples {
                        rows.push(format!("{}: {e}", c.name));
                    }
                    if c.problems > c.examples.len() {
                        rows.push(format!(
                            "{}: {} more",
                            c.name,
                            c.problems - c.examples.len()
                        ));
                    }
                }
                if rows.is_empty() {
                    rows.push("ok".into());
                }
                rows.truncate(limit.max(1));
                ResultSet {
                    columns: vec![name.clone()],
                    rows: rows.into_iter().map(|r| vec![Val::Text(r)]).collect(),
                }
            }
            "table_info" | "table_xinfo" => {
                let t = arg.and_then(schema::table);
                let rows = t
                    .map(|t| {
                        t.columns
                            .iter()
                            .enumerate()
                            .map(|(i, c)| {
                                let pk = t
                                    .primary_key
                                    .iter()
                                    .position(|p| *p == c.name)
                                    .map(|p| p + 1)
                                    .unwrap_or(0);
                                let mut r = vec![
                                    Val::Int(i as i64),
                                    Val::Text(c.name.into()),
                                    Val::Text(c.ty.into()),
                                    // Like SQLite: a single-column PRIMARY KEY does not imply NOT NULL.
                                    Val::Int((c.not_null && t.primary_key != [c.name]) as i64),
                                    Val::Null,
                                    Val::Int(pk as i64),
                                ];
                                if name == "table_xinfo" {
                                    r.push(Val::Int(0));
                                }
                                r
                            })
                            .collect()
                    })
                    .unwrap_or_default();
                let mut columns: Vec<String> =
                    ["cid", "name", "type", "notnull", "dflt_value", "pk"]
                        .map(String::from)
                        .to_vec();
                if name == "table_xinfo" {
                    columns.push("hidden".into());
                }
                ResultSet { columns, rows }
            }
            "table_list" => ResultSet {
                columns: ["schema", "name", "type", "ncol", "wr", "strict"]
                    .map(String::from)
                    .to_vec(),
                rows: schema::TABLES
                    .iter()
                    .map(|t| {
                        vec![
                            Val::Text("main".into()),
                            Val::Text(t.name.into()),
                            Val::Text("table".into()),
                            Val::Int(t.columns.len() as i64),
                            Val::Int(0),
                            Val::Int(0),
                        ]
                    })
                    .collect(),
            },
            "index_list" => ResultSet {
                columns: ["seq", "name", "unique", "origin", "partial"]
                    .map(String::from)
                    .to_vec(),
                rows: schema::INDEXES
                    .iter()
                    .filter(|i| Some(i.table) == arg.map(|a| a.to_ascii_lowercase()).as_deref())
                    .enumerate()
                    .map(|(n, i)| {
                        vec![
                            Val::Int(n as i64),
                            Val::Text(i.name.into()),
                            Val::Int(0),
                            Val::Text("c".into()),
                            Val::Int(0),
                        ]
                    })
                    .collect(),
            },
            "index_info" => {
                let idx = schema::INDEXES.iter().find(|i| Some(i.name) == arg);
                ResultSet {
                    columns: ["seqno", "cid", "name"].map(String::from).to_vec(),
                    rows: idx
                        .map(|i| {
                            let t = schema::table(i.table).unwrap();
                            i.columns
                                .iter()
                                .enumerate()
                                .map(|(n, c)| {
                                    let cid =
                                        t.columns.iter().position(|x| x.name == *c).unwrap_or(0);
                                    vec![
                                        Val::Int(n as i64),
                                        Val::Int(cid as i64),
                                        Val::Text((*c).into()),
                                    ]
                                })
                                .collect()
                        })
                        .unwrap_or_default(),
                }
            }
            "database_list" => ResultSet {
                columns: ["seq", "name", "file"].map(String::from).to_vec(),
                rows: vec![vec![
                    Val::Int(0),
                    Val::Text("main".into()),
                    Val::Text(loaded.path.display().to_string()),
                ]],
            },
            "user_version" => one(
                "user_version",
                Val::Int(
                    model
                        .meta
                        .get("schema_version")
                        .and_then(|v| v.parse().ok())
                        .unwrap_or(0),
                ),
            ),
            "schema_version" => one(
                "schema_version",
                Val::Int(
                    model
                        .meta
                        .get("schema_version")
                        .and_then(|v| v.parse().ok())
                        .unwrap_or(0),
                ),
            ),
            // Changes whenever the database does: the CommitSeq.
            "data_version" => one("data_version", Val::Int(loaded.commit_seq as i64)),
            "commit_seq" => one("commit_seq", Val::Int(loaded.commit_seq as i64)),
            "format_version" => one(
                "format_version",
                model
                    .meta
                    .get("format_version")
                    .map(|v| Val::Text(v.clone()))
                    .unwrap_or(Val::Null),
            ),
            "encoding" => one("encoding", Val::Text("UTF-8".into())),
            "foreign_keys" => one("foreign_keys", Val::Int(0)),
            "query_only" => one("query_only", Val::Int(1)),
            "journal_mode" => one("journal_mode", Val::Text(loaded.engine.into())),
            // SQLite ignores pragmas it does not know; so does this shell.
            _ => return Ok(None),
        };
        Ok(Some(rs))
    }

    // ------------------------------------------------------------ dot-commands

    fn dot(&mut self, line: &str) -> Result<()> {
        let args = split_args(&line[1..])?;
        let Some(cmd) = args.first().map(|s| s.as_str()) else {
            return Ok(());
        };
        let arg = |i: usize| args.get(i).map(|s| s.as_str());
        let on = |i: usize| -> Result<bool> {
            match arg(i).map(|s| s.to_ascii_lowercase()) {
                Some(v) if ["on", "1", "yes", "true"].contains(&v.as_str()) => Ok(true),
                Some(v) if ["off", "0", "no", "false"].contains(&v.as_str()) => Ok(false),
                _ => bail!("Usage: .{cmd} on|off"),
            }
        };
        match cmd {
            "quit" | "exit" => self.quit = Some(arg(1).and_then(|c| c.parse().ok()).unwrap_or(0)),
            "help" => self.write(HELP)?,
            "headers" | "header" => self.s.headers = on(1)?,
            "echo" => self.s.echo = on(1)?,
            "bail" => self.s.bail = on(1)?,
            "timer" => self.s.timer = on(1)?,
            "mode" => match arg(1) {
                None => {
                    let m = format!("current output mode: {}\n", self.s.mode.name());
                    self.write(&m)?
                }
                Some(m) => {
                    self.s.mode = Mode::parse(&m.to_ascii_lowercase(), arg(2)).ok_or_else(|| {
                        anyhow!("mode should be one of: box column csv insert json line list markdown quote table tabs")
                    })?;
                    match self.s.mode {
                        Mode::Csv => self.s.separator = ",".into(),
                        Mode::Tabs => self.s.separator = "\t".into(),
                        Mode::List if self.s.separator == "," || self.s.separator == "\t" => {
                            self.s.separator = "|".into()
                        }
                        Mode::Column => self.s.headers = true,
                        _ => {}
                    }
                }
            },
            "separator" => {
                self.s.separator =
                    unescape(arg(1).ok_or_else(|| anyhow!("Usage: .separator COL ?ROW?"))?);
                if let Some(r) = arg(2) {
                    self.s.newline = unescape(r);
                }
            }
            "nullvalue" => self.s.nullvalue = arg(1).unwrap_or("").to_string(),
            "width" => {} // column widths are computed from the data
            "print" => {
                let text = args[1..].join(" ") + "\n";
                self.write(&text)?
            }
            "output" | "once" => match arg(1) {
                None | Some("stdout") => self.out = Out::Stdout,
                Some(f) => {
                    self.out = Out::File(
                        fs::File::create(f).with_context(|| format!("cannot open \"{f}\""))?,
                    );
                    self.once = cmd == "once";
                }
            },
            "read" => {
                let f = arg(1).ok_or_else(|| anyhow!("Usage: .read FILE"))?;
                let text = fs::read_to_string(f).with_context(|| format!("cannot open \"{f}\""))?;
                if let Flow::Quit(c) = self.run_script(&text) {
                    self.quit = Some(c);
                }
            }
            "open" => {
                let p = args[1..]
                    .iter()
                    .find(|a| !a.starts_with('-'))
                    .ok_or_else(|| anyhow!("Usage: .open DIR"))?;
                self.open(Path::new(p))?;
            }
            "databases" => {
                let (l, _) = self.db()?;
                let s = format!("main: {} r/o\n", l.path.display());
                self.write(&s)?
            }
            "tables" => {
                let pat = arg(1);
                let names: Vec<&str> = schema::TABLES
                    .iter()
                    .map(|t| t.name)
                    .filter(|n| pat.is_none_or(|p| like(n, p)))
                    .collect();
                let s = columns_layout(&names);
                self.write(&s)?
            }
            "indexes" | "indices" => {
                let t = arg(1);
                let names: Vec<&str> = schema::INDEXES
                    .iter()
                    .filter(|i| t.is_none_or(|t| like(i.table, t)))
                    .map(|i| i.name)
                    .collect();
                let s = columns_layout(&names);
                self.write(&s)?
            }
            "schema" => {
                let pat = args[1..]
                    .iter()
                    .find(|a| !a.starts_with("--"))
                    .map(|s| s.as_str());
                let mut s = String::new();
                for r in query::schema_rows() {
                    let name = r["name"].text().unwrap_or_default();
                    let tbl = r["tbl_name"].text().unwrap_or_default();
                    if pat.is_none_or(|p| like(&name, p) || like(&tbl, p)) {
                        s += &format!("{};\n", r["sql"].text().unwrap_or_default());
                    }
                }
                self.write(&s)?
            }
            "dump" => {
                let tables: Vec<String> = args[1..]
                    .iter()
                    .filter(|a| !a.starts_with("--"))
                    .cloned()
                    .collect();
                let s = self.dump(&tables)?;
                self.write(&s)?
            }
            "dbinfo" => {
                let (l, _) = self.db()?;
                let s = report::info_text(l);
                self.write(&s)?
            }
            "show" => {
                let s = format!(
                    "        echo: {}\n     headers: {}\n        mode: {}\n   nullvalue: \"{}\"\n      output: {}\n colseparator: \"{}\"\n rowseparator: \"{}\"\n       timer: {}\n",
                    onoff(self.s.echo),
                    onoff(self.s.headers),
                    self.s.mode.name(),
                    self.s.nullvalue,
                    match self.out {
                        Out::Stdout => "stdout",
                        Out::File(_) => "file",
                    },
                    escape(&self.s.separator),
                    escape(&self.s.newline),
                    onoff(self.s.timer)
                );
                self.write(&s)?
            }
            "backup" | "save" => {
                // .backup ?DB? FILE
                let dest = args[1..]
                    .iter().rfind(|a| !a.starts_with("--"))
                    .ok_or_else(|| anyhow!("Usage: .backup ?DB? FILE"))?;
                let (l, _) = self.db()?;
                let out = backup::backup(l, Path::new(dest))?;
                if let Some(e) = out.bookkeeping_error {
                    eprintln!("warning: backup is good, but recording it for `info` failed: {e}");
                }
            }
            "restore" => {
                // .restore ?DB? FILE. The shell is read-only, like `sqlite3 -readonly`.
                bail!(
                    "attempt to write a readonly database (use `sliqtly-db --db DIR restore FILE`)"
                );
            }
            "import" | "load" | "clone" | "recover" => {
                bail!("attempt to write a readonly database")
            }
            _ => {
                bail!("unknown command or invalid arguments:  \"{cmd}\". Enter \".help\" for help")
            }
        }
        Ok(())
    }

    /// Same shape as `sqlite3 .dump`. Rooms, documents and memberships become
    /// typed rows; every other key goes to `sliqtly_kv`, so nothing is lost.
    fn dump(&self, only: &[String]) -> Result<String> {
        let (_, model) = self.db()?;
        let want = |name: &str| only.is_empty() || only.iter().any(|p| like(name, p));
        let mut s = String::from("PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\n");
        for t in schema::TABLES.iter().filter(|t| want(t.name)) {
            s += &format!("{};\n", t.create_sql());
            for row in query::Source::rows(model, t.name) {
                let vals: Vec<String> = t.columns.iter().map(|c| row[c.name].literal()).collect();
                s += &format!("INSERT INTO {} VALUES({});\n", t.name, vals.join(","));
            }
        }
        for i in schema::INDEXES.iter().filter(|i| want(i.table)) {
            s += &format!("{};\n", i.create_sql());
        }
        s += "COMMIT;\n";
        Ok(s)
    }

    /// Run a whole script (from `.read`, `-init` or piped stdin).
    pub fn run_script(&mut self, text: &str) -> Flow {
        let mut buf = String::new();
        for line in text.lines() {
            if let Flow::Quit(c) = self.feed(&mut buf, line) {
                return Flow::Quit(c);
            }
        }
        if !buf.trim().is_empty() {
            return self.run(&buf);
        }
        Flow::Continue
    }

    /// Add one input line; runs it when a statement or dot-command is complete.
    fn feed(&mut self, buf: &mut String, line: &str) -> Flow {
        if buf.trim().is_empty() && line.trim_start().starts_with('.') {
            buf.clear();
            return self.run(line);
        }
        buf.push_str(line);
        buf.push('\n');
        if statement_complete(buf) {
            let stmt = std::mem::take(buf);
            return self.run(&stmt);
        }
        Flow::Continue
    }

    pub fn interact(&mut self) -> Flow {
        let stdin = io::stdin();
        let tty = stdin.is_terminal();
        if tty {
            eprintln!(
                "sliqtly-db {} (sqlite3-compatible, read-only)\nEnter \".help\" for usage hints.",
                env!("CARGO_PKG_VERSION")
            );
            if self.db.is_none() {
                eprintln!("No database open. Use \".open DIR\".");
            }
        }
        let mut buf = String::new();
        let prompt = |cont: bool| {
            if tty {
                eprint!("{}", if cont { "   ...> " } else { "sliqtly> " });
                let _ = io::stderr().flush();
            }
        };
        prompt(false);
        for line in stdin.lock().lines() {
            let Ok(line) = line else { break };
            if let Flow::Quit(c) = self.feed(&mut buf, &line) {
                return Flow::Quit(c);
            }
            prompt(!buf.trim().is_empty());
        }
        if !buf.trim().is_empty() {
            return self.run(&buf);
        }
        Flow::Continue
    }
}

fn onoff(b: bool) -> &'static str {
    if b {
        "on"
    } else {
        "off"
    }
}

fn like(text: &str, pat: &str) -> bool {
    let t: Vec<char> = text.to_ascii_lowercase().chars().collect();
    let p: Vec<char> = pat.to_ascii_lowercase().chars().collect();
    fn go(t: &[char], p: &[char]) -> bool {
        match p.first() {
            None => t.is_empty(),
            Some('%') | Some('*') => (0..=t.len()).any(|i| go(&t[i..], &p[1..])),
            Some('_') | Some('?') => !t.is_empty() && go(&t[1..], &p[1..]),
            Some(c) => t.first() == Some(c) && go(&t[1..], &p[1..]),
        }
    }
    go(&t, &p)
}

/// `.tables` layout: names in columns, column-major like sqlite3.
fn columns_layout(names: &[&str]) -> String {
    if names.is_empty() {
        return String::new();
    }
    let w = names.iter().map(|n| n.len()).max().unwrap() + 2;
    let per_line = (80 / w).max(1);
    let rows = names.len().div_ceil(per_line);
    let mut s = String::new();
    for r in 0..rows {
        let mut line = String::new();
        for c in 0..per_line {
            if let Some(n) = names.get(c * rows + r) {
                line += &format!("{n:<w$}");
            }
        }
        s += line.trim_end();
        s += "\n";
    }
    s
}

fn unescape(s: &str) -> String {
    s.replace("\\t", "\t")
        .replace("\\n", "\n")
        .replace("\\r", "\r")
        .replace("\\\\", "\\")
}

fn escape(s: &str) -> String {
    s.replace('\\', "\\\\")
        .replace('\t', "\\t")
        .replace('\n', "\\n")
        .replace('\r', "\\r")
}

/// Dot-command argument splitting: whitespace, with '...' and "..." quoting.
fn split_args(s: &str) -> Result<Vec<String>> {
    let mut out = Vec::new();
    let mut cur = String::new();
    let mut quote: Option<char> = None;
    let mut has = false;
    for ch in s.chars() {
        match quote {
            Some(q) if ch == q => quote = None,
            Some(_) => cur.push(ch),
            None if ch == '\'' || ch == '"' => {
                quote = Some(ch);
                has = true;
            }
            None if ch.is_whitespace() => {
                if has || !cur.is_empty() {
                    out.push(std::mem::take(&mut cur));
                    has = false;
                }
            }
            None => cur.push(ch),
        }
    }
    if quote.is_some() {
        bail!("unterminated quote in dot-command");
    }
    if has || !cur.is_empty() {
        out.push(cur);
    }
    Ok(out)
}

/// True when `sql` ends with a `;` outside quotes and comments.
pub fn statement_complete(sql: &str) -> bool {
    let mut quote: Option<char> = None;
    let mut last_semicolon = false;
    let c: Vec<char> = sql.chars().collect();
    let mut i = 0;
    while i < c.len() {
        let ch = c[i];
        match quote {
            Some(q) => {
                if ch == q {
                    quote = None;
                }
            }
            None => match ch {
                '\'' | '"' | '`' => {
                    quote = Some(ch);
                    last_semicolon = false;
                }
                '[' => {
                    quote = Some(']');
                    last_semicolon = false;
                }
                '-' if c.get(i + 1) == Some(&'-') => {
                    while i < c.len() && c[i] != '\n' {
                        i += 1;
                    }
                }
                ';' => last_semicolon = true,
                x if x.is_whitespace() => {}
                _ => last_semicolon = false,
            },
        }
        i += 1;
    }
    quote.is_none() && last_semicolon
}

// ---------------------------------------------------------------- rendering

fn cell(v: &Val, s: &Settings) -> String {
    v.text().unwrap_or_else(|| s.nullvalue.clone())
}

fn csv_field(v: &Val, s: &Settings) -> String {
    match v {
        Val::Null => s.nullvalue.clone(),
        Val::Int(_) | Val::Real(_) => v.text().unwrap(),
        _ => {
            let t = v.text().unwrap_or_default();
            if t.contains(['"', '\n', '\r'])
                || t.contains(s.separator.as_str())
                || t.starts_with(' ')
                || t.ends_with(' ')
            {
                format!("\"{}\"", t.replace('"', "\"\""))
            } else {
                t
            }
        }
    }
}

fn json_value(v: &Val) -> String {
    match v {
        Val::Null => "null".into(),
        Val::Int(n) => n.to_string(),
        Val::Real(f) => {
            if f.is_finite() {
                format!("{f}")
            } else {
                "null".into()
            }
        }
        Val::Blob(b) => serde_json::to_string(&crate::catalog::hex(b)).unwrap(),
        other => serde_json::to_string(&other.text().unwrap_or_default()).unwrap(),
    }
}

fn width(s: &str) -> usize {
    s.chars().count()
}

pub fn render(rs: &ResultSet, s: &Settings) -> String {
    let mut out = String::new();
    let nl = &s.newline;
    match &s.mode {
        Mode::List | Mode::Tabs => {
            let sep = if s.mode == Mode::Tabs {
                "\t"
            } else {
                s.separator.as_str()
            };
            if s.headers && !rs.rows.is_empty() {
                out += &(rs.columns.join(sep) + nl);
            }
            for r in &rs.rows {
                out += &(r.iter().map(|v| cell(v, s)).collect::<Vec<_>>().join(sep) + nl);
            }
        }
        Mode::Csv => {
            if s.headers && !rs.rows.is_empty() {
                let h: Vec<String> = rs
                    .columns
                    .iter()
                    .map(|c| csv_field(&Val::Text(c.clone()), s))
                    .collect();
                out += &(h.join(&s.separator) + "\r\n");
            }
            for r in &rs.rows {
                out += &(r
                    .iter()
                    .map(|v| csv_field(v, s))
                    .collect::<Vec<_>>()
                    .join(&s.separator)
                    + "\r\n");
            }
        }
        Mode::Quote => {
            if s.headers && !rs.rows.is_empty() {
                out += &(rs
                    .columns
                    .iter()
                    .map(|c| format!("'{c}'"))
                    .collect::<Vec<_>>()
                    .join(",")
                    + nl);
            }
            for r in &rs.rows {
                out += &(r.iter().map(Val::literal).collect::<Vec<_>>().join(",") + nl);
            }
        }
        Mode::Insert(table) => {
            for r in &rs.rows {
                let cols = if s.headers {
                    format!("({})", rs.columns.join(","))
                } else {
                    String::new()
                };
                out += &format!(
                    "INSERT INTO {table}{cols} VALUES({});{nl}",
                    r.iter().map(Val::literal).collect::<Vec<_>>().join(",")
                );
            }
        }
        Mode::Json => {
            if rs.rows.is_empty() {
                return out;
            }
            let objs: Vec<String> = rs
                .rows
                .iter()
                .map(|r| {
                    let fields: Vec<String> = rs
                        .columns
                        .iter()
                        .zip(r)
                        .map(|(c, v)| {
                            format!("{}:{}", serde_json::to_string(c).unwrap(), json_value(v))
                        })
                        .collect();
                    format!("{{{}}}", fields.join(","))
                })
                .collect();
            out += &format!("[{}]\n", objs.join(",\n"));
        }
        Mode::Line => {
            let w = rs.columns.iter().map(|c| width(c)).max().unwrap_or(0);
            for (n, r) in rs.rows.iter().enumerate() {
                if n > 0 {
                    out += "\n";
                }
                for (c, v) in rs.columns.iter().zip(r) {
                    out += &format!("{c:>w$} = {}\n", cell(v, s));
                }
            }
        }
        Mode::Column | Mode::Table | Mode::Box | Mode::Markdown => {
            if rs.rows.is_empty() {
                return out;
            }
            let cells: Vec<Vec<String>> = rs
                .rows
                .iter()
                .map(|r| r.iter().map(|v| cell(v, s)).collect())
                .collect();
            let mut w: Vec<usize> = rs.columns.iter().map(|c| width(c)).collect();
            for r in &cells {
                for (i, c) in r.iter().enumerate() {
                    w[i] = w[i].max(width(c));
                }
            }
            let numeric: Vec<bool> = (0..rs.columns.len())
                .map(|i| {
                    rs.rows
                        .iter()
                        .all(|r| matches!(r[i], Val::Int(_) | Val::Real(_) | Val::Null))
                })
                .collect();
            let pad = |i: usize, t: &str| {
                if numeric[i] && s.mode != Mode::Column {
                    format!("{t:>width$}", width = w[i])
                } else {
                    format!("{t:<width$}", width = w[i])
                }
            };
            let line = |l: &str, m: &str, r: &str, fill: &str| {
                format!(
                    "{l}{}{r}\n",
                    w.iter()
                        .map(|n| fill.repeat(n + 2))
                        .collect::<Vec<_>>()
                        .join(m)
                )
            };
            let row = |vals: &[String], edge: &str, mid: &str| {
                let parts: Vec<String> = vals.iter().enumerate().map(|(i, v)| pad(i, v)).collect();
                format!("{edge} {} {edge}\n", parts.join(&format!(" {mid} ")))
            };
            // Headers are left-aligned even over numeric columns.
            let header = |edge: &str, mid: &str| {
                let parts: Vec<String> = rs
                    .columns
                    .iter()
                    .enumerate()
                    .map(|(i, v)| format!("{v:<width$}", width = w[i]))
                    .collect();
                format!("{edge} {} {edge}\n", parts.join(&format!(" {mid} ")))
            };
            match s.mode {
                Mode::Column => {
                    let fmt = |vals: &[String]| {
                        vals.iter()
                            .enumerate()
                            .map(|(i, v)| format!("{v:<width$}", width = w[i]))
                            .collect::<Vec<_>>()
                            .join("  ")
                            .trim_end()
                            .to_string()
                            + "\n"
                    };
                    if s.headers {
                        out += &fmt(&rs.columns);
                        out += &(w
                            .iter()
                            .map(|n| "-".repeat(*n))
                            .collect::<Vec<_>>()
                            .join("  ")
                            + "\n");
                    }
                    for r in &cells {
                        out += &fmt(r);
                    }
                }
                Mode::Table => {
                    out += &line("+", "+", "+", "-");
                    out += &header("|", "|");
                    out += &line("+", "+", "+", "-");
                    for r in &cells {
                        out += &row(r, "|", "|");
                    }
                    out += &line("+", "+", "+", "-");
                }
                Mode::Box => {
                    out += &line("┌", "┬", "┐", "─");
                    out += &header("│", "│");
                    out += &line("├", "┼", "┤", "─");
                    for r in &cells {
                        out += &row(r, "│", "│");
                    }
                    out += &line("└", "┴", "┘", "─");
                }
                _ => {
                    out += &header("|", "|");
                    out += &format!(
                        "|{}|\n",
                        w.iter()
                            .map(|n| "-".repeat(n + 2))
                            .collect::<Vec<_>>()
                            .join("|")
                    );
                    for r in &cells {
                        out += &row(r, "|", "|");
                    }
                }
            }
        }
    }
    out
}

const HELP: &str = "\
.backup ?DB? FILE        Back up DB (default \"main\") to directory FILE
.bail on|off             Stop after hitting an error.  Default OFF
.databases               List names and files of attached databases
.dbinfo                  Show status information about the database
.dump ?OBJECTS?          Render database content as SQL
.echo on|off             Turn command echo on or off
.exit ?CODE?             Exit this program with return-code CODE
.headers on|off          Turn display of headers on or off
.help                    Show this message
.indexes ?TABLE?         Show names of indexes
.mode MODE ?TABLE?       Set output mode: box column csv insert json line list
                         markdown quote table tabs
.nullvalue STRING        Use STRING in place of NULL values
.once FILE               Output for the next SQL command only to FILE
.open DIR                Close existing database and reopen DIR
.output ?FILE?           Send output to FILE or stdout if FILE is omitted
.print STRING...         Print literal STRING
.quit                    Exit this program
.read FILE               Read input from FILE
.schema ?PATTERN?        Show the CREATE statements matching PATTERN
.separator COL ?ROW?     Change the column and row separators
.show                    Show the current values for various settings
.tables ?TABLE?          List names of tables matching LIKE pattern TABLE
.timer on|off            Turn SQL timer on or off

PRAGMA integrity_check, quick_check, table_info(T), table_list, index_list(T),
index_info(I), database_list, user_version, data_version, commit_seq.
The database is opened read-only; writes fail with
\"attempt to write a readonly database\".
";

/// Parse `sqlite3`-style arguments: `[OPTIONS] [DBDIR [SQL|.cmd ...]]`.
pub fn main_sqlite(args: Vec<String>) -> i32 {
    let mut s = Settings::default();
    let mut cmds: Vec<String> = Vec::new();
    let mut positional: Vec<String> = Vec::new();
    let mut init: Option<String> = None;
    let mut it = args.into_iter();
    while let Some(a) = it.next() {
        // Like sqlite3, options may appear anywhere; anything else is positional.
        if !a.starts_with('-') || a == "-" {
            positional.push(a);
            continue;
        }
        let opt = a.trim_start_matches('-');
        let mut next = |name: &str| -> Option<String> {
            let v = it.next();
            if v.is_none() {
                eprintln!("Error: missing argument to -{name}");
            }
            v
        };
        match opt {
            "bail" => s.bail = true,
            "batch" | "interactive" | "readonly" | "safe" | "nofollow" => {}
            "echo" => s.echo = true,
            "header" | "headers" => s.headers = true,
            "noheader" | "noheaders" => s.headers = false,
            "list" | "csv" | "tabs" | "json" | "line" | "column" | "table" | "box" | "markdown"
            | "quote" => {
                s.mode = Mode::parse(opt, None).unwrap();
                match s.mode {
                    Mode::Csv => s.separator = ",".into(),
                    Mode::Tabs => s.separator = "\t".into(),
                    Mode::Column => s.headers = true,
                    _ => {}
                }
            }
            "separator" => match next(opt) {
                Some(v) => s.separator = unescape(&v),
                None => return 1,
            },
            "newline" => match next(opt) {
                Some(v) => s.newline = unescape(&v),
                None => return 1,
            },
            "nullvalue" => match next(opt) {
                Some(v) => s.nullvalue = v,
                None => return 1,
            },
            "cmd" => match next(opt) {
                Some(v) => cmds.push(v),
                None => return 1,
            },
            "init" => match next(opt) {
                Some(v) => init = Some(v),
                None => return 1,
            },
            "version" => {
                println!(
                    "sliqtly-db {} (sqlite3-compatible shell)",
                    env!("CARGO_PKG_VERSION")
                );
                return 0;
            }
            "help" => {
                print!("{USAGE}");
                return 0;
            }
            other => {
                eprintln!(
                    "sliqtly-db: Error: unknown option: -{other}\nUse -help for a list of options."
                );
                return 1;
            }
        }
    }

    let mut sh = Shell::new(s);
    let mut pos = positional.into_iter();
    if let Some(dir) = pos.next() {
        if let Err(e) = sh.open(&PathBuf::from(&dir)) {
            eprintln!("Error: unable to open database \"{dir}\": {e:#}");
            return 1;
        }
    }
    if let Some(f) = init {
        match fs::read_to_string(&f) {
            Ok(t) => {
                if let Flow::Quit(c) = sh.run_script(&t) {
                    return c;
                }
            }
            Err(e) => {
                eprintln!("Error: cannot open \"{f}\": {e}");
                return 1;
            }
        }
    }
    for c in cmds {
        if let Flow::Quit(code) = sh.run(&c) {
            return code;
        }
    }
    let rest: Vec<String> = pos.collect();
    if !rest.is_empty() {
        // Like sqlite3: each trailing argument is one SQL text or dot-command,
        // and the first error stops the run with status 1.
        sh.s.bail = true;
        for a in rest {
            let flow = if a.trim_start().starts_with('.') {
                sh.run(&a)
            } else {
                sh.run_script(&a)
            };
            if let Flow::Quit(c) = flow {
                return c;
            }
        }
        return (sh.errors > 0) as i32;
    }
    match sh.interact() {
        Flow::Quit(c) => c,
        Flow::Continue => (sh.errors > 0 && !io::stdin().is_terminal()) as i32,
    }
}

const USAGE: &str = "\
Usage: sliqtly-db [OPTIONS] DBDIR [SQL|.COMMAND ...]
       sliqtly-db --db DBDIR <info|stats|verify|query|backup|restore> ...

sqlite3-compatible shell over a SliqtlyDB database, opened read-only.
OPTIONS include:
   -bail                stop after hitting an error
   -box                 set output mode to 'box'
   -cmd COMMAND         run \"COMMAND\" before reading stdin
   -column              set output mode to 'column'
   -csv                 set output mode to 'csv'
   -echo                print inputs before execution
   -init FILENAME       read/process named file
   -[no]header          turn headers on or off
   -help                show this message
   -json                set output mode to 'json'
   -line                set output mode to 'line'
   -list                set output mode to 'list'
   -markdown            set output mode to 'markdown'
   -newline SEP         set output row separator. Default: '\\n'
   -nullvalue TEXT      set text string for NULL values. Default ''
   -quote               set output mode to 'quote'
   -readonly            open the database read-only (always the case)
   -separator SEP       set output column separator. Default: '|'
   -table               set output mode to 'table'
   -tabs                set output mode to 'tabs'
   -version             show version
";
