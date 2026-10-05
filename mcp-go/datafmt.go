// SPDX-License-Identifier: AGPL-3.0-or-later

// The data folder's format, and the migrations that bring an older one up
// to it. A server started on a folder (prepareData):
//
//  1. locks it (<root>/.lock), so a second server on the same folder stops
//     instead of writing beside the first
//  2. reads <root>/format.json; a folder with no such file and no decks is
//     new and gets the current format at once, one with decks and no file
//     is format 1
//  3. refuses a folder in a NEWER format than it knows: an older server
//     reading a newer layout would see no decks and write beside them
//  4. for an older format: a backup first, then each migration in order,
//     each one recorded in format.json when it is done
//
// The backup is <root>/backups/<time>-format-<n>/: every file of db/ and
// files/ hard-linked, which takes no room, and from format 4 on a copy of
// sliqtly.db. It stays a true copy: the store
// never writes into a file, it writes a new one and renames it over the old
// (writeAtomic), and a migration only renames, so the backup's links keep
// the old contents. The three newest backups are kept.
//
// A migration must be safe to stop at any point and run again: it renames
// one entry at a time, never copies and deletes, and skips what is already
// in place. Anything it would overwrite is moved to backups/conflicts/
// instead. After it, the number of files must be the number before plus
// the new ones it says it made, or the server stops with the folder as it
// is and the backup beside it.

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the layout this server reads and writes
const currentFormat = 4

type formatFile struct {
	Format int `json:"format"`
	// the server version that last wrote this file
	Server string `json:"server,omitempty"`
	// what was done to the folder, oldest first
	History []formatStep `json:"history,omitempty"`
}

type formatStep struct {
	Format int    `json:"format"`
	At     string `json:"at"`
	Server string `json:"server"`
	Note   string `json:"note,omitempty"`
}

// a migration from format From to From+1. user owns the documents that
// name no owner. → the files it made
type migration struct {
	From int
	Note string
	Run  func(root, user string) (int, error)
}

var migrations = []migration{
	{From: 1, Note: "decks and files in 256 shard folders", Run: func(root, _ string) (int, error) { return 0, shardFolders(root) }},
	{From: 2, Note: "rooms General and Playground, every deck in General", Run: homeRooms},
	{From: 3, Note: "documents and files into sliqtly.db and blobs.db (SQLite)", Run: intoSQLite},
}

func readFormat(root string) (*formatFile, error) {
	b, err := os.ReadFile(filepath.Join(root, "format.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f formatFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("format.json: %w", err)
	}
	if f.Format < 1 {
		return nil, fmt.Errorf("format.json: no format")
	}
	return &f, nil
}

func writeFormat(root string, f *formatFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(root, "format.json"), append(b, '\n'))
}

// dataFormat is the folder's format: format.json's, else 1 when it has
// decks or files and the current one when it has nothing yet
func dataFormat(root string) (int, error) {
	f, err := readFormat(root)
	if err != nil {
		return 0, err
	}
	if f != nil {
		return f.Format, nil
	}
	n, err := countFiles(root)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return 1, nil
	}
	return currentFormat, nil
}

