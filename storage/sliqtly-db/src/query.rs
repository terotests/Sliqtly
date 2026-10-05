//! Read-only SQL in the SQLite dialect over the relational view in `schema.rs`.
//!
//! ```sql
//! SELECT id, title FROM rooms
//! WHERE updated_at > datetime('now', '-7 days')
//! ORDER BY updated_at DESC LIMIT 20;
//! ```
//!
//! Supported: `SELECT [DISTINCT] * | expr [AS alias], ...` with an optional
//! `FROM table`, `WHERE`, `GROUP BY`, `HAVING`, `ORDER BY expr|alias|ordinal
//! [ASC|DESC]`, `LIMIT n [OFFSET m]` / `LIMIT m, n`. Expressions: literals,
//! columns, `|| * / % + -`, comparisons, `IS [NOT]`, `[NOT] IN (...)`,
//! `[NOT] BETWEEN`, `[NOT] LIKE` (ASCII case-insensitive, as in SQLite),
//! `AND OR NOT`, `CASE WHEN`, scalar functions (see `call`) and the aggregates
//! count, sum, total, avg, min, max, group_concat. Joins and subqueries are
//! not supported. Write statements fail with SQLite's read-only error.

use crate::catalog;
use crate::schema;
use anyhow::{anyhow, bail, Result};
use chrono::{DateTime, Duration, NaiveDateTime, Utc};
use std::cmp::Ordering;
use std::collections::BTreeMap;

// ---------------------------------------------------------------- tokens

#[derive(Debug, Clone, PartialEq)]
enum Tok {
    Word(String),
    /// A `"quoted"` or `[bracketed]` identifier.
    Ident(String),
    Str(String),
    Int(i64),
    Real(f64),
    Blob(Vec<u8>),
    Sym(&'static str),
}

struct Lexed {
    toks: Vec<Tok>,
    spans: Vec<(usize, usize)>,
}

fn lex(src: &str) -> Result<Lexed> {
    let c: Vec<(usize, char)> = src.char_indices().collect();
    let at = |i: usize| c.get(i).map(|x| x.1);
    let pos = |i: usize| c.get(i).map(|x| x.0).unwrap_or(src.len());
    let mut i = 0;
    let mut toks = Vec::new();
    let mut spans = Vec::new();
    while i < c.len() {
        let ch = c[i].1;
        let start = i;
        if ch.is_whitespace() {
            i += 1;
            continue;
        }
        if ch == '-' && at(i + 1) == Some('-') {
            while i < c.len() && c[i].1 != '\n' {
                i += 1;
            }
            continue;
        }
        if ch == '/' && at(i + 1) == Some('*') {
            i += 2;
            while i < c.len() && !(c[i].1 == '*' && at(i + 1) == Some('/')) {
                i += 1;
            }
            i += 2;
            continue;
        }
        let tok = if (ch == 'x' || ch == 'X') && at(i + 1) == Some('\'') {
            i += 2;
            let s = i;
            while i < c.len() && c[i].1 != '\'' {
                i += 1;
            }
            let hex: String = c[s..i.min(c.len())].iter().map(|x| x.1).collect();
            i += 1;
            if !hex.len().is_multiple_of(2) || !hex.chars().all(|h| h.is_ascii_hexdigit()) {
                bail!("malformed blob literal X'{hex}'");
            }
            Tok::Blob(
                (0..hex.len())
                    .step_by(2)
                    .map(|j| u8::from_str_radix(&hex[j..j + 2], 16).unwrap())
                    .collect(),
            )
        } else if ch.is_ascii_alphabetic() || ch == '_' {
            while i < c.len() && (c[i].1.is_ascii_alphanumeric() || c[i].1 == '_' || c[i].1 == '$')
            {
                i += 1;
            }
            Tok::Word(c[start..i].iter().map(|x| x.1).collect())
        } else if ch.is_ascii_digit()
            || (ch == '.' && at(i + 1).is_some_and(|d| d.is_ascii_digit()))
        {
            while i < c.len()
                && (c[i].1.is_ascii_digit() || c[i].1 == '.' || c[i].1 == 'e' || c[i].1 == 'E')
            {
                i += 1;
            }
            let t: String = c[start..i].iter().map(|x| x.1).collect();
            match t.parse::<i64>() {
                Ok(n) => Tok::Int(n),
                Err(_) => Tok::Real(
                    t.parse()
                        .map_err(|_| anyhow!("near \"{t}\": syntax error"))?,
                ),
            }
        } else if ch == '\'' || ch == '"' || ch == '[' || ch == '`' {
            let close = if ch == '[' { ']' } else { ch };
            let mut s = String::new();
            i += 1;
            loop {
                match at(i) {
                    None => bail!("unrecognized token: \"{}\"", &src[pos(start)..]),
                    Some(x) if x == close && at(i + 1) == Some(close) && close != ']' => {
                        s.push(x);
                        i += 2;
                    }
                    Some(x) if x == close => {
                        i += 1;
                        break;
                    }
                    Some(x) => {
                        s.push(x);
                        i += 1;
                    }
                }
            }
            if ch == '\'' {
                Tok::Str(s)
            } else {
                Tok::Ident(s)
            }
        } else {
            let two: String = c[i..(i + 2).min(c.len())].iter().map(|x| x.1).collect();
            if let Some(s) = ["<=", ">=", "!=", "<>", "==", "||"]
                .into_iter()
                .find(|s| *s == two)
            {
                i += 2;
                Tok::Sym(s)
            } else {
                i += 1;
                Tok::Sym(match ch {
                    '*' => "*",
                    ',' => ",",
                    '(' => "(",
                    ')' => ")",
                    '=' => "=",
                    '<' => "<",
                    '>' => ">",
                    '+' => "+",
                    '-' => "-",
                    '/' => "/",
                    '%' => "%",
                    ';' => ";",
                    '.' => ".",
                    _ => bail!("unrecognized token: \"{ch}\""),
                })
            }
        };
        toks.push(tok);
        spans.push((pos(start), pos(i)));
    }
    Ok(Lexed { toks, spans })
}

// ---------------------------------------------------------------- values

/// SQLite storage classes, plus two internal kinds for date arithmetic.
#[derive(Debug, Clone, PartialEq)]
pub enum Val {
    Null,
    Int(i64),
    Real(f64),
    Text(String),
    Blob(Vec<u8>),
    Time(DateTime<Utc>),
    Interval(Duration),
}

impl Val {
    fn truthy(&self) -> Option<bool> {
        match self {
            Val::Null => None,
            Val::Int(n) => Some(*n != 0),
            Val::Real(f) => Some(*f != 0.0),
            Val::Text(s) => Some(s.trim().parse::<f64>().map(|f| f != 0.0).unwrap_or(false)),
            _ => Some(true),
        }
    }

    fn bool(b: bool) -> Val {
        Val::Int(b as i64)
    }

    fn num(&self) -> Option<f64> {
        match self {
            Val::Int(n) => Some(*n as f64),
            Val::Real(f) => Some(*f),
            Val::Text(s) => s.trim().parse().ok(),
            _ => None,
        }
    }

    pub fn type_name(&self) -> &'static str {
        match self {
            Val::Null => "null",
            Val::Int(_) => "integer",
            Val::Real(_) => "real",
            Val::Text(_) | Val::Time(_) | Val::Interval(_) => "text",
            Val::Blob(_) => "blob",
        }
    }

    /// Text as the sqlite3 shell prints it in list/column modes.
    pub fn text(&self) -> Option<String> {
        Some(match self {
            Val::Null => return None,
            Val::Int(n) => n.to_string(),
            Val::Real(f) => fmt_real(*f),
            Val::Text(s) => s.clone(),
            Val::Blob(b) => String::from_utf8_lossy(b).into_owned(),
            Val::Time(t) => sqlite_time(t),
            Val::Interval(d) => format!("{} seconds", d.num_seconds()),
        })
    }

