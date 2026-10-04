// SPDX-License-Identifier: AGPL-3.0-or-later

// The DB and Bucket on a folder, for a server of one's own (SLIQTLY_DATA):
//
//	<root>/db/<collection>/<id>.json   a document
//	<root>/files/<path>                a kept file (shares/{id}/media/…)
//
// One process owns the folder. A document is written to a temporary file
// and renamed over the old one, so a crash leaves the old document or the
// new one, never half of one. A query reads the collection's folder, which
// is fine for the thousands of decks a team keeps.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type fsDB struct {
	root string
	mu   sync.Mutex
	// told of every write, after it and before the next one, with what the
	// document is now (nil: deleted); nil: nobody. It runs while the folder
	// is locked, so it must not read or write the folder itself
	// (localevents.go, collab.go)
	changed func(col, id string, doc Doc)
}

func (d *fsDB) wrote(col, id string, doc Doc) {
	if d.changed != nil {
		d.changed(col, id, doc)
	}
}

type fsBucket struct {
	root string
	mu   sync.Mutex
}

func newFSStore(root string) (*fsDB, *fsBucket, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range []string{"db", "files"} {
		if err := os.MkdirAll(filepath.Join(abs, d), 0o750); err != nil {
			return nil, nil, err
		}
	}
	return &fsDB{root: filepath.Join(abs, "db")}, &fsBucket{root: filepath.Join(abs, "files")}, nil
}

// a collection or id is one plain path segment
func segment(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\\x00") {
		return fmt.Errorf("bad name %q", s)
	}
	return nil
}

func (d *fsDB) file(col, id string) (string, error) {
	if err := segment(col); err != nil {
		return "", err
	}
	if err := segment(id); err != nil {
		return "", err
	}
	return filepath.Join(d.root, col, id+".json"), nil
}

// time.Time is kept as {"$ts": ms} so it reads back as a time
func encodeValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return map[string]any{"$ts": x.UnixMilli()}
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = encodeValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = encodeValue(e)
		}
		return out
	}
	return v
}

func decodeValue(v any) any {
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
			out[k] = decodeValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = decodeValue(e)
		}
		return out
	}
	return v
}

func readDoc(path string) (Doc, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var d map[string]any
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return decodeValue(d).(map[string]any), nil
}

// write to a temporary file beside it, then rename over it
func writeAtomic(path string, data []byte) error {
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
	return nil
}

func writeDoc(path string, d Doc) error {
	b, err := json.MarshalIndent(encodeValue(d), "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

func (d *fsDB) Get(_ context.Context, col, id string) (Doc, error) {
	p, err := d.file(col, id)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return readDoc(p)
}

func (d *fsDB) Set(_ context.Context, col, id string, doc Doc) error {
	p, err := d.file(col, id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err = writeDoc(p, doc)
	if err == nil {
		d.wrote(col, id, doc)
	}
	return err
}

// Firestore's update: the document must exist; a key with dots is a path
// into nested maps
func (d *fsDB) Update(_ context.Context, col, id string, doc Doc) error {
	p, err := d.file(col, id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cur, err := readDoc(p)
	if err != nil {
		return err
	}
	if cur == nil {
		return fmt.Errorf("%s/%s: no such document", col, id)
	}
	for k, v := range doc {
		parts := strings.Split(k, ".")
		m := cur
		for _, part := range parts[:len(parts)-1] {
			next, ok := m[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[part] = next
			}
			m = next
		}
		m[parts[len(parts)-1]] = v
	}
	if err := writeDoc(p, cur); err != nil {
		return err
	}
	d.wrote(col, id, cur)
	return nil
}

func (d *fsDB) Delete(_ context.Context, col, id string) error {
	p, err := d.file(col, id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	d.wrote(col, id, nil)
	return nil
}

// equal as the stored value: a number asked for as a string matches too,
// since the Ranger side passes every value as a string
func sameValue(have, want any) bool {
	if have == want {
		return true
	}
	return fmt.Sprint(have) == fmt.Sprint(want)
}

func (d *fsDB) WhereEq(_ context.Context, col, field string, value any) ([]Doc, []string, error) {
	if err := segment(col); err != nil {
		return nil, nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	docs, ids := []Doc{}, []string{}
	entries, err := os.ReadDir(filepath.Join(d.root, col))
	if errors.Is(err, os.ErrNotExist) {
		return docs, ids, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		doc, err := readDoc(filepath.Join(d.root, col, name))
		if err != nil || doc == nil {
			continue
		}
		if v, ok := doc[field]; ok && sameValue(v, value) {
			docs = append(docs, doc)
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return docs, ids, nil
}

func (d *fsDB) ServerTime() any { return time.Now().UTC() }

func (d *fsDB) Create(_ context.Context, col, id string, doc Doc) (Doc, error) {
	p, err := d.file(col, id)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	had, err := readDoc(p)
	if err != nil || had != nil {
		return had, err
	}
	if err := writeDoc(p, doc); err != nil {
		return nil, err
	}
	d.wrote(col, id, doc)
	return nil, nil
}

func (d *fsDB) Increment(_ context.Context, col, id string, add Doc) error {
	p, err := d.file(col, id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cur, err := readDoc(p)
	if err != nil {
		return err
	}
	var inc func(cur, add Doc) Doc
	inc = func(cur, add Doc) Doc {
		if cur == nil {
			cur = Doc{}
		}
		for k, v := range add {
			if sub, ok := v.(map[string]any); ok {
				had, _ := cur[k].(map[string]any)
				cur[k] = inc(had, sub)
				continue
			}
			n, _ := cur[k].(int64)
			a, _ := v.(int64)
			cur[k] = n + a
		}
		return cur
	}
	return writeDoc(p, inc(cur, add))
}

// a kept file's path inside the folder, refused when it would leave it
func (b *fsBucket) file(path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if path == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bad path %q", path)
	}
	return filepath.Join(b.root, clean), nil
}

func (b *fsBucket) Name() string { return "local" }

// the content type is kept beside the file, as <name>.type
func (b *fsBucket) Save(_ context.Context, path, contentType string, data []byte, _ map[string]string) error {
	p, err := b.file(path)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := writeAtomic(p, data); err != nil {
		return err
	}
	return writeAtomic(p+".type", []byte(contentType))
}

func (b *fsBucket) Read(_ context.Context, path string, limit int64) ([]byte, error) {
	p, err := b.file(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// the file and its content type for /files/…; ok false when there is none
func (b *fsBucket) Open(path string) (f *os.File, contentType string, ok bool) {
	p, err := b.file(path)
	if err != nil || strings.HasSuffix(p, ".type") {
		return nil, "", false
	}
	f, err = os.Open(p)
	if err != nil {
		return nil, "", false
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, "", false
	}
	ct, _ := os.ReadFile(p + ".type")
	return f, string(ct), true
}