// countFiles counts the regular files under db/ and files/, leaving out a
// write's temporary file
func countFiles(root string) (int, error) {
	n := 0
	for _, d := range []string{"db", "files"} {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e os.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".tmp-") {
				n++
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}

// prepareData locks the folder and brings it to the current format. The
// lock is held until the returned release is called (the process's end).
// user owns the decks that name no owner; say is told what is being done,
// for the log.
func prepareData(root, server, user string, say func(string)) (release func(), err error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	unlock, err := lockFolder(filepath.Join(abs, ".lock"))
	if err != nil {
		return nil, fmt.Errorf("%s is in use by another server: %w", abs, err)
	}
	defer func() {
		if err != nil {
			unlock()
		}
	}()
	f, err := readFormat(abs)
	if err != nil {
		return nil, err
	}
	have, err := dataFormat(abs)
	if err != nil {
		return nil, err
	}
	if have > currentFormat {
		return nil, fmt.Errorf("%s is in data format %d, written by a newer server (%s); this one (%s) reads %d. Install that version again", abs, have, serverOf(f), server, currentFormat)
	}
	if f == nil {
		f = &formatFile{Format: have}
		if have == currentFormat {
			f.History = append(f.History, formatStep{Format: have, At: now(), Server: server, Note: "new folder"})
		}
	}
	if have < currentFormat {
		before, err := countFiles(abs)
		if err != nil {
			return nil, err
		}
		// set aside by an earlier run of a migration that was stopped
		aside, err := countConflicts(abs)
		if err != nil {
			return nil, err
		}
		before += aside
		dir, err := backupFolder(abs, have)
		if err != nil {
			return nil, fmt.Errorf("backup before migrating: %w", err)
		}
		say(fmt.Sprintf("backup of format %d (%d files) in %s", have, before, dir))
		for _, m := range migrations {
			if m.From < have {
				continue
			}
			say(fmt.Sprintf("migrating %s from format %d to %d: %s", abs, m.From, m.From+1, m.Note))
			made, err := m.Run(abs, user)
			if err != nil {
				return nil, fmt.Errorf("migration %d→%d: %w (the folder before it is in %s)", m.From, m.From+1, err, dir)
			}
			before += made
			after, err := countFiles(abs)
			if err != nil {
				return nil, err
			}
			moved, err := countConflicts(abs)
			if err != nil {
				return nil, err
			}
			if after+moved != before {
				return nil, fmt.Errorf("migration %d→%d: %d files before, %d after: stopped (the folder before it is in %s)", m.From, m.From+1, before, after+moved, dir)
			}
			f.Format = m.From + 1
			f.History = append(f.History, formatStep{Format: f.Format, At: now(), Server: server, Note: m.Note})
			f.Server = server
			if err := writeFormat(abs, f); err != nil {
				return nil, err
			}
		}
		if f.Format != currentFormat {
			return nil, fmt.Errorf("no migration from format %d", f.Format)
		}
	}
	if f.Server != server || !hasFormatFile(abs) {
		f.Server = server
		if err := writeFormat(abs, f); err != nil {
			return nil, err
		}
	}
	return unlock, nil
}

func hasFormatFile(root string) bool {
	_, err := os.Stat(filepath.Join(root, "format.json"))
	return err == nil
}

func serverOf(f *formatFile) string {
	if f == nil || f.Server == "" {
		return "unknown"
	}
	return f.Server
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// ------------------------------------------------------------- backups --

const keepBackups = 3

// backupFolder hard-links every file of db/ and files/ (and format.json)
// into backups/<time>-format-<n>/, copying where a link cannot be made,
// and drops the oldest backups past keepBackups
func backupFolder(root string, format int) (string, error) {
	base := filepath.Join(root, "backups")
	dir := filepath.Join(base, time.Now().UTC().Format("20060102T150405Z")+fmt.Sprintf("-format-%d", format))
	for _, d := range []string{"db", "files", "format.json"} {
		src := filepath.Join(root, d)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(src, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			dst := filepath.Join(dir, rel)
			if e.IsDir() {
				return os.MkdirAll(dst, 0o750)
			}
			if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".tmp-") {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				return err
			}
			if os.Link(p, dst) == nil {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return writeAtomic(dst, b)
		})
		if err != nil {
			return "", err
		}
	}
	// from format 4 on the documents are a database written in place: a
	// link would not keep the old contents, so it is copied (VACUUM INTO).
	// blobs.db's rows are never rewritten, only added and collected; a
	// migration that changes it backs it up itself (store.Migrate).
	if _, err := os.Stat(filepath.Join(root, docsFile)); err == nil {
		if err := vacuumInto(filepath.Join(root, docsFile), filepath.Join(dir, docsFile)); err != nil {
			return "", err
		}
	}
	entries, _ := os.ReadDir(base)
	var old []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "conflicts" {
			old = append(old, e.Name())
		}
	}
	sort.Strings(old)
	for len(old) > keepBackups {
		os.RemoveAll(filepath.Join(base, old[0]))
		old = old[1:]
	}
	return dir, nil
}