    /// SQL literal, as `.dump` and `.mode insert` write it.
    pub fn literal(&self) -> String {
        match self {
            Val::Null => "NULL".into(),
            Val::Int(n) => n.to_string(),
            Val::Real(f) => fmt_real(*f),
            Val::Blob(b) => format!("X'{}'", catalog::hex(b).to_uppercase()),
            other => format!("'{}'", other.text().unwrap_or_default().replace('\'', "''")),
        }
    }
}

pub fn fmt_real(f: f64) -> String {
    if f.is_finite() && f.fract() == 0.0 && f.abs() < 1e15 {
        format!("{f:.1}")
    } else {
        format!("{f}")
    }
}

fn sqlite_time(t: &DateTime<Utc>) -> String {
    t.format("%Y-%m-%d %H:%M:%S").to_string()
}

pub fn parse_time(s: &str) -> Option<DateTime<Utc>> {
    if let Ok(t) = DateTime::parse_from_rfc3339(s) {
        return Some(t.with_timezone(&Utc));
    }
    for f in [
        "%Y-%m-%d %H:%M:%S%.f",
        "%Y-%m-%d %H:%M:%S",
        "%Y-%m-%d %H:%M",
        "%Y-%m-%dT%H:%M:%S%.f",
    ] {
        if let Ok(t) = NaiveDateTime::parse_from_str(s, f) {
            return Some(t.and_utc());
        }
    }
    let d = chrono::NaiveDate::parse_from_str(s, "%Y-%m-%d").ok()?;
    Some(d.and_hms_opt(0, 0, 0)?.and_utc())
}

fn looks_like_time(s: &str) -> bool {
    let b = s.as_bytes();
    b.len() >= 10 && b[4] == b'-' && b[7] == b'-' && b[..4].iter().all(u8::is_ascii_digit)
}

/// SQLite ordering across classes: NULL < numbers < text < blob. Two values
/// that both read as timestamps compare as instants, so the RFC 3339 text the
/// store writes and `datetime()` output order correctly against each other.
pub fn compare(a: &Val, b: &Val) -> Ordering {
    use Val::*;
    let rank = |v: &Val| match v {
        Null => 0,
        Int(_) | Real(_) => 1,
        Text(_) | Time(_) | Interval(_) => 2,
        Blob(_) => 3,
    };
    match (a, b) {
        (Int(x), Int(y)) => x.cmp(y),
        (Int(_) | Real(_), Int(_) | Real(_)) => a
            .num()
            .unwrap()
            .partial_cmp(&b.num().unwrap())
            .unwrap_or(Ordering::Equal),
        (Blob(x), Blob(y)) => x.cmp(y),
        (Time(x), Time(y)) => x.cmp(y),
        (Time(x), Text(y)) => parse_time(y).map(|y| x.cmp(&y)).unwrap_or(Ordering::Less),
        (Text(x), Time(y)) => parse_time(x).map(|x| x.cmp(y)).unwrap_or(Ordering::Greater),
        (Text(x), Text(y)) => {
            if looks_like_time(x) && looks_like_time(y) {
                if let (Some(p), Some(q)) = (parse_time(x), parse_time(y)) {
                    return p.cmp(&q);
                }
            }
            x.cmp(y)
        }
        _ => rank(a).cmp(&rank(b)),
    }
}

// ---------------------------------------------------------------- AST

#[derive(Debug, Clone)]
pub enum Expr {
    Lit(Val),
    Col(String),
    Unary(&'static str, Box<Expr>),
    Bin(Box<Expr>, &'static str, Box<Expr>),
    Is(Box<Expr>, Box<Expr>, bool),
    In(Box<Expr>, Vec<Expr>, bool),
    Between(Box<Expr>, Box<Expr>, Box<Expr>, bool),
    Like(Box<Expr>, Box<Expr>, bool),
    Case(Option<Box<Expr>>, Vec<(Expr, Expr)>, Option<Box<Expr>>),
    Call(String, Vec<Expr>),
    /// Aggregate: name, argument (`None` for `count(*)`), DISTINCT.
    Agg(String, Option<Box<Expr>>, bool),
}

#[derive(Debug)]
pub struct Item {
    pub expr: Option<Expr>, // None = `*`
    pub name: String,
}

#[derive(Debug)]
pub struct Select {
    pub distinct: bool,
    pub items: Vec<Item>,
    pub from: Option<String>,
    pub filter: Option<Expr>,
    pub group_by: Vec<Expr>,
    pub having: Option<Expr>,
    pub order: Vec<(Expr, bool)>,
    pub limit: Option<Expr>,
    pub offset: Option<Expr>,
}

#[derive(Debug)]
pub enum Stmt {
    Select(Box<Select>),
    /// `PRAGMA name`, `PRAGMA name(arg)`, `PRAGMA name = arg`.
    Pragma(String, Option<String>),
    /// BEGIN / COMMIT / END / ROLLBACK: accepted and ignored, so `.dump`-style
    /// scripts that wrap reads in a transaction still run.
    Nothing,
}

const AGGREGATES: &[&str] = &["count", "sum", "total", "avg", "min", "max", "group_concat"];
const WRITES: &[&str] = &[
    "insert", "update", "delete", "replace", "create", "drop", "alter", "vacuum", "reindex",
    "attach", "detach", "analyze",
];

struct Parser<'a> {
    src: &'a str,
    t: Vec<Tok>,
    spans: Vec<(usize, usize)>,
    i: usize,
}

impl<'a> Parser<'a> {
    fn peek(&self) -> Option<&Tok> {
        self.t.get(self.i)
    }

    fn peek_at(&self, n: usize) -> Option<&Tok> {
        self.t.get(self.i + n)
    }

    fn kw(&self, w: &str) -> bool {
        matches!(self.peek(), Some(Tok::Word(x)) if x.eq_ignore_ascii_case(w))
    }

    fn eat_kw(&mut self, w: &str) -> bool {
        let hit = self.kw(w);
        if hit {
            self.i += 1;
        }
        hit
    }

    fn expect_kw(&mut self, w: &str) -> Result<()> {
        if self.eat_kw(w) {
            Ok(())
        } else {
            Err(self.syntax())
        }
    }

    fn sym(&self, s: &str) -> bool {
        matches!(self.peek(), Some(Tok::Sym(x)) if *x == s)
    }

    fn eat_sym(&mut self, s: &str) -> bool {
        let hit = self.sym(s);
        if hit {
            self.i += 1;
        }
        hit
    }

    fn expect_sym(&mut self, s: &str) -> Result<()> {
        if self.eat_sym(s) {
            Ok(())
        } else {
            Err(self.syntax())
        }
    }

    /// SQLite's wording: `near "X": syntax error`.
    fn syntax(&self) -> anyhow::Error {
        match self.spans.get(self.i) {
            Some(&(a, b)) => anyhow!("near \"{}\": syntax error", &self.src[a..b]),
            None => anyhow!("incomplete input"),
        }
    }

    fn text(&self, from: usize, to: usize) -> String {
        if from >= to {
            return String::new();
        }
        self.src[self.spans[from].0..self.spans[to - 1].1].to_string()
    }

    fn ident(&mut self) -> Result<String> {
        match self.peek().cloned() {
            Some(Tok::Word(w)) => {
                self.i += 1;
                Ok(w.to_ascii_lowercase())
            }
            Some(Tok::Ident(w)) => {
                self.i += 1;
                Ok(w.to_ascii_lowercase())
            }
            _ => Err(self.syntax()),
        }
    }

