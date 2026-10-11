// SPDX-License-Identifier: AGPL-3.0-or-later

// sliqtly.com's shared files (sharedfiles.go): each signed-in account has
// its own, seen by it alone (Tero, 2026-10-11: "uploader only"). Its
// editor's Files place, its assistant's tools and the slides it makes use
// them; a deck's Figma pictures are drawn from its owner's files, so the
// people it is shared with see the screens the owner put on its slides and
// nothing else of the files.
//
// In Storage:
//
//	users/{uid}/figma-upload/{name}  the page puts a file here (storage.rules
//	                                 lets the account write it, up to the
//	                                 size the server takes); the server moves
//	                                 it in at once
//	figma/{uid}/shared/…             the files, as sharedfiles.go keeps them,
//	figma/{uid}/sharedmeta/…         written by the server only (no rule
//	                                 lets a page read or write them)
//
// A file is uploaded straight to Storage, not through Hosting and Cloud Run,
// whose requests stop at 32 MB. Reading it in takes memory several times
// its size, more than the service's instances have to spare: it is read by
// the Cloud Run job SLIQTLY_FIGMA_JOB names (the same binary, run as
// `server figma-index <uid> <id>`, cloudjob.go). Without one (a test, an
// emulator) it is read here, one file at a time.

package main

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the largest file sliqtly.com takes unless SLIQTLY_SHARED_MAX says; the
// same as storage.rules' figma-upload
const cloudSharedMaxDefault = 200 << 20

// what the cloud's shared files need of Storage
type sharedBucket interface {
	sharedStore
	// Move makes the object `from` the object `to` → its size and a sum
	// of its bytes
	Move(ctx context.Context, from, to string) (size int64, sum string, err error)
}

type cloudShared struct {
	bucket sharedBucket
	max    int64
	// at most two drawings at once, whoever's
	draws chan struct{}
	// start reads file id of account uid somewhere else; nil: here
	start func(ctx context.Context, uid, id string) error
	local chan [2]string

	mu    sync.Mutex
	users map[string]*sharedFiles
}

func newCloudShared(b sharedBucket, start func(ctx context.Context, uid, id string) error) *cloudShared {
	c := &cloudShared{bucket: b, max: sharedMaxFromEnv(cloudSharedMaxDefault), draws: make(chan struct{}, 2), start: start, users: map[string]*sharedFiles{}}
	if start == nil {
		c.local = make(chan [2]string, 64)
		go func() {
			for q := range c.local {
				s := c.of(q[0])
				s.read(context.Background(), q[1])
				if s.indexed != nil {
					s.indexed(q[1])
				}
			}
		}()
	}
	return c
}

// where account uid's files are kept
func cloudSharedRoot(uid string) string { return "figma/" + uid + "/" }

// where its page uploads a file
func cloudUploadDir(uid string) string { return "users/" + uid + "/figma-upload/" }

