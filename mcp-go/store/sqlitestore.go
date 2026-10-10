// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

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
// Query narrows the rows in SQL where it can say exactly what Match would
// (sqlWhere: a field equal to a string, in a list of them, a list holding
// one; on indexed fields for the ones every room and share query names),
// then evaluates the Query in Go over what is left (Match, Run), as the
// folder and memory do, so the three cannot differ.
type SQLiteStore struct {
	path   string
	db     *sql.DB
	mu     sync.Mutex   // writes
	state  sync.RWMutex // closed
	closed bool
	feed   *feed
	stop   chan struct{} // ends the optimize loop
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
	{Version: 2, Note: "rooms' chat messages", Up: SQLExec(`
-- one row per message (chat.go): seq orders a room's messages, a reply
-- names its root's id in thread ("" at the top level); doc is the message
-- as JSON
CREATE TABLE chat_msgs (
  tenant TEXT NOT NULL,
  room   TEXT NOT NULL,
  seq    INTEGER NOT NULL,
  id     TEXT NOT NULL,
  thread TEXT NOT NULL,
  at     INTEGER NOT NULL,
  doc    TEXT NOT NULL,
  PRIMARY KEY (tenant, room, seq)
) WITHOUT ROWID;
CREATE UNIQUE INDEX chat_msgs_id ON chat_msgs (tenant, room, id);
CREATE INDEX chat_msgs_thread ON chat_msgs (tenant, room, thread, seq);`)},
	{Version: 3, Note: "indexes on the fields queries name", Up: SQLExec(`
CREATE INDEX docs_tenant ON docs (col, json_extract(doc, '$.tenant'));
CREATE INDEX docs_owner  ON docs (col, json_extract(doc, '$.owner'));
CREATE INDEX docs_room   ON docs (col, json_extract(doc, '$.room'));
CREATE INDEX docs_member ON docs (col, json_extract(doc, '$.member'));`)},
	{Version: 4, Note: "questionnaires' links, responses and counters", Additive: true, Up: SQLExec(`
-- forms.go: a way into a questionnaire, by the SHA-256 of its key (the
-- key itself is never kept)
CREATE TABLE form_links (
  hash    TEXT PRIMARY KEY,
  id      TEXT NOT NULL,
  deck    TEXT NOT NULL,
  file    TEXT NOT NULL,
  kind    TEXT NOT NULL,
  once    INTEGER NOT NULL,
  used    INTEGER NOT NULL,
  revoked INTEGER NOT NULL,
  created INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX form_links_deck ON form_links (deck, file);
-- one row per response, seq in the order they came; record and deltas
-- are JSON
CREATE TABLE form_responses (
  deck    TEXT NOT NULL,
  file    TEXT NOT NULL,
  seq     INTEGER NOT NULL,
  id      TEXT NOT NULL,
  version TEXT NOT NULL,
  at      INTEGER NOT NULL,
  link    TEXT NOT NULL,
  record  TEXT NOT NULL,
  deltas  TEXT NOT NULL,
  PRIMARY KEY (deck, file, seq)
) WITHOUT ROWID;
CREATE UNIQUE INDEX form_responses_id ON form_responses (deck, file, id);
-- the counters charts read: question "" counts responses, key "" answers
CREATE TABLE form_tally (
  deck TEXT NOT NULL,
  file TEXT NOT NULL,
  q    TEXT NOT NULL,
  key  TEXT NOT NULL,
  n    INTEGER NOT NULL,
  PRIMARY KEY (deck, file, q, key)
) WITHOUT ROWID;
-- the server's sealing key (the answer page's s value)
CREATE TABLE form_keys (
  name TEXT PRIMARY KEY,
  key  BLOB NOT NULL
) WITHOUT ROWID;`)},
}

// OpenSQLiteStore opens (or creates) the documents in the SQLite file at path.
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(abs, "analysis_limit(400)"))
	if err != nil {
		return nil, err
	}
	if _, err := Migrate(context.Background(), db, abs, SQLiteSchema, Build); err != nil {
		db.Close()
		return nil, err
	}
	s := &SQLiteStore{path: abs, db: db, feed: newFeed(4096), stop: make(chan struct{})}
	s.optimize()
	go s.optimizeLoop()
	return s, nil
}

// optimize refreshes the planner's statistics where they are missing or
// stale, so a query takes the field indexes rather than reading the whole
// collection: without them SQLite guesses, and guesses the primary key.
// analysis_limit keeps each run to a sample of rows.
func (s *SQLiteStore) optimize() {
	if _, err := s.db.Exec(`PRAGMA optimize=0x10002`); err != nil {
		log.Printf("sqlite optimize: %v", err)
	}
}

// how often a long-running server refreshes the statistics, as SQLite
// advises for long-lived connections
const optimizeEvery = 3 * time.Hour

func (s *SQLiteStore) optimizeLoop() {
	t := time.NewTicker(optimizeEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.optimize()
		}
	}
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

// openReadOnly opens the SQLite file at path for reading only, beside a
// process that writes it, and checks that it is at schema version want.
func openReadOnly(path string, want int) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(10000)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	have, err := SchemaVersion(context.Background(), db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if have != want {
		db.Close()
		return nil, fmt.Errorf("%s is at schema version %d, this build reads %d: start the server once to migrate it", path, have, want)
	}
	return db, nil
}

// OpenSQLiteReadOnly is sliqtly.db at path opened for reading only (for a
// backup taken beside the running server).
func OpenSQLiteReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return openReadOnly(abs, len(SQLiteSchema))
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
	sel, args := `SELECT id, rev, doc FROM docs WHERE col = ?`, []any{q.From}
	if w, a := sqlWhere(q.Where); w != "" {
		sel += " AND " + w
		args = append(args, a...)
	}
	rows, err := s.db.QueryContext(ctx, sel, args...)
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
	close(s.stop)
	s.feed.close()
	s.optimize()
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
