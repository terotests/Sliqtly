// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// a data folder as the server keeps it: sliqtly.db and blobs.db
type dataFolder struct {
	dir   string
	docs  *store.SQLiteStore
	refs  *store.FileRefs
	blobs *store.SQLiteBlobStore
}

func newDataFolder(t *testing.T) *dataFolder {
	t.Helper()
	dir := t.TempDir()
	docs, err := store.OpenSQLiteStore(filepath.Join(dir, "sliqtly.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := store.OpenSQLiteBlobStore(filepath.Join(dir, "blobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { docs.Close(); blobs.Close() })
	os.WriteFile(filepath.Join(dir, "format.json"), []byte(`{"format":4}`+"\n"), 0o640)
	return &dataFolder{dir: dir, docs: docs, refs: store.NewFileRefs(docs), blobs: blobs}
}

func (d *dataFolder) save(t *testing.T, path string, data []byte) {
	t.Helper()
	ctx := context.Background()
	info, err := d.blobs.Put(ctx, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.refs.Set(ctx, path, info, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
}

func (d *dataFolder) deck(t *testing.T, id string, n int) {
	t.Helper()
	if _, err := store.Put(context.Background(), d.docs, "shares", id, store.Doc{"name": id, "n": int64(n)}, store.AnyRev); err != nil {
		t.Fatal(err)
	}
}

// the source as a backup run beside the server opens it: read-only
func (d *dataFolder) source(t *testing.T) store.BackupSource {
	t.Helper()
	docs, err := store.OpenSQLiteReadOnly(filepath.Join(d.dir, "sliqtly.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := store.OpenSQLiteBlobStoreReadOnly(filepath.Join(d.dir, "blobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { docs.Close(); blobs.Close() })
	format, _ := os.ReadFile(filepath.Join(d.dir, "format.json"))
	return store.BackupSource{Docs: docs, Blobs: blobs, Extra: map[string][]byte{"format.json": format}, Server: "test"}
}

// what a data folder holds: every document and every file's bytes
type folderState struct {
	Docs  map[string]store.Doc
	Files map[string][]byte
}

func stateOf(t *testing.T, dir string) folderState {
	t.Helper()
	ctx := context.Background()
	docs, err := store.OpenSQLiteStore(filepath.Join(dir, "sliqtly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	blobs, err := store.OpenSQLiteBlobStore(filepath.Join(dir, "blobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	st := folderState{Docs: map[string]store.Doc{}, Files: map[string][]byte{}}
	for _, col := range []string{"shares", "rooms"} {
		list, err := docs.Query(ctx, store.Query{From: col})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range list {
			st.Docs[col+"/"+e.ID] = e.Doc
		}
	}
	refs, err := store.NewFileRefs(docs).List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		st.Files[r.Path] = read(t, blobs, r.Hash)
	}
	return st
}

func sameState(t *testing.T, what string, got, want folderState) {
	t.Helper()
	if !reflect.DeepEqual(got.Docs, want.Docs) {
		t.Fatalf("%s: documents differ:\n got %v\nwant %v", what, got.Docs, want.Docs)
	}
	if len(got.Files) != len(want.Files) {
		t.Fatalf("%s: %d files, want %d", what, len(got.Files), len(want.Files))
	}
	for p, b := range want.Files {
		if !bytes.Equal(got.Files[p], b) {
			t.Fatalf("%s: %s differs", what, p)
		}
	}
}

func openRepo(t *testing.T, dir string) *store.BackupRepo {
	t.Helper()
	r, err := store.OpenBackupRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// The whole round: back up, change, back up again (only what changed is
// copied, older versions become deltas), restore each backup into a new
// folder and find there exactly what the folder held then.
func TestBackupRestore(t *testing.T) {
	ctx := context.Background()
	d := newDataFolder(t)
	photo := make([]byte, 300<<10)
	rand.New(rand.NewSource(3)).Read(photo)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("deck%02d", i)
		d.deck(t, id, i)
		d.save(t, "shares/"+id+"/deck.md", deckText(30, i))
		d.save(t, "shares/"+id+"/media/photo.jpg", photo) // one blob, 12 names
	}
	d.save(t, "shares/deck00/data.xlsx", deckText(80, 0))
	state1 := stateOf(t, d.dir)

	repoDir := filepath.Join(t.TempDir(), "backup")
	repo := openRepo(t, repoDir)
	s1, err := repo.Take(ctx, d.source(t))
	if err != nil {
		t.Fatal(err)
	}
	// sliqtly.db, 12 decks, the photo once, the workbook, format.json
	if s1.New.Blobs != 16 || len(s1.Files) != 25 {
		t.Fatalf("first backup: %d new blobs, %d files", s1.New.Blobs, len(s1.Files))
	}

	// a little later: three decks edited, a file added, one removed, a deck
	// document changed and one removed
	for _, i := range []int{1, 5, 9} {
		d.save(t, fmt.Sprintf("shares/deck%02d/deck.md", i), deckText(30, i+50))
	}
	d.save(t, "shares/deck03/notes.md", []byte("new notes"))
	d.refs.Remove(ctx, "shares/deck00/data.xlsx")
	d.deck(t, "deck02", 200)
	store.Delete(ctx, d.docs, "shares", "deck11", store.AnyRev)
	state2 := stateOf(t, d.dir)

	s2, err := repo.Take(ctx, d.source(t))
	if err != nil {
		t.Fatal(err)
	}
	// sliqtly.db, three edited decks, the notes: nothing else is copied
	if s2.New.Blobs != 5 {
		t.Fatalf("second backup copied %d blobs, want 5", s2.New.Blobs)
	}
	u, _ := repo.Blobs().Usage(ctx)
	// the first sliqtly.db and the three old deck texts are deltas now
	if u.Deltas != 4 {
		t.Fatalf("%d deltas, want 4 (%+v)", u.Deltas, u)
	}

	if rep, err := repo.Verify(ctx, true); err != nil || !rep.OK() {
		t.Fatalf("verify: %+v %v", rep, err)
	}

	// restore both; the restored folders hold what the folder held then
	r1 := filepath.Join(t.TempDir(), "restored1")
	if _, err := repo.Restore(ctx, s1.ID, r1); err != nil {
		t.Fatal(err)
	}
	sameState(t, "backup 1", stateOf(t, r1), state1)
	r2 := filepath.Join(t.TempDir(), "restored2")
	rep, err := repo.Restore(ctx, "latest", r2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Snapshot != s2.ID {
		t.Fatalf("latest restored %s", rep.Snapshot)
	}
	sameState(t, "backup 2", stateOf(t, r2), state2)
	if b, _ := os.ReadFile(filepath.Join(r2, "format.json")); string(b) != `{"format":4}`+"\n" {
		t.Fatalf("format.json %q", b)
	}

	// never over a folder with something in it
	if _, err := repo.Restore(ctx, "latest", r2); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("restore over a folder: %v", err)
	}

	// prune to the newest: what only the first named goes, the second
	// still restores
	pr, err := repo.Prune(ctx, store.BackupKeep{Last: 1})
	if err != nil {
		t.Fatal(err)
	}
	// the first sliqtly.db, three old deck texts, the workbook
	if len(pr.Snapshots) != 1 || pr.Blobs != 5 {
		t.Fatalf("prune %+v", pr)
	}
	if rep, err := repo.Verify(ctx, true); err != nil || !rep.OK() {
		t.Fatalf("verify after prune: %+v %v", rep, err)
	}
	r3 := filepath.Join(t.TempDir(), "restored3")
	if _, err := repo.Restore(ctx, "latest", r3); err != nil {
		t.Fatal(err)
	}
	sameState(t, "backup 2 after prune", stateOf(t, r3), state2)
}

// A damaged byte in the backup is found by verify, and a restore of it
// fails and leaves no folder behind.
func TestBackupDamageFound(t *testing.T) {
	ctx := context.Background()
	d := newDataFolder(t)
	d.deck(t, "a", 1)
	d.save(t, "shares/a/deck.md", deckText(20, 0))
	repo := openRepo(t, filepath.Join(t.TempDir(), "backup"))
	s, err := repo.Take(ctx, d.source(t))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := store.ParseHash(s.Files[0].Hash)
	if _, err := repo.Blobs().DB().Exec(`UPDATE chunks SET data = CAST(upper(CAST(data AS TEXT)) AS BLOB) WHERE body = (SELECT body FROM blobs WHERE hash = ?)`, h[:]); err != nil {
		t.Fatal(err)
	}
	rep, err := repo.Verify(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatal("verify missed a damaged blob")
	}
	to := filepath.Join(t.TempDir(), "restored")
	if _, err := repo.Restore(ctx, "latest", to); err == nil {
		t.Fatal("restore of a damaged backup succeeded")
	}
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed restore left its folder")
	}
}

// A file collected between the copy of sliqtly.db and its own copy makes
// the snapshot fail as ErrBackupSourceChanged, and leaves no manifest.
func TestBackupSourceChanged(t *testing.T) {
	ctx := context.Background()
	d := newDataFolder(t)
	d.save(t, "shares/a/x.md", []byte("will go"))
	info, _ := d.refs.Get(ctx, "shares/a/x.md")
	d.blobs.Delete(ctx, info.Hash) // as if collected; the reference stays
	repo := openRepo(t, filepath.Join(t.TempDir(), "backup"))
	if _, err := repo.Take(ctx, d.source(t)); !errors.Is(err, store.ErrBackupSourceChanged) {
		t.Fatalf("got %v", err)
	}
	if snaps, _ := repo.Snapshots(); len(snaps) != 0 {
		t.Fatal("a failed backup left a manifest")
	}
}

// which snapshots a keep rule keeps
func TestBackupKeep(t *testing.T) {
	ctx := context.Background()
	d := newDataFolder(t)
	repo := openRepo(t, filepath.Join(t.TempDir(), "backup"))
	for i := 0; i < 4; i++ {
		d.deck(t, "a", i)
		if _, err := repo.Take(ctx, d.source(t)); err != nil {
			t.Fatal(err)
		}
	}
	// all on one day: daily keeps the newest of it, last=2 one more
	pr, err := repo.Prune(ctx, store.BackupKeep{Last: 2, Daily: 7})
	if err != nil {
		t.Fatal(err)
	}
	snaps, _ := repo.Snapshots()
	if len(snaps) != 2 || len(pr.Snapshots) != 2 {
		t.Fatalf("kept %d, removed %v", len(snaps), pr.Snapshots)
	}
	// keep everything
	if pr, _ := repo.Prune(ctx, store.BackupKeep{}); len(pr.Snapshots) != 0 {
		t.Fatal("a zero rule removed backups")
	}
}
