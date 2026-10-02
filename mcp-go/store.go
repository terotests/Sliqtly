// Where the MCP server keeps a deck (mcp/src/store.js).
//
// FirebaseStore writes the same share the editor's Share button makes
// (web/sliqtly.js): shares/{id} in Firestore, its pictures in Storage under
// shares/{id}/media/…, so /s/{id} opens it with no change to the page. The
// edit key's hash lives apart, in mcp_keys/{id}, which only the server
// reaches (firestore.rules has no match for it).
//
// LinkStore keeps nothing: the deck travels compressed in the link (#md=…).
// It is what a local run uses without Google credentials.

package main

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Doc is a Firestore document as plain values: string, int64, float64, bool,
// time.Time, []any, map[string]any, nil.
type Doc = map[string]any

// DB is the little of Firestore the server uses; firebase.go has the real
// one, the tests a map.
type DB interface {
	Get(ctx context.Context, col, id string) (Doc, error) // nil, nil when missing
	Set(ctx context.Context, col, id string, d Doc) error
	Update(ctx context.Context, col, id string, d Doc) error
	Delete(ctx context.Context, col, id string) error
	WhereEq(ctx context.Context, col, field string, value any) ([]Doc, []string, error)
	ServerTime() any
}

// Bucket is the little of Cloud Storage the server uses.
type Bucket interface {
	Name() string
	Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error
}

type Store interface {
	Kind() string
	Create(ctx context.Context, d DeckWrite, owner string) (id, key string, err error)
	Update(ctx context.Context, id string, key *string, d DeckWrite) (Doc, error)
	Get(ctx context.Context, id string) (Doc, error)
	List(ctx context.Context, uid string, limit int) ([]Doc, error)
}

// DeckWrite: what changes. A nil pointer leaves a field as it is; CSS.Set
// with a nil Val clears the deck's own stylesheet.
type DeckWrite struct {
	Name, MD, Theme *string
	CSS             OptStr
	Images          []image
}

type OptStr struct {
	Set bool
	Val *string
}

const abc = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// n characters of a-z, A-Z, 0-9, as the editor's shortId()
func shortID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// a flate writer costs about 1 MB to set up, so they are kept
var flaters = sync.Pool{New: func() any { w, _ := flate.NewWriter(nil, 6); return w }}

