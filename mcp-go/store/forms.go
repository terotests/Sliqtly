// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// A deck's questionnaires (Sliqtly Curious): the ways in to each one, the
// responses it took and the counters charts read.
//
//	FormLink      one way in: a QR code's short code, an exported file's
//	              key, one person's link, or a viewer's results link. The
//	              key itself is never kept, only its SHA-256 (Hash), so a
//	              copy of the database opens no questionnaire. The link is
//	              the only thing tying a key to its deck and file.
//	FormResponse  one response as the model stored it (Record), with what
//	              it added to the counters (Deltas): taking it back takes
//	              back exactly that, whatever the form says by then.
//	TallyRow      a counter: question, key (an option, a value, a word; ""
//	              counts answers) and n. Question "" counts responses.
//
// What a form asks, which answers it takes and what they count is the
// model's (src/PresForm.rgr through rgr/Forms.rgr); this keeps the rows.
// SQLiteForms keeps them in sliqtly.db, MemForms in memory; both pass
// storetest.RunForms.

// FormLink is one way into a questionnaire.
type FormLink struct {
	Hash string `json:"-"`  // hex SHA-256 of the key
	ID   string `json:"id"` // the hash's first 16 hex digits, for the owner
	Deck string `json:"deck"`
	File string `json:"file"`
	// "code" (QR / short code), "token" (a long link or an exported file),
	// "person" (one person's link) or "results" (read the results)
	Kind    string `json:"kind"`
	Once    bool   `json:"once,omitempty"` // takes one response
	Used    bool   `json:"used,omitempty"`
	Revoked bool   `json:"revoked,omitempty"`
	Created int64  `json:"created"` // ms since 1970
}

// FormResponse is one stored response.
type FormResponse struct {
	ID      string `json:"id"`
	Deck    string `json:"deck"`
	File    string `json:"file"`
	Version string `json:"version"`
	At      int64  `json:"at"`
	// the link it came through (ID); "" when the form is anonymous
	Link   string `json:"link,omitempty"`
	Record string `json:"record"` // JSON object
	Deltas string `json:"deltas"` // JSON [[question, key, n], …]
}

// TallyRow is one counter.
type TallyRow struct {
	Q   string
	Key string
	N   int
}

var (
	// ErrFormFull: the questionnaire has all the responses it takes.
	ErrFormFull = errors.New("store: the questionnaire takes no more responses")
	// ErrLinkUsed: a one-response link that was used already.
	ErrLinkUsed = errors.New("store: this link was used already")
)

// Forms keeps questionnaires' links, responses and counters.
type Forms interface {
	AddLink(ctx context.Context, l FormLink) error
	// Link by the key's hash; ErrNotFound when there is none.
	Link(ctx context.Context, hash string) (FormLink, error)
	Links(ctx context.Context, deck, file string) ([]FormLink, error)
	// SetRevoked turns the deck's link (by ID) off or on again.
	SetRevoked(ctx context.Context, deck, id string, revoked bool) error
	// Submit stores r and adds its deltas, in one step: ErrFormFull when
	// limit (> 0) responses are there already; ErrLinkUsed when the link
	// (by hash, "" for none) takes one response and took it. The link is
	// then marked used.
	Submit(ctx context.Context, r FormResponse, limit int, linkHash string) (FormResponse, error)
	// Remove takes a response back, its deltas with it.
	Remove(ctx context.Context, deck, file, id string) error
	Tally(ctx context.Context, deck, file string) ([]TallyRow, error)
	Responses(ctx context.Context, deck, file string) ([]FormResponse, error)
	// Key is the server's sealing key (32 bytes), made on first use.
	Key(ctx context.Context) ([]byte, error)
	// DropDeck forgets everything of a deck that is removed.
	DropDeck(ctx context.Context, deck string) error
}

