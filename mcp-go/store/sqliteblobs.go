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

	"github.com/terotests/sliqtly/mcp-go/rdiff"
)

// SQLiteBlobStore keeps blobs in their own SQLite file (blobs.db), apart
// from the documents, so gigabytes of attachments do not grow sliqtly.db's
// backups, WAL and checkpoints:
//
//	blobs(hash, body, size, mime, created, chunk,   one row per blob
//	      enc, base, stored)
//	chunks(body, n, data)                           its body, `chunk` each
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
//
// A blob's body is its bytes (enc 0), or a delta against another blob,
// `base` (enc 1, RangerDiff's RdSmart, package rdiff): Deltify rewrites a
// blob that way when that is under 80 % of its size. The hash, size and
// bytes read stay what they were; only what is kept changes. An older
// version is kept as a delta against the newer one (the newest whole, as
// RangerDiff's RdRepo does), so reading the current one costs nothing
// more, and dropping the oldest drops a leaf. A delta blob is read whole
// into memory (they are at most DeltaMax) and checked against its hash.
// Deleting a base first rewrites the blobs on it.
type SQLiteBlobStore struct {
	path   string
	db     *sql.DB
	chunk  int
	state  sync.RWMutex
	closed bool
	// readOnly: OpenSQLiteBlobStoreReadOnly
	readOnly bool
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

// DeltaMax is the largest blob kept as a delta, and the largest base: a
// delta is made and read in memory. Larger files are video and the like,
// which do not delta anyway.
var DeltaMax int64 = 64 << 20

// MaxDeltaChain is how many deltas a read may go through to reach a whole
// blob.
const MaxDeltaChain = 16

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
	{Version: 2, Note: "blobs as deltas", Additive: true, Up: SQLExec(`
ALTER TABLE blobs ADD COLUMN enc INTEGER NOT NULL DEFAULT 0;
ALTER TABLE blobs ADD COLUMN base BLOB;
ALTER TABLE blobs ADD COLUMN stored INTEGER;
CREATE INDEX blobs_base ON blobs (base) WHERE base IS NOT NULL;`)},
}

// a blob's encodings (blobs.enc)
const (
	encWhole = 0
	encDelta = 1
)

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