    fn stmt(&mut self) -> Result<Stmt> {
        let first = match self.peek() {
            Some(Tok::Word(w)) => w.to_ascii_lowercase(),
            _ => return Err(self.syntax()),
        };
        let s = match first.as_str() {
            "select" => Stmt::Select(Box::new(self.select()?)),
            "pragma" => {
                self.i += 1;
                let mut name = self.ident()?;
                if self.eat_sym(".") {
                    name = self.ident()?; // schema-qualified: main.integrity_check
                }
                let arg = if self.eat_sym("(") {
                    let a = self.pragma_arg()?;
                    self.expect_sym(")")?;
                    Some(a)
                } else if self.eat_sym("=") {
                    Some(self.pragma_arg()?)
                } else {
                    None
                };
                Stmt::Pragma(name, arg)
            }
            "begin" | "commit" | "end" | "rollback" => {
                self.i += 1;
                while !self.sym(";") && self.peek().is_some() {
                    self.i += 1;
                }
                Stmt::Nothing
            }
            w if WRITES.contains(&w) => bail!("attempt to write a readonly database"),
            _ => return Err(self.syntax()),
        };
        self.eat_sym(";");
        if self.peek().is_some() {
            if let Some(Tok::Word(w)) = self.peek() {
                if WRITES.contains(&w.to_ascii_lowercase().as_str()) {
                    bail!("attempt to write a readonly database");
                }
            }
            bail!("only one statement at a time; {}", self.syntax());
        }
        Ok(s)
    }

    fn pragma_arg(&mut self) -> Result<String> {
        match self.peek().cloned() {
            Some(Tok::Word(w)) | Some(Tok::Ident(w)) | Some(Tok::Str(w)) => {
                self.i += 1;
                Ok(w)
            }
            Some(Tok::Int(n)) => {
                self.i += 1;
                Ok(n.to_string())
            }
            Some(Tok::Sym("-")) => {
                self.i += 1;
                Ok(format!("-{}", self.pragma_arg()?))
            }
            _ => Err(self.syntax()),
        }
    }

    fn select(&mut self) -> Result<Select> {
        self.expect_kw("select")?;
        let distinct = self.eat_kw("distinct");
        self.eat_kw("all");
        let mut items = Vec::new();
        loop {
            let start = self.i;
            if self.eat_sym("*") {
                items.push(Item {
                    expr: None,
                    name: "*".into(),
                });
            } else {
                let e = self.expr()?;
                let end = self.i;
                let explicit = self.eat_kw("as");
                let name = if explicit
                    || matches!(self.peek(), Some(Tok::Ident(_)))
                    || matches!(self.peek(), Some(Tok::Word(w)) if !is_clause_kw(w))
                {
                    // Aliases keep their case, as in SQLite.
                    match self.peek().cloned() {
                        Some(Tok::Word(w)) | Some(Tok::Ident(w)) | Some(Tok::Str(w)) => {
                            self.i += 1;
                            w
                        }
                        _ => return Err(self.syntax()),
                    }
                } else {
                    match &e {
                        Expr::Col(c) => c.clone(),
                        _ => self.text(start, end),
                    }
                };
                items.push(Item {
                    expr: Some(e),
                    name,
                });
            }
            if !self.eat_sym(",") {
                break;
            }
        }
        let from = if self.eat_kw("from") {
            let mut t = self.ident()?;
            if self.eat_sym(".") {
                t = self.ident()?; // main.rooms
            }
            Some(t)
        } else {
            None
        };
        let filter = if self.eat_kw("where") {
            Some(self.expr()?)
        } else {
            None
        };
        let mut group_by = Vec::new();
        if self.eat_kw("group") {
            self.expect_kw("by")?;
            loop {
                group_by.push(self.expr()?);
                if !self.eat_sym(",") {
                    break;
                }
            }
        }
        let having = if self.eat_kw("having") {
            Some(self.expr()?)
        } else {
            None
        };
        let mut order = Vec::new();
        if self.eat_kw("order") {
            self.expect_kw("by")?;
            loop {
                let e = self.expr()?;
                let desc = if self.eat_kw("desc") {
                    true
                } else {
                    self.eat_kw("asc");
                    false
                };
                order.push((e, desc));
                if !self.eat_sym(",") {
                    break;
                }
            }
        }
        let (mut limit, mut offset) = (None, None);
        if self.eat_kw("limit") {
            let a = self.expr()?;
            if self.eat_sym(",") {
                offset = Some(a);
                limit = Some(self.expr()?);
            } else {
                limit = Some(a);
                if self.eat_kw("offset") {
                    offset = Some(self.expr()?);
                }
            }
        }
        Ok(Select {
            distinct,
            items,
            from,
            filter,
            group_by,
            having,
            order,
            limit,
            offset,
        })
    }

    fn expr(&mut self) -> Result<Expr> {
        self.or()
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
            return Ok(Expr::Unary("not", Box::new(self.not()?)));
        }
        self.cmp()
    }

    fn cmp(&mut self) -> Result<Expr> {
        let l = self.rel()?;
        for (sym, op) in [("=", "="), ("==", "="), ("!=", "!="), ("<>", "!=")] {
            if self.eat_sym(sym) {
                return Ok(Expr::Bin(Box::new(l), op, Box::new(self.rel()?)));
            }
        }
        if self.eat_kw("is") {
            let neg = self.eat_kw("not");
            return Ok(Expr::Is(Box::new(l), Box::new(self.rel()?), neg));
        }
        if self.eat_kw("isnull") {
            return Ok(Expr::Is(Box::new(l), Box::new(Expr::Lit(Val::Null)), false));
        }
        if self.eat_kw("notnull") {
            return Ok(Expr::Is(Box::new(l), Box::new(Expr::Lit(Val::Null)), true));
        }
        let neg = self.kw("not")
            && matches!(self.peek_at(1), Some(Tok::Word(w)) if ["like", "in", "between", "null"].contains(&w.to_ascii_lowercase().as_str()));
        if neg {
            self.i += 1;
            if self.eat_kw("null") {
                return Ok(Expr::Is(Box::new(l), Box::new(Expr::Lit(Val::Null)), true));
            }
        }
        if self.eat_kw("like") {
            return Ok(Expr::Like(Box::new(l), Box::new(self.rel()?), neg));
        }
        if self.eat_kw("in") {
            self.expect_sym("(")?;
            let mut list = Vec::new();
            if !self.sym(")") {
                loop {
                    list.push(self.expr()?);
                    if !self.eat_sym(",") {
                        break;
                    }
                }
            }
            self.expect_sym(")")?;
            return Ok(Expr::In(Box::new(l), list, neg));
        }
        if self.eat_kw("between") {
            let lo = self.rel()?;
            self.expect_kw("and")?;
            let hi = self.rel()?;
            return Ok(Expr::Between(Box::new(l), Box::new(lo), Box::new(hi), neg));
        }
        Ok(l)
    }

    fn rel(&mut self) -> Result<Expr> {
        let mut l = self.add()?;
        loop {
            let op = ["<=", ">=", "<", ">"].into_iter().find(|s| self.sym(s));
            match op {
                Some(op) => {
                    self.i += 1;
                    l = Expr::Bin(Box::new(l), op, Box::new(self.add()?));
                }
                None => return Ok(l),
            }
        }
    }

    fn add(&mut self) -> Result<Expr> {
        let mut l = self.mul()?;
        loop {
            let op = ["+", "-"].into_iter().find(|s| self.sym(s));
            match op {
                Some(op) => {
                    self.i += 1;
                    l = Expr::Bin(Box::new(l), op, Box::new(self.mul()?));
                }
                None => return Ok(l),
            }
        }
    }

