// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
	"github.com/terotests/sliqtly/mcp-go/store/storetest"
)

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
