// SPDX-License-Identifier: AGPL-3.0-or-later

// The pack a Games link plays (/games/p/<token>/game.rgpk) and the files it
// leaves to be fetched later (/games/p/<token>/b/<sha>).
//
// A game's files are read from Storage side by side and the game's part of
// the pack is kept in memory for its revision, so a second load does not read
// Storage again. With ?lazy=1 the pack leaves out the .wav files (the voice
// clips speak makes are most of a game's size) and lists them in
// <id>/.remote.json ({path: sha}); the page fetches a clip from b/<sha> the
// first time the game says it. A blob's address is its content, so it is
// cached for good.
//
// The close button opens /games/launcher, which starts the game again at its
// first page (no menu in between). With ?menu=0 (the editor's frame) the pack
// has no launcher, and the engine then draws no close button.

package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	gamesPackReaders = 16
	gamesPackKeep    = 96 << 20 // bytes of game parts kept in memory
	gamesTokenKeep   = 20 * time.Second
)

// a pack bigger than this is streamed file by file (chunked, no
// Content-Length) instead of built in memory: Cloud Run refuses a response
// of more than 32 MB that is not streamed, and a 250 MB game would not fit
// an instance's memory (a var for the tests)
var gamesPackStreamAbove int64 = 16 << 20

type gamesPackOpts struct {
	lazy bool
	// the menu's files; nil: no menu
	launcher []gamesStarterFile
}

type gamesPackPart struct {
	body  []byte
	count int
	used  time.Time
}

type gamesPackCache struct {
	mu    sync.Mutex
	parts map[string]*gamesPackPart
	size  int
	// play links read in the last seconds (the clips of one game come in a
	// burst, each naming the link)
	tokens map[string]gamesTokenSeen
}

type gamesTokenSeen struct {
	g  *gamesGame
	at time.Time
}

var gamesPacks = &gamesPackCache{parts: map[string]*gamesPackPart{}, tokens: map[string]gamesTokenSeen{}}

func (c *gamesPackCache) get(key string) *gamesPackPart {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.parts[key]
	if p != nil {
		p.used = time.Now()
	}
	return p
}

func (c *gamesPackCache) put(key string, p *gamesPackPart) {
	if len(p.body) > gamesPackKeep/4 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.parts[key]; old != nil {
		c.size -= len(old.body)
	}
	p.used = time.Now()
	c.parts[key] = p
	c.size += len(p.body)
	for c.size > gamesPackKeep {
		var oldest string
		var at time.Time
		for k, v := range c.parts {
			if oldest == "" || v.used.Before(at) {
				oldest, at = k, v.used
			}
		}
		c.size -= len(c.parts[oldest].body)
		delete(c.parts, oldest)
	}
}

func gamesLazyFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".wav")
}

// the game's files read side by side, in the order given
func (s *gamesService) readFiles(ctx context.Context, id string, fs []gamesFile) ([][]byte, error) {
	out := make([][]byte, len(fs))
	errs := make([]error, len(fs))
	sem := make(chan struct{}, gamesPackReaders)
	var wg sync.WaitGroup
	for i, f := range fs {
		wg.Add(1)
		go func(i int, f gamesFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i], errs[i] = s.bucket.Read(ctx, s.blobPath(id, f.Sha), gamesMaxFile+1)
		}(i, f)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

// the game's own part of a pack: its entries and how many
func (s *gamesService) packPart(ctx context.Context, g *gamesGame, lazy bool) (*gamesPackPart, error) {
	key := g.id + "/" + docStr(g.doc, "rev") + "/" + strconv.FormatBool(lazy)
	if rev := docStr(g.doc, "rev"); rev != "" {
		if p := gamesPacks.get(key); p != nil {
			return p, nil
		}
	}
	fs := g.files()
	var read []gamesFile
	remote := map[string]string{}
	for _, f := range fs {
		if lazy && gamesLazyFile(f.Path) {
			remote[f.Path] = f.Sha
		} else {
			read = append(read, f)
		}
	}
	data, err := s.readFiles(ctx, g.id, read)
	if err != nil {
		return nil, err
	}
	var out []byte
	for i, f := range read {
		out = append(out, g.id+"/"+f.Path+"\n"+strconv.Itoa(len(data[i]))+"\n"...)
		out = append(out, data[i]...)
	}
	n := len(read)
	if len(remote) > 0 {
		j, _ := json.Marshal(remote)
		out = append(out, g.id+"/.remote.json\n"+strconv.Itoa(len(j))+"\n"...)
		out = append(out, j...)
		n++
	}
	p := &gamesPackPart{body: out, count: n}
	if docStr(g.doc, "rev") != "" {
		gamesPacks.put(key, p)
	}
	return p, nil
}

func (s *gamesService) pack(ctx context.Context, g *gamesGame, o gamesPackOpts) ([]byte, error) {
	part, err := s.packPart(ctx, g, o.lazy)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(part.body)+4096)
	out = append(out, "RGPK1\n"+strconv.Itoa(part.count+len(o.launcher))+"\n"...)
	for _, f := range o.launcher {
		out = append(out, "launcher/"+f.path+"\n"+strconv.Itoa(len(f.data))+"\n"...)
		out = append(out, f.data...)
	}
	return append(out, part.body...), nil
}

