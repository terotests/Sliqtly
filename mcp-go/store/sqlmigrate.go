// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A SQLite file's schema is a numbered list of migrations, applied in
// order when the file is opened:
//
//	PRAGMA user_version    the number of the last one applied
//	schema_history         one row per migration: version, note, when, by
//
// Each runs in its own transaction with the row that records it, so a
// crash leaves the file before or after a migration, never inside one. A
// file at a version this server does not know (written by a newer one) is
// refused, not read wrong. Before an existing file is migrated it is copied
// with VACUUM INTO to <dir>/backups/<time>-<name>-v<n>.db, a consistent
// copy of the file as it was, unless every migration to run is Additive.
//
// A migration is only ever appended: one that has been released is never
// changed, since files out there are already past it.

// SQLMigration takes a schema from Version-1 to Version.
type SQLMigration struct {
	Version int
	Note    string
	Up      func(ctx context.Context, tx *sql.Tx) error
	// Additive: it only adds columns, tables or indexes, and the rows stay
	// as they were. Its one transaction either happens or does not, so no
	// copy of the file is made first: a copy of blobs.db can be gigabytes.
	Additive bool
}

// SQLExec is a migration made of SQL statements.
func SQLExec(stmts string) func(ctx context.Context, tx *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, stmts)
		return err
	}
}

// MigrateReport says what Migrate did.
type MigrateReport struct {
	From, To int
	Backup   string // "" when nothing was migrated or the file was new
}

// Migrate brings db (the file at path) to the last of ms. server names the
// build doing it, for schema_history.
func Migrate(ctx context.Context, db *sql.DB, path string, ms []SQLMigration, server string) (MigrateReport, error) {
	for i, m := range ms {
		if m.Version != i+1 {
			return MigrateReport{}, fmt.Errorf("store: migration %d is numbered %d", i+1, m.Version)
		}
	}
	var have int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&have); err != nil {
		return MigrateReport{}, err
	}
	want := len(ms)
	rep := MigrateReport{From: have, To: have}
	if have > want {
		return rep, fmt.Errorf("%s is at schema version %d, written by a newer server; this one reads %d. Install that version again", path, have, want)
	}
	if have == want {
		return rep, nil
	}
	additive := true
	for _, m := range ms[have:] {
		additive = additive && m.Additive
	}
	if have > 0 && !additive {
		b, err := BackupSQLite(ctx, db, path, fmt.Sprintf("v%d", have))
		if err != nil {
			return rep, fmt.Errorf("backup before migrating %s: %w", path, err)
		}
		rep.Backup = b
	}
	for _, m := range ms[have:] {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return rep, err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_history (
  version INTEGER PRIMARY KEY, note TEXT NOT NULL, at TEXT NOT NULL, server TEXT NOT NULL)`); err != nil {
			tx.Rollback()
			return rep, err
		}
		if err := m.Up(ctx, tx); err != nil {
			tx.Rollback()
			return rep, fmt.Errorf("%s: migration %d (%s): %w", path, m.Version, m.Note, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_history VALUES (?, ?, ?, ?)`,
			m.Version, m.Note, time.Now().UTC().Format(time.RFC3339), server); err != nil {
			tx.Rollback()
			return rep, err
		}
		// PRAGMA takes no parameters; the number is ours
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.Version)); err != nil {
			tx.Rollback()
			return rep, err
		}
		if err := tx.Commit(); err != nil {
			return rep, err
		}
		rep.To = m.Version
	}
	return rep, nil
}

// BackupSQLite writes a consistent copy of the database to
// <dir of path>/backups/<time>-<name>-<tag>.db and returns its path. It
// runs beside readers and waits for a writer.
func BackupSQLite(ctx context.Context, db *sql.DB, path, tag string) (string, error) {
	dir := filepath.Join(filepath.Dir(path), "backups")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	dst := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000Z")+"-"+base[:len(base)-len(filepath.Ext(base))]+"-"+tag+".db")
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// SchemaVersion is the file's user_version.
func SchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}
