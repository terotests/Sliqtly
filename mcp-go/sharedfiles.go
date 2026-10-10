// SPDX-License-Identifier: AGPL-3.0-or-later

// The shared files: files every one on this server sees, whatever room or
// deck they work in (the editor's Files place, beside Rooms). The first
// kind is an exported Figma design (.fig), and it is why they are kept the
// way they are: a design file can be hundreds of megabytes, and a slide
// that shows its login screen must not have to open it.
//
// So a .fig is read ONCE, when it is put here, with DesignViewer (the
// package figdv, rgr/FigGo.rgr), and what is kept beside it is
//
//	shared/{id}/source.fig          the file as it came
//	shared/{id}/index.json          pages, screens, their words, parts and
//	                                metadata (DesignViewer's FigIndex)
//	shared/{id}/cut/{node}.evg.json one screen as an EVG document
//	shared/{id}/img/{name}          the pictures the screens paint
//	shared/{id}/png/{node}@{k}.png  a screen or a part drawn, made the first
//	                                time it is asked for
//	sharedmeta/{id}.json            the file's card: name, size, state
//
// A screen is drawn from its cut alone, and a part of a screen from the
// screen's cut, so after the reading nothing opens source.fig again.
//
//	GET    /api/shared                       the files
//	PUT    /api/shared/{name}[?id=]          a file, as the body; the same
//	                                         name (or ?id) replaces it
//	POST   /api/shared/{id}/rename {name}
//	DELETE /api/shared/{id}
//	GET    /files/shared/{id}/index.json
//	GET    /files/shared/{id}/png/{node}.png[?scale=1|2|3][&max=px]
//
// Only a server of its own keeps them (a folder: SLIQTLY_DATA or -data);
// everyone who reaches it is its one user, as with rooms.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/terotests/sliqtly/mcp-go/figdv"
	"github.com/terotests/sliqtly/mcp-go/store"
)

// the largest shared file, unless SLIQTLY_SHARED_MAX says (in MB)
const sharedMaxDefault = 512 << 20

// the most pixels on a drawn screen's long side
const sharedMaxSide = 4096

// what the shared files need of the kept files (localBucket)
type sharedStore interface {
	Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error
	SaveFrom(ctx context.Context, path, contentType string, r io.Reader) (store.BlobInfo, error)
	Read(ctx context.Context, path string, limit int64) ([]byte, error)
	List(ctx context.Context, dir string) ([]store.FileRef, error)
	Remove(path string) error
	RemoveAll(path string) error
}

type sharedFiles struct {
	files sharedStore
	max   int64
	log   func(string)
	// one file read at a time: reading one takes several times its size
	queue chan string
	// at most two drawings at once
	draws chan struct{}
	mu    sync.Mutex // the cards
	// the indexes as read, by file id and the card's update time, so a
	// search does not read every index from the store
	idxMu sync.Mutex
	idx   map[string]cachedIndex
	// for tests: called when a file has been read (or failed)
	indexed func(id string)
}

type cachedIndex struct {
	updated int64
	json    string
}

// a shared file's card (sharedmeta/{id}.json), as answered
type sharedFile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Bytes   int64  `json:"bytes"`
	Sha     string `json:"sha"`
	Status  string `json:"status"` // indexing | ready | failed
	Error   string `json:"error,omitempty"`
	Added   int64  `json:"added"`
	Updated int64  `json:"updated"`
	Pages   int    `json:"pages"`
	Screens int    `json:"screens"`
	Index   string `json:"index,omitempty"`
}

func newSharedFiles(files sharedStore, logf func(string)) *sharedFiles {
	max := int64(sharedMaxDefault)
	if v, err := strconv.ParseInt(os.Getenv("SLIQTLY_SHARED_MAX"), 10, 64); err == nil && v > 0 {
		max = v << 20
	}
	if logf == nil {
		logf = func(string) {}
	}
	s := &sharedFiles{files: files, max: max, log: logf, queue: make(chan string, 64), draws: make(chan struct{}, 2), idx: map[string]cachedIndex{}}
	go s.worker()
	return s
}