// the launcher's files: it starts the game again
func gamesLauncher() []gamesStarterFile {
	return []gamesStarterFile{{"game.json", gamesLauncherJSON}, {"game.tsx", gamesLauncherTSX}, {"style.css", gamesLauncherCSS}}
}

// byToken, remembered a few seconds
func (s *gamesService) byTokenSoon(ctx context.Context, token string) (*gamesGame, error) {
	c := gamesPacks
	c.mu.Lock()
	seen, ok := c.tokens[token]
	c.mu.Unlock()
	if ok && time.Since(seen.at) < gamesTokenKeep {
		return seen.g, nil
	}
	g, err := s.byToken(ctx, token)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if len(c.tokens) > 2000 {
		c.tokens = map[string]gamesTokenSeen{}
	}
	c.tokens[token] = gamesTokenSeen{g, time.Now()}
	c.mu.Unlock()
	return g, nil
}

// /games/p/<token>/game.rgpk
func serveGamesPack(env *Env, w http.ResponseWriter, r *http.Request, g *gamesGame) {
	q := r.URL.Query()
	o := gamesPackOpts{lazy: q.Get("lazy") == "1"}
	tag := docStr(g.doc, "rev")
	if o.lazy {
		tag += "-l"
	}
	// the launcher's version is in the tag, so a browser holding a pack with
	// the old menu (or none) fetches the new one
	if q.Get("menu") != "0" {
		o.launcher = gamesLauncher()
		tag += "-r1"
	} else {
		tag += "-m0"
	}
	etag := `"` + tag + `"`
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	if gamesPackSize(g, o.lazy) > gamesPackStreamAbove {
		w.Header().Set("Content-Type", "application/octet-stream")
		if err := env.games.streamPack(r.Context(), w, g, o); err != nil {
			// the start is sent: the page sees a cut pack and says so
			log.Printf("games pack %s (streamed): %v", g.id, err)
		}
		return
	}
	b, err := env.games.pack(r.Context(), g, o)
	if err != nil {
		gamesPackFail(w, g, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// the bytes of the game's files a pack holds (the lazy pack leaves out .wav)
func gamesPackSize(g *gamesGame, lazy bool) int64 {
	var n int64
	for _, f := range g.files() {
		if !(lazy && gamesLazyFile(f.Path)) {
			n += f.Size
		}
	}
	return n
}

// the pack written as it is read: a few files at a time from Storage, each
// sent (and flushed) before the next are read
func (s *gamesService) streamPack(ctx context.Context, w http.ResponseWriter, g *gamesGame, o gamesPackOpts) error {
	var read []gamesFile
	remote := map[string]string{}
	for _, f := range g.files() {
		if o.lazy && gamesLazyFile(f.Path) {
			remote[f.Path] = f.Sha
		} else {
			read = append(read, f)
		}
	}
	n := len(read) + len(o.launcher)
	if len(remote) > 0 {
		n++
	}
	flusher, _ := w.(http.Flusher)
	head := []byte("RGPK1\n" + strconv.Itoa(n) + "\n")
	for _, f := range o.launcher {
		head = append(head, "launcher/"+f.path+"\n"+strconv.Itoa(len(f.data))+"\n"...)
		head = append(head, f.data...)
	}
	if _, err := w.Write(head); err != nil {
		return err
	}
	for i := 0; i < len(read); i += gamesPackReaders {
		batch := read[i:min(i+gamesPackReaders, len(read))]
		data, err := s.readFiles(ctx, g.id, batch)
		if err != nil {
			return err
		}
		for j, f := range batch {
			if _, err := w.Write([]byte(g.id + "/" + f.Path + "\n" + strconv.Itoa(len(data[j])) + "\n")); err != nil {
				return err
			}
			if _, err := w.Write(data[j]); err != nil {
				return err
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if len(remote) > 0 {
		j, _ := json.Marshal(remote)
		if _, err := w.Write([]byte(g.id + "/.remote.json\n" + strconv.Itoa(len(j)) + "\n")); err != nil {
			return err
		}
		if _, err := w.Write(j); err != nil {
			return err
		}
	}
	return nil
}

func gamesPackFail(w http.ResponseWriter, g *gamesGame, err error) {
	log.Printf("games pack %s: %v", g.id, err)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "The game could not be read; try again in a moment.", 503)
}

// /games/p/<token>/b/<sha>: one file the pack left out
func serveGamesBlob(env *Env, w http.ResponseWriter, r *http.Request, g *gamesGame, token, sha string) {
	find := func(g *gamesGame) *gamesFile {
		for _, f := range g.files() {
			if f.Sha == sha {
				return &f
			}
		}
		return nil
	}
	var file *gamesFile
	if gamesShaRe.MatchString(sha) {
		file = find(g)
		if file == nil {
			// written after the link was last read
			if fresh, err := env.games.byToken(r.Context(), token); err == nil && fresh != nil {
				g, file = fresh, find(fresh)
			}
		}
	}
	if file == nil {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	etag := `"` + sha + `"`
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	b, err := env.games.bucket.Read(r.Context(), env.games.blobPath(g.id, sha), gamesMaxFile+1)
	if err != nil {
		gamesPackFail(w, g, err)
		return
	}
	ct := "application/octet-stream"
	if gamesLazyFile(file.Path) {
		ct = "audio/wav"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}
