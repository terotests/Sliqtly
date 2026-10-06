// SPDX-License-Identifier: AGPL-3.0-or-later

// The DB and Bucket on a folder, for a server of one's own (SLIQTLY_DATA).
// Since data format 4 the folder holds two SQLite files:
//
//	<root>/format.json    the layout's version (datafmt.go)
//	<root>/sliqtly.db     the documents (store.SQLiteStore), every deck in a
//	                      home room, and the kept files by path (file_refs)
//	<root>/blobs.db       the kept files' bytes by SHA-256, in chunks
//	                      (store.SQLiteBlobStore); see sqlbucket.go
//	<root>/backups/       what migrations kept of the folder as it was
//
// Formats 1–3 kept a JSON file per document under db/ and the files under
// files/ (store.FileStore and shard folders); the migration to format 4
// copies them into the two databases (datafmt.go intoSQLite).
//
// One process owns the folder (datafmt.go locks it). The documents are a
// store.Engine; engineDB is the little of Firestore the Ranger side asks
// for (host.go DB) on top of one. It is the privileged side, as the
// folder server has one user: what checks who may do what is store.Store.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

var (
	shard        = store.Shard
	isShard      = store.IsShard
	writeAtomic  = store.WriteAtomic
	encodeValue  = store.EncodeValue
	decodeValue  = store.DecodeValue
	errNoSuchDoc = errors.New("no such document")
)

// engineDB is host.go's DB on a store.Engine
type engineDB struct{ e store.Engine }

// the folder server's one tenant
const localTenant = "local"

// the store on a folder in the current layout; prepareData (datafmt.go)
// brings an older one up to it first, and a folder in another layout is
// refused rather than read wrong. A deck written without a room goes to
// General; user owns the starter rooms.
func newFSStore(root, user string) (*engineDB, *localBucket, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	have, err := dataFormat(abs)
	if err != nil {
		return nil, nil, err
	}
	if have != currentFormat {
		return nil, nil, fmt.Errorf("%s is in data format %d, this server reads %d: start it with -data to migrate", abs, have, currentFormat)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, nil, err
	}
	// a new folder says which format it is in before anything is written
	if !hasFormatFile(abs) {
		f := &formatFile{Format: currentFormat, Server: version, History: []formatStep{{Format: currentFormat, At: now(), Server: version, Note: "new folder"}}}
		if err := writeFormat(abs, f); err != nil {
			return nil, nil, err
		}
	}
	docs, err := store.OpenSQLiteStore(filepath.Join(abs, docsFile))
	if err != nil {
		return nil, nil, err
	}
	blobs, err := store.OpenSQLiteBlobStore(filepath.Join(abs, blobsFile))
	if err != nil {
		docs.Close()
		return nil, nil, err
	}
	e := &store.HomeRooms{Engine: docs, Cols: map[string]bool{"shares": true}, Tenant: localTenant, Owner: user}
	return &engineDB{e}, &localBucket{refs: store.NewFileRefs(docs), blobs: blobs, docs: docs.DB()}, nil
}

// the folder's two databases
const (
	docsFile  = "sliqtly.db"
	blobsFile = "blobs.db"
)

func (d *engineDB) Get(ctx context.Context, col, id string) (Doc, error) {
	doc, _, err := d.e.Get(ctx, col, id)
	return doc, err
}

func (d *engineDB) Set(ctx context.Context, col, id string, doc Doc) error {
	_, err := store.Put(ctx, d.e, col, id, doc, store.AnyRev)
	return err
}

