//! `sliqtly-db query`: a read-only SQL subset over the semantic tables.
//!
//! ```sql
//! SELECT id, title FROM rooms
//! WHERE updated > now() - interval '7 days'
//! ORDER BY updated DESC LIMIT 20;
//! ```
//!
//! Supported: `SELECT * | COUNT(*) | col, ...`, `FROM rooms|documents|memberships`,
//! `WHERE` with `AND OR NOT`, `= != <> < <= > >=`, `LIKE`, `IS [NOT] NULL`,
//! `now()`, `interval 'N unit'`, `+ -`; `ORDER BY col [ASC|DESC], ...`; `LIMIT n`.
//! Only SELECT exists, so the shell cannot modify the database.

use crate::model::Model;
use anyhow::{anyhow, bail, Result};
use chrono::{DateTime, Duration, Utc};
use std::cmp::Ordering;
use std::collections::BTreeMap;

#[derive(Debug)]
pub struct Table {
    pub name: &'static str,
    pub columns: &'static [&'static str],
}

pub const TABLES: &[Table] = &[
    Table {
        name: "rooms",
        columns: &[
            "id",
            "title",
            "description",
            "created_by",
            "created_at",
            "updated_at",
            "metadata",
        ],
    },
    Table {
        name: "documents",
        columns: &[
            "id",
            "room_id",
            "title",
            "version",
            "created_by",
            "created_at",
            "updated_at",
            "metadata",
        ],
    },
    Table {
        name: "memberships",
        columns: &["user_id", "room_id", "role", "joined_at", "metadata"],
    },
];

/// Short names operators reach for first.
fn canonical(col: &str) -> &str {
    match col {
        "updated" => "updated_at",
        "created" => "created_at",
        "joined" => "joined_at",
        other => other,
    }
}

// ---------------------------------------------------------------- tokens

#[derive(Debug, Clone, PartialEq)]
enum Tok {
    Word(String),
    Str(String),
    Num(f64),
    Sym(&'static str),
}

fn lex(src: &str) -> Result<Vec<Tok>> {
    let c: Vec<char> = src.chars().collect();
    let mut i = 0;
    let mut out = Vec::new();
    while i < c.len() {
        let ch = c[i];
        if ch.is_whitespace() {
            i += 1;
        } else if ch == '-' && c.get(i + 1) == Some(&'-') {
            while i < c.len() && c[i] != '\n' {
                i += 1;
            }
        } else if ch.is_ascii_alphabetic() || ch == '_' {
            let s = i;
            while i < c.len() && (c[i].is_ascii_alphanumeric() || c[i] == '_') {
                i += 1;
            }
            out.push(Tok::Word(c[s..i].iter().collect()));
        } else if ch.is_ascii_digit() {
            let s = i;
            while i < c.len() && (c[i].is_ascii_digit() || c[i] == '.') {
                i += 1;
            }
            let t: String = c[s..i].iter().collect();
            out.push(Tok::Num(t.parse().map_err(|_| anyhow!("bad number {t}"))?));
        } else if ch == '\'' {
            let mut s = String::new();
            i += 1;
            loop {
                match c.get(i) {
                    None => bail!("unterminated string literal"),
                    Some('\'') if c.get(i + 1) == Some(&'\'') => {
                        s.push('\'');
                        i += 2;
                    }
                    Some('\'') => {
                        i += 1;
                        break;
                    }
                    Some(&x) => {
                        s.push(x);
                        i += 1;
                    }
                }
            }
            out.push(Tok::Str(s));
        } else {
            let two: String = c[i..(i + 2).min(c.len())].iter().collect();
            let sym = ["<=", ">=", "!=", "<>"].into_iter().find(|s| *s == two);
            if let Some(s) = sym {
                out.push(Tok::Sym(s));
                i += 2;
            } else {
                let s = match ch {
                    '*' => "*",
                    ',' => ",",
                    '(' => "(",
                    ')' => ")",
                    '=' => "=",
                    '<' => "<",
                    '>' => ">",
                    '+' => "+",
                    '-' => "-",
                    ';' => ";",
                    _ => bail!("unexpected character {ch:?}"),
                };
                out.push(Tok::Sym(s));
                i += 1;
            }
        }
    }
    Ok(out)
}

// ---------------------------------------------------------------- AST

#[derive(Debug, Clone)]
pub enum Expr {
    Lit(Val),
    Col(String),
    Now,
    Bin(Box<Expr>, &'static str, Box<Expr>),
    Not(Box<Expr>),
    IsNull(Box<Expr>, bool),
    Like(Box<Expr>, Box<Expr>, bool),
}

#[derive(Debug)]
pub enum Projection {
    All,
    Count,
    Cols(Vec<String>),
}

#[derive(Debug)]
pub struct Select {
    pub projection: Projection,
    pub table: &'static Table,
    pub filter: Option<Expr>,
    pub order: Vec<(String, bool)>,
    pub limit: Option<usize>,
}

struct Parser {
    t: Vec<Tok>,
    i: usize,
}

impl Parser {
    fn peek(&self) -> Option<&Tok> {
        self.t.get(self.i)
    }

