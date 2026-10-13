// SPDX-License-Identifier: AGPL-3.0-or-later

// The folder server's decks in a Git repository (git-storage DESIGN.md,
// phase 1): <data>/repo.git, a bare repository the server writes itself
// with Ranger's Git client (mcp-go/rgit). It is on when <data>/git.json
// says so (`sliqtly-server git init` writes it).
//
// sliqtly.db stays the live store: reads, writes, revisions and the change
// feed are its, as before. The repository is its history: every commit is
// a snapshot of what may be published with the decks, taken
//
//   - when the decks have been still for idleSeconds (60) after a change,
//   - at the latest maxMinutes (10) after the first change not committed,
//   - every 5 minutes when something changed that the feed does not tell
//     (a room's files),
//   - when the server stops, after the rooms have written their text.
//
// What goes in (gitSnapshot):
//
//	sliqtly.json                 the repository's format
//	README.md                    rooms and decks by name
//	.gitattributes
//	decks/<id>/deck.md           the share's md
//	decks/<id>/deck.json         name, theme, room, folder, created, source, files
//	decks/<id>/theme.css         the share's css, when it has one
//	decks/<id>/<path>            the deck's kept files (media/, data/, forms/…)
//	rooms/<id>.json              title, kind, archived, created, folders
//	rooms/<id>/files/<path>      the room's files
//	links/<id>.json              from, rel, to, created
//
// What stays out: who owns a deck, edit keys and their expiry, invitations,
// memberships, chat, form answers, users and tokens. Git keeps every
// version for good, so nothing personal or secret is put there. Files over
// maxFileMB (20) are not kept: in Git mode the folder refuses them
// (localBucket.Save), and ones kept before are left out of the snapshot
// and named in the log.

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/rgit"
	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	gitConfigFile = "git.json"
	gitRepoDir    = "repo.git"
	// GitHub refuses files over 100 MB, so no setting goes past it
	gitMaxFileCeiling = 100
	// the sha-256 → blob id of files already written (gitMirror.ids)
	gitBlobIndex = "sliqtly-blobs"
	// how often a snapshot is compared when the feed tells nothing
	gitSweep = 5 * time.Minute
)

// gitConfig is <data>/git.json.
type gitConfig struct {
	Enabled bool `json:"enabled"`
	// the branch commits go to (main)
	Branch string `json:"branch,omitempty"`
	// whose name a commit carries: "editor" (the people who edited, from
	// the rooms; the server's when none did) or "server"
	Author string `json:"author,omitempty"`
	// the committer, and the author when Author is "server"
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// files larger than this are not kept (MB, at most 100)
	MaxFileMB   int `json:"maxFileMB,omitempty"`
	IdleSeconds int `json:"idleSeconds,omitempty"`
	MaxMinutes  int `json:"maxMinutes,omitempty"`
}

func (c *gitConfig) defaults() {
	if c.Branch == "" {
		c.Branch = "main"
	}
	if c.Author == "" {
		c.Author = "editor"
	}
	if c.Name == "" {
		c.Name = "Sliqtly server"
	}
	if c.Email == "" {
		c.Email = "server@sliqtly.invalid"
	}
	if c.MaxFileMB <= 0 {
		c.MaxFileMB = 20
	}
	if c.IdleSeconds <= 0 {
		c.IdleSeconds = 60
	}
	if c.MaxMinutes <= 0 {
		c.MaxMinutes = 10
	}
}

func (c gitConfig) check() error {
	if c.Author != "editor" && c.Author != "server" {
		return fmt.Errorf("git.json: author is %q: editor or server", c.Author)
	}
	if c.MaxFileMB > gitMaxFileCeiling {
		return fmt.Errorf("git.json: maxFileMB %d is over %d, the most GitHub takes", c.MaxFileMB, gitMaxFileCeiling)
	}
	if strings.ContainsAny(c.Branch, " ~^:?*[\\") || strings.Contains(c.Branch, "..") {
		return fmt.Errorf("git.json: bad branch %q", c.Branch)
	}
	return nil
}

func (c gitConfig) maxFile() int64 { return int64(c.MaxFileMB) << 20 }