// the editor's packText(): deflate-raw, then base64url
func packText(text string) string {
	var buf bytes.Buffer
	w := flaters.Get().(*flate.Writer)
	defer flaters.Put(w)
	w.Reset(&buf)
	w.Write([]byte(text))
	w.Close()
	return base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

func unpackText(code string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil {
		return "", err
	}
	out, err := io.ReadAll(flate.NewReader(bytes.NewReader(b)))
	return string(out), err
}

// Firestore refuses ids with "/" and the like; nothing the server writes
// has them.
var reDocID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

type FirebaseStore struct {
	DB     DB
	Bucket Bucket
}

func (s *FirebaseStore) Kind() string { return "cloud" }

// owner: the signed-in user's uid, or "mcp"
func (s *FirebaseStore) Create(ctx context.Context, d DeckWrite, owner string) (string, string, error) {
	id := shortID(10)
	key := shortID(24)
	if err := s.DB.Set(ctx, "mcp_keys", id, Doc{"hash": hashKey(key), "created": s.DB.ServerTime()}); err != nil {
		return "", "", err
	}
	files, err := s.upload(ctx, id, d.Images)
	if err != nil {
		return "", "", err
	}
	doc := Doc{"name": deref(d.Name), "md": deref(d.MD), "theme": deref(d.Theme), "css": nil, "owner": owner,
		"deck": "mcp", "source": "mcp", "files": files, "created": s.DB.ServerTime()}
	if d.CSS.Val != nil {
		doc["css"] = *d.CSS.Val
	}
	if err := s.DB.Set(ctx, "shares", id, doc); err != nil {
		return "", "", err
	}
	return id, key, nil
}

// nil when the id or the key does not match; key nil: the caller has already
// checked that the signed-in user owns it
func (s *FirebaseStore) Update(ctx context.Context, id string, key *string, d DeckWrite) (Doc, error) {
	if !reDocID.MatchString(id) {
		return nil, nil
	}
	if key != nil {
		k, err := s.DB.Get(ctx, "mcp_keys", id)
		if err != nil {
			return nil, err
		}
		if k == nil || str(k["hash"]) != hashKey(*key) {
			return nil, nil
		}
	}
	cur, err := s.DB.Get(ctx, "shares", id)
	if err != nil || cur == nil {
		return nil, err
	}
	added, err := s.upload(ctx, id, d.Images)
	if err != nil {
		return nil, err
	}
	files := []any{}
	for _, f := range list(cur["files"]) {
		replaced := false
		for _, a := range added {
			if a.(Doc)["path"] == str(mapOf(f)["path"]) {
				replaced = true
			}
		}
		if !replaced {
			files = append(files, f)
		}
	}
	files = append(files, added...)
	patch := Doc{"files": files, "updated": s.DB.ServerTime()}
	if d.Name != nil {
		patch["name"] = *d.Name
	}
	if d.MD != nil {
		patch["md"] = *d.MD
	}
	if d.Theme != nil {
		patch["theme"] = *d.Theme
	}
	if d.CSS.Set {
		if d.CSS.Val != nil {
			patch["css"] = *d.CSS.Val
		} else {
			patch["css"] = nil
		}
	}
	if err := s.DB.Update(ctx, "shares", id, patch); err != nil {
		return nil, err
	}
	for k, v := range patch {
		cur[k] = v
	}
	return cur, nil
}

func (s *FirebaseStore) Get(ctx context.Context, id string) (Doc, error) {
	if !reDocID.MatchString(id) {
		return nil, nil
	}
	return s.DB.Get(ctx, "shares", id)
}

// the user's shares, newest first (an equality filter needs no index)
func (s *FirebaseStore) List(ctx context.Context, uid string, limit int) ([]Doc, error) {
	docs, ids, err := s.DB.WhereEq(ctx, "shares", "owner", uid)
	if err != nil {
		return nil, err
	}
	for i, d := range docs {
		d["id"] = ids[i]
		t := millis(d["updated"])
		if t == 0 {
			t = millis(d["created"])
		}
		d["_ms"] = t
	}
	sort.SliceStable(docs, func(i, j int) bool { return docs[i]["_ms"].(int64) > docs[j]["_ms"].(int64) })
	if len(docs) > limit {
		docs = docs[:limit]
	}
	for _, d := range docs {
		if t := d["_ms"].(int64); t != 0 {
			d["updated"] = time.UnixMilli(t).UTC().Format("2006-01-02T15:04:05.000Z")
		} else {
			d["updated"] = nil
		}
		delete(d, "_ms")
	}
	return docs, nil
}

func (s *FirebaseStore) upload(ctx context.Context, id string, images []image) ([]any, error) {
	out := []any{}
	for _, img := range images {
		path := "media/" + img.Name
		name := fmt.Sprintf("shares/%s/%s", id, path)
		// the token is what getDownloadURL() hands the editor for its own uploads
		token := uuid4()
		if err := s.Bucket.Save(ctx, name, img.Type, img.Data, map[string]string{"firebaseStorageDownloadTokens": token}); err != nil {
			return nil, err
		}
		u := fmt.Sprintf("https://firebasestorage.googleapis.com/v0/b/%s/o/%s?alt=media&token=%s", s.Bucket.Name(), encodeURIComponent(name), token)
		out = append(out, Doc{"path": path, "type": img.Type, "size": int64(len(img.Data)), "url": u})
	}
	return out, nil
}

type LinkStore struct{}

func (LinkStore) Kind() string { return "link" }
func (LinkStore) Create(context.Context, DeckWrite, string) (string, string, error) {
	return "", "", fmt.Errorf("link store keeps nothing")
}
func (LinkStore) Update(context.Context, string, *string, DeckWrite) (Doc, error) { return nil, nil }
func (LinkStore) Get(context.Context, string) (Doc, error)                        { return nil, nil }
func (LinkStore) List(context.Context, string, int) ([]Doc, error)                { return nil, nil }

// --- small helpers for Firestore's loosely typed values

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func mapOf(v any) Doc {
	m, _ := v.(map[string]any)
	return m
}

// Firestore keeps a JavaScript number as an integer or a double.
func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func millis(v any) int64 {
	if t, ok := v.(time.Time); ok {
		return t.UnixMilli()
	}
	return num(v)
}

func uuid4() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// JavaScript's encodeURIComponent
func encodeURIComponent(s string) string {
	e := url.QueryEscape(s)
	r := bytes.Buffer{}
	for i := 0; i < len(e); i++ {
		switch {
		case e[i] == '+':
			r.WriteString("%20")
		case e[i] == '%' && i+2 < len(e) && (e[i+1:i+3] == "21" || e[i+1:i+3] == "27" || e[i+1:i+3] == "28" || e[i+1:i+3] == "29" || e[i+1:i+3] == "2A"):
			r.WriteByte(map[string]byte{"21": '!', "27": '\'', "28": '(', "29": ')', "2A": '*'}[e[i+1:i+3]])
			i += 2
		default:
			r.WriteByte(e[i])
		}
	}
	return r.String()
}