// Firestore's update: the document must exist; a key with dots is a path
// into nested maps
func (d *engineDB) Update(ctx context.Context, col, id string, doc Doc) error {
	_, _, err := d.e.Update(ctx, col, id, func(cur Doc, _ store.Rev) (Doc, error) {
		if cur == nil {
			return nil, fmt.Errorf("%s/%s: %w", col, id, errNoSuchDoc)
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
		return cur, nil
	})
	return err
}

func (d *engineDB) Delete(ctx context.Context, col, id string) error {
	return store.Delete(ctx, d.e, col, id, store.AnyRev)
}

func (d *engineDB) Take(ctx context.Context, col, id string) (Doc, error) {
	doc, rev, err := d.e.Get(ctx, col, id)
	if err != nil || doc == nil {
		return nil, err
	}
	// deleted only at the revision read: a second taker finds it changed
	// or gone, and gets nothing
	err = store.Delete(ctx, d.e, col, id, rev)
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// equal as the stored value, the Ranger side passing every value as a
// string: "3" finds 3, and 3 finds "3"
func (d *engineDB) WhereEq(ctx context.Context, col, field string, value any) ([]Doc, []string, error) {
	match := store.Or{store.Eq(field, value)}
	switch v := value.(type) {
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			match = append(match, store.Eq(field, i))
		} else if f, err := strconv.ParseFloat(v, 64); err == nil {
			match = append(match, store.Eq(field, f))
		}
		if b, err := strconv.ParseBool(v); err == nil {
			match = append(match, store.Eq(field, b))
		}
	default:
		match = append(match, store.Eq(field, fmt.Sprint(v)))
	}
	items, err := d.e.Query(ctx, store.Query{From: col, Where: match})
	if err != nil {
		return nil, nil, err
	}
	docs, ids := []Doc{}, []string{}
	for _, it := range items {
		docs = append(docs, it.Doc)
		ids = append(ids, it.ID)
	}
	return docs, ids, nil
}

func (d *engineDB) ServerTime() any { return time.Now().UTC() }

func (d *engineDB) Create(ctx context.Context, col, id string, doc Doc) (Doc, error) {
	var had Doc
	_, _, err := d.e.Update(ctx, col, id, func(cur Doc, _ store.Rev) (Doc, error) {
		if cur != nil {
			had = cur
			return nil, errExists
		}
		return doc, nil
	})
	if errors.Is(err, errExists) {
		return had, nil
	}
	return nil, err
}

var errExists = errors.New("exists")

func (d *engineDB) UpdateIf(ctx context.Context, col, id, field, want string, doc Doc) (bool, error) {
	_, _, err := d.e.Update(ctx, col, id, func(cur Doc, _ store.Rev) (Doc, error) {
		if fieldText(cur, field) != want {
			return nil, errNotWanted
		}
		if cur == nil {
			cur = Doc{}
		}
		for k, v := range doc {
			cur[k] = v
		}
		return cur, nil
	})
	if errors.Is(err, errNotWanted) {
		return false, nil
	}
	return err == nil, err
}

var errNotWanted = errors.New("field changed")

func (d *engineDB) Increment(ctx context.Context, col, id string, add Doc) error {
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
	_, _, err := d.e.Update(ctx, col, id, func(cur Doc, _ store.Rev) (Doc, error) {
		return inc(cur, add), nil
	})
	return err
}

// the collections whose documents go once their `expires` has passed, as
// Firestore's TTL policies have them (firestore.indexes.json)
var ttlCollections = []string{"shares", "mcp_keys", "mcp_quota", "mcp_oauth_requests", "mcp_oauth_codes", "mcp_oauth_tokens", "stats_salt", "stats_seen", "mcp_work", "mcp_bases"}

// removes the documents of col whose `expires` is before t, each looked at
// again as it is removed, so one given a later `expires` meanwhile stays.
// → the ids removed
func sweepExpired(ctx context.Context, e store.Engine, col string, t time.Time) ([]string, error) {
	old, err := e.Query(ctx, store.Query{From: col, Where: store.Lt("expires", t)})
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, it := range old {
		removed := false
		_, _, err := e.Update(ctx, col, it.ID, func(cur Doc, _ store.Rev) (Doc, error) {
			if exp, ok := cur["expires"].(time.Time); cur != nil && ok && exp.Before(t) {
				removed = true
				return nil, nil
			}
			return cur, errKept
		})
		if removed && err == nil {
			gone = append(gone, it.ID)
		}
	}
	return gone, nil
}

var errKept = errors.New("kept")