// OpenSQLiteBlobStoreReadOnly opens the blobs at path for reading only,
// beside the server that owns the file (a backup taken while it runs). It
// migrates nothing and refuses a file at another schema version than this
// build's.
func OpenSQLiteBlobStoreReadOnly(path string) (*SQLiteBlobStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := openReadOnly(abs, len(SQLiteBlobSchema))
	if err != nil {
		return nil, err
	}
	return &SQLiteBlobStore{path: abs, db: db, chunk: BlobChunk, readOnly: true}, nil
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

// ErrReadOnly: a write to a store opened read-only
var ErrReadOnly = errors.New("store: opened read-only")

func (s *SQLiteBlobStore) writable() error {
	if err := s.live(); err != nil {
		return err
	}
	if s.readOnly {
		return ErrReadOnly
	}
	return nil
}

func (s *SQLiteBlobStore) Put(ctx context.Context, r io.Reader, mime string) (BlobInfo, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.writable(); err != nil {
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

// a blob's row
type blobRow struct {
	info  BlobInfo
	body  int64
	chunk int
	enc   int
	base  Hash  // enc 1
	kept  int64 // bytes in its chunks
}

func (s *SQLiteBlobStore) stat(ctx context.Context, h Hash) (blobRow, error) {
	var r blobRow
	var created int64
	var base []byte
	var stored sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT body, size, mime, created, chunk, enc, base, stored FROM blobs WHERE hash = ?`, h[:]).
		Scan(&r.body, &r.info.Size, &r.info.Mime, &created, &r.chunk, &r.enc, &base, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoBlob
	}
	if err != nil {
		return r, err
	}
	r.info.Hash = h
	r.info.Created = time.UnixMilli(created).UTC()
	copy(r.base[:], base)
	r.kept = r.info.Size
	if stored.Valid {
		r.kept = stored.Int64
	}
	return r, nil
}

func (s *SQLiteBlobStore) Stat(ctx context.Context, h Hash) (BlobInfo, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return BlobInfo{}, err
	}
	r, err := s.stat(ctx, h)
	return r.info, err
}

func (s *SQLiteBlobStore) Open(ctx context.Context, h Hash) (BlobReader, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return nil, err
	}
	row, err := s.stat(ctx, h)
	if err != nil {
		return nil, err
	}
	r := &sqliteBlobReader{s: s, ctx: ctx, info: row.info, cached: -1}
	r.use(row)
	r.readerAtSeeker = readerAtSeeker{at: r, size: row.info.Size}
	return r, nil
}

type sqliteBlobReader struct {
	readerAtSeeker
	s      *SQLiteBlobStore
	ctx    context.Context
	info   BlobInfo
	body   int64
	chunk  int64
	delta  bool
	cached int64 // the chunk in data; -1: none
	data   []byte
	whole  []byte // a delta blob's bytes, once read
	mu     sync.Mutex
}

func (r *sqliteBlobReader) use(row blobRow) {
	r.body, r.chunk, r.delta, r.cached = row.body, int64(row.chunk), row.enc == encDelta, -1
}

func (r *sqliteBlobReader) Info() BlobInfo { return r.info }

func (r *sqliteBlobReader) Close() error { return nil }

func (r *sqliteBlobReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if off >= r.info.Size {
		return 0, io.EOF
	}
	if r.delta && r.whole == nil {
		b, err := r.s.bytesOf(r.ctx, r.info.Hash)
		if err != nil {
			return 0, err
		}
		r.whole = b
	}
	if r.whole != nil {
		n := copy(p, r.whole[off:])
		if n < len(p) {
			return n, io.EOF
		}
		return n, nil
	}
	done := 0
	again := true
	for done < len(p) && off < r.info.Size {
		n := off / r.chunk
		if n != r.cached {
			r.s.state.RLock()
			err := r.s.live()
			if err == nil {
				err = r.s.db.QueryRowContext(r.ctx, `SELECT data FROM chunks WHERE body = ? AND n = ?`, r.body, n).Scan(&r.data)
			}
			r.s.state.RUnlock()
			if errors.Is(err, sql.ErrNoRows) && again {
				// Deltify or a Delete of its base rewrote the body since
				// the blob was opened: read the blob as it is kept now
				again = false
				if row, serr := r.s.statLive(r.ctx, r.info.Hash); serr == nil {
					r.use(row)
					if !r.delta {
						continue
					}
					b, berr := r.s.bytesOf(r.ctx, r.info.Hash)
					if berr != nil {
						return done, berr
					}
					r.whole = b
					k := copy(p[done:], b[off:])
					if done+k < len(p) {
						return done + k, io.EOF
					}
					return done + k, nil
				}
			}
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

func (s *SQLiteBlobStore) statLive(ctx context.Context, h Hash) (blobRow, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return blobRow{}, err
	}
	return s.stat(ctx, h)
}

// body reads a body's chunks into one slice
func (s *SQLiteBlobStore) body(ctx context.Context, body, size int64) ([]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM chunks WHERE body = ? ORDER BY n`, body)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]byte, 0, size)
	for rows.Next() {
		var d []byte
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out)) != size {
		return nil, fmt.Errorf("body %d has %d bytes, %d expected", body, len(out), size)
	}
	return out, nil
}

// bytesOf is a blob's bytes, whole or through its deltas, checked against
// its hash. For blobs of at most DeltaMax, or a delta's own bytes.
func (s *SQLiteBlobStore) bytesOf(ctx context.Context, h Hash) ([]byte, error) {
	return s.bytesAt(ctx, h, 0)
}

func (s *SQLiteBlobStore) bytesAt(ctx context.Context, h Hash, depth int) ([]byte, error) {
	if depth > MaxDeltaChain*2 {
		return nil, fmt.Errorf("blob %s: delta chain longer than %d", h, MaxDeltaChain*2)
	}
	// a Deltify or Delete may rewrite the body between the row and the
	// chunks: then the row is read again
	for try := 0; ; try++ {
		row, err := s.statLive(ctx, h)
		if err != nil {
			return nil, err
		}
		s.state.RLock()
		err = s.live()
		var kept []byte
		if err == nil {
			kept, err = s.body(ctx, row.body, row.kept)
		}
		s.state.RUnlock()
		if err != nil {
			if try < 3 {
				if again, _ := s.statLive(ctx, h); again.body != row.body {
					continue
				}
			}
			return nil, fmt.Errorf("blob %s: %w", h, err)
		}
		out := kept
		if row.enc == encDelta {
			base, err := s.bytesAt(ctx, row.base, depth+1)
			if err != nil {
				return nil, fmt.Errorf("blob %s: base: %w", h, err)
			}
			if out, err = rdiff.Apply(base, kept); err != nil {
				return nil, fmt.Errorf("blob %s: %w", h, err)
			}
		}
		if int64(len(out)) != row.info.Size || HashOf(out) != h {
			return nil, fmt.Errorf("blob %s: its bytes do not match its hash", h)
		}
		return out, nil
	}
}

