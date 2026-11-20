// SPDX-License-Identifier: AGPL-3.0-or-later

// Downloadable games: the RangerGames phone app's "Lataa pelejä" page lists
// the games the admin accepted and installs one as a pack that stays on the
// phone and plays offline. This is not the play link (games.go): a play
// link is secret and always plays the latest files; a downloadable game is
// listed openly and plays the files the admin accepted, as a numbered
// version, until the admin accepts newer ones.
//
//	GET /games/catalog.json            every accepted game: name, icon,
//	                                   section, version, size, kind and
//	                                   the pack's address (anyone, CORS)
//	GET /games/d/<id>/<version>.rgpk   the accepted version as one pack,
//	                                   paths <id>/… (an add-on's
//	                                   <extends>/addons/<id>/…), cached for
//	                                   good
//
// The game's document keeps the accepted version:
//
//	download      "accepted" while it is listed ("" or missing: not)
//	dl_files      the files accepted ([{path, sha, size}]); their blobs stay
//	              while listed, whatever the owner writes after
//	dl_version    1, 2, … one more on each acceptance
//	dl_meta       game.json's name, description, icon, colour, section,
//	              players, i18n, kind, extends, as accepted
//	dl_at         when accepted
//	dl_requested  the owner asked the admin to accept the current files
//
// publish_game downloadable: true accepts at once for an admin and asks the
// admin otherwise; the admin page (/games/main/admin) lists the asks and
// the accepted games with accept and withdraw. Games as add-ons (game.json
// "kind": "addon", "extends": "<game id>") are listed with their base game;
// the app installs one into the base game's folder. Paid add-ons (in-app
// purchase) would come later: "price" is 0 for every game now.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// a pack the phone keeps in memory while it mounts it (Huawei EML-L29:
	// Java heap ~384 MB)
	gamesMaxDownload = 64 << 20
	gamesCatalogKeep = time.Minute
	gamesCatalogPath = gamesPath + "/catalog.json"
	gamesDownPath    = gamesPath + "/d/"
)

// a base game's id an add-on names: a Games id or a RangerGames folder name
var gamesExtendsRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var gamesCatalogCache struct {
	mu   sync.Mutex
	at   time.Time
	body []byte
}

func gamesCatalogForget() {
	gamesCatalogCache.mu.Lock()
	gamesCatalogCache.body = nil
	gamesCatalogCache.mu.Unlock()
}

// the accepted files, as files() reads the current ones
func (g *gamesGame) dlFiles() []gamesFile {
	return (&gamesGame{g.id, Doc{"files": g.doc["dl_files"]}}).files()
}

func (g *gamesGame) downloadable() bool {
	return docStr(g.doc, "download") == "accepted" && g.doc["hidden"] != true
}

// what publish_game and get_game say about it
func (s *gamesService) downloadJSON(g *gamesGame) map[string]any {
	out := map[string]any{}
	if docStr(g.doc, "download") == "accepted" {
		out["state"] = "accepted"
		out["version"] = docInt(g.doc["dl_version"])
		out["accepted"] = time.UnixMilli(docInt(g.doc["dl_at"])).UTC().Format(time.RFC3339)
	}
	if g.doc["dl_requested"] == true {
		out["asked"] = true
		if out["state"] == nil {
			out["state"] = "asked"
		}
	}
	if out["state"] == nil {
		return nil
	}
	return out
}

// game.json's fields the catalog shows, checked: an add-on names its base
func (s *gamesService) dlManifest(ctx context.Context, g *gamesGame, fs []gamesFile) (Doc, error) {
	var manifest *gamesFile
	var total int64
	for i, f := range fs {
		total += f.Size
		if f.Path == "game.json" {
			manifest = &fs[i]
		}
	}
	if manifest == nil {
		return nil, roomErr{"The game has no game.json."}
	}
	if total > gamesMaxDownload {
		return nil, roomErr{fmt.Sprintf("A downloadable game may be at most %d MB (the phone keeps the pack in memory while installing it); this one is %.1f MB.", gamesMaxDownload>>20, float64(total)/(1<<20))}
	}
	b, err := s.bucket.Read(ctx, s.blobPath(g.id, manifest.Sha), gamesMaxFile+1)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, roomErr{"game.json: not a JSON object: " + err.Error()}
	}
	str := func(k string) string { v, _ := m[k].(string); return strings.TrimSpace(v) }
	meta := Doc{"name": str("name"), "description": str("description"), "icon": str("icon"),
		"color": str("color"), "section": str("section"), "players": str("players"), "kind": "game"}
	if meta["name"] == "" {
		meta["name"] = docStr(g.doc, "name")
	}
	if i18n, ok := m["i18n"].(map[string]any); ok {
		meta["i18n"] = i18n
	}
	if str("kind") == "addon" {
		ext := str("extends")
		if !gamesExtendsRe.MatchString(ext) || ext == g.id {
			return nil, roomErr{"game.json: an add-on (\"kind\": \"addon\") names its base game in \"extends\" (a game id such as g0123456789ab or a RangerGames game's folder)."}
		}
		meta["kind"], meta["extends"] = "addon", ext
	}
	return meta, nil
}