    fn kw(&self, w: &str) -> bool {
        matches!(self.peek(), Some(Tok::Word(x)) if x.eq_ignore_ascii_case(w))
    }

    fn eat_kw(&mut self, w: &str) -> bool {
        if self.kw(w) {
            self.i += 1;
            true
        } else {
            false
        }
    }

    fn expect_kw(&mut self, w: &str) -> Result<()> {
        if self.eat_kw(w) {
            Ok(())
        } else {
            bail!("expected {w}, found {}", self.found())
        }
    }

    fn sym(&self, s: &str) -> bool {
        matches!(self.peek(), Some(Tok::Sym(x)) if *x == s)
    }

    fn eat_sym(&mut self, s: &str) -> bool {
        if self.sym(s) {
            self.i += 1;
            true
        } else {
            false
        }
    }

    fn expect_sym(&mut self, s: &str) -> Result<()> {
        if self.eat_sym(s) {
            Ok(())
        } else {
            bail!("expected '{s}', found {}", self.found())
        }
    }

    fn found(&self) -> String {
        match self.peek() {
            None => "end of query".into(),
            Some(Tok::Word(w)) => w.clone(),
            Some(Tok::Str(s)) => format!("'{s}'"),
            Some(Tok::Num(n)) => n.to_string(),
            Some(Tok::Sym(s)) => format!("'{s}'"),
        }
    }

    fn ident(&mut self) -> Result<String> {
        match self.peek().cloned() {
            Some(Tok::Word(w)) => {
                self.i += 1;
                Ok(w.to_ascii_lowercase())
            }
            _ => bail!("expected a name, found {}", self.found()),
        }
    }

    fn select(&mut self) -> Result<Select> {
        self.expect_kw("select")?;
        let projection = if self.eat_sym("*") {
            Projection::All
        } else if self.kw("count") {
            self.i += 1;
            self.expect_sym("(")?;
            self.expect_sym("*")?;
            self.expect_sym(")")?;
            Projection::Count
        } else {
            let mut cols = vec![self.ident()?];
            while self.eat_sym(",") {
                cols.push(self.ident()?);
            }
            Projection::Cols(cols)
        };
        self.expect_kw("from")?;
        let name = self.ident()?;
        let table = TABLES.iter().find(|t| t.name == name).ok_or_else(|| {
            anyhow!(
                "unknown table {name}; tables: {}",
                TABLES.iter().map(|t| t.name).collect::<Vec<_>>().join(", ")
            )
        })?;
        let filter = if self.eat_kw("where") {
            Some(self.or()?)
        } else {
            None
        };
        let mut order = Vec::new();
        if self.eat_kw("order") {
            self.expect_kw("by")?;
            loop {
                let c = self.ident()?;
                let desc = if self.eat_kw("desc") {
                    true
                } else {
                    self.eat_kw("asc");
                    false
                };
                order.push((c, desc));
                if !self.eat_sym(",") {
                    break;
                }
            }
        }
        let limit = if self.eat_kw("limit") {
            match self.peek().cloned() {
                Some(Tok::Num(n)) if n >= 0.0 && n.fract() == 0.0 => {
                    self.i += 1;
                    Some(n as usize)
                }
                _ => bail!("LIMIT needs a non-negative integer"),
            }
        } else {
            None
        };
        self.eat_sym(";");
        if self.i < self.t.len() {
            if self.kw("insert") || self.kw("update") || self.kw("delete") || self.kw("drop") {
                bail!("the query shell is read-only");
            }
            bail!("unexpected {} after query", self.found());
        }
        let s = Select {
            projection,
            table,
            filter,
            order,
            limit,
        };
        check_columns(&s)?;
        Ok(s)
    }

