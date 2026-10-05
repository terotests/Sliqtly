// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"sync"

	_ "modernc.org/sqlite" // pure Go: the server is built with CGO_ENABLED=0
)

// SQLiteStore keeps documents in one SQLite file (sliqtly.db):
//
//	docs(col, id, rev, doc)   doc is the document as JSON, times as
//	                          {"$ts": ms} (EncodeValue), as the folder keeps them
//	file_refs, file_lines     kept files by path (FileRefs); their bytes are
//	                          in blobs.db (SQLiteBlobStore)
//
// The schema is SQLiteSchema, applied by Migrate when the file is opened.
//
// WAL journal, synchronous=FULL: a write that returned is on disk. One
// process owns the file (the server locks the data folder); within it one
// lock keeps writes in order, which is also the order Watch tells them in.
// Reads go to the database directly and run beside a write.
//
// Query evaluates the Query in Go over the collection's rows (Match, Run),
// as the folder and memory do, so the three cannot differ; compiling it to
// SQL is a later step for collections too large to read.
type SQLiteStore struct {
	path   string
	db     *sql.DB
	mu     sync.Mutex   // writes
	state  sync.RWMutex // closed
	closed bool
	feed   *feed
}

// Build names the server build, as recorded in a database's schema_history.
var Build = "dev"

// SQLiteSchema is sliqtly.db's schema, as its migrations (sqlmigrate.go).
// Append only.
var SQLiteSchema = []SQLMigration{
	{Version: 1, Note: "documents, file references and file logs", Up: SQLExec(`
CREATE TABLE docs (
  col TEXT NOT NULL,
  id  TEXT NOT NULL,
  rev INTEGER NOT NULL,
  doc TEXT NOT NULL,
  PRIMARY KEY (col, id)
) WITHOUT ROWID;
-- a kept file's path (shares/{id}/media/x) and the blob holding its bytes
-- in blobs.db; many paths may name one blob
CREATE TABLE file_refs (
  path    TEXT PRIMARY KEY,
  hash    BLOB NOT NULL,
  size    INTEGER NOT NULL,
  mime    TEXT NOT NULL,
  updated INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX file_refs_hash ON file_refs (hash);
-- append-only text files (a room's chat): one row per line
CREATE TABLE file_lines (
  path TEXT NOT NULL,
  n    INTEGER NOT NULL,
  line TEXT NOT NULL,
  PRIMARY KEY (path, n)
) WITHOUT ROWID;`)},
}

// OpenSQLiteStore opens (or creates) the documents in the SQLite file at path.
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(abs))
	if err != nil {
		return nil, err
	}
	if _, err := Migrate(context.Background(), db, abs, SQLiteSchema, Build); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteStore{path: abs, db: db, feed: newFeed(4096)}, nil
}

// the connection settings every SQLite file of the server uses: WAL,
// writes on disk when they return, a writer waits for another rather than
// failing, and a transaction takes the write lock when it begins. first
// are pragmas applied before those (page_size only takes on a new file).
func sqliteDSN(path string, first ...string) string {
	q := url.Values{}
	for _, p := range append(first, "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(10000)", "foreign_keys(1)") {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// Path is the database file.
func (s *SQLiteStore) Path() string { return s.path }

// DB is the connection, for maintenance (backup, checks).
func (s *SQLiteStore) DB() *sql.DB { return s.db }

func (s *SQLiteStore) open() bool {
	s.state.RLock()
	defer s.state.RUnlock()
	return !s.closed
}

func (s *SQLiteStore) Get(ctx context.Context, col, id string) (Doc, Rev, error) {
	if err := checkName(col, id); err != nil {
		return nil, 0, err
	}
	s.state.RLock()
	defer s.state.RUnlock()
	if s.closed {
		return nil, 0, ErrClosed
	}
	return getDoc(ctx, s.db, col, id)
}

type querier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func getDoc(ctx context.Context, q querier, col, id string) (Doc, Rev, error) {
	var rev int64
	var text string
	err := q.QueryRowContext(ctx, `SELECT rev, doc FROM docs WHERE col = ? AND id = ?`, col, id).Scan(&rev, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	d, err := decodeDoc(text)
	if err != nil {
		return nil, 0, fmt.Errorf("%s/%s: %w", col, id, err)
	}
	return d, Rev(rev), nil
}

func (s *SQLiteStore) Update(ctx context.Context, col, id string, fn UpdateFunc) (Doc, Rev, error) {
	if err := checkName(col, id); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.RLock()
	defer s.state.RUnlock()
	if s.closed {
		return nil, 0, ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	cur, rev, err := getDoc(ctx, tx, col, id)
	if err != nil {
		return nil, 0, err
	}
	next, err := fn(Clone(cur), rev)
	if err != nil {
		return nil, 0, err
	}
	if next == nil {
		if cur == nil {
			return nil, 0, nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM docs WHERE col = ? AND id = ?`, col, id); err != nil {
			return nil, 0, err
		}
		if err := tx.Commit(); err != nil {
			return nil, 0, err
		}
		s.feed.publish(Change{Col: col, ID: id, Old: cur})
		return nil, 0, nil
	}
	next = Clone(next)
	text, err := encodeDoc(next)
	if err != nil {
		return nil, 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO docs (col, id, rev, doc) VALUES (?, ?, ?, ?)
ON CONFLICT (col, id) DO UPDATE SET rev = excluded.rev, doc = excluded.doc`, col, id, int64(rev+1), text); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	s.feed.publish(Change{Col: col, ID: id, Rev: rev + 1, Doc: Clone(next), Old: cur})
	return Clone(next), rev + 1, nil
}

func (s *SQLiteStore) Query(ctx context.Context, q Query) ([]Item, error) {
	if err := checkName(q.From, "x"); err != nil {
		return nil, err
	}
	s.state.RLock()
	defer s.state.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, rev, doc FROM docs WHERE col = ?`, q.From)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var id, text string
		var rev int64
		if err := rows.Scan(&id, &rev, &text); err != nil {
			return nil, err
		}
		d, err := decodeDoc(text)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", q.From, id, err)
		}
		if Match(q.Where, id, d) {
			items = append(items, Item{ID: id, Rev: Rev(rev), Doc: d})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return Run(Query{OrderBy: q.OrderBy, Limit: q.Limit, Offset: q.Offset}, items), nil
}

func (s *SQLiteStore) Head() Seq { return s.feed.Head() }

func (s *SQLiteStore) Watch(ctx context.Context, after Seq) (<-chan Change, error) {
	return s.feed.watch(ctx, after)
}

func (s *SQLiteStore) Caps() Capabilities {
	return Capabilities{Transactions: true, Watch: true, Durable: true}
}

func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Lock()
	if s.closed {
		s.state.Unlock()
		return nil
	}
	s.closed = true
	s.state.Unlock()
	s.feed.close()
	return s.db.Close()
}

func (s *SQLiteStore) Collections(ctx context.Context) ([]string, error) {
	if !s.open() {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT col FROM docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return out, rows.Err()
}

func (s *SQLiteStore) Import(ctx context.Context, col, id string, d Doc, rev Rev) error {
	if err := checkName(col, id); err != nil {
		return err
	}
	text, err := encodeDoc(Clone(d))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open() {
		return ErrClosed
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO docs (col, id, rev, doc) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, col, id, int64(rev), text)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

func encodeDoc(d Doc) (string, error) {
	b, err := json.Marshal(EncodeValue(d))
	return string(b), err
}

func decodeDoc(text string) (Doc, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var d map[string]any
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	return DecodeValue(d).(map[string]any), nil
}