// a Firebase uid as a part of a path: letters, digits, - and _
func cloudUID(uid string) bool {
	if uid == "" || len(uid) > 128 {
		return false
	}
	for _, r := range uid {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// of is account uid's shared files; nil for no account
func (c *cloudShared) of(uid string) *sharedFiles {
	if c == nil || !cloudUID(uid) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.users[uid]; ok {
		return s
	}
	// the indexes kept are the last accounts' only
	if len(c.users) >= 256 {
		c.users = map[string]*sharedFiles{}
	}
	s := &sharedFiles{
		files: prefixStore{c.bucket, cloudSharedRoot(uid)},
		max:   c.max,
		log:   func(m string) { log.Print("figma " + uid + ": " + m) },
		draws: c.draws,
		urls:  "/editor/api/shared/",
		stale: 45 * time.Minute,
		idx:   map[string]cachedIndex{},
	}
	s.queue = func(id string) {
		if c.start == nil {
			c.local <- [2]string{uid, id}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.start(ctx, uid, id); err != nil {
			log.Printf("figma %s: the job for %s did not start: %v", uid, id, err)
			s.mu.Lock()
			defer s.mu.Unlock()
			if f, err := s.card(context.Background(), id); err == nil && f.Status == "indexing" {
				f.Status, f.Error = "failed", "reading it could not be started; add the file again"
				s.putCard(context.Background(), f)
			}
		}
	}
	c.users[uid] = s
	return s
}

// adopt moves what the page uploaded to `upload` in as account uid's file
// `name` (replacing `id`, or the one the name gives)
func (c *cloudShared) adopt(ctx context.Context, uid, name, id, upload string) (sharedFile, error) {
	s := c.of(uid)
	if s == nil {
		return sharedFile{}, sharedErr{401, "sign in again"}
	}
	dir := cloudUploadDir(uid)
	rest, ok := strings.CutPrefix(upload, dir)
	if !ok || rest == "" || strings.Contains(rest, "/") || strings.Contains(rest, "..") {
		return sharedFile{}, sharedErr{400, "the upload is not yours"}
	}
	defer c.bucket.Remove(upload)
	return s.place(ctx, name, id, func(src string) (int64, string, error) {
		size, sum, err := c.bucket.Move(ctx, upload, cloudSharedRoot(uid)+src)
		if err != nil {
			return 0, "", sharedErr{400, "the upload was not found; add the file again"}
		}
		return size, sum, nil
	})
}

// a store seen from a folder of it: the shared files of one account
type prefixStore struct {
	b   sharedStore
	pre string
}

func (p prefixStore) Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error {
	return p.b.Save(ctx, p.pre+path, contentType, data, metadata)
}

func (p prefixStore) SaveFrom(ctx context.Context, path, contentType string, r io.Reader) (store.BlobInfo, error) {
	return p.b.SaveFrom(ctx, p.pre+path, contentType, r)
}

func (p prefixStore) Read(ctx context.Context, path string, limit int64) ([]byte, error) {
	return p.b.Read(ctx, p.pre+path, limit)
}

func (p prefixStore) List(ctx context.Context, dir string) ([]store.FileRef, error) {
	refs, err := p.b.List(ctx, p.pre+dir)
	for i := range refs {
		refs[i].Path = strings.TrimPrefix(refs[i].Path, p.pre)
	}
	return refs, err
}

func (p prefixStore) Remove(path string) error    { return p.b.Remove(p.pre + path) }
func (p prefixStore) RemoveAll(path string) error { return p.b.RemoveAll(p.pre + path) }

// the Firebase project and its bucket, as Cloud Run gives them
func cloudProject() string { return env("GOOGLE_CLOUD_PROJECT", env("GCLOUD_PROJECT", "sliqtly")) }

func cloudBucketName() string { return env("SLIQTLY_BUCKET", "sliqtly.firebasestorage.app") }

// figma-index <uid> <id>: the Cloud Run job's work, reading one file of one
// account (cloudjob.go starts it); its card says how it went
func figmaIndexCmd(args []string) int {
	if len(args) != 2 {
		log.Print("usage: figma-index <uid> <file id>")
		return 2
	}
	ctx := context.Background()
	e := &Env{}
	if err := connectFirebase(ctx, e, cloudProject(), cloudBucketName()); err != nil {
		log.Printf("firebase: %v", err)
		return 1
	}
	s := e.cloudShared.of(args[0])
	if s == nil || !sharedIDPattern.MatchString(args[1]) {
		log.Print("figma-index: bad account or file id")
		return 2
	}
	start := time.Now()
	s.read(ctx, args[1])
	f, err := s.card(ctx, args[1])
	if err != nil {
		log.Printf("figma-index: %s is gone", args[1])
		return 1
	}
	log.Printf("figma-index: %s %s in %s", args[1], f.Status, time.Since(start).Round(time.Millisecond))
	if f.Status != "ready" {
		return 1
	}
	return 0
}