    fn or(&mut self) -> Result<Expr> {
        let mut l = self.and()?;
        while self.eat_kw("or") {
            l = Expr::Bin(Box::new(l), "or", Box::new(self.and()?));
        }
        Ok(l)
    }

    fn and(&mut self) -> Result<Expr> {
        let mut l = self.not()?;
        while self.eat_kw("and") {
            l = Expr::Bin(Box::new(l), "and", Box::new(self.not()?));
        }
        Ok(l)
    }

    fn not(&mut self) -> Result<Expr> {
        if self.eat_kw("not") {
            return Ok(Expr::Not(Box::new(self.not()?)));
        }
        self.cmp()
    }

    fn cmp(&mut self) -> Result<Expr> {
        let l = self.add()?;
        for op in ["=", "!=", "<>", "<=", ">=", "<", ">"] {
            if self.eat_sym(op) {
                let op = if op == "<>" { "!=" } else { op };
                return Ok(Expr::Bin(Box::new(l), op, Box::new(self.add()?)));
            }
        }
        if self.eat_kw("is") {
            let neg = self.eat_kw("not");
            self.expect_kw("null")?;
            return Ok(Expr::IsNull(Box::new(l), neg));
        }
        let neg = if self.kw("not")
            && matches!(self.t.get(self.i + 1), Some(Tok::Word(w)) if w.eq_ignore_ascii_case("like"))
        {
            self.i += 1;
            true
        } else {
            false
        };
        if self.eat_kw("like") {
            return Ok(Expr::Like(Box::new(l), Box::new(self.add()?), neg));
        }
        Ok(l)
    }

    fn add(&mut self) -> Result<Expr> {
        let mut l = self.primary()?;
        loop {
            if self.eat_sym("+") {
                l = Expr::Bin(Box::new(l), "+", Box::new(self.primary()?));
            } else if self.eat_sym("-") {
                l = Expr::Bin(Box::new(l), "-", Box::new(self.primary()?));
            } else {
                return Ok(l);
            }
        }
    }

