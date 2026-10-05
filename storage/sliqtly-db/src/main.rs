//! `sliqtly-db`: operator tool for SliqtlyDB databases.
//!
//! Every command except `restore` opens the database read-only and works on
//! one consistent snapshot, so it is safe against a live server.

mod backend;
mod backup;
mod catalog;
mod model;
mod query;
mod report;
mod verify;

use anyhow::{Context, Result};
use clap::{Parser, Subcommand};
use std::io::{BufRead, IsTerminal, Write};
use std::path::PathBuf;
use std::process::ExitCode;
use verify::Status;

#[derive(Parser)]
#[command(
    name = "sliqtly-db",
    version,
    about = "Inspect, verify, query, back up and restore a SliqtlyDB database"
)]
struct Cli {
    /// Database directory.
    #[arg(short, long, env = "SLIQTLY_DB", global = true)]
    db: Option<PathBuf>,

    /// Machine-readable output.
    #[arg(long, global = true)]
    json: bool,

    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
    /// Format, versions, CommitSeq, record counts, index health, storage use, last backup.
    Info,
    /// Per key family: keys, key bytes, value bytes, largest value.
    Stats,
    /// Integrity checks. Exit status 1 on any failure.
    Verify {
        /// Also check references, index completeness, edge symmetry and blob hashes.
        #[arg(long)]
        deep: bool,
        /// Treat warnings as failures.
        #[arg(long)]
        strict: bool,
    },
    /// Read-only SQL over rooms, documents and memberships. Without SQL, reads statements from stdin.
    Query {
        /// e.g. "SELECT id, title FROM rooms ORDER BY updated DESC LIMIT 10"
        sql: Option<String>,
    },
    /// Write a consistent, checksummed snapshot to an empty directory.
    Backup {
        /// Destination directory (created; must be empty if it exists).
        dest: PathBuf,
    },
    /// Validate a backup and restore it into --db, then verify the result.
    Restore {
        /// Backup directory written by `sliqtly-db backup`.
        src: PathBuf,
        /// Replace an existing database at --db.
        #[arg(long)]
        force: bool,
        /// Only validate the backup; write nothing.
        #[arg(long)]
        dry_run: bool,
    },
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    match run(cli) {
        Ok(code) => code,
        Err(e) => {
            eprintln!("error: {e:#}");
            ExitCode::from(2)
        }
    }
}

fn db_path(cli: &Cli) -> Result<PathBuf> {
    cli.db
        .clone()
        .context("no database given: pass --db <dir> or set SLIQTLY_DB")
}

fn print_json(v: &impl serde::Serialize) -> Result<()> {
    println!("{}", serde_json::to_string_pretty(v)?);
    Ok(())
}