    fn mul(&mut self) -> Result<Expr> {
        let mut l = self.concat()?;
        loop {
            let op = ["*", "/", "%"].into_iter().find(|s| self.sym(s));
            match op {
                Some(op) => {
                    self.i += 1;
                    l = Expr::Bin(Box::new(l), op, Box::new(self.concat()?));
                }
                None => return Ok(l),
            }
        }
    }

    fn concat(&mut self) -> Result<Expr> {
        let mut l = self.unary()?;
        while self.eat_sym("||") {
            l = Expr::Bin(Box::new(l), "||", Box::new(self.unary()?));
        }
        Ok(l)
    }

    fn unary(&mut self) -> Result<Expr> {
        if self.eat_sym("-") {
            return Ok(Expr::Unary("-", Box::new(self.unary()?)));
        }
        if self.eat_sym("+") {
            return self.unary();
        }
        self.primary()
    }

    fn primary(&mut self) -> Result<Expr> {
        let tok = self
            .peek()
            .cloned()
            .ok_or_else(|| anyhow!("incomplete input"))?;
        self.i += 1;
        Ok(match tok {
            Tok::Int(n) => Expr::Lit(Val::Int(n)),
            Tok::Real(f) => Expr::Lit(Val::Real(f)),
            Tok::Str(s) => Expr::Lit(Val::Text(s)),
            Tok::Blob(b) => Expr::Lit(Val::Blob(b)),
            Tok::Ident(s) => Expr::Col(s.to_ascii_lowercase()),
            Tok::Sym("(") => {
                let e = self.expr()?;
                self.expect_sym(")")?;
                e
            }
            Tok::Word(w) => {
                let lw = w.to_ascii_lowercase();
                match lw.as_str() {
                    "null" => Expr::Lit(Val::Null),
                    "true" => Expr::Lit(Val::Int(1)),
                    "false" => Expr::Lit(Val::Int(0)),
                    "current_timestamp" => {
                        Expr::Call("datetime".into(), vec![Expr::Lit(Val::Text("now".into()))])
                    }
                    "current_date" => {
                        Expr::Call("date".into(), vec![Expr::Lit(Val::Text("now".into()))])
                    }
                    "interval" => match self.peek().cloned() {
                        Some(Tok::Str(s)) => {
                            self.i += 1;
                            Expr::Lit(Val::Interval(parse_interval(&s)?))
                        }
                        _ => return Err(self.syntax()),
                    },
                    "case" => self.case()?,
                    _ if self.sym("(") => {
                        self.i += 1;
                        if AGGREGATES.contains(&lw.as_str()) {
                            let distinct = self.eat_kw("distinct");
                            let arg = if self.eat_sym("*") {
                                None
                            } else {
                                Some(Box::new(self.expr()?))
                            };
                            // group_concat(x, sep): keep the separator as a second call arg.
                            let e = if lw == "group_concat" && self.eat_sym(",") {
                                let sep = self.expr()?;
                                Expr::Call(
                                    "group_concat_sep".into(),
                                    vec![Expr::Agg(lw, arg, distinct), sep],
                                )
                            } else {
                                Expr::Agg(lw, arg, distinct)
                            };
                            self.expect_sym(")")?;
                            if let Expr::Agg(name, None, _) = &e {
                                if name != "count" {
                                    bail!("wrong number of arguments to function {name}()");
                                }
                            }
                            e
                        } else {
                            let mut args = Vec::new();
                            if !self.sym(")") {
                                loop {
                                    args.push(self.expr()?);
                                    if !self.eat_sym(",") {
                                        break;
                                    }
                                }
                            }
                            self.expect_sym(")")?;
                            Expr::Call(lw, args)
                        }
                    }
                    _ => {
                        // table.column: the table prefix is accepted and dropped (no joins).
                        if self.sym(".") {
                            self.i += 1;
                            Expr::Col(self.ident()?)
                        } else {
                            Expr::Col(lw)
                        }
                    }
                }
            }
            _ => {
                self.i -= 1;
                return Err(self.syntax());
            }
        })
    }

    fn case(&mut self) -> Result<Expr> {
        let base = if self.kw("when") {
            None
        } else {
            Some(Box::new(self.expr()?))
        };
        let mut arms = Vec::new();
        while self.eat_kw("when") {
            let w = self.expr()?;
            self.expect_kw("then")?;
            arms.push((w, self.expr()?));
        }
        let otherwise = if self.eat_kw("else") {
            Some(Box::new(self.expr()?))
        } else {
            None
        };
        self.expect_kw("end")?;
        Ok(Expr::Case(base, arms, otherwise))
    }
}

fn is_clause_kw(w: &str) -> bool {
    [
        "from", "where", "group", "having", "order", "limit", "offset", "union", "as",
    ]
    .contains(&w.to_ascii_lowercase().as_str())
}

fn parse_interval(s: &str) -> Result<Duration> {
    let mut parts = s.split_whitespace();
    let n: f64 = parts
        .next()
        .and_then(|p| p.parse().ok())
        .ok_or_else(|| anyhow!("interval '{s}': expected '<number> <unit>'"))?;
    let unit = parts.next().unwrap_or("days").to_ascii_lowercase();
    let secs = match unit.trim_end_matches('s') {
        "second" | "sec" => 1.0,
        "minute" | "min" => 60.0,
        "hour" => 3600.0,
        "day" => 86400.0,
        "week" => 604800.0,
        "month" => 30.0 * 86400.0,
        "year" => 365.0 * 86400.0,
        _ => bail!("interval unit {unit} not supported"),
    };
    Ok(Duration::milliseconds((n * secs * 1000.0) as i64))
}

pub fn parse(sql: &str) -> Result<Stmt> {
    let l = lex(sql)?;
    if l.toks.iter().all(|t| *t == Tok::Sym(";")) {
        return Ok(Stmt::Nothing);
    }
    Parser {
        src: sql,
        t: l.toks,
        spans: l.spans,
        i: 0,
    }
    .stmt()
}

// ---------------------------------------------------------------- evaluation

pub type Row = BTreeMap<String, Val>;

/// Supplies table rows; `query` itself provides `sqlite_schema`.
pub trait Source {
    fn rows(&self, table: &str) -> Vec<Row>;
}

struct Ctx<'a> {
    row: &'a Row,
    group: Option<&'a [Row]>,
    now: DateTime<Utc>,
}