    fn primary(&mut self) -> Result<Expr> {
        match self.peek().cloned() {
            Some(Tok::Num(n)) => {
                self.i += 1;
                Ok(Expr::Lit(Val::Num(n)))
            }
            Some(Tok::Str(s)) => {
                self.i += 1;
                Ok(Expr::Lit(Val::Str(s)))
            }
            Some(Tok::Sym("(")) => {
                self.i += 1;
                let e = self.or()?;
                self.expect_sym(")")?;
                Ok(e)
            }
            Some(Tok::Word(w)) => {
                self.i += 1;
                match w.to_ascii_lowercase().as_str() {
                    "null" => Ok(Expr::Lit(Val::Null)),
                    "true" => Ok(Expr::Lit(Val::Bool(true))),
                    "false" => Ok(Expr::Lit(Val::Bool(false))),
                    "now" => {
                        self.expect_sym("(")?;
                        self.expect_sym(")")?;
                        Ok(Expr::Now)
                    }
                    "interval" => match self.peek().cloned() {
                        Some(Tok::Str(s)) => {
                            self.i += 1;
                            Ok(Expr::Lit(Val::Interval(parse_interval(&s)?)))
                        }
                        _ => bail!("interval needs a string such as '7 days'"),
                    },
                    other => Ok(Expr::Col(other.to_string())),
                }
            }
            _ => bail!("unexpected {}", self.found()),
        }
    }
}

fn parse_interval(s: &str) -> Result<Duration> {
    let mut parts = s.split_whitespace();
    let n: i64 = parts
        .next()
        .and_then(|p| p.parse().ok())
        .ok_or_else(|| anyhow!("interval '{s}': expected '<number> <unit>'"))?;
    let unit = parts.next().unwrap_or("days").to_ascii_lowercase();
    let d = match unit.trim_end_matches('s') {
        "second" | "sec" => Duration::seconds(n),
        "minute" | "min" => Duration::minutes(n),
        "hour" => Duration::hours(n),
        "day" => Duration::days(n),
        "week" => Duration::weeks(n),
        _ => bail!("interval unit {unit} not supported (seconds, minutes, hours, days, weeks)"),
    };
    Ok(d)
}

fn check_columns(s: &Select) -> Result<()> {
    let known = |c: &str| s.table.columns.contains(&canonical(c));
    let mut cols: Vec<String> = s.order.iter().map(|(c, _)| c.clone()).collect();
    if let Projection::Cols(c) = &s.projection {
        cols.extend(c.iter().cloned());
    }
    if let Some(f) = &s.filter {
        collect_cols(f, &mut cols);
    }
    for c in cols {
        if !known(&c) {
            bail!(
                "unknown column {c} in {}; columns: {}",
                s.table.name,
                s.table.columns.join(", ")
            );
        }
    }
    Ok(())
}

fn collect_cols(e: &Expr, out: &mut Vec<String>) {
    match e {
        Expr::Col(c) => out.push(c.clone()),
        Expr::Bin(a, _, b) | Expr::Like(a, b, _) => {
            collect_cols(a, out);
            collect_cols(b, out);
        }
        Expr::Not(a) | Expr::IsNull(a, _) => collect_cols(a, out),
        Expr::Lit(_) | Expr::Now => {}
    }
}

pub fn parse(sql: &str) -> Result<Select> {
    let t = lex(sql)?;
    if t.is_empty() {
        bail!("empty query");
    }
    Parser { t, i: 0 }.select()
}

// ---------------------------------------------------------------- values

#[derive(Debug, Clone, PartialEq)]
pub enum Val {
    Null,
    Bool(bool),
    Num(f64),
    Str(String),
    Time(DateTime<Utc>),
    Interval(Duration),
    Json(serde_json::Value),
}

impl Val {
    fn from_json(v: &serde_json::Value) -> Val {
        match v {
            serde_json::Value::Null => Val::Null,
            serde_json::Value::Bool(b) => Val::Bool(*b),
            serde_json::Value::Number(n) => Val::Num(n.as_f64().unwrap_or(f64::NAN)),
            serde_json::Value::String(s) => match DateTime::parse_from_rfc3339(s) {
                Ok(t) => Val::Time(t.with_timezone(&Utc)),
                Err(_) => Val::Str(s.clone()),
            },
            other => Val::Json(other.clone()),
        }
    }

    fn truthy(&self) -> bool {
        matches!(self, Val::Bool(true))
    }