// ParseDeltas reads a response's deltas.
func ParseDeltas(text string) ([]TallyRow, error) {
	var raw [][]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("store: deltas: %w", err)
	}
	out := make([]TallyRow, 0, len(raw))
	for _, r := range raw {
		if len(r) != 3 {
			return nil, errors.New("store: a delta is [question, key, n]")
		}
		q, ok1 := r[0].(string)
		k, ok2 := r[1].(string)
		n, ok3 := r[2].(float64)
		if !ok1 || !ok2 || !ok3 {
			return nil, errors.New("store: a delta is [question, key, n]")
		}
		out = append(out, TallyRow{Q: q, Key: k, N: int(n)})
	}
	return out, nil
}

// TallyJSON writes counters as the model reads them: [[q, key, n], …].
func TallyJSON(rows []TallyRow) string {
	raw := make([][]any, len(rows))
	for i, r := range rows {
		raw[i] = []any{r.Q, r.Key, r.N}
	}
	b, _ := json.Marshal(raw)
	return string(b)
}

func checkResponse(r FormResponse) ([]TallyRow, error) {
	if r.Deck == "" || r.File == "" {
		return nil, errors.New("store: a response needs its deck and file")
	}
	if !json.Valid([]byte(r.Record)) {
		return nil, errors.New("store: a response's record is JSON")
	}
	return ParseDeltas(r.Deltas)
}

func sortTally(rows []TallyRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Q != rows[j].Q {
			return rows[i].Q < rows[j].Q
		}
		return rows[i].Key < rows[j].Key
	})
}

func formNow() int64 { return time.Now().UnixMilli() }

// --- in memory

// MemForms is Forms in memory.
type MemForms struct {
	mu        sync.Mutex
	links     map[string]FormLink
	responses map[string][]FormResponse // deck\x00file → in order
	tally     map[string]map[[2]string]int
	key       []byte
}

func NewMemForms() *MemForms {
	return &MemForms{links: map[string]FormLink{}, responses: map[string][]FormResponse{}, tally: map[string]map[[2]string]int{}}
}

func formKey(deck, file string) string { return deck + "\x00" + file }