fn eval(e: &Expr, cx: &Ctx) -> Result<Val> {
    Ok(match e {
        Expr::Lit(v) => v.clone(),
        Expr::Col(c) => match cx.row.get(c) {
            Some(v) => v.clone(),
            None => bail!("no such column: {c}"),
        },
        Expr::Unary(op, a) => {
            let v = eval(a, cx)?;
            match *op {
                "not" => match v.truthy() {
                    None => Val::Null,
                    Some(b) => Val::bool(!b),
                },
                _ => match v {
                    Val::Null => Val::Null,
                    Val::Int(n) => Val::Int(-n),
                    other => Val::Real(-other.num().unwrap_or(0.0)),
                },
            }
        }
        Expr::Is(a, b, neg) => {
            let (x, y) = (eval(a, cx)?, eval(b, cx)?);
            let same = match (&x, &y) {
                (Val::Null, Val::Null) => true,
                (Val::Null, _) | (_, Val::Null) => false,
                _ => compare(&x, &y) == Ordering::Equal,
            };
            Val::bool(same != *neg)
        }
        Expr::In(a, list, neg) => {
            let x = eval(a, cx)?;
            if x == Val::Null {
                return Ok(Val::Null);
            }
            let mut saw_null = false;
            for item in list {
                let y = eval(item, cx)?;
                if y == Val::Null {
                    saw_null = true;
                } else if compare(&x, &y) == Ordering::Equal {
                    return Ok(Val::bool(!neg));
                }
            }
            if saw_null {
                Val::Null
            } else {
                Val::bool(*neg)
            }
        }
        Expr::Between(a, lo, hi, neg) => {
            let (x, l, h) = (eval(a, cx)?, eval(lo, cx)?, eval(hi, cx)?);
            if [&x, &l, &h].iter().any(|v| **v == Val::Null) {
                return Ok(Val::Null);
            }
            let inside = compare(&x, &l) != Ordering::Less && compare(&x, &h) != Ordering::Greater;
            Val::bool(inside != *neg)
        }
        Expr::Like(a, p, neg) => match (eval(a, cx)?.text(), eval(p, cx)?.text()) {
            (Some(t), Some(p)) => Val::bool(like(&t, &p) != *neg),
            _ => Val::Null,
        },
        Expr::Case(base, arms, otherwise) => {
            let b = base.as_ref().map(|b| eval(b, cx)).transpose()?;
            for (w, then) in arms {
                let hit = match &b {
                    Some(b) => {
                        let w = eval(w, cx)?;
                        *b != Val::Null && w != Val::Null && compare(b, &w) == Ordering::Equal
                    }
                    None => eval(w, cx)?.truthy() == Some(true),
                };
                if hit {
                    return eval(then, cx);
                }
            }
            match otherwise {
                Some(o) => eval(o, cx)?,
                None => Val::Null,
            }
        }
        Expr::Bin(a, op, b) => {
            let x = eval(a, cx)?;
            if *op == "and" {
                if x.truthy() == Some(false) {
                    return Ok(Val::Int(0));
                }
                let y = eval(b, cx)?;
                return Ok(match (x.truthy(), y.truthy()) {
                    (_, Some(false)) => Val::Int(0),
                    (Some(true), Some(true)) => Val::Int(1),
                    _ => Val::Null,
                });
            }
            if *op == "or" {
                if x.truthy() == Some(true) {
                    return Ok(Val::Int(1));
                }
                let y = eval(b, cx)?;
                return Ok(match (x.truthy(), y.truthy()) {
                    (_, Some(true)) => Val::Int(1),
                    (Some(false), Some(false)) => Val::Int(0),
                    _ => Val::Null,
                });
            }
            let y = eval(b, cx)?;
            binary(&x, op, &y)?
        }
        Expr::Call(name, args) => {
            let vals = args
                .iter()
                .map(|a| eval(a, cx))
                .collect::<Result<Vec<_>>>()?;
            call(name, vals, cx.now)?
        }
        Expr::Agg(name, arg, distinct) => {
            let Some(group) = cx.group else {
                bail!("misuse of aggregate: {name}()");
            };
            aggregate(name, arg.as_deref(), *distinct, group, cx.now)?
        }
    })
}

fn binary(x: &Val, op: &str, y: &Val) -> Result<Val> {
    if matches!(x, Val::Null) || matches!(y, Val::Null) {
        return Ok(Val::Null);
    }
    Ok(match op {
        "=" => Val::bool(compare(x, y) == Ordering::Equal),
        "!=" => Val::bool(compare(x, y) != Ordering::Equal),
        "<" => Val::bool(compare(x, y) == Ordering::Less),
        "<=" => Val::bool(compare(x, y) != Ordering::Greater),
        ">" => Val::bool(compare(x, y) == Ordering::Greater),
        ">=" => Val::bool(compare(x, y) != Ordering::Less),
        "||" => Val::Text(format!(
            "{}{}",
            x.text().unwrap_or_default(),
            y.text().unwrap_or_default()
        )),
        "+" | "-" => match (x, y) {
            (Val::Time(t), Val::Interval(d)) => {
                Val::Time(if op == "+" { *t + *d } else { *t - *d })
            }
            (Val::Text(s), Val::Interval(d)) if parse_time(s).is_some() => {
                let t = parse_time(s).unwrap();
                Val::Time(if op == "+" { t + *d } else { t - *d })
            }
            (Val::Int(a), Val::Int(b)) => Val::Int(if op == "+" {
                a.wrapping_add(*b)
            } else {
                a.wrapping_sub(*b)
            }),
            _ => {
                let (a, b) = (x.num().unwrap_or(0.0), y.num().unwrap_or(0.0));
                Val::Real(if op == "+" { a + b } else { a - b })
            }
        },
        "*" => match (x, y) {
            (Val::Int(a), Val::Int(b)) => Val::Int(a.wrapping_mul(*b)),
            _ => Val::Real(x.num().unwrap_or(0.0) * y.num().unwrap_or(0.0)),
        },
        "/" | "%" => match (x, y) {
            (_, Val::Int(0)) => Val::Null,
            (Val::Int(a), Val::Int(b)) => Val::Int(if op == "/" { a / b } else { a % b }),
            _ => {
                let b = y.num().unwrap_or(0.0);
                if b == 0.0 {
                    Val::Null
                } else if op == "/" {
                    Val::Real(x.num().unwrap_or(0.0) / b)
                } else {
                    Val::Real(x.num().unwrap_or(0.0) % b)
                }
            }
        },
        _ => bail!("unsupported operator {op}"),
    })
}

/// SQLite LIKE: `%` and `_`, ASCII case-insensitive.
fn like(text: &str, pat: &str) -> bool {
    fn go(t: &[char], p: &[char]) -> bool {
        match p.first() {
            None => t.is_empty(),
            Some('%') => (0..=t.len()).any(|i| go(&t[i..], &p[1..])),
            Some('_') => !t.is_empty() && go(&t[1..], &p[1..]),
            Some(c) => t.first().is_some_and(|x| x.eq_ignore_ascii_case(c)) && go(&t[1..], &p[1..]),
        }
    }
    let t: Vec<char> = text.chars().collect();
    let p: Vec<char> = pat.chars().collect();
    go(&t, &p)
}

/// SQLite date modifiers: `'now'`, `'+7 days'`, `'-3 hours'`, `'start of day'`.
fn date_arg(args: &[Val], now: DateTime<Utc>) -> Result<Option<DateTime<Utc>>> {
    let base = match args.first() {
        None => now,
        Some(Val::Null) => return Ok(None),
        Some(Val::Time(t)) => *t,
        Some(v) => {
            let s = v.text().unwrap_or_default();
            if s.eq_ignore_ascii_case("now") {
                now
            } else if let Some(t) = parse_time(&s) {
                t
            } else if let Some(n) = v.num() {
                DateTime::from_timestamp(n as i64, 0).unwrap_or(now)
            } else {
                return Ok(None);
            }
        }
    };
    let mut t = base;
    for m in args.iter().skip(1) {
        let m = m.text().unwrap_or_default().to_ascii_lowercase();
        let m = m.trim();
        match m {
            "start of day" => t = t.date_naive().and_hms_opt(0, 0, 0).unwrap().and_utc(),
            "utc" | "localtime" => {}
            _ => {
                let (sign, rest) = match m.as_bytes().first() {
                    Some(b'-') => (-1.0, &m[1..]),
                    Some(b'+') => (1.0, &m[1..]),
                    _ => (1.0, m),
                };
                t += parse_interval(rest.trim())? * if sign < 0.0 { -1 } else { 1 };
            }
        }
    }
    Ok(Some(t))
}