func sharedDir(id string) string      { return "shared/" + id }
func sharedMetaPath(id string) string { return "sharedmeta/" + id + ".json" }

// a layer id as a file name: "12:34#5:6;7:8" has no slash after this
func sharedNodeFile(node string) string { return url.PathEscape(node) }

var sharedIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// a file id from its name: lower-case letters and digits, the rest a dash,
// without the extension; "file" when nothing is left
func sharedSlug(name string) string {
	name = strings.TrimSuffix(name, ".fig")
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case r == 'ä' || r == 'å':
			b.WriteRune('a')
			dash = false
		case r == 'ö':
			b.WriteRune('o')
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "file"
	}
	return s
}

// the name a file is shown by: its last part, no control characters
func sharedName(name string) string {
	name = roomFileName(name)
	if name == "" {
		return "Untitled.fig"
	}
	return name
}

// --- the cards

func (s *sharedFiles) card(ctx context.Context, id string) (sharedFile, error) {
	var f sharedFile
	b, err := s.files.Read(ctx, sharedMetaPath(id), 1<<20)
	if err != nil {
		return f, store.ErrNotFound
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, err
	}
	return f, nil
}

func (s *sharedFiles) putCard(ctx context.Context, f sharedFile) error {
	f.Updated = time.Now().UnixMilli()
	b, _ := json.Marshal(f)
	return s.files.Save(ctx, sharedMetaPath(f.ID), "application/json", b, nil)
}

// the cards, newest first
func (s *sharedFiles) list(ctx context.Context) ([]sharedFile, error) {
	refs, err := s.files.List(ctx, "sharedmeta")
	if err != nil {
		return nil, err
	}
	out := []sharedFile{}
	for _, r := range refs {
		id := strings.TrimSuffix(strings.TrimPrefix(r.Path, "sharedmeta/"), ".json")
		if f, err := s.card(ctx, id); err == nil {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Added > out[j].Added })
	return out, nil
}

// --- putting a file in

type sharedErr struct {
	status int
	msg    string
}

func (e sharedErr) Error() string { return e.msg }