// the files a migration set aside rather than overwrite
func countConflicts(root string) (int, error) {
	n := 0
	err := filepath.WalkDir(filepath.Join(root, "backups", "conflicts"), func(p string, e os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if e.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n, err
}

// moveInto renames src to dst. Something already at dst is not
// overwritten: src goes to backups/conflicts/ instead, under its path in
// the folder, and the server's log says so.
func moveInto(root, src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		rel, _ := filepath.Rel(root, src)
		aside := filepath.Join(root, "backups", "conflicts", time.Now().UTC().Format("20060102T150405Z"), rel)
		if err := os.MkdirAll(filepath.Dir(aside), 0o750); err != nil {
			return err
		}
		return os.Rename(src, aside)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// ---------------------------------------------------------- migrations --

// 1 → 2: db/<col>/<id>.json → db/<col>/<sh>/<id>.json, and
// files/<top>/<name>/… → files/<top>/<sh>/<name>/… (a single file directly
// under <top> moves the same way). What is already a shard folder stays,
// which is what makes a second run continue where the first stopped.
func shardFolders(root string) error {
	db := filepath.Join(root, "db")
	cols, err := os.ReadDir(db)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, c := range cols {
		if !c.IsDir() {
			continue
		}
		dir := filepath.Join(db, c.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
				continue
			}
			id := strings.TrimSuffix(name, ".json")
			if err := moveInto(root, filepath.Join(dir, name), filepath.Join(dir, shard(id), name)); err != nil {
				return err
			}
		}
	}
	files := filepath.Join(root, "files")
	tops, err := os.ReadDir(files)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, t := range tops {
		if !t.IsDir() {
			continue
		}
		dir := filepath.Join(files, t.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if (e.IsDir() && isShard(name)) || strings.HasPrefix(name, ".tmp-") {
				continue
			}
			// a kept file's type rides with it: x and x.type share x's shard
			key := strings.TrimSuffix(name, ".type")
			if !e.IsDir() && key != name {
				if _, err := os.Stat(filepath.Join(dir, key)); err != nil {
					key = name
				}
			}
			if err := moveInto(root, filepath.Join(dir, name), filepath.Join(dir, shard(key), name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// 2 → 3: the rooms General and Playground (ADR 0001), owned by the
// server's user, and every deck in General. A deck does not see the room's files (inherit_room_files is
// false), and its address does not change. The decks are written in place,
// a new file renamed over each (store.WriteAtomic), so the backup keeps
// the old ones.
func homeRooms(root, user string) (int, error) {
	fs, err := store.NewFileStore(filepath.Join(root, "db"))
	if err != nil {
		return 0, err
	}
	defer fs.Close()
	return store.HomeAll(context.Background(), fs, "shares", localTenant, user, time.Now())
}

// ------------------------------------------------------ 3 → 4: SQLite --

// 3 → 4: every document of db/ into sliqtly.db and every file of files/
// into blobs.db with its path in sliqtly.db's file_refs (a room's chat log
// into file_lines). The databases are built beside the folder as
// *.migrating, checked against it (the documents' digests, each file's
// SHA-256, every blob read back), and renamed into place, sliqtly.db last:
// its name is the migration's commit point. Only then are db/ and files/
// removed; the backup made before the migration keeps them. A run stopped
// before the rename starts over; one stopped after it only finishes the
// removal. → the files it made, less the ones it removed
func intoSQLite(root, _ string) (int, error) {
	ctx := context.Background()
	docsPath, blobsPath := filepath.Join(root, docsFile), filepath.Join(root, blobsFile)
	if _, err := os.Stat(docsPath); errors.Is(err, os.ErrNotExist) {
		if err := buildSQLite(ctx, root, docsPath, blobsPath); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	n, err := countFiles(root)
	if err != nil {
		return 0, err
	}
	for _, d := range []string{"db", "files"} {
		if err := os.RemoveAll(filepath.Join(root, d)); err != nil {
			return 0, err
		}
	}
	return -n, syncDirOf(root)
}

const migratingSuffix = ".migrating"

func buildSQLite(ctx context.Context, root, docsPath, blobsPath string) error {
	tmpDocs, tmpBlobs := docsPath+migratingSuffix, blobsPath+migratingSuffix
	for _, p := range []string{tmpDocs, tmpBlobs} {
		for _, x := range []string{"", "-wal", "-shm", "-journal"} {
			if err := os.Remove(p + x); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	docs, err := store.OpenSQLiteStore(tmpDocs)
	if err != nil {
		return err
	}
	blobs, err := store.OpenSQLiteBlobStore(tmpBlobs)
	if err != nil {
		docs.Close()
		return err
	}
	err = fillSQLite(ctx, root, docs, blobs)
	for _, db := range []*sql.DB{docs.DB(), blobs.DB()} {
		if _, cerr := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err == nil && cerr != nil {
			err = cerr
		}
	}
	if cerr := blobs.Close(); err == nil {
		err = cerr
	}
	if cerr := docs.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// closed and checkpointed: each database is its one file now
	for _, p := range []string{tmpDocs, tmpBlobs} {
		if st, err := os.Stat(p + "-wal"); err == nil && st.Size() > 0 {
			return fmt.Errorf("%s-wal was left with %d bytes", p, st.Size())
		}
		os.Remove(p + "-wal")
		os.Remove(p + "-shm")
	}
	if err := os.Rename(tmpBlobs, blobsPath); err != nil {
		return err
	}
	if err := syncDirOf(root); err != nil {
		return err
	}
	if err := os.Rename(tmpDocs, docsPath); err != nil {
		return err
	}
	return syncDirOf(root)
}

// the folder's documents and files into the two databases, and checked
func fillSQLite(ctx context.Context, root string, docs *store.SQLiteStore, blobs *store.SQLiteBlobStore) error {
	src, err := store.NewFileStore(filepath.Join(root, "db"))
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := store.Copy(ctx, docs, src); err != nil {
		return err
	}
	if err := store.Verify(ctx, docs, src); err != nil {
		return err
	}
	refs := store.NewFileRefs(docs)
	files := filepath.Join(root, "files")
	want := map[string]store.Hash{}
	lines := map[string]int{}
	err = filepath.WalkDir(files, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".tmp-") || strings.HasSuffix(name, ".type") {
			return nil
		}
		rel, _ := filepath.Rel(files, p)
		logical := unshardedPath(filepath.ToSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// a room's chat: one JSON line per message
		if strings.HasSuffix(logical, "/.collab/chat.jsonl") {
			for _, l := range strings.Split(string(b), "\n") {
				if strings.TrimSpace(l) == "" {
					continue
				}
				if err := refs.AppendLine(ctx, logical, l); err != nil {
					return err
				}
				lines[logical]++
			}
			return nil
		}
		ct, _ := os.ReadFile(p + ".type")
		info, err := blobs.Put(ctx, bytes.NewReader(b), string(ct))
		if err != nil {
			return err
		}
		if err := refs.Set(ctx, logical, info, string(ct)); err != nil {
			return err
		}
		want[logical] = store.HashOf(b)
		return nil
	})
	if err != nil {
		return err
	}
	// every path where the bucket will look, with the bytes it had
	got, err := refs.List(ctx, "")
	if err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("%d files, %d in sliqtly.db", len(want), len(got))
	}
	for _, r := range got {
		if h, ok := want[r.Path]; !ok || h != r.Hash {
			return fmt.Errorf("%s: not copied as it was", r.Path)
		}
	}
	for p, n := range lines {
		l, err := refs.Lines(ctx, p, 0)
		if err != nil {
			return err
		}
		if len(l) != n {
			return fmt.Errorf("%s: %d lines, %d copied", p, n, len(l))
		}
	}
	if _, bad, err := store.VerifyBlobs(ctx, blobs); err != nil || len(bad) > 0 {
		return fmt.Errorf("blobs read back wrong: %v %v", bad, err)
	}
	return nil
}

// unshardedPath takes the shard folder out of a kept file's path inside
// files/ (format 2 and 3): shares/<sh>/{id}/media/x → shares/{id}/media/x.
// A path with no shard in that place (a room's chat log was kept without
// one) stays as it is.
func unshardedPath(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) == 3 && isShard(parts[1]) {
		return parts[0] + "/" + parts[2]
	}
	return rel
}

func vacuumInto(src, dst string) error {
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM INTO ?`, dst)
	return err
}

func syncDirOf(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return nil
}