fn json_path<'v>(v: &'v serde_json::Value, path: &str) -> Option<&'v serde_json::Value> {
    let mut cur = v;
    let rest = path.strip_prefix('$')?;
    for part in rest.split('.').filter(|p| !p.is_empty()) {
        let (key, idx) = match part.find('[') {
            Some(i) => (&part[..i], Some(&part[i..])),
            None => (part, None),
        };
        if !key.is_empty() {
            cur = cur.get(key)?;
        }
        if let Some(idx) = idx {
            for n in idx.split(['[', ']']).filter(|s| !s.is_empty()) {
                cur = cur.get(n.parse::<usize>().ok()?)?;
            }
        }
    }
    Some(cur)
}

fn from_json(v: &serde_json::Value) -> Val {
    match v {
        serde_json::Value::Null => Val::Null,
        serde_json::Value::Bool(b) => Val::Int(*b as i64),
        serde_json::Value::Number(n) => n
            .as_i64()
            .map(Val::Int)
            .unwrap_or(Val::Real(n.as_f64().unwrap_or(f64::NAN))),
        serde_json::Value::String(s) => Val::Text(s.clone()),
        other => Val::Text(other.to_string()),
    }
}

fn call(name: &str, a: Vec<Val>, now: DateTime<Utc>) -> Result<Val> {
    let arg = |i: usize| a.get(i).cloned().unwrap_or(Val::Null);
    let need = |n: usize| -> Result<()> {
        if a.len() < n {
            bail!("wrong number of arguments to function {name}()");
        }
        Ok(())
    };
    Ok(match name {
        "lower" | "upper" => {
            need(1)?;
            match arg(0).text() {
                None => Val::Null,
                Some(s) => Val::Text(if name == "lower" {
                    s.to_ascii_lowercase()
                } else {
                    s.to_ascii_uppercase()
                }),
            }
        }
        "length" => {
            need(1)?;
            match arg(0) {
                Val::Null => Val::Null,
                Val::Blob(b) => Val::Int(b.len() as i64),
                v => Val::Int(v.text().unwrap_or_default().chars().count() as i64),
            }
        }
        "hex" => {
            need(1)?;
            match arg(0) {
                Val::Blob(b) => Val::Text(catalog::hex(&b).to_uppercase()),
                Val::Null => Val::Text(String::new()),
                v => {
                    Val::Text(catalog::hex(v.text().unwrap_or_default().as_bytes()).to_uppercase())
                }
            }
        }
        "typeof" => {
            need(1)?;
            Val::Text(arg(0).type_name().into())
        }
        "abs" => match arg(0) {
            Val::Int(n) => Val::Int(n.abs()),
            Val::Null => Val::Null,
            v => Val::Real(v.num().unwrap_or(0.0).abs()),
        },
        "round" => {
            let p = arg(1).num().unwrap_or(0.0) as i32;
            match arg(0).num() {
                None => Val::Null,
                Some(x) => Val::Real((x * 10f64.powi(p)).round() / 10f64.powi(p)),
            }
        }
        "coalesce" | "ifnull" => a.into_iter().find(|v| *v != Val::Null).unwrap_or(Val::Null),
        "nullif" => {
            if compare(&arg(0), &arg(1)) == Ordering::Equal {
                Val::Null
            } else {
                arg(0)
            }
        }
        "trim" | "ltrim" | "rtrim" => match arg(0).text() {
            None => Val::Null,
            Some(s) => Val::Text(match name {
                "ltrim" => s.trim_start().into(),
                "rtrim" => s.trim_end().into(),
                _ => s.trim().into(),
            }),
        },
        "substr" | "substring" => {
            need(2)?;
            match arg(0).text() {
                None => Val::Null,
                Some(s) => {
                    let chars: Vec<char> = s.chars().collect();
                    let start = arg(1).num().unwrap_or(1.0) as i64;
                    let start = if start > 0 {
                        start - 1
                    } else {
                        (chars.len() as i64 + start).max(0)
                    } as usize;
                    let len = arg(2).num().map(|l| l as usize).unwrap_or(chars.len());
                    Val::Text(chars.iter().skip(start).take(len).collect())
                }
            }
        }
        "instr" => match (arg(0).text(), arg(1).text()) {
            (Some(h), Some(n)) => Val::Int(
                h.find(&n)
                    .map(|i| h[..i].chars().count() as i64 + 1)
                    .unwrap_or(0),
            ),
            _ => Val::Null,
        },
        "replace" => match (arg(0).text(), arg(1).text(), arg(2).text()) {
            (Some(s), Some(f), Some(t)) if !f.is_empty() => Val::Text(s.replace(&f, &t)),
            (Some(s), _, _) => Val::Text(s),
            _ => Val::Null,
        },
        "datetime" => match date_arg(&a, now)? {
            Some(t) => Val::Text(sqlite_time(&t)),
            None => Val::Null,
        },
        "date" => match date_arg(&a, now)? {
            Some(t) => Val::Text(t.format("%Y-%m-%d").to_string()),
            None => Val::Null,
        },
        "time" => match date_arg(&a, now)? {
            Some(t) => Val::Text(t.format("%H:%M:%S").to_string()),
            None => Val::Null,
        },
        "unixepoch" => match date_arg(&a, now)? {
            Some(t) => Val::Int(t.timestamp()),
            None => Val::Null,
        },
        "strftime" => {
            need(2)?;
            match date_arg(&a[1..], now)? {
                Some(t) => {
                    let f = arg(0).text().unwrap_or_default().replace("%f", "%S%.3f");
                    Val::Text(t.format(&f).to_string())
                }
                None => Val::Null,
            }
        }
        // PostgreSQL-style now(); stays a timestamp so `now() - interval '7 days'` works.
        "now" => Val::Time(now),
        "json_extract" => {
            need(2)?;
            match (arg(0).text(), arg(1).text()) {
                (Some(doc), Some(path)) => match serde_json::from_str::<serde_json::Value>(&doc) {
                    Ok(v) => json_path(&v, &path).map(from_json).unwrap_or(Val::Null),
                    Err(_) => bail!("malformed JSON"),
                },
                _ => Val::Null,
            }
        }
        "json_valid" => Val::bool(
            arg(0)
                .text()
                .is_some_and(|s| serde_json::from_str::<serde_json::Value>(&s).is_ok()),
        ),
        // Extensions for the key/value table.
        "sliqtly_family" => match arg(0) {
            Val::Blob(b) => catalog::family(&b)
                .map(|f| Val::Text(f.name.into()))
                .unwrap_or(Val::Null),
            _ => Val::Null,
        },
        "sliqtly_key" => match arg(0) {
            Val::Blob(b) => Val::Text(catalog::describe_key(&b)),
            _ => Val::Null,
        },
        "group_concat_sep" => arg(0), // separator already applied in aggregate()
        _ => bail!("no such function: {name}"),
    })
}

fn aggregate(
    name: &str,
    arg: Option<&Expr>,
    distinct: bool,
    group: &[Row],
    now: DateTime<Utc>,
) -> Result<Val> {
    let mut vals = Vec::new();
    for row in group {
        let cx = Ctx {
            row,
            group: None,
            now,
        };
        match arg {
            None => vals.push(Val::Int(1)),
            Some(e) => {
                let v = eval(e, &cx)?;
                if v != Val::Null {
                    vals.push(v);
                }
            }
        }
    }
    if distinct {
        let mut seen: Vec<Val> = Vec::new();
        vals.retain(|v| {
            let dup = seen.iter().any(|s| compare(s, v) == Ordering::Equal);
            if !dup {
                seen.push(v.clone());
            }
            !dup
        });
    }
    Ok(match name {
        "count" => Val::Int(vals.len() as i64),
        "min" => vals.into_iter().min_by(compare).unwrap_or(Val::Null),
        "max" => vals.into_iter().max_by(compare).unwrap_or(Val::Null),
        "sum" | "total" | "avg" => {
            if vals.is_empty() {
                return Ok(match name {
                    "total" => Val::Real(0.0),
                    _ => Val::Null,
                });
            }
            let all_int = vals.iter().all(|v| matches!(v, Val::Int(_)));
            let sum: f64 = vals.iter().map(|v| v.num().unwrap_or(0.0)).sum();
            match name {
                "sum" if all_int => Val::Int(
                    vals.iter()
                        .map(|v| if let Val::Int(n) = v { *n } else { 0 })
                        .sum(),
                ),
                "avg" => Val::Real(sum / vals.len() as f64),
                _ => Val::Real(sum),
            }
        }
        "group_concat" => {
            if vals.is_empty() {
                Val::Null
            } else {
                Val::Text(
                    vals.iter()
                        .filter_map(Val::text)
                        .collect::<Vec<_>>()
                        .join(","),
                )
            }
        }
        _ => bail!("no such function: {name}"),
    })
}

