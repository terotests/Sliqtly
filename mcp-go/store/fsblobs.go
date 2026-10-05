// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FSBlobStore keeps each blob as a file named by its hash:
//
//	<root>/<h[0:2]>/<hash>        the bytes
//	<root>/<h[0:2]>/<hash>.json   size, mime, created
//
// A blob is written to a temporary file, synced and renamed into place, so
// a crash leaves the blob or nothing. It is the BlobStore the folder layout
// had (bytes as files), kept as the benchmark's baseline and for a server
// that wants its files as files.
type FSBlobStore struct {
	root   string
	mu     sync.Mutex
	closed bool
}

func NewFSBlobStore(root string) (*FSBlobStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &FSBlobStore{root: abs}, nil
}

func (f *FSBlobStore) file(h Hash) string {
	s := h.String()
	return filepath.Join(f.root, s[:2], s)
}

func (f *FSBlobStore) Put(ctx context.Context, r io.Reader, mime string) (BlobInfo, error) {
	if f.isClosed() {
		return BlobInfo{}, ErrClosed
	}
	tmp, err := os.CreateTemp(f.root, ".tmp-*")
	if err != nil {
		return BlobInfo{}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return BlobInfo{}, err
	}
	var hash Hash
	copy(hash[:], h.Sum(nil))
	f.mu.Lock()
	defer f.mu.Unlock()
	if info, err := f.stat(hash); err == nil {
		return info, nil
	}
	p := f.file(hash)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return BlobInfo{}, err
	}
	info := BlobInfo{Hash: hash, Size: size, Mime: mime, Created: time.UnixMilli(time.Now().UnixMilli()).UTC()}
	meta, _ := json.Marshal(map[string]any{"size": size, "mime": mime, "created": info.Created.UnixMilli()})
	// the bytes first, then the description: a blob is there when both are
	if err := os.Rename(name, p); err != nil {
		return BlobInfo{}, err
	}
	if err := WriteAtomic(p+".json", meta); err != nil {
		return BlobInfo{}, err
	}
	return info, nil
}

func (f *FSBlobStore) stat(h Hash) (BlobInfo, error) {
	b, err := os.ReadFile(f.file(h) + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return BlobInfo{}, ErrNoBlob
	}
	if err != nil {
		return BlobInfo{}, err
	}
	var m struct {
		Size    int64  `json:"size"`
		Mime    string `json:"mime"`
		Created int64  `json:"created"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return BlobInfo{}, err
	}
	return BlobInfo{Hash: h, Size: m.Size, Mime: m.Mime, Created: time.UnixMilli(m.Created).UTC()}, nil
}

func (f *FSBlobStore) Stat(_ context.Context, h Hash) (BlobInfo, error) {
	if f.isClosed() {
		return BlobInfo{}, ErrClosed
	}
	return f.stat(h)
}

type fsBlobReader struct {
	*os.File
	info BlobInfo
}

func (r fsBlobReader) Info() BlobInfo { return r.info }

func (f *FSBlobStore) Open(_ context.Context, h Hash) (BlobReader, error) {
	if f.isClosed() {
		return nil, ErrClosed
	}
	info, err := f.stat(h)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(f.file(h))
	if err != nil {
		return nil, err
	}
	return fsBlobReader{file, info}, nil
}

func (f *FSBlobStore) Delete(_ context.Context, h Hash) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.file(h)
	for _, x := range []string{p + ".json", p} {
		if err := os.Remove(x); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (f *FSBlobStore) Each(ctx context.Context, fn func(BlobInfo) error) error {
	var hashes []Hash
	err := filepath.WalkDir(f.root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name := e.Name(); strings.HasSuffix(name, ".json") {
			if h, err := ParseHash(strings.TrimSuffix(name, ".json")); err == nil {
				hashes = append(hashes, h)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].String() < hashes[j].String() })
	for _, h := range hashes {
		info, err := f.stat(h)
		if err != nil {
			return err
		}
		if err := fn(info); err != nil {
			return err
		}
	}
	return nil
}

func (f *FSBlobStore) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *FSBlobStore) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
