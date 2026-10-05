// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"
)

// SQLiteBlobStore keeps blobs in their own SQLite file (blobs.db), apart
// from the documents, so gigabytes of attachments do not grow sliqtly.db's
// backups, WAL and checkpoints:
//
//	blobs(hash, body, size, mime, created, chunk)   one row per blob
//	chunks(body, n, data)                           its bytes, `chunk` each
//	uploads(body, started)                          bodies being written
//
// A blob is written in chunks (255 KiB), committed 64 at a time, under a
// body number from uploads; the blobs row is the commit point, added in
// the last transaction. So a 2 GB video is never one record, the WAL holds
// at most 16 MiB of it, and a crash leaves an upload that the next open
// removes, never a blob with missing bytes. Bytes already kept under the
// same hash are not kept twice: the new body is dropped instead.
//
// Reading fetches the chunks a read covers, so a Range request on a large
// video reads only those.
type SQLiteBlobStore struct {
	path   string
	db     *sql.DB
	chunk  int
	state  sync.RWMutex
	closed bool
}

// BlobChunk is the chunk size of new blobs: 255 KiB, which with 64 KiB
// pages is one page in the row and three overflow pages, filled (a round
// 256 KiB would need a fifth page for its last bytes). Measured against
// 1 MiB and 64 KiB chunks (BENCHMARK-blobs.md): a 64 KB range read costs
// about the chunk it falls in, 1.1 ms with 1 MiB chunks and 0.2 ms with
// these, at the same write and streaming speed. A blob keeps the chunk
// size it was written with, so this may change between releases.
var BlobChunk = 255 << 10

// chunks committed per transaction while a blob is written
const blobBatch = 64

// SQLiteBlobSchema is blobs.db's schema (sqlmigrate.go). Append only.
var SQLiteBlobSchema = []SQLMigration{
	{Version: 1, Note: "blobs in chunks", Up: SQLExec(`
CREATE TABLE uploads (
  body    INTEGER PRIMARY KEY AUTOINCREMENT,
  started INTEGER NOT NULL
);
CREATE TABLE blobs (
  hash    BLOB PRIMARY KEY,
  body    INTEGER NOT NULL UNIQUE,
  size    INTEGER NOT NULL,
  mime    TEXT NOT NULL,
  created INTEGER NOT NULL,
  chunk   INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE chunks (
  body INTEGER NOT NULL,
  n    INTEGER NOT NULL,
  data BLOB NOT NULL,
  PRIMARY KEY (body, n)
);`)},
}

// OpenSQLiteBlobStore opens (or creates) the blobs in the SQLite file at
// path, and removes what an upload stopped by a crash left.
func OpenSQLiteBlobStore(path string) (*SQLiteBlobStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// large pages: fewer overflow pages per chunk; incremental vacuum, so
	// the room of collected blobs can be given back (both: a new file only)
	db, err := sql.Open("sqlite", sqliteDSN(abs, "page_size(65536)", "auto_vacuum(INCREMENTAL)"))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if _, err := Migrate(ctx, db, abs, SQLiteBlobSchema, Build); err != nil {
		db.Close()
		return nil, err
	}
	// one process owns the file: every upload still listed was stopped
	if _, err := db.ExecContext(ctx, `DELETE FROM chunks WHERE body IN (SELECT body FROM uploads); DELETE FROM uploads;`); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteBlobStore{path: abs, db: db, chunk: BlobChunk}, nil
}

func (s *SQLiteBlobStore) Path() string { return s.path }

// DB is the connection, for maintenance (backup, checks).
func (s *SQLiteBlobStore) DB() *sql.DB { return s.db }

func (s *SQLiteBlobStore) live() error {
	if s.closed {
		return ErrClosed
	}
	return nil
}

func (s *SQLiteBlobStore) Put(ctx context.Context, r io.Reader, mime string) (BlobInfo, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return BlobInfo{}, err
	}
	h := sha256.New()
	buf := make([]byte, s.chunk)
	var size int64
	var n int64
	var body int64
	var tx *sql.Tx
	var err error
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	begin := func() error {
		tx, err = s.db.BeginTx(ctx, nil)
		return err
	}
	if err := begin(); err != nil {
		return BlobInfo{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO uploads (started) VALUES (?)`, time.Now().UnixMilli())
	if err != nil {
		return BlobInfo{}, err
	}
	if body, err = res.LastInsertId(); err != nil {
		return BlobInfo{}, err
	}
	for {
		k, rerr := io.ReadFull(r, buf)
		if k > 0 {
			h.Write(buf[:k])
			if _, err := tx.ExecContext(ctx, `INSERT INTO chunks (body, n, data) VALUES (?, ?, ?)`, body, n, buf[:k]); err != nil {
				return BlobInfo{}, err
			}
			size += int64(k)
			n++
			if n%blobBatch == 0 {
				if err := tx.Commit(); err != nil {
					tx = nil
					return BlobInfo{}, err
				}
				if err := begin(); err != nil {
					tx = nil
					return BlobInfo{}, err
				}
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			// the chunks committed so far are removed with the upload
			s.dropUpload(body)
			return BlobInfo{}, rerr
		}
	}
	var hash Hash
	copy(hash[:], h.Sum(nil))
	info := BlobInfo{Hash: hash, Size: size, Mime: mime, Created: time.UnixMilli(time.Now().UnixMilli()).UTC()}
	// the commit point: the blob is there when its row is
	var had int64
	err = tx.QueryRowContext(ctx, `SELECT size FROM blobs WHERE hash = ?`, hash[:]).Scan(&had)
	dup := err == nil
	switch {
	case dup:
		if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE body = ?`, body); err != nil {
			return BlobInfo{}, err
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO blobs (hash, body, size, mime, created, chunk) VALUES (?, ?, ?, ?, ?, ?)`,
			hash[:], body, size, mime, info.Created.UnixMilli(), s.chunk); err != nil {
			return BlobInfo{}, err
		}
	default:
		return BlobInfo{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE body = ?`, body); err != nil {
		return BlobInfo{}, err
	}
	err = tx.Commit()
	tx = nil
	if err != nil {
		return BlobInfo{}, err
	}
	if dup {
		return s.Stat(ctx, hash)
	}
	return info, nil
}

