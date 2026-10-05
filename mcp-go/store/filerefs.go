// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// FileRefs are the kept files by path, in sliqtly.db (file_refs): which
// blob holds a path's bytes, its size and type. Many paths may name one
// blob; a blob no path names is collected (CollectBlobs with Referenced).
// Whether someone may read a path is decided by what the path belongs to
// (the deck in shares/{id}/…), never by knowing a blob's hash.
//
// file_lines keeps append-only text files (a room's chat) as rows, so an
// append is one insert rather than a rewrite.
type FileRefs struct {
	db *sql.DB
}

// FileRef is one path's entry.
type FileRef struct {
	Path    string
	Hash    Hash
	Size    int64
	Mime    string
	Updated time.Time
}

func NewFileRefs(s *SQLiteStore) *FileRefs { return &FileRefs{db: s.db} }

// Set points path at a blob (one already in the BlobStore: put the blob
// first, then its reference).
func (f *FileRefs) Set(ctx context.Context, path string, info BlobInfo, mime string) error {
	_, err := f.db.ExecContext(ctx, `INSERT INTO file_refs (path, hash, size, mime, updated) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (path) DO UPDATE SET hash = excluded.hash, size = excluded.size, mime = excluded.mime, updated = excluded.updated`,
		path, info.Hash[:], info.Size, mime, time.Now().UnixMilli())
	return err
}

// Get is the path's entry; ErrNotFound when it has none.
func (f *FileRefs) Get(ctx context.Context, path string) (FileRef, error) {
	r := FileRef{Path: path}
	var hb []byte
	var updated int64
	err := f.db.QueryRowContext(ctx, `SELECT hash, size, mime, updated FROM file_refs WHERE path = ?`, path).Scan(&hb, &r.Size, &r.Mime, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	copy(r.Hash[:], hb)
	r.Updated = time.UnixMilli(updated).UTC()
	return r, nil
}

// Remove drops the path's entry and its lines.
func (f *FileRefs) Remove(ctx context.Context, path string) error {
	if _, err := f.db.ExecContext(ctx, `DELETE FROM file_refs WHERE path = ?`, path); err != nil {
		return err
	}
	_, err := f.db.ExecContext(ctx, `DELETE FROM file_lines WHERE path = ?`, path)
	return err
}

// RemoveAll drops every entry under dir (dir/…), as one step.
func (f *FileRefs) RemoveAll(ctx context.Context, dir string) error {
	lo, hi := prefixRange(strings.TrimSuffix(dir, "/") + "/")
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"file_refs", "file_lines"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE path >= ? AND path < ?`, lo, hi); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// List is every entry under dir, by path.
func (f *FileRefs) List(ctx context.Context, dir string) ([]FileRef, error) {
	lo, hi := prefixRange(strings.TrimSuffix(dir, "/") + "/")
	q := `SELECT path, hash, size, mime, updated FROM file_refs WHERE path >= ? AND path < ? ORDER BY path`
	args := []any{lo, hi}
	if dir == "" {
		q, args = `SELECT path, hash, size, mime, updated FROM file_refs ORDER BY path`, nil
	}
	rows, err := f.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRef
	for rows.Next() {
		var r FileRef
		var hb []byte
		var updated int64
		if err := rows.Scan(&r.Path, &hb, &r.Size, &r.Mime, &updated); err != nil {
			return nil, err
		}
		copy(r.Hash[:], hb)
		r.Updated = time.UnixMilli(updated).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// Referenced: some path names the blob.
func (f *FileRefs) Referenced(ctx context.Context, h Hash) (bool, error) {
	var one int
	err := f.db.QueryRowContext(ctx, `SELECT 1 FROM file_refs WHERE hash = ? LIMIT 1`, h[:]).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// AppendLine adds a line to the end of path's lines.
func (f *FileRefs) AppendLine(ctx context.Context, path, line string) error {
	_, err := f.db.ExecContext(ctx, `INSERT INTO file_lines (path, n, line)
VALUES (?, (SELECT coalesce(max(n), 0) + 1 FROM file_lines WHERE path = ?), ?)`, path, path, line)
	return err
}

// Lines is path's last `last` lines (all when last <= 0), oldest first.
func (f *FileRefs) Lines(ctx context.Context, path string, last int) ([]string, error) {
	q := `SELECT line FROM (SELECT n, line FROM file_lines WHERE path = ? ORDER BY n DESC LIMIT ?) ORDER BY n`
	if last <= 0 {
		last = -1
	}
	rows, err := f.db.QueryContext(ctx, q, path, last)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// the paths starting with p: [p, p with its last byte one higher)
func prefixRange(p string) (string, string) {
	b := []byte(p)
	hi := append([]byte{}, b...)
	hi[len(hi)-1]++ // p ends in "/", never 0xff
	return p, string(hi)
}