fn has_agg(e: &Expr) -> bool {
    match e {
        Expr::Agg(..) => true,
        Expr::Lit(_) | Expr::Col(_) => false,
        Expr::Unary(_, a) => has_agg(a),
        Expr::Bin(a, _, b) | Expr::Is(a, b, _) | Expr::Like(a, b, _) => has_agg(a) || has_agg(b),
        Expr::In(a, l, _) => has_agg(a) || l.iter().any(has_agg),
        Expr::Between(a, b, c, _) => has_agg(a) || has_agg(b) || has_agg(c),
        Expr::Case(b, arms, o) => {
            b.as_deref().is_some_and(has_agg)
                || arms.iter().any(|(w, t)| has_agg(w) || has_agg(t))
                || o.as_deref().is_some_and(has_agg)
        }
        Expr::Call(_, args) => args.iter().any(has_agg),
    }
}

/// `group_concat(x, sep)` is parsed as `group_concat_sep(Agg, sep)`; rewrite
/// the separator in after aggregation.
fn fix_group_concat(e: &Expr, v: Val, cx: &Ctx) -> Result<Val> {
    if let Expr::Call(n, args) = e {
        if n == "group_concat_sep" {
            if let (Expr::Agg(_, Some(arg), distinct), Some(group)) = (&args[0], cx.group) {
                let sep = eval(
                    &args[1],
                    &Ctx {
                        row: cx.row,
                        group: None,
                        now: cx.now,
                    },
                )?
                .text()
                .unwrap_or_default();
                let mut parts = Vec::new();
                for row in group {
                    if let Some(t) = eval(
                        arg,
                        &Ctx {
                            row,
                            group: None,
                            now: cx.now,
                        },
                    )?
                    .text()
                    {
                        if !(*distinct && parts.contains(&t)) {
                            parts.push(t);
                        }
                    }
                }
                return Ok(if parts.is_empty() {
                    Val::Null
                } else {
                    Val::Text(parts.join(&sep))
                });
            }
        }
    }
    Ok(v)
}

pub struct ResultSet {
    pub columns: Vec<String>,
    pub rows: Vec<Vec<Val>>,
}

pub fn schema_rows() -> Vec<Row> {
    let mut out = Vec::new();
    let mk = |ty: &str, name: &str, tbl: &str, sql: String| -> Row {
        [
            ("type", Val::Text(ty.into())),
            ("name", Val::Text(name.into())),
            ("tbl_name", Val::Text(tbl.into())),
            ("rootpage", Val::Int(0)),
            ("sql", Val::Text(sql)),
        ]
        .into_iter()
        .map(|(k, v)| (k.to_string(), v))
        .collect()
    };
    for t in schema::TABLES {
        out.push(mk("table", t.name, t.name, t.create_sql()));
    }
    for i in schema::INDEXES {
        out.push(mk("index", i.name, i.table, i.create_sql()));
    }
    out
}

const SCHEMA_COLUMNS: &[&str] = &["type", "name", "tbl_name", "rootpage", "sql"];