// putBody writes data as a new body in chunks, committed blobBatch at a
// time, under a row in uploads that the caller's last transaction removes
func (s *SQLiteBlobStore) putBody(ctx context.Context, data []byte) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	res, err := tx.ExecContext(ctx, `INSERT INTO uploads (started) VALUES (?)`, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	body, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	n := 0
	for off := 0; off < len(data); off += s.chunk {
		end := min(off+s.chunk, len(data))
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks (body, n, data) VALUES (?, ?, ?)`, body, n, data[off:end]); err != nil {
			return 0, err
		}
		n++
		if n%blobBatch == 0 {
			if err := tx.Commit(); err != nil {
				tx = nil
				s.dropUpload(body)
				return 0, err
			}
			if tx, err = s.db.BeginTx(ctx, nil); err != nil {
				tx = nil
				s.dropUpload(body)
				return 0, err
			}
		}
	}
	err = tx.Commit()
	tx = nil
	if err != nil {
		s.dropUpload(body)
		return 0, err
	}
	return body, nil
}

// Deltify keeps blob h as a delta against base, when that delta is under
// 80 % of h and the delta chains stay at most MaxDeltaChain long. h's hash,
// size and bytes stay the same. The delta is checked to rebuild h before it
// replaces h's body. → whether h is now a delta
func (s *SQLiteBlobStore) Deltify(ctx context.Context, h, base Hash) (bool, error) {
	s.state.RLock()
	err := s.writable()
	s.state.RUnlock()
	if err != nil {
		return false, err
	}
	if h == base {
		return false, nil
	}
	row, err := s.statLive(ctx, h)
	if err != nil {
		return false, err
	}
	brow, err := s.statLive(ctx, base)
	if err != nil {
		return false, err
	}
	if row.info.Size > DeltaMax || brow.info.Size > DeltaMax || row.info.Size == 0 {
		return false, nil
	}
	if row.enc == encDelta && row.base == base {
		return true, nil
	}
	// base's chain must not come back to h, and with what is built on h it
	// must stay short
	up, err := s.chainTo(ctx, base, h)
	if err != nil || up < 0 {
		return false, err
	}
	down, err := s.dependentDepth(ctx, h)
	if err != nil {
		return false, err
	}
	if up+1+down > MaxDeltaChain {
		return false, nil
	}
	target, err := s.bytesOf(ctx, h)
	if err != nil {
		return false, err
	}
	from, err := s.bytesOf(ctx, base)
	if err != nil {
		return false, err
	}
	d := rdiff.Exact(from, target)
	if int64(len(d))*5 >= row.info.Size*4 {
		return false, nil
	}
	if got, err := rdiff.Apply(from, d); err != nil || string(got) != string(target) {
		return false, nil
	}
	body, err := s.putBody(ctx, d)
	if err != nil {
		return false, err
	}
	return s.swapBody(ctx, h, row.body, body, encDelta, &base, int64(len(d)))
}

// swapBody points h at a new body (made by putBody) if h is still at old,
// and removes the old body's chunks. A delta's base must still be there.
func (s *SQLiteBlobStore) swapBody(ctx context.Context, h Hash, old, body int64, enc int, base *Hash, kept int64) (bool, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.writable(); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.dropUpload(body)
		return false, err
	}
	defer tx.Rollback()
	ok := true
	if base != nil {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM blobs WHERE hash = ?`, base[:]).Scan(&one); err != nil {
			ok = false
		}
	}
	var res sql.Result
	if ok {
		var b any
		var stored any
		if base != nil {
			b, stored = base[:], kept
		}
		res, err = tx.ExecContext(ctx, `UPDATE blobs SET body = ?, enc = ?, base = ?, stored = ?, chunk = ? WHERE hash = ? AND body = ?`,
			body, enc, b, stored, s.chunk, h[:], old)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			ok = false
		}
	}
	gone := old
	if !ok {
		gone = body
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE body = ?`, gone); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE body = ?`, body); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return ok, nil
}