func (m *MemForms) AddLink(_ context.Context, l FormLink) error {
	if l.Hash == "" || l.Deck == "" || l.File == "" {
		return errors.New("store: a link needs its hash, deck and file")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[l.Hash]; ok {
		return ErrConflict
	}
	m.links[l.Hash] = l
	return nil
}

func (m *MemForms) Link(_ context.Context, hash string) (FormLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[hash]
	if !ok {
		return FormLink{}, ErrNotFound
	}
	return l, nil
}

func (m *MemForms) Links(_ context.Context, deck, file string) ([]FormLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FormLink
	for _, l := range m.links {
		if l.Deck == deck && (file == "" || l.File == file) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemForms) SetRevoked(_ context.Context, deck, id string, revoked bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, l := range m.links {
		if l.Deck == deck && l.ID == id {
			l.Revoked = revoked
			m.links[h] = l
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemForms) Submit(_ context.Context, r FormResponse, limit int, linkHash string) (FormResponse, error) {
	deltas, err := checkResponse(r)
	if err != nil {
		return r, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := formKey(r.Deck, r.File)
	if limit > 0 && len(m.responses[k]) >= limit {
		return r, ErrFormFull
	}
	if linkHash != "" {
		l, ok := m.links[linkHash]
		if !ok {
			return r, ErrNotFound
		}
		if l.Once && l.Used {
			return r, ErrLinkUsed
		}
		l.Used = true
		m.links[linkHash] = l
	}
	if r.ID == "" {
		r.ID = newID()
	}
	r.At = formNow()
	m.responses[k] = append(m.responses[k], r)
	m.add(k, deltas, 1)
	return r, nil
}

func (m *MemForms) add(k string, deltas []TallyRow, sign int) {
	t := m.tally[k]
	if t == nil {
		t = map[[2]string]int{}
		m.tally[k] = t
	}
	for _, d := range deltas {
		key := [2]string{d.Q, d.Key}
		if n := t[key] + sign*d.N; n > 0 {
			t[key] = n
		} else {
			delete(t, key)
		}
	}
}

func (m *MemForms) Remove(_ context.Context, deck, file, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := formKey(deck, file)
	list := m.responses[k]
	for i, r := range list {
		if r.ID == id {
			deltas, err := ParseDeltas(r.Deltas)
			if err != nil {
				return err
			}
			m.responses[k] = append(list[:i:i], list[i+1:]...)
			m.add(k, deltas, -1)
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemForms) Tally(_ context.Context, deck, file string) ([]TallyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TallyRow
	for key, n := range m.tally[formKey(deck, file)] {
		out = append(out, TallyRow{Q: key[0], Key: key[1], N: n})
	}
	sortTally(out)
	return out, nil
}

func (m *MemForms) Responses(_ context.Context, deck, file string) ([]FormResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]FormResponse(nil), m.responses[formKey(deck, file)]...), nil
}

func (m *MemForms) Key(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.key == nil {
		m.key = make([]byte, 32)
		if _, err := rand.Read(m.key); err != nil {
			return nil, err
		}
	}
	return append([]byte(nil), m.key...), nil
}

func (m *MemForms) DropDeck(_ context.Context, deck string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, l := range m.links {
		if l.Deck == deck {
			delete(m.links, h)
		}
	}
	prefix := deck + "\x00"
	for k := range m.responses {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(m.responses, k)
		}
	}
	for k := range m.tally {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(m.tally, k)
		}
	}
	return nil
}

// --- SQLite

// SQLiteForms is Forms in sliqtly.db (form_links, form_responses,
// form_tally, form_keys).
type SQLiteForms struct {
	s  *SQLiteStore
	mu sync.Mutex // this process's writes in order
}

func NewSQLiteForms(s *SQLiteStore) *SQLiteForms { return &SQLiteForms{s: s} }

// run fn in one transaction, the store held open
func (f *SQLiteForms) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.s.state.RLock()
	defer f.s.state.RUnlock()
	if f.s.closed {
		return ErrClosed
	}
	tx, err := f.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (f *SQLiteForms) AddLink(ctx context.Context, l FormLink) error {
	if l.Hash == "" || l.Deck == "" || l.File == "" {
		return errors.New("store: a link needs its hash, deck and file")
	}
	return f.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO form_links (hash, id, deck, file, kind, once, used, revoked, created)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (hash) DO NOTHING`,
			l.Hash, l.ID, l.Deck, l.File, l.Kind, b2i(l.Once), b2i(l.Used), b2i(l.Revoked), l.Created)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrConflict
		}
		return nil
	})
}

const linkCols = `hash, id, deck, file, kind, once, used, revoked, created`

type rowScanner interface{ Scan(dest ...any) error }

func scanLink(r rowScanner) (FormLink, error) {
	var l FormLink
	var once, used, revoked int
	err := r.Scan(&l.Hash, &l.ID, &l.Deck, &l.File, &l.Kind, &once, &used, &revoked, &l.Created)
	l.Once, l.Used, l.Revoked = once == 1, used == 1, revoked == 1
	return l, err
}

func (f *SQLiteForms) Link(ctx context.Context, hash string) (FormLink, error) {
	if !f.s.open() {
		return FormLink{}, ErrClosed
	}
	l, err := scanLink(f.s.db.QueryRowContext(ctx, `SELECT `+linkCols+` FROM form_links WHERE hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return FormLink{}, ErrNotFound
	}
	return l, err
}

func (f *SQLiteForms) Links(ctx context.Context, deck, file string) ([]FormLink, error) {
	if !f.s.open() {
		return nil, ErrClosed
	}
	rows, err := f.s.db.QueryContext(ctx, `SELECT `+linkCols+` FROM form_links WHERE deck = ? AND (? = '' OR file = ?) ORDER BY created, id`, deck, file, file)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FormLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (f *SQLiteForms) SetRevoked(ctx context.Context, deck, id string, revoked bool) error {
	return f.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE form_links SET revoked = ? WHERE deck = ? AND id = ?`, b2i(revoked), deck, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func addTally(ctx context.Context, tx *sql.Tx, deck, file string, deltas []TallyRow, sign int) error {
	for _, d := range deltas {
		if _, err := tx.ExecContext(ctx, `INSERT INTO form_tally (deck, file, q, key, n) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (deck, file, q, key) DO UPDATE SET n = n + excluded.n`, deck, file, d.Q, d.Key, sign*d.N); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM form_tally WHERE deck = ? AND file = ? AND n <= 0`, deck, file)
	return err
}

func (f *SQLiteForms) Submit(ctx context.Context, r FormResponse, limit int, linkHash string) (FormResponse, error) {
	deltas, err := checkResponse(r)
	if err != nil {
		return r, err
	}
	err = f.tx(ctx, func(tx *sql.Tx) error {
		if limit > 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM form_responses WHERE deck = ? AND file = ?`, r.Deck, r.File).Scan(&n); err != nil {
				return err
			}
			if n >= limit {
				return ErrFormFull
			}
		}
		if linkHash != "" {
			l, err := scanLink(tx.QueryRowContext(ctx, `SELECT `+linkCols+` FROM form_links WHERE hash = ?`, linkHash))
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if l.Once && l.Used {
				return ErrLinkUsed
			}
			if _, err := tx.ExecContext(ctx, `UPDATE form_links SET used = 1 WHERE hash = ?`, linkHash); err != nil {
				return err
			}
		}
		if r.ID == "" {
			r.ID = newID()
		}
		r.At = formNow()
		if _, err := tx.ExecContext(ctx, `INSERT INTO form_responses (deck, file, seq, id, version, at, link, record, deltas)
VALUES (?, ?, (SELECT coalesce(max(seq), 0) + 1 FROM form_responses WHERE deck = ? AND file = ?), ?, ?, ?, ?, ?, ?)`,
			r.Deck, r.File, r.Deck, r.File, r.ID, r.Version, r.At, r.Link, r.Record, r.Deltas); err != nil {
			return err
		}
		return addTally(ctx, tx, r.Deck, r.File, deltas, 1)
	})
	return r, err
}

func (f *SQLiteForms) Remove(ctx context.Context, deck, file, id string) error {
	return f.tx(ctx, func(tx *sql.Tx) error {
		var text string
		err := tx.QueryRowContext(ctx, `SELECT deltas FROM form_responses WHERE deck = ? AND file = ? AND id = ?`, deck, file, id).Scan(&text)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		deltas, err := ParseDeltas(text)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM form_responses WHERE deck = ? AND file = ? AND id = ?`, deck, file, id); err != nil {
			return err
		}
		return addTally(ctx, tx, deck, file, deltas, -1)
	})
}

func (f *SQLiteForms) Tally(ctx context.Context, deck, file string) ([]TallyRow, error) {
	if !f.s.open() {
		return nil, ErrClosed
	}
	rows, err := f.s.db.QueryContext(ctx, `SELECT q, key, n FROM form_tally WHERE deck = ? AND file = ? ORDER BY q, key`, deck, file)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TallyRow
	for rows.Next() {
		var t TallyRow
		if err := rows.Scan(&t.Q, &t.Key, &t.N); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (f *SQLiteForms) Responses(ctx context.Context, deck, file string) ([]FormResponse, error) {
	if !f.s.open() {
		return nil, ErrClosed
	}
	rows, err := f.s.db.QueryContext(ctx, `SELECT id, version, at, link, record, deltas FROM form_responses WHERE deck = ? AND file = ? ORDER BY seq`, deck, file)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FormResponse
	for rows.Next() {
		r := FormResponse{Deck: deck, File: file}
		if err := rows.Scan(&r.ID, &r.Version, &r.At, &r.Link, &r.Record, &r.Deltas); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (f *SQLiteForms) Key(ctx context.Context) ([]byte, error) {
	var key []byte
	err := f.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT key FROM form_keys WHERE name = 'seal'`).Scan(&key)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO form_keys (name, key) VALUES ('seal', ?)`, key)
		return err
	})
	return key, err
}

func (f *SQLiteForms) DropDeck(ctx context.Context, deck string) error {
	return f.tx(ctx, func(tx *sql.Tx) error {
		for _, t := range []string{"form_links", "form_responses", "form_tally"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE deck = ?`, deck); err != nil {
				return err
			}
		}
		return nil
	})
}