// readGitConfig reads <data>/git.json, defaults filled in; ok is false
// when there is none
func readGitConfig(data string) (c gitConfig, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(data, gitConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		c.defaults()
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, false, fmt.Errorf("%s: %w", gitConfigFile, err)
	}
	c.defaults()
	return c, true, c.check()
}

func writeGitConfig(data string, c gitConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(data, gitConfigFile+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(data, gitConfigFile))
}

// --- the mirror

// gitMirror commits snapshots of the folder to its repository.
type gitMirror struct {
	cfg   gitConfig
	repo  *rgit.Repo
	docs  *sql.DB
	blobs store.BlobStore
	logf  func(format string, args ...any)

	mu  sync.Mutex // one snapshot or commit at a time
	ids map[store.Hash]string

	edMu    sync.Mutex
	editors map[string][]string // deck → the names of who edited it since the last commit
}

// openGitMirror opens (makes, when there is none) the repository of the
// folder data, whose documents and files are in docs and blobs.
func openGitMirror(data string, cfg gitConfig, docs *sql.DB, blobs store.BlobStore) (*gitMirror, error) {
	repo, err := rgit.Init(filepath.Join(data, gitRepoDir), cfg.Branch)
	if err != nil {
		return nil, err
	}
	m := &gitMirror{cfg: cfg, repo: repo, docs: docs, blobs: blobs, logf: log.Printf, ids: map[store.Hash]string{}, editors: map[string][]string{}}
	m.readIDs()
	return m, nil
}

func (m *gitMirror) ref() string { return "refs/heads/" + m.cfg.Branch }

// Head is the branch's newest commit; "" before the first
func (m *gitMirror) Head() (string, error) { return m.repo.Ref(m.ref()) }

// noteEditor records that name edited the deck: the next commit of the
// deck carries the name (cfg.Author "editor")
func (m *gitMirror) noteEditor(deck, name string) {
	name = strings.TrimSpace(name)
	if m == nil || name == "" {
		return
	}
	m.edMu.Lock()
	defer m.edMu.Unlock()
	for _, n := range m.editors[deck] {
		if n == name {
			return
		}
	}
	m.editors[deck] = append(m.editors[deck], name)
}

// the blob ids of files written before, by their sha-256: a snapshot reads
// a file's bytes only the first time
func (m *gitMirror) readIDs() {
	b, err := os.ReadFile(filepath.Join(m.repo.Dir, gitBlobIndex))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if h, err := store.ParseHash(f[0]); err == nil && len(f[1]) == 40 {
			m.ids[h] = f[1]
		}
	}
}