// chainTo is how many deltas lead from from to a whole blob; -1 when the
// chain reaches avoid
func (s *SQLiteBlobStore) chainTo(ctx context.Context, from, avoid Hash) (int, error) {
	n := 0
	for cur := from; ; n++ {
		if cur == avoid {
			return -1, nil
		}
		row, err := s.statLive(ctx, cur)
		if err != nil {
			return 0, err
		}
		if row.enc != encDelta {
			return n, nil
		}
		if n > MaxDeltaChain*2 {
			return -1, nil
		}
		cur = row.base
	}
}

// dependents are the blobs kept as a delta against h
func (s *SQLiteBlobStore) dependents(ctx context.Context, h Hash) ([]Hash, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.live(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT hash FROM blobs WHERE base = ?`, h[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hash
	for rows.Next() {
		var hb []byte
		if err := rows.Scan(&hb); err != nil {
			return nil, err
		}
		var d Hash
		copy(d[:], hb)
		out = append(out, d)
	}
	return out, rows.Err()
}

// dependentDepth is the longest chain of deltas built on h
func (s *SQLiteBlobStore) dependentDepth(ctx context.Context, h Hash) (int, error) {
	level := []Hash{h}
	depth := 0
	for len(level) > 0 && depth <= MaxDeltaChain*2 {
		var next []Hash
		for _, x := range level {
			ds, err := s.dependents(ctx, x)
			if err != nil {
				return 0, err
			}
			next = append(next, ds...)
		}
		if len(next) == 0 {
			break
		}
		depth++
		level = next
	}
	return depth, nil
}

// BlobUsage is what a store keeps: its blobs' sizes, and the bytes their
// bodies take (less, for the deltas)
type BlobUsage struct {
	Blobs, Deltas int
	Size, Kept    int64
}

func (s *SQLiteBlobStore) Usage(ctx context.Context) (BlobUsage, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	var u BlobUsage
	if err := s.live(); err != nil {
		return u, err
	}
	err := s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(enc = 1), 0), coalesce(sum(size), 0), coalesce(sum(coalesce(stored, size)), 0) FROM blobs`).
		Scan(&u.Blobs, &u.Deltas, &u.Size, &u.Kept)
	return u, err
}

// Delete removes h. The blobs kept as a delta against h are first kept
// whole, then as a delta against h's own base when h had one.
func (s *SQLiteBlobStore) Delete(ctx context.Context, h Hash) error {
	s.state.RLock()
	err := s.writable()
	s.state.RUnlock()
	if err != nil {
		return err
	}
	row, err := s.statLive(ctx, h)
	if errors.Is(err, ErrNoBlob) {
		return nil
	}
	if err != nil {
		return err
	}
	for try := 0; ; try++ {
		deps, err := s.dependents(ctx, h)
		if err != nil {
			return err
		}
		for _, d := range deps {
			if err := s.makeWhole(ctx, d); err != nil {
				return err
			}
			if row.enc == encDelta {
				// best effort: d stays whole when it does not pay
				s.Deltify(ctx, d, row.base)
			}
		}
		done, err := s.deleteLeaf(ctx, h)
		if err != nil || done {
			return err
		}
		if try > 8 {
			return fmt.Errorf("blob %s: blobs keep being made on it", h)
		}
	}
}

// makeWhole keeps a delta blob whole
func (s *SQLiteBlobStore) makeWhole(ctx context.Context, h Hash) error {
	for try := 0; try < 8; try++ {
		row, err := s.statLive(ctx, h)
		if errors.Is(err, ErrNoBlob) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.enc != encDelta {
			return nil
		}
		b, err := s.bytesOf(ctx, h)
		if err != nil {
			return err
		}
		body, err := s.putBody(ctx, b)
		if err != nil {
			return err
		}
		ok, err := s.swapBody(ctx, h, row.body, body, encWhole, nil, 0)
		if err != nil || ok {
			return err
		}
	}
	return fmt.Errorf("blob %s keeps changing", h)
}

// deleteLeaf removes h if no blob is a delta against it
func (s *SQLiteBlobStore) deleteLeaf(ctx context.Context, h Hash) (bool, error) {
	s.state.RLock()
	defer s.state.RUnlock()
	if err := s.writable(); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM blobs WHERE base = ? LIMIT 1`, h[:]).Scan(&one)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var body int64
	err = tx.QueryRowContext(ctx, `SELECT body FROM blobs WHERE hash = ?`, h[:]).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE body = ?`, body); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blobs WHERE hash = ?`, h[:]); err != nil {
		return false, err
	}
	return true, tx.Commit()
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
	if err := s.writable(); err != nil {
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