func (s *SQLiteBlobStore) dropUpload(body int64) {
	s.db.Exec(`DELETE FROM chunks WHERE body = ?`, body)
	s.db.Exec(`DELETE FROM uploads WHERE body = ?`, body)
}

func (s *SQLiteBlobStore) stat(ctx context.Context, h Hash) (BlobInfo, int64, int, error) {
	var body, size, created int64
	var chunk int
	var mime string
	err := s.db.QueryRowContext(ctx, `SELECT body, size, mime, created, chunk FROM blobs WHERE hash = ?`, h[:]).Scan(&body, &size, &mime, &created, &chunk)
	if errors.Is(err, sql.ErrNoRows) {
		return BlobInfo{}, 0, 0, ErrNoBlob
	}
	if err != nil {
		return BlobInfo{}, 0, 0, err
	}
	return BlobInfo{Hash: h, Size: size, Mime: mime, Created: time.UnixMilli(created).UTC()}, body, chunk, nil
}

func (s *SQLiteBlobStore) Stat(ctx context.Context, h Hash) (BlobInfo, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return BlobInfo{}, err
	}
	info, _, _, err := s.stat(ctx, h)
	return info, err
}

func (s *SQLiteBlobStore) Open(ctx context.Context, h Hash) (BlobReader, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return nil, err
	}
	info, body, chunk, err := s.stat(ctx, h)
	if err != nil {
		return nil, err
	}
	r := &sqliteBlobReader{s: s, ctx: ctx, info: info, body: body, chunk: int64(chunk), cached: -1}
	r.readerAtSeeker = readerAtSeeker{at: r, size: info.Size}
	return r, nil
}

type sqliteBlobReader struct {
	readerAtSeeker
	s      *SQLiteBlobStore
	ctx    context.Context
	info   BlobInfo
	body   int64
	chunk  int64
	cached int64 // the chunk in data; -1: none
	data   []byte
	mu     sync.Mutex
}

func (r *sqliteBlobReader) Info() BlobInfo { return r.info }

func (r *sqliteBlobReader) Close() error { return nil }

func (r *sqliteBlobReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if off >= r.info.Size {
		return 0, io.EOF
	}
	done := 0
	for done < len(p) && off < r.info.Size {
		n := off / r.chunk
		if n != r.cached {
			r.s.state.RLock()
			err := r.s.live()
			if err == nil {
				err = r.s.db.QueryRowContext(r.ctx, `SELECT data FROM chunks WHERE body = ? AND n = ?`, r.body, n).Scan(&r.data)
			}
			r.s.state.RUnlock()
			if err != nil {
				r.cached = -1
				return done, fmt.Errorf("blob %s chunk %d: %w", r.info.Hash, n, err)
			}
			r.cached = n
		}
		k := copy(p[done:], r.data[off-n*r.chunk:])
		if k == 0 {
			return done, fmt.Errorf("blob %s chunk %d is short", r.info.Hash, n)
		}
		done += k
		off += int64(k)
	}
	if done < len(p) {
		return done, io.EOF
	}
	return done, nil
}

func (s *SQLiteBlobStore) Delete(ctx context.Context, h Hash) error {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var body int64
	err = tx.QueryRowContext(ctx, `SELECT body FROM blobs WHERE hash = ?`, h[:]).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE body = ?`, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blobs WHERE hash = ?`, h[:]); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteBlobStore) Each(ctx context.Context, fn func(BlobInfo) error) error {
	s.state.RLock()
	if err := s.live(); err != nil {
		s.state.RUnlock()
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT hash, size, mime, created FROM blobs ORDER BY hash`)
	if err != nil {
		s.state.RUnlock()
		return err
	}
	var all []BlobInfo
	for rows.Next() {
		var hb []byte
		var info BlobInfo
		var created int64
		if err := rows.Scan(&hb, &info.Size, &info.Mime, &created); err != nil {
			rows.Close()
			s.state.RUnlock()
			return err
		}
		copy(info.Hash[:], hb)
		info.Created = time.UnixMilli(created).UTC()
		all = append(all, info)
	}
	err = rows.Err()
	rows.Close()
	s.state.RUnlock()
	if err != nil {
		return err
	}
	// fn may read the blobs: the listing is closed first
	for _, info := range all {
		if err := fn(info); err != nil {
			return err
		}
	}
	return nil
}

// Shrink gives the file's free pages (from deleted blobs) back to the disk.
func (s *SQLiteBlobStore) Shrink(ctx context.Context) error {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`)
	return err
}

func (s *SQLiteBlobStore) Close() error {
	s.state.Lock()
	defer s.state.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}
