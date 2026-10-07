// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/terotests/sliqtly/mcp-go/store"
	"github.com/terotests/sliqtly/mcp-go/store/storetest"
)

// A string cut from a big one (a request body) is copied, so a cloned
// document does not keep the body alive (the change feed keeps clones).
func TestCloneOwnsItsStrings(t *testing.T) {
	body := strings.Repeat("x", 1<<20)
	md := body[10:20]
	got := store.Clone(store.Doc{"md": md, "list": []any{md}})
	for _, v := range []string{got["md"].(string), got["list"].([]any)[0].(string)} {
		if v != md {
			t.Fatalf("clone changed the text: %q", v)
		}
		if unsafe.StringData(v) == unsafe.StringData(md) {
			t.Fatal("the clone shares the body's bytes")
		}
	}
}

func TestMemStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Engine { return store.NewMemStore() })
}

func TestFileStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Engine {
		f, err := store.NewFileStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return f
	})
}

// the way from the folder to another backend: every document with its
// revision, and the same digests after
func TestCopyFolderToMemory(t *testing.T) {
	ctx := context.Background()
	src, err := store.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprint("d", i)
		for r := 0; r <= i%3; r++ {
			if _, err := store.Put(ctx, src, "shares", id, store.Doc{"n": int64(r), "at": time.UnixMilli(1700000000000).UTC()}, store.AnyRev); err != nil {
				t.Fatal(err)
			}
		}
	}
	store.Put(ctx, src, "settings", "network", store.Doc{"access": "local"}, 0)
	dst := store.NewMemStore()
	rep, err := store.Copy(ctx, dst, src)
	if err != nil {
		t.Fatal(err)
	}
	if rep["shares"] != 50 || rep["settings"] != 1 {
		t.Fatalf("copied %v", rep)
	}
	if err := store.Verify(ctx, dst, src); err != nil {
		t.Fatal(err)
	}
	_, rev, _ := dst.Get(ctx, "shares", "d2")
	if rev != 3 {
		t.Fatalf("rev %d", rev)
	}
	if _, err := store.Copy(ctx, dst, src); err == nil {
		t.Fatal("copied over what was there")
	}
	store.Put(ctx, dst, "shares", "d1", store.Doc{"n": int64(9)}, store.AnyRev)
	if err := store.Verify(ctx, dst, src); err == nil {
		t.Fatal("a difference was not seen")
	}
}

func TestSQLiteStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Engine {
		s, err := store.OpenSQLiteStore(filepath.Join(t.TempDir(), "sliqtly.db"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestMemChat(t *testing.T) {
	storetest.RunChat(t, func(t *testing.T) store.ChatLog { return store.NewMemChat() })
}

func TestSQLiteChat(t *testing.T) {
	storetest.RunChat(t, func(t *testing.T) store.ChatLog {
		s, err := store.OpenSQLiteStore(filepath.Join(t.TempDir(), "sliqtly.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return store.NewSQLiteChat(s)
	})
}

func TestSQLiteBlobStore(t *testing.T) {
	storetest.RunBlobs(t, func(t *testing.T) store.BlobStore {
		b, err := store.OpenSQLiteBlobStore(filepath.Join(t.TempDir(), "blobs.db"))
		if err != nil {
			t.Fatal(err)
		}
		return b
	})
}

func TestFSBlobStore(t *testing.T) {
	storetest.RunBlobs(t, func(t *testing.T) store.BlobStore {
		b, err := store.NewFSBlobStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return b
	})
}

// an upload stopped by a crash leaves chunks under its body; the next open
// removes them and keeps every blob that was finished
func TestSQLiteBlobUploadStopped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "blobs.db")
	b, err := store.OpenSQLiteBlobStore(path)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := b.Put(ctx, bytes.NewReader([]byte("finished")), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.DB().Exec(`INSERT INTO uploads (started) VALUES (1); INSERT INTO chunks (body, n, data) VALUES (last_insert_rowid(), 0, x'00ff')`); err != nil {
		t.Fatal(err)
	}
	b.Close()
	b, err = store.OpenSQLiteBlobStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var chunks, uploads int
	b.DB().QueryRow(`SELECT (SELECT count(*) FROM chunks), (SELECT count(*) FROM uploads)`).Scan(&chunks, &uploads)
	if chunks != 1 || uploads != 0 {
		t.Fatalf("%d chunks, %d uploads after reopen", chunks, uploads)
	}
	if _, err := b.Stat(ctx, kept.Hash); err != nil {
		t.Fatal(err)
	}
}

// a file at a newer schema than this build knows is refused, and an
// older one is backed up before it is migrated
func TestSQLiteSchemaVersions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sliqtly.db")
	s, err := store.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.DB().Exec(`PRAGMA user_version = 99`)
	s.Close()
	if _, err := store.OpenSQLiteStore(path); err == nil || !strings.Contains(err.Error(), "newer server") {
		t.Fatalf("newer schema: %v", err)
	}

	path2 := filepath.Join(dir, "two.db")
	s, err = store.OpenSQLiteStore(path2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store.Put(ctx, s, "shares", "a", store.Doc{"n": int64(1)}, 0)
	next := append(append([]store.SQLMigration{}, store.SQLiteSchema...), store.SQLMigration{
		Version: len(store.SQLiteSchema) + 1, Note: "test column", Up: store.SQLExec(`CREATE TABLE extra (x)`)})
	rep, err := store.Migrate(ctx, s.DB(), path2, next, "test")
	if err != nil {
		t.Fatal(err)
	}
	if rep.From != len(store.SQLiteSchema) || rep.To != len(store.SQLiteSchema)+1 || rep.Backup == "" {
		t.Fatalf("report %+v", rep)
	}
	var notes int
	s.DB().QueryRow(`SELECT count(*) FROM schema_history`).Scan(&notes)
	if notes != len(store.SQLiteSchema)+1 {
		t.Fatalf("%d history rows", notes)
	}
	s.Close()
	old, err := store.OpenSQLiteStore(rep.Backup)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if d, _, _ := old.Get(ctx, "shares", "a"); d["n"] != int64(1) {
		t.Fatalf("backup holds %v", d)
	}
}
