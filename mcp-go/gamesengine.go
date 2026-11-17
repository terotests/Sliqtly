// SPDX-License-Identifier: AGPL-3.0-or-later

// The RangerGames web build at sliqtly.com/games/engine/, so a Games link
// plays on this site: /games/p/<token> sends the browser to
// /games/engine/?pack=/games/p/<token>/game.rgpk&game=<id>.
//
// The page is assets/games-play.html; the other files are read from the
// build's own home (its GitHub Pages,
// SLIQTLY_GAMES_ENGINE) and kept here for a while, so a new engine on
// RangerGames' main reaches the games without a deploy here. Only the
// page, its scripts and the engine (rg_web.wasm) are served; the games
// pack and voice clips of the RangerGames site are not.
//
// What a game's code can reach: it runs inside the engine (CErXes in
// WebAssembly), which hands it no DOM, no fetch and no cookies, and the
// site's sign-in cookie is HttpOnly. The page itself is the engine's, not
// the game's.

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const gamesEnginePath = gamesPath + "/engine/"

// the page is our own copy of the RangerGames page, so a Games link does
// not wait for the RangerGames site to publish a change to it
//
//go:embed assets/games-play.html
var gamesPlayHTML []byte

var gamesEngineFile = regexp.MustCompile(`^(index\.html|rg_web\.wasm|sw\.js|manifest\.json|sizes\.json|icon\.svg|js/[a-z0-9-]{1,40}\.js)$`)

const (
	gamesEngineKeep = 10 * time.Minute
	gamesEngineMax  = 32 << 20
)

type gamesEngineFileCached struct {
	body []byte
	at   time.Time
}

type gamesEngine struct {
	client *http.Client
	mu     sync.Mutex
	files  map[string]gamesEngineFileCached
	now    func() time.Time
}

func newGamesEngine(client *http.Client) *gamesEngine {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &gamesEngine{client: client, files: map[string]gamesEngineFileCached{}, now: time.Now}
}

// one file of the build: kept a while; a stale copy when the source fails
func (e *gamesEngine) get(ctx context.Context, src, name string) ([]byte, error) {
	e.mu.Lock()
	c, ok := e.files[name]
	e.mu.Unlock()
	if ok && e.now().Sub(c.at) < gamesEngineKeep {
		return c.body, nil
	}
	b, err := e.fetch(ctx, src+name)
	if err != nil {
		if ok {
			log.Printf("games engine %s: %v (the copy kept is served)", name, err)
			return c.body, nil
		}
		return nil, err
	}
	e.mu.Lock()
	e.files[name] = gamesEngineFileCached{b, e.now()}
	e.mu.Unlock()
	return b, nil
}

func (e *gamesEngine) fetch(ctx context.Context, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %d", u, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, gamesEngineMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) > gamesEngineMax {
		return nil, fmt.Errorf("%s: over %d bytes", u, gamesEngineMax)
	}
	return b, nil
}

var gamesEngineTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".wasm": "application/wasm",
	".json": "application/manifest+json", ".svg": "image/svg+xml",
}

func serveGamesEngine(env *Env, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == gamesPath+"/engine" {
		http.Redirect(w, r, gamesEnginePath, http.StatusMovedPermanently)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET only", 405)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, gamesEnginePath)
	if name == "" {
		name = "index.html"
	}
	if !gamesEngineFile.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	var b []byte
	var err error
	if name == "index.html" {
		b = gamesPlayHTML
	} else {
		b, err = env.games.engine.get(r.Context(), env.games.engineSrc, name)
	}
	if err != nil {
		log.Printf("games engine %s: %v", name, err)
		http.Error(w, "The game engine could not be loaded; try again in a moment.", 503)
		return
	}
	z := gamesEngineZip(b)
	w.Header().Set("Content-Type", gamesEngineTypes[path.Ext(name)])
	w.Header().Set("ETag", z.etag)
	w.Header().Set("Vary", "Accept-Encoding")
	// a new engine reaches the players within the keeping time; the page
	// asks again every time
	if name == "index.html" || name == "sw.js" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=600")
	}
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if r.Header.Get("If-None-Match") == z.etag {
		w.WriteHeader(304)
		return
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		b = z.gz
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// an engine file compressed (rg_web.wasm goes from about 5.5 MB to under 2),
// kept for as long as the same bytes are served
type gamesEngineZipped struct {
	gz   []byte
	etag string
}

var gamesEngineZips = struct {
	sync.Mutex
	m map[[32]byte]gamesEngineZipped
}{m: map[[32]byte]gamesEngineZipped{}}

func gamesEngineZip(b []byte) gamesEngineZipped {
	key := sha256.Sum256(b)
	gamesEngineZips.Lock()
	z, ok := gamesEngineZips.m[key]
	gamesEngineZips.Unlock()
	if ok {
		return z
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(b)
	zw.Close()
	z = gamesEngineZipped{buf.Bytes(), `"` + hex.EncodeToString(key[:12]) + `"`}
	gamesEngineZips.Lock()
	if len(gamesEngineZips.m) > 64 {
		gamesEngineZips.m = map[[32]byte]gamesEngineZipped{}
	}
	gamesEngineZips.m[key] = z
	gamesEngineZips.Unlock()
	return z
}
