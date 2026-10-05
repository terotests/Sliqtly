// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// FileStore keeps documents in a folder, one JSON file each:
//
//	<root>/<collection>/<sh>/<id>.json
//
// <sh> is Shard(id), one of 256 folders, so no folder holds more than
// about 1/256 of a collection. A document is written to a temporary file
// and renamed over the old one (the folder synced after), so a crash
// leaves the old document or the new one, never half of one. Its revision
// is kept in it as "_rev" (a document from before revisions reads as 1).
//
// One process owns the folder (the server locks it); within it one lock
// keeps writes in order, which is also the order Watch tells them in. A
// query reads the collection's files: fine for the thousands of decks a
// team keeps, and what SQLite is for beyond that.
type FileStore struct {
	root   string
	mu     sync.Mutex
	feed   *feed
	closed bool
}

const revKey = "_rev"

// NewFileStore opens the documents under root (created when missing).
func NewFileStore(root string) (*FileStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &FileStore{root: abs, feed: newFeed(4096)}, nil
}

// Root is the folder the documents are in.
func (f *FileStore) Root() string { return f.root }

func (f *FileStore) file(col, id string) (string, error) {
	if err := checkName(col, id); err != nil {
		return "", err
	}
	return filepath.Join(f.root, col, Shard(id), id+".json"), nil
}

func (f *FileStore) Get(_ context.Context, col, id string) (Doc, Rev, error) {
	p, err := f.file(col, id)
	if err != nil {
		return nil, 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, 0, ErrClosed
	}
	return readDoc(p)
}

func (f *FileStore) Update(_ context.Context, col, id string, fn UpdateFunc) (Doc, Rev, error) {
	p, err := f.file(col, id)
	if err != nil {
		return nil, 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, 0, ErrClosed
	}
	cur, rev, err := readDoc(p)
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
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, 0, err
		}
		f.feed.publish(Change{Col: col, ID: id, Old: cur})
		return nil, 0, nil
	}
	next = Clone(next)
	if err := writeDoc(p, next, rev+1); err != nil {
		return nil, 0, err
	}
	f.feed.publish(Change{Col: col, ID: id, Rev: rev + 1, Doc: Clone(next), Old: cur})
	return Clone(next), rev + 1, nil
}

func (f *FileStore) Query(_ context.Context, q Query) ([]Item, error) {
	if err := checkName(q.From, "x"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, ErrClosed
	}
	var items []Item
	err := EachFile(filepath.Join(f.root, q.From), func(id, path string) {
		d, rev, err := readDoc(path)
		if err == nil && d != nil && Match(q.Where, id, d) {
			items = append(items, Item{ID: id, Rev: rev, Doc: d})
		}
	})
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return Run(Query{OrderBy: q.OrderBy, Limit: q.Limit, Offset: q.Offset}, items), nil
}

func (f *FileStore) Head() Seq { return f.feed.Head() }

func (f *FileStore) Watch(ctx context.Context, after Seq) (<-chan Change, error) {
	return f.feed.watch(ctx, after)
}

func (f *FileStore) Caps() Capabilities {
	return Capabilities{Transactions: true, Watch: true, Durable: true}
}

func (f *FileStore) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.feed.close()
	return nil
}

// --- names and shards

// checkName: a collection or id is one plain path segment
func checkName(col, id string) error {
	for _, s := range []string{col, id} {
		if s == "" || s == "." || s == ".." || strings.HasPrefix(s, ".") || strings.ContainsAny(s, "/\\\x00") {
			return fmt.Errorf("store: bad name %q", s)
		}
	}
	return nil
}

// Shard is the folder an id's entry is kept in: two hex digits of its
// FNV-1a hash. It is part of the data format: change it only with a
// migration.
func Shard(id string) string {
	h := fnv.New32a()
	h.Write([]byte(id))
	return fmt.Sprintf("%02x", h.Sum32()&0xff)
}

// IsShard: a shard folder's name, two lowercase hex digits
func IsShard(name string) bool {
	if len(name) != 2 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// EachFile calls fn with the id and file of every document in a
// collection's folder, shard by shard
func EachFile(dir string, fn func(id, path string)) error {
	shards, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, sh := range shards {
		if !sh.IsDir() || !IsShard(sh.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, sh.Name()))
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
				continue
			}
			fn(strings.TrimSuffix(name, ".json"), filepath.Join(dir, sh.Name(), name))
		}
	}
	return nil
}

// --- the files

// time.Time is kept as {"$ts": ms} so it reads back as a time
func EncodeValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return map[string]any{"$ts": x.UnixMilli()}
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = EncodeValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = EncodeValue(e)
		}
		return out
	}
	return v
}

// DecodeValue undoes EncodeValue on JSON read with UseNumber: whole
// numbers are int64, others float64.
func DecodeValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		if ms, ok := x["$ts"].(json.Number); ok && len(x) == 1 {
			if i, err := ms.Int64(); err == nil {
				return time.UnixMilli(i).UTC()
			}
		}
		out := map[string]any{}
		for k, e := range x {
			out[k] = DecodeValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = DecodeValue(e)
		}
		return out
	}
	return v
}

func readDoc(path string) (Doc, Rev, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var d map[string]any
	if err := dec.Decode(&d); err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	doc := DecodeValue(d).(map[string]any)
	rev := Rev(1)
	if r, ok := doc[revKey].(int64); ok && r > 0 {
		rev = Rev(r)
	}
	delete(doc, revKey)
	return doc, rev, nil
}

func writeDoc(path string, d Doc, rev Rev) error {
	enc := EncodeValue(d).(map[string]any)
	enc[revKey] = int64(rev)
	b, err := json.MarshalIndent(enc, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, b)
}

// WriteAtomic writes to a temporary file beside path, then renames it over
// path and syncs the folder.
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return syncDir(filepath.Dir(path))
}

// the folder written to disk, so a rename in it outlasts a power cut too;
// Windows cannot open a folder for that, and keeps renames by itself
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	f.Close()
	return err
}

// Collections: the folders that are collections (a name a collection can
// have, holding shard folders)
func (f *FileStore) Collections(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(f.root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && checkName(e.Name(), "x") == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (f *FileStore) Import(_ context.Context, col, id string, d Doc, rev Rev) error {
	p, err := f.file(col, id)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := os.Stat(p); err == nil {
		return ErrConflict
	}
	return writeDoc(p, Clone(d), rev)
}