    pub fn render(&self) -> String {
        match self {
            Val::Null => "NULL".into(),
            Val::Bool(b) => b.to_string(),
            Val::Num(n) if n.fract() == 0.0 && n.abs() < 1e15 => format!("{}", *n as i64),
            Val::Num(n) => n.to_string(),
            Val::Str(s) => s.clone(),
            Val::Time(t) => t.format("%Y-%m-%d %H:%M:%S").to_string(),
            Val::Interval(d) => format!("{}s", d.num_seconds()),
            Val::Json(j) => j.to_string(),
        }
    }
}

/// SQL three-valued comparison: None when either side is NULL or the types
/// cannot be compared.
fn compare(a: &Val, b: &Val) -> Option<Ordering> {
    use Val::*;
    match (a, b) {
        (Null, _) | (_, Null) => None,
        (Num(x), Num(y)) => x.partial_cmp(y),
        (Str(x), Str(y)) => Some(x.cmp(y)),
        (Bool(x), Bool(y)) => Some(x.cmp(y)),
        (Time(x), Time(y)) => Some(x.cmp(y)),
        (Time(x), Str(y)) => parse_time(y).map(|y| x.cmp(&y)),
        (Str(x), Time(y)) => parse_time(x).map(|x| x.cmp(y)),
        _ => None,
    }
}

fn parse_time(s: &str) -> Option<DateTime<Utc>> {
    if let Ok(t) = DateTime::parse_from_rfc3339(s) {
        return Some(t.with_timezone(&Utc));
    }
    let d = chrono::NaiveDate::parse_from_str(s, "%Y-%m-%d").ok()?;
    Some(d.and_hms_opt(0, 0, 0)?.and_utc())
}

fn like(text: &str, pat: &str) -> bool {
    fn go(t: &[char], p: &[char]) -> bool {
        match p.first() {
            None => t.is_empty(),
            Some('%') => (0..=t.len()).any(|i| go(&t[i..], &p[1..])),
            Some('_') => !t.is_empty() && go(&t[1..], &p[1..]),
            Some(c) => t.first() == Some(c) && go(&t[1..], &p[1..]),
        }
    }
    let t: Vec<char> = text.chars().collect();
    let p: Vec<char> = pat.chars().collect();
    go(&t, &p)
}

type Row = BTreeMap<String, Val>;

fn eval(e: &Expr, row: &Row, now: DateTime<Utc>) -> Result<Val> {
    Ok(match e {
        Expr::Lit(v) => v.clone(),
        Expr::Now => Val::Time(now),
        Expr::Col(c) => row.get(canonical(c)).cloned().unwrap_or(Val::Null),
        Expr::Not(a) => match eval(a, row, now)? {
            Val::Bool(b) => Val::Bool(!b),
            _ => Val::Null,
        },
        Expr::IsNull(a, neg) => Val::Bool((eval(a, row, now)? == Val::Null) != *neg),
        Expr::Like(a, p, neg) => match (eval(a, row, now)?, eval(p, row, now)?) {
            (Val::Str(t), Val::Str(p)) => Val::Bool(like(&t, &p) != *neg),
            _ => Val::Null,
        },
        Expr::Bin(a, op, b) => {
            let (x, y) = (eval(a, row, now)?, eval(b, row, now)?);
            match *op {
                "and" => match (x, y) {
                    (Val::Bool(false), _) | (_, Val::Bool(false)) => Val::Bool(false),
                    (Val::Bool(true), Val::Bool(true)) => Val::Bool(true),
                    _ => Val::Null,
                },
                "or" => match (x, y) {
                    (Val::Bool(true), _) | (_, Val::Bool(true)) => Val::Bool(true),
                    (Val::Bool(false), Val::Bool(false)) => Val::Bool(false),
                    _ => Val::Null,
                },
                "+" | "-" => {
                    let sign = if *op == "+" { 1 } else { -1 };
                    match (x, y) {
                        (Val::Num(p), Val::Num(q)) => Val::Num(p + sign as f64 * q),
                        (Val::Time(t), Val::Interval(d)) => Val::Time(t + d * sign),
                        (Val::Null, _) | (_, Val::Null) => Val::Null,
                        (p, q) => bail!("cannot apply {op} to {} and {}", p.render(), q.render()),
                    }
                }
                cmp => match compare(&x, &y) {
                    None => Val::Null,
                    Some(o) => Val::Bool(match cmp {
                        "=" => o == Ordering::Equal,
                        "!=" => o != Ordering::Equal,
                        "<" => o == Ordering::Less,
                        "<=" => o != Ordering::Greater,
                        ">" => o == Ordering::Greater,
                        ">=" => o != Ordering::Less,
                        _ => unreachable!(),
                    }),
                },
            }
        }
    })
}

// ---------------------------------------------------------------- execution

pub struct ResultSet {
    pub columns: Vec<String>,
    pub rows: Vec<Vec<Val>>,
}

fn rows_of(model: &Model, table: &str) -> Vec<Row> {
    fn to_row<T: serde::Serialize>(v: &T) -> Row {
        match serde_json::to_value(v) {
            Ok(serde_json::Value::Object(m)) => m
                .iter()
                .map(|(k, v)| (k.clone(), Val::from_json(v)))
                .collect(),
            _ => Row::new(),
        }
    }
    match table {
        "rooms" => model.rooms.values().map(|d| to_row(&d.value)).collect(),
        "documents" => model.documents.values().map(|d| to_row(&d.value)).collect(),
        "memberships" => model
            .memberships
            .values()
            .map(|d| to_row(&d.value))
            .collect(),
        _ => Vec::new(),
    }
}

pub fn execute(model: &Model, q: &Select, now: DateTime<Utc>) -> Result<ResultSet> {
    let mut rows = Vec::new();
    for row in rows_of(model, q.table.name) {
        let keep = match &q.filter {
            None => true,
            Some(f) => eval(f, &row, now)?.truthy(),
        };
        if keep {
            rows.push(row);
        }
    }
    if let Projection::Count = q.projection {
        return Ok(ResultSet {
            columns: vec!["count".into()],
            rows: vec![vec![Val::Num(rows.len() as f64)]],
        });
    }
    if !q.order.is_empty() {
        rows.sort_by(|a, b| {
            for (c, desc) in &q.order {
                let c = canonical(c);
                let (x, y) = (
                    a.get(c).unwrap_or(&Val::Null),
                    b.get(c).unwrap_or(&Val::Null),
                );
                // NULLs sort last ascending, like PostgreSQL.
                let o = match (x, y) {
                    (Val::Null, Val::Null) => Ordering::Equal,
                    (Val::Null, _) => Ordering::Greater,
                    (_, Val::Null) => Ordering::Less,
                    _ => compare(x, y).unwrap_or(Ordering::Equal),
                };
                let o = if *desc { o.reverse() } else { o };
                if o != Ordering::Equal {
                    return o;
                }
            }
            Ordering::Equal
        });
    }
    if let Some(n) = q.limit {
        rows.truncate(n);
    }
    let columns: Vec<String> = match &q.projection {
        Projection::Cols(c) => c.clone(),
        _ => q.table.columns.iter().map(|c| c.to_string()).collect(),
    };
    let rows = rows
        .into_iter()
        .map(|r| {
            columns
                .iter()
                .map(|c| r.get(canonical(c)).cloned().unwrap_or(Val::Null))
                .collect()
        })
        .collect();
    Ok(ResultSet { columns, rows })
}

pub fn render_table(rs: &ResultSet) -> String {
    const MAX: usize = 60;
    let cell = |v: &Val| {
        let s = v.render();
        if s.chars().count() > MAX {
            format!("{}…", s.chars().take(MAX - 1).collect::<String>())
        } else {
            s
        }
    };
    let cells: Vec<Vec<String>> = rs
        .rows
        .iter()
        .map(|r| r.iter().map(cell).collect())
        .collect();
    let mut w: Vec<usize> = rs.columns.iter().map(|c| c.chars().count()).collect();
    for r in &cells {
        for (i, c) in r.iter().enumerate() {
            w[i] = w[i].max(c.chars().count());
        }
    }
    let line = |vals: &[String]| {
        vals.iter()
            .enumerate()
            .map(|(i, v)| format!("{v:<width$}", width = w[i]))
            .collect::<Vec<_>>()
            .join(" | ")
            .trim_end()
            .to_string()
    };
    let mut out = line(&rs.columns) + "\n";
    out += &w
        .iter()
        .map(|n| "-".repeat(*n))
        .collect::<Vec<_>>()
        .join("-+-");
    out += "\n";
    for r in &cells {
        out += &line(r);
        out += "\n";
    }
    out += &format!(
        "({} row{})\n",
        rs.rows.len(),
        if rs.rows.len() == 1 { "" } else { "s" }
    );
    out
}

pub fn render_json(rs: &ResultSet) -> serde_json::Value {
    let to_json = |v: &Val| match v {
        Val::Null => serde_json::Value::Null,
        Val::Bool(b) => serde_json::json!(b),
        Val::Num(n) => serde_json::json!(n),
        Val::Str(s) => serde_json::json!(s),
        Val::Time(t) => serde_json::json!(t.to_rfc3339()),
        Val::Interval(d) => serde_json::json!(d.num_seconds()),
        Val::Json(j) => j.clone(),
    };
    serde_json::Value::Array(
        rs.rows
            .iter()
            .map(|r| {
                serde_json::Value::Object(
                    rs.columns
                        .iter()
                        .zip(r)
                        .map(|(c, v)| (c.clone(), to_json(v)))
                        .collect(),
                )
            })
            .collect(),
    )
}