fn run(cli: Cli) -> Result<ExitCode> {
    match &cli.cmd {
        Cmd::Info => {
            let db = backend::open(&db_path(&cli)?)?;
            if cli.json {
                print_json(&report::info_json(&db))?;
            } else {
                print!("{}", report::info_text(&db));
            }
        }
        Cmd::Stats => {
            let db = backend::open(&db_path(&cli)?)?;
            if cli.json {
                print_json(&report::stats_json(&db))?;
            } else {
                print!("{}", report::stats_text(&db));
            }
        }
        Cmd::Verify { deep, strict } => {
            let db = backend::open(&db_path(&cli)?)?;
            let r = verify::run(&db, *deep);
            if cli.json {
                print_json(&r)?;
            } else {
                print_verify(&r);
            }
            return Ok(verify_exit(r.worst(), *strict));
        }
        Cmd::Query { sql } => {
            let db = backend::open(&db_path(&cli)?)?;
            let model = model::Model::build(&db);
            match sql {
                Some(sql) => run_query(&model, sql, cli.json)?,
                None => shell(&model, cli.json)?,
            }
        }
        Cmd::Backup { dest } => {
            let db = backend::open(&db_path(&cli)?)?;
            let out = backup::backup(&db, dest)?;
            if cli.json {
                print_json(&out.manifest)?;
            } else {
                let m = &out.manifest;
                println!("Backup written to {}", dest.display());
                println!("  CommitSeq   {}", report::group(m.commit_seq));
                println!("  records     {}", report::group(m.records));
                println!("  blobs       {}", report::group(m.blobs.len() as u64));
                for f in &m.files {
                    println!(
                        "  {}  {}  sha256 {}",
                        f.name,
                        report::bytes(f.size),
                        f.sha256
                    );
                }
                println!("  read back and checksums verified");
            }
            if let Some(e) = out.bookkeeping_error {
                eprintln!("warning: backup is good, but recording it for `info` failed: {e}");
            }
        }
        Cmd::Restore {
            src,
            force,
            dry_run,
        } => {
            let target = db_path(&cli)?;
            let out = backup::restore(src, &target, *force, *dry_run)?;
            let m = &out.manifest;
            if *dry_run {
                println!(
                    "Backup {} is valid: CommitSeq {}, {} records, checksums match. Nothing written.",
                    src.display(),
                    report::group(m.commit_seq),
                    report::group(m.records)
                );
                return Ok(ExitCode::SUCCESS);
            }
            println!(
                "Restored CommitSeq {} ({} records) into {}{}",
                report::group(m.commit_seq),
                report::group(m.records),
                target.display(),
                if out.replaced_existing {
                    ", replacing the previous database"
                } else {
                    ""
                }
            );
            let db = backend::open(&target)?;
            let r = verify::run(&db, true);
            println!("\nverify --deep on the restored database:");
            print_verify(&r);
            return Ok(verify_exit(r.worst(), false));
        }
    }
    Ok(ExitCode::SUCCESS)
}

fn verify_exit(worst: Status, strict: bool) -> ExitCode {
    match worst {
        Status::Fail => ExitCode::from(1),
        Status::Warn if strict => ExitCode::from(1),
        _ => ExitCode::SUCCESS,
    }
}

fn print_verify(r: &verify::Report) {
    println!(
        "{} at CommitSeq {}{}",
        r.path,
        report::group(r.commit_seq),
        if r.deep { " (deep)" } else { "" }
    );
    for c in &r.checks {
        let mark = match c.status {
            Status::Ok => "ok  ",
            Status::Skip => "skip",
            Status::Warn => "WARN",
            Status::Fail => "FAIL",
        };
        println!("  [{mark}] {:<32} {}", c.name, c.summary);
        for e in &c.examples {
            println!("         - {e}");
        }
        if c.problems > c.examples.len() {
            println!("         … {} more", c.problems - c.examples.len());
        }
    }
    let verdict = match r.worst() {
        Status::Fail => "FAILED",
        Status::Warn => "passed with warnings",
        _ => "passed",
    };
    println!("verify {verdict}");
}

fn run_query(model: &model::Model, sql: &str, json: bool) -> Result<()> {
    let q = query::parse(sql)?;
    let rs = query::execute(model, &q, chrono::Utc::now())?;
    if json {
        print_json(&query::render_json(&rs))?;
    } else {
        print!("{}", query::render_table(&rs));
    }
    Ok(())
}

/// Statements end with `;`. Errors are reported and the shell continues.
fn shell(model: &model::Model, json: bool) -> Result<()> {
    let stdin = std::io::stdin();
    let interactive = stdin.is_terminal();
    if interactive {
        eprintln!(
            "sliqtly-db query (read-only). Tables: {}. End statements with ';'. Ctrl-D to exit.",
            query::TABLES
                .iter()
                .map(|t| t.name)
                .collect::<Vec<_>>()
                .join(", ")
        );
    }
    let mut buf = String::new();
    let prompt = |cont: bool| {
        if interactive {
            eprint!("{}", if cont { "   ...> " } else { "sliqtly> " });
            let _ = std::io::stderr().flush();
        }
    };
    prompt(false);
    for line in stdin.lock().lines() {
        let line = line?;
        buf.push_str(&line);
        buf.push('\n');
        if line.trim_end().ends_with(';') {
            if let Err(e) = run_query(model, &buf, json) {
                eprintln!("error: {e:#}");
            }
            buf.clear();
        }
        prompt(!buf.trim().is_empty());
    }
    if !buf.trim().is_empty() {
        run_query(model, &buf, json)?;
    }
    Ok(())
}