// put keeps body as the shared file `name` (replacing the file `id`, or the
// one the name gives, when there is one) and queues it to be read.
func (s *sharedFiles) put(ctx context.Context, name, id string, body io.Reader) (sharedFile, error) {
	name = sharedName(name)
	if !strings.EqualFold(pathExt(name), ".fig") {
		return sharedFile{}, sharedErr{400, "only Figma files (.fig) can be shared yet"}
	}
	if id == "" {
		id = sharedSlug(name)
	}
	if !sharedIDPattern.MatchString(id) {
		return sharedFile{}, sharedErr{400, "bad file id"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := sharedDir(id) + "/source.fig"
	info, err := s.files.SaveFrom(ctx, src, "application/octet-stream", io.LimitReader(body, s.max+1))
	if err != nil {
		return sharedFile{}, err
	}
	if info.Size > s.max {
		s.files.Remove(src)
		return sharedFile{}, sharedErr{413, fmt.Sprintf("a shared file is at most %d MB", s.max>>20)}
	}
	if info.Size == 0 {
		s.files.Remove(src)
		return sharedFile{}, sharedErr{400, "the file is empty"}
	}
	old, oldErr := s.card(ctx, id)
	f := sharedFile{ID: id, Name: name, Kind: "figma", Bytes: info.Size, Sha: fmt.Sprintf("%x", info.Hash[:]), Status: "indexing", Added: time.Now().UnixMilli()}
	if oldErr == nil {
		f.Added = old.Added
	}
	if err := s.putCard(ctx, f); err != nil {
		return sharedFile{}, err
	}
	s.queue <- id
	return f, nil
}

func pathExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

func (s *sharedFiles) rename(ctx context.Context, id, name string) (sharedFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.card(ctx, id)
	if err != nil {
		return f, err
	}
	name = strings.TrimSpace(roomFileName(name))
	if name == "" {
		return f, sharedErr{400, "the file needs a name"}
	}
	f.Name = name
	return f, s.putCard(ctx, f)
}

func (s *sharedFiles) remove(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.card(ctx, id); err != nil {
		return err
	}
	if err := s.files.RemoveAll(sharedDir(id)); err != nil {
		return err
	}
	s.forget(id)
	return s.files.Remove(sharedMetaPath(id))
}

// --- reading a file (once)

func (s *sharedFiles) worker() {
	for id := range s.queue {
		s.read(context.Background(), id)
		if s.indexed != nil {
			s.indexed(id)
		}
	}
}

// read reads the .fig and keeps its index and cuts; the card says how it
// went
func (s *sharedFiles) read(ctx context.Context, id string) {
	f, err := s.card(ctx, id)
	if err != nil {
		return
	}
	start := time.Now()
	fail := func(msg string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		f.Status, f.Error = "failed", msg
		s.putCard(ctx, f)
		s.log("shared " + id + ": " + msg)
	}
	data, err := s.files.Read(ctx, sharedDir(id)+"/source.fig", s.max+1)
	if err != nil {
		fail("the file is gone")
		return
	}
	n, err := s.cutFile(ctx, id, f.Name, data)
	data = nil
	debug.FreeOSMemory()
	if err != nil {
		fail(err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// a newer upload while this one was read queues itself again
	if cur, err := s.card(ctx, id); err == nil && cur.Sha != f.Sha {
		return
	}
	f.Status, f.Error, f.Pages, f.Screens = "ready", "", n.pages, n.screens
	f.Index = s.indexURL(id)
	s.putCard(ctx, f)
	s.forget(id)
	s.log(fmt.Sprintf("shared %s: %d screens on %d pages in %s", id, n.screens, n.pages, time.Since(start).Round(time.Millisecond)))
}

type sharedCounts struct{ pages, screens int }

// cutFile opens a design file and keeps its index, cuts and pictures in
// place of the old ones
func (s *sharedFiles) cutFile(ctx context.Context, id, name string, data []byte) (counts sharedCounts, err error) {
	// the reader is generated code: a file it cannot read must fail the
	// file, not the server
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the file could not be read (%v)", r)
		}
	}()
	d := figdv.FigGo_static_open(data, name)
	if !figdv.FigGo_static_ok(d) {
		msg := figdv.FigGo_static_err(d)
		if msg == "" {
			msg = "not a Figma file"
		}
		return counts, errors.New(msg)
	}
	ix := figdv.FigGo_static_indexJson(d)
	var head struct {
		Pages   []json.RawMessage `json:"pages"`
		Screens []json.RawMessage `json:"screens"`
	}
	if err := json.Unmarshal([]byte(ix), &head); err != nil {
		return counts, fmt.Errorf("the index did not come out as JSON: %v", err)
	}
	dir := sharedDir(id)
	for _, sub := range []string{"cut", "img", "png"} {
		if err := s.files.RemoveAll(dir + "/" + sub); err != nil {
			return counts, err
		}
	}
	saved := map[string]bool{}
	for _, node := range figdv.FigGo_static_screenIds(d) {
		cut := figdv.FigGo_static_cut(d, node)
		if cut == "" {
			continue
		}
		if err := s.files.Save(ctx, dir+"/cut/"+sharedNodeFile(node)+".evg.json", "application/json", []byte(cut), nil); err != nil {
			return counts, err
		}
		for _, img := range figdv.FigGo_static_cutImages(d, node) {
			if saved[img] {
				continue
			}
			saved[img] = true
			b := figdv.FigGo_static_imageBytes(d, img)
			if len(b) == 0 {
				continue
			}
			if err := s.files.Save(ctx, dir+"/img/"+sharedNodeFile(img), sniffPicture(b), b, nil); err != nil {
				return counts, err
			}
		}
	}
	if err := s.files.Save(ctx, dir+"/index.json", "application/json", []byte(ix), nil); err != nil {
		return counts, err
	}
	return sharedCounts{len(head.Pages), len(head.Screens)}, nil
}

func sniffPicture(b []byte) string {
	t := http.DetectContentType(b)
	if strings.HasPrefix(t, "image/") {
		return t
	}
	return "application/octet-stream"
}

func (s *sharedFiles) indexURL(id string) string { return "/files/shared/" + id + "/index.json" }

// --- the indexes, for searching

// the ready files with their index JSON, as rgr/Tools.rgr reads them
// (PresFigma's FigmaLibrary): [{id, name, status, bytes, index}]
func (s *sharedFiles) indexes(ctx context.Context) ([]map[string]any, error) {
	files, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, f := range files {
		row := map[string]any{"id": f.ID, "name": f.Name, "status": f.Status, "bytes": f.Bytes, "error": f.Error}
		if f.Status == "ready" {
			ix, err := s.index(ctx, f)
			if err != nil {
				row["status"], row["error"] = "failed", "the index is missing"
			} else {
				row["index"] = ix
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *sharedFiles) index(ctx context.Context, f sharedFile) (string, error) {
	s.idxMu.Lock()
	c, ok := s.idx[f.ID]
	s.idxMu.Unlock()
	if ok && c.updated == f.Updated {
		return c.json, nil
	}
	b, err := s.files.Read(ctx, sharedDir(f.ID)+"/index.json", 256<<20)
	if err != nil {
		return "", err
	}
	s.idxMu.Lock()
	s.idx[f.ID] = cachedIndex{f.Updated, string(b)}
	s.idxMu.Unlock()
	return string(b), nil
}

func (s *sharedFiles) forget(id string) {
	s.idxMu.Lock()
	delete(s.idx, id)
	s.idxMu.Unlock()
}

// --- drawing a screen or a part

// the screen whose cut has layer `node`: the screen itself, one the index
// lists it under, or the first cut that has the id
func (s *sharedFiles) cutOf(ctx context.Context, f sharedFile, node string) (string, error) {
	dir := sharedDir(f.ID) + "/cut/"
	if b, err := s.files.Read(ctx, dir+sharedNodeFile(node)+".evg.json", 64<<20); err == nil {
		return string(b), nil
	}
	ix, err := s.index(ctx, f)
	if err != nil {
		return "", err
	}
	var head struct {
		Screens []struct {
			ID    string `json:"id"`
			Parts []struct {
				ID string `json:"id"`
			} `json:"parts"`
		} `json:"screens"`
	}
	json.Unmarshal([]byte(ix), &head)
	for _, sc := range head.Screens {
		for _, p := range sc.Parts {
			if p.ID == node {
				b, err := s.files.Read(ctx, dir+sharedNodeFile(sc.ID)+".evg.json", 64<<20)
				if err != nil {
					return "", err
				}
				return string(b), nil
			}
		}
	}
	needle, _ := json.Marshal(node)
	want := []byte(`"id":` + string(needle))
	for _, sc := range head.Screens {
		b, err := s.files.Read(ctx, dir+sharedNodeFile(sc.ID)+".evg.json", 64<<20)
		if err == nil && bytes.Contains(b, want) {
			return string(b), nil
		}
	}
	return "", store.ErrNotFound
}

var listSrc = regexp.MustCompile(`"src":"((?:[^"\\]|\\.)*)"`)

// draw is layer `node` of file `id` as a PNG, `scale` pixels to a design
// pixel and at most maxSide on its long side; kept, so the second time is
// a read
func (s *sharedFiles) draw(ctx context.Context, id, node string, scale float64, maxSide int) ([]byte, error) {
	if scale <= 0 {
		scale = 2
	}
	if maxSide <= 0 || maxSide > sharedMaxSide {
		maxSide = sharedMaxSide
	}
	f, err := s.card(ctx, id)
	if err != nil {
		return nil, err
	}
	if f.Status != "ready" {
		return nil, sharedErr{409, f.Name + " is " + f.Status}
	}
	kept := sharedDir(id) + "/png/" + sharedNodeFile(node) + "@" + strconv.FormatFloat(scale, 'f', -1, 64) + "-" + strconv.Itoa(maxSide) + ".png"
	if b, err := s.files.Read(ctx, kept, 64<<20); err == nil {
		return b, nil
	}
	cut, err := s.cutOf(ctx, f, node)
	if err != nil {
		return nil, err
	}
	select {
	case s.draws <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.draws }()
	view := figdv.FigGo_static_view(cut, node)
	head, list, ok := strings.Cut(view, "\n")
	if !ok {
		return nil, store.ErrNotFound
	}
	var bx, by, bw, bh, cw, ch float64
	if _, err := fmt.Sscan(head, &bx, &by, &bw, &bh, &cw, &ch); err != nil || bw <= 0 || bh <= 0 {
		return nil, fmt.Errorf("layer %s has no size", node)
	}
	k := scale
	if long := math.Max(bw, bh) * k; long > float64(maxSide) {
		k = float64(maxSide) / math.Max(bw, bh)
	}
	pw, ph := int(math.Max(1, math.Round(bw*k))), int(math.Max(1, math.Round(bh*k)))
	pics := &renderPics{data: map[string][]byte{}, log: s.log}
	for _, m := range listSrc.FindAllStringSubmatch(list, -1) {
		var name string
		if json.Unmarshal([]byte(`"`+m[1]+`"`), &name) != nil || pics.data[name] != nil {
			continue
		}
		if b, err := s.files.Read(ctx, sharedDir(id)+"/img/"+sharedNodeFile(utf8Of(name)), 64<<20); err == nil {
			pics.data[name] = b
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, pw, ph))
	ox, oy := int(math.Round(-bx*k)), int(math.Round(-by*k))
	area := image.Rect(ox, oy, ox+int(math.Round(cw*k)), oy+int(math.Round(ch*k)))
	if err := renderList(dst, list, cw, ch, area, pics, nil); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, dst); err != nil {
		return nil, err
	}
	s.files.Save(ctx, kept, "image/png", b.Bytes(), nil)
	return b.Bytes(), nil
}

// --- the page's API (localweb.go)

// /api/shared…
func (s *localServer) sharedAPI(r *http.Request, rest string) (any, int, error) {
	sh := s.env.shared
	if sh == nil {
		return nil, 0, fail(404, "", "this server keeps no shared files")
	}
	ctx := r.Context()
	rest = strings.TrimPrefix(rest, "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		files, err := sh.list(ctx)
		return map[string]any{"files": files, "max": sh.max}, 200, err
	case r.Method == http.MethodPut && rest != "" && !strings.Contains(rest, "/"):
		name, err := url.PathUnescape(rest)
		if err != nil {
			return nil, 0, fail(400, "", "bad file name")
		}
		f, err := sh.put(ctx, name, r.URL.Query().Get("id"), r.Body)
		return f, 201, sharedFail(err)
	case strings.HasSuffix(rest, "/rename") && r.Method == http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
			return nil, 0, fail(400, "", "send {name}")
		}
		f, err := sh.rename(ctx, strings.TrimSuffix(rest, "/rename"), body.Name)
		return f, 200, sharedFail(err)
	case r.Method == http.MethodDelete && !strings.Contains(rest, "/"):
		return nil, 204, sharedFail(sh.remove(ctx, rest))
	}
	return nil, 0, fail(405, "", "method not allowed")
}

func sharedFail(err error) error {
	var se sharedErr
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se):
		return fail(se.status, "", se.msg)
	case errors.Is(err, store.ErrNotFound):
		return fail(404, "", "no such shared file")
	}
	return err
}

var sharedPngPath = regexp.MustCompile(`^/files/shared/([a-z0-9-]{1,48})/png/(.+)\.png$`)

// GET /files/shared/{id}/png/{node}.png: a screen or a part, drawn
func (s *localServer) sharedPicture(w http.ResponseWriter, r *http.Request) {
	sh := s.env.shared
	m := sharedPngPath.FindStringSubmatch(r.URL.EscapedPath())
	if sh == nil || m == nil {
		http.NotFound(w, r)
		return
	}
	node, err := url.PathUnescape(m[2])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	scale, _ := strconv.ParseFloat(r.URL.Query().Get("scale"), 64)
	if scale <= 0 || scale > 4 {
		scale = 2
	}
	max, _ := strconv.Atoi(r.URL.Query().Get("max"))
	b, err := sh.draw(r.Context(), m[1], node, scale, max)
	var se sharedErr
	switch {
	case errors.As(err, &se):
		http.Error(w, se.msg, se.status)
		return
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}