pub fn execute(src: &dyn Source, q: &Select, now: DateTime<Utc>) -> Result<ResultSet> {
    let (columns_of_table, rows): (Vec<String>, Vec<Row>) = match q.from.as_deref() {
        None => (Vec::new(), vec![Row::new()]),
        Some("sqlite_schema" | "sqlite_master" | "sqlite_temp_schema" | "sqlite_temp_master") => (
            SCHEMA_COLUMNS.iter().map(|s| s.to_string()).collect(),
            if q.from.as_deref().is_some_and(|f| f.contains("temp")) {
                Vec::new()
            } else {
                schema_rows()
            },
        ),
        Some(name) => {
            let t = schema::table(name).ok_or_else(|| anyhow!("no such table: {name}"))?;
            (
                t.column_names().iter().map(|s| s.to_string()).collect(),
                src.rows(t.name),
            )
        }
    };

    // Validate column names up front, so an empty table still reports typos.
    let mut probe: Row = columns_of_table
        .iter()
        .map(|c| (c.clone(), Val::Null))
        .collect();
    if let Some(t) = q.from.as_deref().and_then(schema::table) {
        probe.extend(short_names(t).map(|c| (c, Val::Null)));
    }
    let aliases: Vec<String> = q.items.iter().map(|i| i.name.to_ascii_lowercase()).collect();
    let mut cols = Vec::new();
    for e in q
        .items
        .iter()
        .filter_map(|i| i.expr.as_ref())
        .chain(q.filter.iter())
    {
        collect_cols(e, &mut cols);
    }
    for e in q.group_by.iter().chain(q.order.iter().map(|(e, _)| e)) {
        let mut c = Vec::new();
        collect_cols(e, &mut c);
        cols.extend(c.into_iter().filter(|c| !aliases.contains(c)));
    }
    if let Some(h) = &q.having {
        let mut c = Vec::new();
        collect_cols(h, &mut c);
        cols.extend(c.into_iter().filter(|c| !aliases.contains(c)));
    }
    for c in cols {
        if !probe.contains_key(&c) {
            bail!("no such column: {c}");
        }
    }
    if q.items.iter().any(|i| i.expr.is_none()) && q.from.is_none() {
        bail!("no tables specified");
    }

    let mut kept = Vec::new();
    for row in rows {
        let keep = match &q.filter {
            None => true,
            Some(f) => {
                if has_agg(f) {
                    bail!("misuse of aggregate function in WHERE");
                }
                eval(
                    f,
                    &Ctx {
                        row: &row,
                        group: None,
                        now,
                    },
                )?
                .truthy()
                    == Some(true)
            }
        };
        if keep {
            kept.push(row);
        }
    }

    // GROUP BY 2 means the second result column, and GROUP BY alias the
    // aliased expression, as in SQLite.
    let group_by: Vec<Expr> = q
        .group_by
        .iter()
        .map(|e| match e {
            Expr::Col(c) if !probe.contains_key(c) => q
                .items
                .iter()
                .find(|i| i.name.eq_ignore_ascii_case(c))
                .and_then(|i| i.expr.clone())
                .ok_or_else(|| anyhow!("no such column: {c}")),
            Expr::Lit(Val::Int(n)) if *n >= 1 => q
                .items
                .get(*n as usize - 1)
                .and_then(|i| i.expr.clone())
                .ok_or_else(|| {
                    anyhow!(
                        "GROUP BY term out of range - should be between 1 and {}",
                        q.items.len()
                    )
                }),
            other => Ok(other.clone()),
        })
        .collect::<Result<_>>()?;

    let aggregated = !group_by.is_empty()
        || q.items.iter().filter_map(|i| i.expr.as_ref()).any(has_agg)
        || q.having.as_ref().is_some_and(has_agg);

    // Each output row keeps the source row (or group) it came from, for ORDER BY.
    let empty = Row::new();
    let groups: Vec<Vec<Row>> = if aggregated {
        if group_by.is_empty() {
            vec![kept]
        } else {
            let mut map: Vec<(Vec<Val>, Vec<Row>)> = Vec::new();
            for row in kept {
                let key = group_by
                    .iter()
                    .map(|e| {
                        eval(
                            e,
                            &Ctx {
                                row: &row,
                                group: None,
                                now,
                            },
                        )
                    })
                    .collect::<Result<Vec<_>>>()?;
                match map.iter_mut().find(|(k, _)| {
                    k.iter()
                        .zip(&key)
                        .all(|(a, b)| compare(a, b) == Ordering::Equal)
                }) {
                    Some((_, g)) => g.push(row),
                    None => map.push((key, vec![row])),
                }
            }
            map.sort_by(|a, b| {
                a.0.iter()
                    .zip(&b.0)
                    .map(|(x, y)| compare(x, y))
                    .find(|o| *o != Ordering::Equal)
                    .unwrap_or(Ordering::Equal)
            });
            map.into_iter().map(|(_, g)| g).collect()
        }
    } else {
        kept.into_iter().map(|r| vec![r]).collect()
    };

    let mut columns = Vec::new();
    for it in &q.items {
        match &it.expr {
            None => columns.extend(columns_of_table.iter().cloned()),
            Some(_) => columns.push(it.name.clone()),
        }
    }

    let mut out: Vec<(Vec<Val>, Vec<Val>)> = Vec::new(); // (values, sort keys)
    for g in &groups {
        let rep = g.first().unwrap_or(&empty);
        let cx = Ctx {
            row: rep,
            group: if aggregated { Some(g) } else { None },
            now,
        };
        if let Some(h) = &q.having {
            let mut hrow = rep.clone();
            for it in &q.items {
                if let Some(e) = &it.expr {
                    let v = fix_group_concat(e, eval(e, &cx)?, &cx)?;
                    hrow.insert(it.name.to_ascii_lowercase(), v);
                }
            }
            let hcx = Ctx {
                row: &hrow,
                group: cx.group,
                now,
            };
            if eval(h, &hcx)?.truthy() != Some(true) {
                continue;
            }
        }
        let mut vals = Vec::new();
        let mut named = rep.clone();
        for it in &q.items {
            match &it.expr {
                None => vals.extend(
                    columns_of_table
                        .iter()
                        .map(|c| rep.get(c).cloned().unwrap_or(Val::Null)),
                ),
                Some(e) => {
                    let v = fix_group_concat(e, eval(e, &cx)?, &cx)?;
                    named.insert(it.name.to_ascii_lowercase(), v.clone());
                    vals.push(v);
                }
            }
        }
        let mut keys = Vec::new();
        for (e, _) in &q.order {
            keys.push(match e {
                Expr::Lit(Val::Int(n)) if *n >= 1 && (*n as usize) <= vals.len() => {
                    vals[*n as usize - 1].clone()
                }
                Expr::Lit(Val::Int(n)) => bail!(
                    "ORDER BY term out of range - should be between 1 and {} (got {n})",
                    vals.len()
                ),
                _ => fix_group_concat(
                    e,
                    eval(
                        e,
                        &Ctx {
                            row: &named,
                            group: cx.group,
                            now,
                        },
                    )?,
                    &cx,
                )?,
            });
        }
        out.push((vals, keys));
    }
    if q.distinct {
        let mut seen: Vec<Vec<Val>> = Vec::new();
        out.retain(|(v, _)| {
            let dup = seen.iter().any(|s| {
                s.iter()
                    .zip(v)
                    .all(|(a, b)| compare(a, b) == Ordering::Equal)
            });
            if !dup {
                seen.push(v.clone());
            }
            !dup
        });
    }
    if !q.order.is_empty() {
        out.sort_by(|a, b| {
            for (i, (_, desc)) in q.order.iter().enumerate() {
                let o = compare(&a.1[i], &b.1[i]);
                let o = if *desc { o.reverse() } else { o };
                if o != Ordering::Equal {
                    return o;
                }
            }
            Ordering::Equal
        });
    }
    let num = |e: &Option<Expr>| -> Result<Option<i64>> {
        match e {
            None => Ok(None),
            Some(e) => Ok(Some(
                eval(
                    e,
                    &Ctx {
                        row: &empty,
                        group: None,
                        now,
                    },
                )?
                .num()
                .ok_or_else(|| anyhow!("datatype mismatch"))? as i64,
            )),
        }
    };
    let offset = num(&q.offset)?.unwrap_or(0).max(0) as usize;
    let limit = num(&q.limit)?;
    let rows: Vec<Vec<Val>> = out
        .into_iter()
        .skip(offset)
        .take(match limit {
            Some(n) if n >= 0 => n as usize,
            _ => usize::MAX,
        })
        .map(|(v, _)| v)
        .collect();
    Ok(ResultSet { columns, rows })
}

fn collect_cols(e: &Expr, out: &mut Vec<String>) {
    match e {
        Expr::Col(c) => out.push(c.clone()),
        Expr::Lit(_) => {}
        Expr::Unary(_, a) => collect_cols(a, out),
        Expr::Bin(a, _, b) | Expr::Is(a, b, _) | Expr::Like(a, b, _) => {
            collect_cols(a, out);
            collect_cols(b, out);
        }
        Expr::In(a, l, _) => {
            collect_cols(a, out);
            l.iter().for_each(|x| collect_cols(x, out));
        }
        Expr::Between(a, b, c, _) => {
            collect_cols(a, out);
            collect_cols(b, out);
            collect_cols(c, out);
        }
        Expr::Case(b, arms, o) => {
            if let Some(b) = b {
                collect_cols(b, out);
            }
            for (w, t) in arms {
                collect_cols(w, out);
                collect_cols(t, out);
            }
            if let Some(o) = o {
                collect_cols(o, out);
            }
        }
        Expr::Call(_, args) => args.iter().for_each(|x| collect_cols(x, out)),
        Expr::Agg(_, a, _) => {
            if let Some(a) = a {
                collect_cols(a, out);
            }
        }
    }
}

/// Typed rows for the relational view of a database.
/// `created_at`/`updated_at`/`joined_at` are also readable as `created`,
/// `updated`, `joined`; `SELECT *` and `.dump` show only the real columns.
pub fn row_from_json(table: &schema::Table, v: &serde_json::Value) -> Row {
    let mut row: Row = table
        .columns
        .iter()
        .map(|c| {
            let val = match v.get(c.name) {
                None | Some(serde_json::Value::Null) => Val::Null,
                Some(serde_json::Value::String(s)) => Val::Text(s.clone()),
                Some(serde_json::Value::Number(n)) => n
                    .as_i64()
                    .map(Val::Int)
                    .unwrap_or(Val::Real(n.as_f64().unwrap_or(0.0))),
                Some(serde_json::Value::Bool(b)) => Val::Int(*b as i64),
                Some(other) => Val::Text(other.to_string()),
            };
            (c.name.to_string(), val)
        })
        .collect();
    for c in table.columns {
        if let Some(short) = c.name.strip_suffix("_at") {
            let v = row[c.name].clone();
            row.insert(short.to_string(), v);
        }
    }
    row
}

pub fn short_names(table: &schema::Table) -> impl Iterator<Item = String> + '_ {
    table
        .columns
        .iter()
        .filter_map(|c| c.name.strip_suffix("_at").map(String::from))
}

pub fn kv_row(k: &[u8], v: &[u8]) -> Row {
    [
        ("key".to_string(), Val::Blob(k.to_vec())),
        ("value".to_string(), Val::Blob(v.to_vec())),
    ]
    .into_iter()
    .collect()
}