// the current files become the next downloadable version
func (s *gamesService) acceptDownload(ctx context.Context, g *gamesGame) error {
	fs := g.files()
	meta, err := s.dlManifest(ctx, g, fs)
	if err != nil {
		return err
	}
	err = s.save(ctx, g, Doc{"download": "accepted", "dl_files": gamesFilesDoc(fs),
		"dl_version": docInt(g.doc["dl_version"]) + 1, "dl_meta": meta,
		"dl_at": s.now().UnixMilli(), "dl_requested": false})
	gamesCatalogForget()
	return err
}

func (s *gamesService) withdrawDownload(ctx context.Context, g *gamesGame) error {
	err := s.save(ctx, g, Doc{"download": "", "dl_requested": false})
	gamesCatalogForget()
	return err
}

// publish_game's downloadable: true / false; what to tell the caller
func (s *gamesService) publishDownload(ctx context.Context, u gamesUser, g *gamesGame, on bool) (string, error) {
	switch {
	case !on:
		if err := s.withdrawDownload(ctx, g); err != nil {
			return "", err
		}
		return "Taken out of the phone app's game list; phones that installed it keep it.", nil
	case u.admin:
		if err := s.acceptDownload(ctx, g); err != nil {
			return "", err
		}
		return fmt.Sprintf("Accepted as version %d: the RangerGames phone app lists it under ⚙️ → Lataa pelejä. Later changes reach phones only when accepted again (publish_game downloadable: true).", docInt(g.doc["dl_version"])), nil
	}
	if _, err := s.dlManifest(ctx, g, g.files()); err != nil {
		return "", err
	}
	if err := s.save(ctx, g, Doc{"dl_requested": true, "dl_asked_at": s.now().UnixMilli()}); err != nil {
		return "", err
	}
	return "Asked: the admin checks the game and accepts it for the phone app's game list (Accepted Downloadable Game). Until then it is not listed.", nil
}

// ---------------------------------------------------------------- catalog --

func (s *gamesService) catalog(ctx context.Context) ([]byte, error) {
	c := &gamesCatalogCache
	c.mu.Lock()
	if c.body != nil && time.Since(c.at) < gamesCatalogKeep {
		b := c.body
		c.mu.Unlock()
		return b, nil
	}
	c.mu.Unlock()
	docs, ids, err := s.db.WhereEq(ctx, gamesCol, "download", "accepted")
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	for i, d := range docs {
		g := &gamesGame{ids[i], d}
		if !g.downloadable() {
			continue
		}
		list = append(list, s.catalogEntry(g))
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i]["name"].(string)) < strings.ToLower(list[j]["name"].(string))
	})
	b, err := json.Marshal(map[string]any{"format": 1, "source": "Sliqtly Games", "games": list})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.body, c.at = b, time.Now()
	c.mu.Unlock()
	return b, nil
}

func (s *gamesService) catalogEntry(g *gamesGame) map[string]any {
	meta, _ := g.doc["dl_meta"].(map[string]any)
	str := func(k string) string { v, _ := meta[k].(string); return v }
	fs := g.dlFiles()
	var total int64
	for _, f := range fs {
		total += f.Size
	}
	ver := docInt(g.doc["dl_version"])
	e := map[string]any{
		"id": g.id, "name": str("name"), "description": str("description"), "icon": str("icon"),
		"color": str("color"), "section": str("section"), "players": str("players"),
		"kind": "game", "version": ver, "bytes": total, "files": len(fs),
		"updated": time.UnixMilli(docInt(g.doc["dl_at"])).UTC().Format(time.RFC3339),
		"pack":    s.origin() + gamesDownPath + g.id + "/" + strconv.FormatInt(ver, 10) + ".rgpk",
		"price":   0,
	}
	if e["name"] == "" {
		e["name"] = docStr(g.doc, "name")
	}
	if i18n, ok := meta["i18n"]; ok {
		e["i18n"] = i18n
	}
	if str("kind") == "addon" {
		e["kind"], e["extends"] = "addon", str("extends")
	}
	return e
}

