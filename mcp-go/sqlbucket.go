// SPDX-License-Identifier: AGPL-3.0-or-later

// The folder server's kept files (Bucket) since data format 4: each path
// is a file reference in sliqtly.db (store.FileRefs) naming a blob in
// blobs.db (store.SQLiteBlobStore). The addresses (/files/shares/{id}/…)
// are the paths and do not change; the same bytes under two paths are
// kept once. A blob is put before its reference, so a crash between the
// two leaves only a blob nothing names, which collectBlobs removes.

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

type localBucket struct {
	refs  *store.FileRefs
	blobs store.BlobStore
	docs  *sql.DB    // sliqtly.db, for backups (backup.go)
	mu    sync.Mutex // Save and Remove of one path in order
}

// a kept file's path, refused when it would leave the files
func cleanFilePath(p string) (string, error) {
	c := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if p == "" || strings.HasPrefix(c, "/") || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("bad path %q", p)
	}
	return c, nil
}

func (b *localBucket) Name() string { return "local" }

func (b *localBucket) Save(ctx context.Context, p, contentType string, data []byte, _ map[string]string) error {
	c, err := cleanFilePath(p)
	if err != nil {
		return err
	}
	info, err := b.blobs.Put(ctx, bytes.NewReader(data), contentType)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	old, oldErr := b.refs.Get(ctx, c)
	if err := b.refs.Set(ctx, c, info, contentType); err != nil {
		return err
	}
	// the bytes the path held before, when another path still names them
	// (a copied deck), are kept as a delta against the new ones
	if oldErr == nil && old.Hash != info.Hash {
		if d, ok := b.blobs.(store.DeltaBlobStore); ok {
			if named, err := b.refs.Referenced(ctx, old.Hash); err == nil && named {
				d.Deltify(ctx, old.Hash, info.Hash)
			}
		}
	}
	return nil
}

func (b *localBucket) Read(ctx context.Context, p string, limit int64) ([]byte, error) {
	f, _, ok := b.open(ctx, p)
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, errNoSuchFile)
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

var errNoSuchFile = errors.New("no such file")

// a kept file being read
type openFile struct {
	store.BlobReader
	ModTime time.Time
}

// the file and its content type for /files/…; ok false when there is none
func (b *localBucket) Open(p string) (f *openFile, contentType string, ok bool) {
	return b.open(context.Background(), p)
}

func (b *localBucket) open(ctx context.Context, p string) (*openFile, string, bool) {
	c, err := cleanFilePath(p)
	if err != nil {
		return nil, "", false
	}
	ref, err := b.refs.Get(ctx, c)
	if err != nil {
		return nil, "", false
	}
	r, err := b.blobs.Open(ctx, ref.Hash)
	if err != nil {
		return nil, "", false
	}
	return &openFile{BlobReader: r, ModTime: ref.Updated}, ref.Mime, true
}

func (b *localBucket) Remove(p string) error {
	c, err := cleanFilePath(p)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refs.Remove(context.Background(), c)
}

func (b *localBucket) RemoveAll(p string) error {
	c, err := cleanFilePath(p)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refs.RemoveAll(context.Background(), c)
}

// the lines of an append-only file (a room's chat), the last `last`
func (b *localBucket) Lines(p string, last int) ([]string, error) {
	c, err := cleanFilePath(p)
	if err != nil {
		return nil, err
	}
	return b.refs.Lines(context.Background(), c, last)
}

func (b *localBucket) AppendLine(p, line string) error {
	c, err := cleanFilePath(p)
	if err != nil {
		return err
	}
	return b.refs.AppendLine(context.Background(), c, line)
}

// blobGrace: a blob younger than this is not collected even with no
// reference, as its reference may be about to be written
const blobGrace = time.Hour

// collectBlobs removes the blobs no file names any more (the files of a
// deleted or expired deck). → how many went
func (b *localBucket) collectBlobs(ctx context.Context, now time.Time) (int, error) {
	return store.CollectBlobs(ctx, b.blobs, func(h store.Hash) (bool, error) {
		return b.refs.Referenced(ctx, h)
	}, now.Add(-blobGrace))
}