func (m *gitMirror) keepIDs(add map[store.Hash]string) {
	if len(add) == 0 {
		return
	}
	f, err := os.OpenFile(filepath.Join(m.repo.Dir, gitBlobIndex), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	defer f.Close()
	var b strings.Builder
	for h, id := range add {
		fmt.Fprintf(&b, "%s %s\n", h, id)
	}
	f.WriteString(b.String())
}

// gitSnapshot is the repository's files as the folder has them now.
type gitSnapshot struct {
	Files   []rgit.Entry
	Decks   map[string]string // id → name
	Rooms   map[string]string // id → title
	TooBig  []string          // kept files left out for their size
	written map[store.Hash]string
}

// snapshot reads the folder in one transaction and writes the blobs the
// repository has not got yet (write false: computes their ids only)
func (m *gitMirror) snapshot(ctx context.Context, write bool) (*gitSnapshot, error) {
	tx, err := m.docs.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s := &gitSnapshot{Decks: map[string]string{}, Rooms: map[string]string{}, written: map[store.Hash]string{}}
	text := func(p string, b []byte) error {
		id := rgit.ID("blob", b)
		if write {
			var err error
			if id, err = m.repo.Put("blob", b); err != nil {
				return err
			}
		}
		s.Files = append(s.Files, rgit.Entry{Path: p, ID: id})
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT col, id, doc FROM docs WHERE col IN ('shares', 'rooms', 'links') ORDER BY col, id`)
	if err != nil {
		return nil, err
	}
	type docRow struct {
		col, id string
		doc     map[string]any
	}
	var docs []docRow
	for rows.Next() {
		var r docRow
		var raw string
		if err := rows.Scan(&r.col, &r.id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if !gitSafeID(r.id) {
			continue
		}
		d, err := decodeDocJSON(raw)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("%s/%s: %w", r.col, r.id, err)
		}
		r.doc = d
		docs = append(docs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range docs {
		switch r.col {
		case "shares":
			md, _ := r.doc["md"].(string)
			name, _ := r.doc["name"].(string)
			s.Decks[r.id] = name
			if err := text("decks/"+r.id+"/deck.md", []byte(md)); err != nil {
				return nil, err
			}
			if err := text("decks/"+r.id+"/deck.json", gitJSON(pick(r.doc, "name", "theme", "room", "folder", "created", "source", "deck"), deckFiles(r.doc["files"]))); err != nil {
				return nil, err
			}
			if css, _ := r.doc["css"].(string); css != "" {
				if err := text("decks/"+r.id+"/theme.css", []byte(css)); err != nil {
					return nil, err
				}
			}
		case "rooms":
			title, _ := r.doc["title"].(string)
			s.Rooms[r.id] = title
			if err := text("rooms/"+r.id+".json", gitJSON(pick(r.doc, "title", "kind", "archived", "created", "folders"), nil)); err != nil {
				return nil, err
			}
		case "links":
			// a recording's viewing link (localplay.go) is the server's
			// state, not the decks': only the links between things go
			if _, ok := r.doc["from"].(string); !ok {
				continue
			}
			if err := text("links/"+r.id+".json", gitJSON(pick(r.doc, "from", "rel", "to", "created"), nil)); err != nil {
				return nil, err
			}
		}
	}
	frows, err := tx.QueryContext(ctx, `SELECT path, hash, size FROM file_refs WHERE path >= 'rooms/' AND path < 'rooms0' OR path >= 'shares/' AND path < 'shares0' ORDER BY path`)
	if err != nil {
		return nil, err
	}
	type fileRow struct {
		path string
		hash store.Hash
		size int64
	}
	var files []fileRow
	for frows.Next() {
		var f fileRow
		var hb []byte
		if err := frows.Scan(&f.path, &hb, &f.size); err != nil {
			frows.Close()
			return nil, err
		}
		copy(f.hash[:], hb)
		files = append(files, f)
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return nil, err
	}
	for _, f := range files {
		p, ok := gitFilePath(f.path, s)
		if !ok {
			continue
		}
		if f.size > m.cfg.maxFile() {
			s.TooBig = append(s.TooBig, fmt.Sprintf("%s (%s)", f.path, sizeText(f.size)))
			continue
		}
		id, err := m.blobID(ctx, f.hash, write, s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.path, err)
		}
		s.Files = append(s.Files, rgit.Entry{Path: p, ID: id})
	}
	for _, f := range []struct {
		p string
		b []byte
	}{
		{"sliqtly.json", []byte("{\n  \"format\": 1\n}\n")},
		{".gitattributes", []byte("*.md text eol=lf\n*.json text eol=lf\n*.css text eol=lf\n*.csv text\n*.png binary\n*.jpg binary\n*.xlsx binary\n*.pptx binary\n")},
		{"README.md", gitReadme(s)},
	} {
		if err := text(f.p, f.b); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// a kept file's place in the repository: shares/<id>/x → decks/<id>/x,
// rooms/<id>/files/x as it is. The rooms' live state (.collab/) and files
// of decks that are gone stay out.
func gitFilePath(p string, s *gitSnapshot) (string, bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 3 || !gitSafeID(parts[1]) {
		return "", false
	}
	for _, part := range parts[2:] {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".collab") || strings.EqualFold(part, ".git") {
			return "", false
		}
	}
	switch parts[0] {
	case "shares":
		if _, ok := s.Decks[parts[1]]; !ok {
			return "", false
		}
		// deck.md, deck.json and theme.css are the deck's own
		if len(parts) == 3 && (parts[2] == "deck.md" || parts[2] == "deck.json" || parts[2] == "theme.css") {
			return "", false
		}
		return "decks/" + strings.Join(parts[1:], "/"), true
	case "rooms":
		if parts[2] != "files" || len(parts) < 4 {
			return "", false
		}
		return p, true
	}
	return "", false
}

// a document id that can be a file name in the repository as it is
func gitSafeID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 200 {
		return false
	}
	return !strings.ContainsAny(id, "/\\\x00")
}

// the blob id of a kept file's bytes, written to the repository when it
// has not got them
func (m *gitMirror) blobID(ctx context.Context, h store.Hash, write bool, s *gitSnapshot) (string, error) {
	if id, ok := m.ids[h]; ok && (!write || m.repo.Has(id)) {
		return id, nil
	}
	if id, ok := s.written[h]; ok {
		return id, nil
	}
	r, err := m.blobs.Open(ctx, h)
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return "", err
	}
	if store.HashOf(b) != h {
		return "", fmt.Errorf("blob %s reads back as other bytes", h)
	}
	id := rgit.ID("blob", b)
	if write {
		if id, err = m.repo.Put("blob", b); err != nil {
			return "", err
		}
		s.written[h] = id
	}
	return id, nil
}

// --- commits

// commit snapshots the folder and commits it when it differs from the
// branch's head; message "" says what changed. → the commit ("" when
// nothing changed)
func (m *gitMirror) commit(ctx context.Context, message string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.snapshot(ctx, true)
	if err != nil {
		return "", err
	}
	m.keepIDs(s.written)
	for h, id := range s.written {
		m.ids[h] = id
	}
	tree, err := m.repo.WriteTree(s.Files)
	if err != nil {
		return "", err
	}
	head, err := m.Head()
	if err != nil {
		return "", err
	}
	var before []rgit.Entry
	if head != "" {
		c, err := m.repo.ReadCommit(head)
		if err != nil {
			return "", err
		}
		if c.Tree == tree {
			return "", nil
		}
		if before, err = m.repo.ReadTree(c.Tree); err != nil {
			return "", err
		}
	}
	ch := gitChanges(before, s.Files)
	m.edMu.Lock()
	var names []string
	for _, d := range ch.decks {
		for _, n := range m.editors[d] {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	m.edMu.Unlock()
	if message == "" {
		if head == "" {
			message = "Import from SQLite"
		} else {
			message = ch.describe(s)
		}
	}
	now := time.Now()
	server := rgit.Ident{Name: m.cfg.Name, Email: m.cfg.Email, When: now}
	author := server
	if m.cfg.Author == "editor" && len(names) > 0 {
		author = rgit.Ident{Name: names[0], Email: m.cfg.Email, When: now}
		var trailers []string
		for _, n := range names[1:] {
			trailers = append(trailers, "Co-authored-by: "+n+" <"+m.cfg.Email+">")
		}
		if len(trailers) > 0 {
			message = strings.TrimRight(message, "\n") + "\n\n" + strings.Join(trailers, "\n")
		}
	}
	c := rgit.Commit{Tree: tree, Author: author, Committer: server, Message: strings.TrimRight(message, "\n") + "\n"}
	if head != "" {
		c.Parents = []string{head}
	}
	id, err := m.repo.WriteCommit(c)
	if err != nil {
		return "", err
	}
	if err := m.repo.SetRef(m.ref(), head, id); err != nil {
		return "", err
	}
	m.edMu.Lock()
	for _, d := range ch.decks {
		delete(m.editors, d)
	}
	m.edMu.Unlock()
	if len(s.TooBig) > 0 {
		m.logf("git: left out of %s, over %d MB: %s", short7(id), m.cfg.MaxFileMB, strings.Join(s.TooBig, ", "))
	}
	return id, nil
}

func short7(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// what a commit changes, by deck and room
type gitChangeSet struct {
	decks  []string // in id order
	byDeck map[string]*gitDeckChange
	other  int // changed paths outside decks (rooms, links)
}

type gitDeckChange struct {
	text, meta              bool
	added, changed, removed int
	created, deleted        bool
}

func gitChanges(before, after []rgit.Entry) *gitChangeSet {
	old := make(map[string]string, len(before))
	for _, e := range before {
		old[e.Path] = e.ID
	}
	now := make(map[string]string, len(after))
	for _, e := range after {
		now[e.Path] = e.ID
	}
	ch := &gitChangeSet{byDeck: map[string]*gitDeckChange{}}
	deck := func(p string) (string, string) {
		if !strings.HasPrefix(p, "decks/") {
			return "", ""
		}
		rest := strings.TrimPrefix(p, "decks/")
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			return "", ""
		}
		return rest[:i], rest[i+1:]
	}
	note := func(p string, was, is string) {
		if p == "README.md" {
			return
		}
		id, file := deck(p)
		if id == "" {
			ch.other++
			return
		}
		d := ch.byDeck[id]
		if d == nil {
			d = &gitDeckChange{}
			ch.byDeck[id] = d
			ch.decks = append(ch.decks, id)
		}
		switch {
		case file == "deck.md":
			d.text = true
			d.created = was == ""
			d.deleted = is == ""
		case file == "deck.json" || file == "theme.css":
			d.meta = true
		case was == "":
			d.added++
		case is == "":
			d.removed++
		default:
			d.changed++
		}
	}
	for p, id := range now {
		if old[p] != id {
			note(p, old[p], id)
		}
	}
	for p, id := range old {
		if _, ok := now[p]; !ok {
			note(p, id, "")
		}
	}
	sort.Strings(ch.decks)
	return ch
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// the commit's subject (and a line per deck when there are several)
func (ch *gitChangeSet) describe(s *gitSnapshot) string {
	var lines []string
	for _, id := range ch.decks {
		d := ch.byDeck[id]
		name := s.Decks[id]
		if name == "" {
			name = id
		}
		var what []string
		switch {
		case d.created:
			what = append(what, "added")
		case d.deleted:
			what = append(what, "removed")
		default:
			if d.text {
				what = append(what, "text changed")
			}
			if d.meta {
				what = append(what, "settings changed")
			}
		}
		if !d.deleted {
			if d.added > 0 {
				what = append(what, plural(d.added, "file", "files")+" added")
			}
			if d.changed > 0 {
				what = append(what, plural(d.changed, "file", "files")+" changed")
			}
			if d.removed > 0 {
				what = append(what, plural(d.removed, "file", "files")+" removed")
			}
		}
		lines = append(lines, fmt.Sprintf("Deck %q: %s", name, strings.Join(what, ", ")))
	}
	if ch.other > 0 {
		lines = append(lines, "Rooms and links: "+plural(ch.other, "file", "files")+" changed")
	}
	if len(lines) == 0 {
		return "Update"
	}
	if len(lines) == 1 {
		return lines[0]
	}
	return plural(len(ch.decks), "deck", "decks") + " changed\n\n" + strings.Join(lines, "\n")
}

// --- documents as files

// a document as kept in sliqtly.db, times as time.Time
func decodeDocJSON(raw string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	d, ok := store.DecodeValue(v).(map[string]any)
	if !ok {
		return nil, errors.New("not an object")
	}
	return d, nil
}

func pick(d map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := d[k]; ok && v != nil {
			if t, ok := v.(time.Time); ok {
				v = t.UTC().Format(time.RFC3339)
			}
			out[k] = v
		}
	}
	return out
}

// a deck's file entries as deck.json keeps them: path, type, size and
// version; the address is the server's and changes with it
func deckFiles(v any) []map[string]any {
	list, _ := v.([]any)
	out := []map[string]any{}
	for _, x := range list {
		f, ok := x.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, pick(f, "path", "type", "size", "sha"))
	}
	return out
}

// pretty, keys in order, ending in a newline
func gitJSON(d map[string]any, files []map[string]any) []byte {
	if files != nil {
		d["files"] = files
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(d)
	return b.Bytes()
}

func gitReadme(s *gitSnapshot) []byte {
	var b strings.Builder
	b.WriteString("# Sliqtly decks\n\nKept by a Sliqtly server. Each deck is in decks/<id>/: its text in deck.md, its name, theme and room in deck.json, its pictures and data beside them.\n")
	type row struct{ id, name string }
	sorted := func(m map[string]string) []row {
		var rs []row
		for id, n := range m {
			rs = append(rs, row{id, n})
		}
		sort.Slice(rs, func(i, j int) bool {
			if a, b := strings.ToLower(rs[i].name), strings.ToLower(rs[j].name); a != b {
				return a < b
			}
			return rs[i].id < rs[j].id
		})
		return rs
	}
	md := strings.NewReplacer("[", "\\[", "]", "\\]", "\n", " ")
	if len(s.Rooms) > 0 {
		b.WriteString("\n## Rooms\n\n")
		for _, r := range sorted(s.Rooms) {
			fmt.Fprintf(&b, "- %s (rooms/%s.json)\n", md.Replace(nonEmpty(r.name, r.id)), r.id)
		}
	}
	if len(s.Decks) > 0 {
		b.WriteString("\n## Decks\n\n")
		for _, d := range sorted(s.Decks) {
			fmt.Fprintf(&b, "- [%s](decks/%s/deck.md)\n", md.Replace(nonEmpty(d.name, d.id)), d.id)
		}
	}
	return []byte(b.String())
}

func nonEmpty(s, or string) string {
	if strings.TrimSpace(s) == "" {
		return or
	}
	return s
}

// --- checking

// gitVerify compares the branch's head with the folder: every object
// read back (and its id checked), every path and blob compared. → the
// differences, empty when the head is the folder as it is now
func (m *gitMirror) verify(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	head, err := m.Head()
	if err != nil {
		return nil, err
	}
	if head == "" {
		return nil, errors.New("the repository has no commits")
	}
	c, err := m.repo.ReadCommit(head)
	if err != nil {
		return nil, err
	}
	have, err := m.repo.ReadTree(c.Tree)
	if err != nil {
		return nil, err
	}
	in := map[string]string{}
	for _, e := range have {
		// reading checks the bytes against their id
		if _, _, err := m.repo.Get(e.ID); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Path, err)
		}
		in[e.Path] = e.ID
	}
	s, err := m.snapshot(ctx, false)
	if err != nil {
		return nil, err
	}
	var diff []string
	for _, e := range s.Files {
		switch was, ok := in[e.Path]; {
		case !ok:
			diff = append(diff, "not in the repository: "+e.Path)
		case was != e.ID:
			diff = append(diff, "differs: "+e.Path)
		}
		delete(in, e.Path)
	}
	for p := range in {
		diff = append(diff, "only in the repository: "+p)
	}
	sort.Strings(diff)
	return diff, nil
}

// --- running

// run commits the folder's changes until ctx ends: after idle seconds of
// quiet, at most maxMinutes after the first, and on a sweep for what the
// feed does not tell (a room's files)
func (m *gitMirror) run(ctx context.Context, feed store.Engine) {
	var changes <-chan store.Change
	if feed != nil {
		if ch, err := feed.Watch(ctx, feed.Head()); err == nil {
			changes = ch
		} else {
			m.logf("git: no change feed (%v); committing every %s", err, gitSweep)
		}
	}
	idle := time.Duration(m.cfg.IdleSeconds) * time.Second
	most := time.Duration(m.cfg.MaxMinutes) * time.Minute
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	sweep := time.NewTicker(gitSweep)
	defer sweep.Stop()
	var first time.Time
	commit := func() {
		first = time.Time{}
		timer.Stop()
		if id, err := m.commit(ctx, ""); err != nil {
			if ctx.Err() == nil {
				m.logf("git: commit: %v", err)
			}
		} else if id != "" {
			m.logf("git: committed %s", short7(id))
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			if c.Col != "shares" && c.Col != "rooms" && c.Col != "links" {
				continue
			}
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			wait := idle
			if left := first.Add(most).Sub(now); left < wait {
				wait = max(left, 0)
			}
			timer.Reset(wait)
		case <-timer.C:
			commit()
		case <-sweep.C:
			if first.IsZero() {
				commit()
			}
		}
	}
}

// start makes the first commit when the repository has none (the folder
// moving to Git, as `sliqtly-server git init` does it) and checks it
func (m *gitMirror) start(ctx context.Context) error {
	head, err := m.Head()
	if err != nil || head != "" {
		return err
	}
	id, err := m.commit(ctx, "")
	if err != nil {
		return fmt.Errorf("first commit: %w", err)
	}
	diff, err := m.verify(ctx)
	if err != nil {
		return fmt.Errorf("checking %s: %w", short7(id), err)
	}
	if len(diff) > 0 {
		return fmt.Errorf("%s does not hold the folder: %s", short7(id), strings.Join(diff, "; "))
	}
	m.logf("git: decks imported into %s as %s, read back and checked", m.repo.Dir, short7(id))
	return nil
}

// stop commits what is not committed yet, when the server stops (after
// the rooms have written their text)
func (m *gitMirror) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if id, err := m.commit(ctx, ""); err != nil {
		m.logf("git: last commit: %v", err)
	} else if id != "" {
		m.logf("git: committed %s", short7(id))
	}
}