// the accepted version as a pack: the game's folder, or an add-on's folder
// inside its base game
func (s *gamesService) downloadPack(ctx context.Context, g *gamesGame) ([]byte, error) {
	ver := docInt(g.doc["dl_version"])
	key := "dl/" + g.id + "/" + strconv.FormatInt(ver, 10)
	if p := gamesPacks.get(key); p != nil {
		return p.body, nil
	}
	fs := g.dlFiles()
	data, err := s.readFiles(ctx, g.id, fs)
	if err != nil {
		return nil, err
	}
	prefix := g.id + "/"
	if meta, _ := g.doc["dl_meta"].(map[string]any); meta != nil && meta["kind"] == "addon" {
		ext, _ := meta["extends"].(string)
		prefix = ext + "/addons/" + g.id + "/"
	}
	out := []byte("RGPK1\n" + strconv.Itoa(len(fs)) + "\n")
	for i, f := range fs {
		out = append(out, prefix+f.Path+"\n"+strconv.Itoa(len(data[i]))+"\n"...)
		out = append(out, data[i]...)
	}
	gamesPacks.put(key, &gamesPackPart{body: out, count: len(fs)})
	return out, nil
}

func serveGamesCatalog(env *Env, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET only", 405)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if why := gamesPlayLimit(clientIP(r)); why != "" {
		http.Error(w, "Too many requests; try again in a few minutes.", 429)
		return
	}
	b, err := env.games.catalog(r.Context())
	if err != nil {
		log.Printf("games catalog: %v", err)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "The list could not be read; try again in a moment.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write(b)
}

// /games/d/<id>/<version>.rgpk
func serveGamesDownload(env *Env, w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET only", 405)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	id, file, _ := strings.Cut(rest, "/")
	vs, ok := strings.CutSuffix(file, ".rgpk")
	ver, err := strconv.ParseInt(vs, 10, 64)
	if !ok || err != nil || ver < 1 || !gamesIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	if why := gamesPlayLimit(clientIP(r)); why != "" {
		http.Error(w, "Too many requests; try again in a few minutes.", 429)
		return
	}
	d, err := env.games.db.Get(r.Context(), gamesCol, id)
	if err != nil {
		log.Printf("games download: %v", err)
		http.Error(w, "The game could not be read; try again in a moment.", 503)
		return
	}
	g := &gamesGame{id, d}
	if d == nil || !g.downloadable() {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "No such downloadable game.", 404)
		return
	}
	if docInt(d["dl_version"]) != ver {
		// an older version: the catalog has the current one
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "This version is no longer offered; read the catalog again.", 410)
		return
	}
	etag := `"` + id + "-" + vs + `"`
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	b, err := env.games.downloadPack(r.Context(), g)
	if err != nil {
		gamesPackFail(w, g, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// ------------------------------------------------------------- admin page --

// the asks and the accepted games
func (s *gamesService) adminDownloads(ctx context.Context) (any, error) {
	seen := map[string]bool{}
	rows := []map[string]any{}
	for _, q := range []struct {
		field string
		value any
	}{{"dl_requested", true}, {"download", "accepted"}} {
		docs, ids, err := s.db.WhereEq(ctx, gamesCol, q.field, q.value)
		if err != nil {
			return nil, err
		}
		for i, d := range docs {
			if seen[ids[i]] {
				continue
			}
			seen[ids[i]] = true
			g := &gamesGame{ids[i], d}
			var total int64
			for _, f := range g.files() {
				total += f.Size
			}
			row := map[string]any{"game_id": g.id, "name": docStr(d, "name"), "owner": docStr(d, "owner_name"),
				"preview_url": s.playURL(docStr(d, "preview")), "bytes": total, "hidden": d["hidden"] == true,
				"asked": d["dl_requested"] == true}
			if docStr(d, "download") == "accepted" {
				row["version"] = docInt(d["dl_version"])
				row["accepted"] = time.UnixMilli(docInt(d["dl_at"])).UTC().Format(time.RFC3339)
				row["changed"] = docInt(d["updated"]) > docInt(d["dl_at"])+1000
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i]["asked"] != rows[j]["asked"] {
			return rows[i]["asked"] == true
		}
		return strings.ToLower(rows[i]["name"].(string)) < strings.ToLower(rows[j]["name"].(string))
	})
	return map[string]any{"games": rows, "catalog": s.origin() + gamesCatalogPath}, nil
}

func (s *gamesService) adminDownloadSet(ctx context.Context, a map[string]any, accept bool) (any, error) {
	id, _ := a["game_id"].(string)
	if !gamesIDRe.MatchString(id) {
		return nil, roomErr{"game_id: a game's id."}
	}
	d, err := s.db.Get(ctx, gamesCol, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, roomErr{"No such game."}
	}
	g := &gamesGame{id, d}
	if accept {
		err = s.acceptDownload(ctx, g)
	} else {
		err = s.withdrawDownload(ctx, g)
	}
	if err != nil {
		return nil, err
	}
	return s.adminDownloads(ctx)
}
