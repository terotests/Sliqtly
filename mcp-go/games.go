// SPDX-License-Identifier: AGPL-3.0-or-later

// Games: RangerGames games made through MCP (gameshttp.go has the routes).
// A section of its own at /games, not tied to Sliqtly's presentations.
//
// A game is a virtual folder, as the RangerGames engine reads one
// (game.json, game.tsx, style.css, pictures, sounds):
//
//	games/{id}          the game: owner, name, files [{path, sha, size}],
//	                    rev, its play links (preview always, link when
//	                    published)
//	Storage games/{id}/blobs/{sha256}
//	                    a file's bytes, named by their hash; a file written
//	                    again with other bytes gets a new blob and the old
//	                    one is removed
//	games_users/{email} who may use Games besides the admins
//	                    (SLIQTLY_ADMIN_EMAILS): invited by the admin page
//	                    (/games/main/admin), with their speech allowance
//	                    (tts_month, characters a month; 0: none)
//	games_tts/{uid}     characters of speech made, by month ("2026-11")
//
// Only the server reads and writes these (no rules let a page in). A game is
// played as one pack (RGPK1, the engine's crates/rg_core/src/vfs.rs) from
// its secret link; anyone with the link plays it, no sign-in.

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	gamesCol      = "games"
	gamesUsersCol = "games_users"
	gamesTTSCol   = "games_tts"

	gamesMaxGames   = 30        // per invited account unless the admin page says otherwise
	gamesOpenGames  = 10        // per account that signed in without an invitation
	gamesMaxFiles   = 1000      // per game
	gamesMaxFile    = 8 << 20   // one file
	gamesMaxTotal   = 100 << 20 // one game's files together
	gamesMaxPerCall = 60        // files in one write_files
	gamesMaxRead    = 2 << 20   // read_file's answer, before base64
	gamesMaxSpeech  = 1500      // characters in one speak
)

var (
	gamesPathRe  = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}){0,5}$`)
	gamesIDRe    = regexp.MustCompile(`^g[0-9a-f]{12}$`)
	gamesTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
	gamesShaRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// what a game's folder may hold, by extension; text is read back as text
var gamesTypes = map[string]struct {
	mime string
	text bool
}{
	".json": {"application/json", true}, ".tsx": {"text/plain", true}, ".ts": {"text/plain", true},
	".js": {"text/plain", true}, ".css": {"text/css", true}, ".txt": {"text/plain", true}, ".md": {"text/markdown", true},
	".png": {"image/png", false}, ".jpg": {"image/jpeg", false}, ".jpeg": {"image/jpeg", false},
	".webp": {"image/webp", false}, ".gif": {"image/gif", false},
	".wav": {"audio/wav", false}, ".mp3": {"audio/mpeg", false}, ".ogg": {"audio/ogg", false},
	// 3D models (GLTFLoader): a .glb, or a .gltf with its .bin
	".glb": {"model/gltf-binary", false}, ".gltf": {"model/gltf+json", true}, ".bin": {"application/octet-stream", false},
}

// the account calling: id, name and the address its sign-in vouched for
// (lower case)
type gamesWho struct{ uid, name, email string }

// makes speech: text → WAV bytes (gamestts.go); nil: none on this server
type gamesSpeaker func(ctx context.Context, text, lang, voice string) ([]byte, error)

type gamesService struct {
	db     DB
	bucket Bucket
	admins []string // lower case
	now    func() time.Time
	// the site's address, for the links
	base string
	// where the RangerGames web build is read from (its GitHub Pages);
	// /games/engine/ serves it from sliqtly.com (gamesengine.go)
	engineSrc string
	engine    *gamesEngine
	// removes Storage objects under a prefix; nil: blobs stay
	removePrefix func(ctx context.Context, prefix string)
	tts          gamesSpeaker
	// Games' own assistant (gamesagent.go); nil: none
	agent *geminiModel
	// only the admins and the invited may make games (SLIQTLY_GAMES_INVITE_ONLY=1);
	// otherwise any Google account with a verified address may
	inviteOnly bool
}

func newGamesService(db DB, bucket Bucket, admins []string) *gamesService {
	if db == nil || bucket == nil {
		return nil
	}
	return &gamesService{db: db, bucket: bucket, admins: admins, now: time.Now,
		engineSrc: "https://terotests.github.io/RangerGames/", engine: newGamesEngine(nil),
		inviteOnly: os.Getenv("SLIQTLY_GAMES_INVITE_ONLY") == "1"}
}

func gamesRandom(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func gamesNewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "g" + hex.EncodeToString(b)
}

func (s *gamesService) isAdmin(who gamesWho) bool {
	return who.email != "" && slices.Contains(s.admins, who.email)
}

// one who may use Games: the admins, those the admin page invited, and
// (unless inviteOnly) anyone signed in with a verified Google address, with
// fewer games and no speech until the admin page gives them more
type gamesUser struct {
	admin    bool
	ttsMonth int64 // characters of speech a month; < 0: no limit
	maxGames int   // games the account may keep; < 0: no limit
	open     bool  // signed in without an invitation
}

func (s *gamesService) user(ctx context.Context, who gamesWho) (gamesUser, error) {
	if who.uid == "" {
		return gamesUser{}, roomErr{"Sign in to use Games."}
	}
	if s.isAdmin(who) {
		return gamesUser{admin: true, ttsMonth: -1, maxGames: -1}, nil
	}
	if who.email == "" {
		return gamesUser{}, roomErr{"Games needs a sign-in with a verified Google address."}
	}
	d, err := s.db.Get(ctx, gamesUsersCol, who.email)
	if err != nil {
		return gamesUser{}, err
	}
	// an address not invited: the admin page lists it (open: true) with
	// its games and speech; inviting it gives it the admin's numbers
	if d == nil || d["listed"] != true {
		if s.inviteOnly {
			return gamesUser{}, roomErr{"Games is by invitation for now: " + who.email + " has not been invited."}
		}
		if d == nil {
			s.db.Set(ctx, gamesUsersCol, who.email, Doc{"uid": who.uid, "name": who.name, "open": true, "first": s.now().UnixMilli()})
		} else if docStr(d, "uid") != who.uid {
			s.db.Update(ctx, gamesUsersCol, who.email, Doc{"uid": who.uid, "name": who.name})
		}
		c, err := s.ttsConfig(ctx)
		if err != nil {
			return gamesUser{}, err
		}
		return gamesUser{maxGames: gamesOpenGames, ttsMonth: c.OpenMonth, open: true}, nil
	}
	// the account behind the address, for the admin page's usage column
	if docStr(d, "uid") != who.uid {
		s.db.Update(ctx, gamesUsersCol, who.email, Doc{"uid": who.uid, "name": who.name})
	}
	return gamesUser{ttsMonth: docInt(d["tts_month"]), maxGames: gamesUserMax(d)}, nil
}

// the games an invited address may keep: the admin page's number, else the default
func gamesUserMax(d Doc) int {
	if v, ok := d["max_games"]; ok && v != nil {
		return int(docInt(v))
	}
	return gamesMaxGames
}

// ------------------------------------------------------------------- game --

type gamesFile struct {
	Path string
	Sha  string
	Size int64
}

type gamesGame struct {
	id  string
	doc Doc
}

func (g *gamesGame) files() []gamesFile {
	out := []gamesFile{}
	for _, m := range docMaps(g.doc, "files") {
		p, _ := m["path"].(string)
		sha, _ := m["sha"].(string)
		if p == "" || !gamesShaRe.MatchString(sha) {
			continue
		}
		out = append(out, gamesFile{p, sha, docInt(m["size"])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func gamesFilesDoc(l []gamesFile) []any {
	out := []any{}
	for _, f := range l {
		out = append(out, map[string]any{"path": f.Path, "sha": f.Sha, "size": f.Size})
	}
	return out
}

func (s *gamesService) blobPath(id, sha string) string {
	return "games/" + id + "/blobs/" + sha
}

// the caller's own game
func (s *gamesService) game(ctx context.Context, who gamesWho, id string) (*gamesGame, error) {
	if !gamesIDRe.MatchString(id) {
		return nil, roomErr{"No such game: give a game_id from list_games."}
	}
	d, err := s.db.Get(ctx, gamesCol, id)
	if err != nil {
		return nil, err
	}
	if d == nil || docStr(d, "owner") != who.uid {
		return nil, roomErr{"No such game: give a game_id from list_games."}
	}
	return &gamesGame{id, d}, nil
}

// writes the game over the one read, unless it changed in between
func (s *gamesService) save(ctx context.Context, g *gamesGame, set Doc) error {
	set["rev"] = gamesRandom(9)
	set["updated"] = s.now().UnixMilli()
	ok, err := s.db.UpdateIf(ctx, gamesCol, g.id, "rev", docStr(g.doc, "rev"), set)
	if err != nil {
		return err
	}
	if !ok {
		return roomErr{"The game changed at the same time (another call?); read it again and retry."}
	}
	for k, v := range set {
		g.doc[k] = v
	}
	return nil
}

func (s *gamesService) origin() string { return strings.TrimRight(s.base, "/") }

func (s *gamesService) playURL(token string) string {
	return s.origin() + gamesPath + "/p/" + token
}

func (s *gamesService) json(g *gamesGame, withFiles bool) map[string]any {
	out := map[string]any{
		"game_id": g.id, "name": docStr(g.doc, "name"),
		"published":   g.doc["published"] == true,
		"hidden":      g.doc["hidden"] == true,
		"preview_url": s.playURL(docStr(g.doc, "preview")),
		"updated":     time.UnixMilli(docInt(g.doc["updated"])).UTC().Format(time.RFC3339),
	}
	if g.doc["published"] == true {
		out["link"] = s.playURL(docStr(g.doc, "link"))
	}
	fs := g.files()
	var total int64
	for _, f := range fs {
		total += f.Size
	}
	out["bytes"] = total
	if withFiles {
		l := []any{}
		for _, f := range fs {
			l = append(l, map[string]any{"path": f.Path, "size": f.Size})
		}
		out["files"] = l
	} else {
		out["file_count"] = len(fs)
	}
	return out
}

func (s *gamesService) listGames(ctx context.Context, who gamesWho) (any, error) {
	docs, ids, err := s.db.WhereEq(ctx, gamesCol, "owner", who.uid)
	if err != nil {
		return nil, err
	}
	l := []any{}
	for i, d := range docs {
		l = append(l, s.json(&gamesGame{ids[i], d}, false))
	}
	sort.Slice(l, func(i, j int) bool {
		return l[i].(map[string]any)["updated"].(string) > l[j].(map[string]any)["updated"].(string)
	})
	return map[string]any{"games": l}, nil
}

func gamesName(a map[string]any) (string, error) {
	n, _ := a["name"].(string)
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > 60 {
		return "", roomErr{"name: 1 to 60 characters."}
	}
	return n, nil
}

func (s *gamesService) createGame(ctx context.Context, who gamesWho, u gamesUser, a map[string]any) (any, error) {
	name, err := gamesName(a)
	if err != nil {
		return nil, err
	}
	_, ids, err := s.db.WhereEq(ctx, gamesCol, "owner", who.uid)
	if err != nil {
		return nil, err
	}
	if u.maxGames >= 0 && len(ids) >= u.maxGames {
		return nil, roomErr{fmt.Sprintf("You have %d games, the most this account keeps (%d); delete one first (sliqtly.com/games).", len(ids), u.maxGames)}
	}
	var from *gamesGame
	if src, _ := a["copy_of"].(string); src != "" {
		if from, err = s.game(ctx, who, src); err != nil {
			return nil, err
		}
	}
	var ex *gamesExample
	if n, _ := a["example"].(string); n != "" && from == nil {
		if ex = gamesExampleOf(strings.TrimPrefix(n, "example:")); ex == nil {
			return nil, roomErr{"No such example game: " + n + " (games_guide topic examples lists them)."}
		}
	}
	id := gamesNewID()
	files := []gamesFile{}
	if from != nil {
		if files, err = s.copyFiles(ctx, from, id, name); err != nil {
			return nil, err
		}
	}
	starter := gamesStarter(name)
	if ex != nil {
		starter = ex.filesNamed(name)
	}
	for _, f := range starter {
		if from != nil {
			break
		}
		sum := sha256.Sum256(f.data)
		sha := hex.EncodeToString(sum[:])
		if err := s.bucket.Save(ctx, s.blobPath(id, sha), "application/octet-stream", f.data, nil); err != nil {
			return nil, err
		}
		files = append(files, gamesFile{f.path, sha, int64(len(f.data))})
	}
	now := s.now().UnixMilli()
	d := Doc{
		"owner": who.uid, "owner_name": who.name, "name": name,
		"created": now, "updated": now, "rev": gamesRandom(9),
		"preview": gamesRandom(24), "link": gamesRandom(24), "published": false,
		"files": gamesFilesDoc(files),
	}
	if old, err := s.db.Create(ctx, gamesCol, id, d); err != nil {
		return nil, err
	} else if old != nil {
		return nil, errors.New("game id taken")
	}
	g := &gamesGame{id, d}
	out := s.json(g, true)
	out["next"] = "The game runs as it is: open preview_url. Change game.tsx and style.css with write_files; games_guide explains the engine."
	if ex != nil {
		out["example"] = ex.Name
		out["next"] = "A copy of the example " + ex.Title + " under the new name: it runs as it is. Change it with write_files; games_guide topic example:" + ex.Name + " shows its files."
	}
	if from != nil {
		out["copy_of"] = from.id
		out["next"] = "A copy of " + docStr(from.doc, "name") + " with all its files, under the new name; the original is unchanged. Change the copy with write_files."
	}
	return out, nil
}

// another game's files into the new game id's own blobs; game.json takes
// the new name
func (s *gamesService) copyFiles(ctx context.Context, from *gamesGame, id, name string) ([]gamesFile, error) {
	fs := from.files()
	data, err := s.readFiles(ctx, from.id, fs)
	if err != nil {
		return nil, err
	}
	out := make([]gamesFile, len(fs))
	errs := make([]error, len(fs))
	sem := make(chan struct{}, gamesPackReaders)
	var wg sync.WaitGroup
	for i, f := range fs {
		b := data[i]
		if f.Path == "game.json" {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && m != nil {
				m["name"] = name
				if nb, err := json.MarshalIndent(m, "", "  "); err == nil {
					b = nb
				}
			}
		}
		sum := sha256.Sum256(b)
		out[i] = gamesFile{f.Path, hex.EncodeToString(sum[:]), int64(len(b))}
		wg.Add(1)
		go func(i int, b []byte) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[i] = s.bucket.Save(ctx, s.blobPath(id, out[i].Sha), "application/octet-stream", b, nil)
		}(i, b)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

func (s *gamesService) getGame(ctx context.Context, g *gamesGame) (any, error) {
	out := s.json(g, true)
	for _, f := range g.files() {
		if f.Path == "game.json" {
			if b, err := s.bucket.Read(ctx, s.blobPath(g.id, f.Sha), 64<<10); err == nil {
				out["game_json"] = string(b)
			}
		}
	}
	return out, nil
}

func gamesExt(p string) string { return strings.ToLower(path.Ext(p)) }

func gamesCheckPath(p string) (string, error) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "/")
	if !gamesPathRe.MatchString(p) || strings.Contains(p, "..") {
		return "", roomErr{fmt.Sprintf("%q: a path inside the game's folder, e.g. game.tsx, img/cat.png or sounds/hop.wav (letters, digits, . _ -)", p)}
	}
	if _, ok := gamesTypes[gamesExt(p)]; !ok {
		return "", roomErr{fmt.Sprintf("%q: the folder takes .json .tsx .ts .js .css .txt .md, pictures .png .jpg .webp .gif, sounds .wav .mp3 .ogg and 3D models .glb .gltf .bin", p)}
	}
	return p, nil
}

func (s *gamesService) readFile(ctx context.Context, g *gamesGame, a map[string]any) (any, error) {
	p, _ := a["path"].(string)
	p, err := gamesCheckPath(p)
	if err != nil {
		return nil, err
	}
	for _, f := range g.files() {
		if f.Path != p {
			continue
		}
		if f.Size > gamesMaxRead {
			return nil, roomErr{fmt.Sprintf("%s is %d bytes, more than read_file gives (%d).", p, f.Size, gamesMaxRead)}
		}
		b, err := s.bucket.Read(ctx, s.blobPath(g.id, f.Sha), gamesMaxRead)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"path": p, "size": len(b)}
		if gamesTypes[gamesExt(p)].text && utf8.Valid(b) {
			out["text"] = string(b)
		} else {
			out["base64"] = base64.StdEncoding.EncodeToString(b)
		}
		return out, nil
	}
	return nil, roomErr{"No file " + p + " in the game; get_game lists them."}
}

type gamesWrite struct {
	path string
	data []byte
}

func gamesWrites(v any) ([]gamesWrite, error) {
	list, _ := v.([]any)
	if len(list) == 0 {
		return nil, roomErr{"files: a list of {path, text} or {path, base64}."}
	}
	if len(list) > gamesMaxPerCall {
		return nil, roomErr{fmt.Sprintf("files: at most %d in one call.", gamesMaxPerCall)}
	}
	out := []gamesWrite{}
	seen := map[string]bool{}
	for _, x := range list {
		m, _ := x.(map[string]any)
		p, _ := m["path"].(string)
		p, err := gamesCheckPath(p)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, roomErr{p + " is given twice."}
		}
		seen[p] = true
		var data []byte
		if t, ok := m["text"].(string); ok {
			if !gamesTypes[gamesExt(p)].text {
				return nil, roomErr{p + ": a picture or sound is given as base64, not text."}
			}
			data = []byte(t)
		} else if b, ok := m["base64"].(string); ok {
			d, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b))
			if err != nil {
				return nil, roomErr{p + ": base64 that does not decode."}
			}
			data = d
		} else {
			return nil, roomErr{p + ": give text or base64."}
		}
		if len(data) > gamesMaxFile {
			return nil, roomErr{fmt.Sprintf("%s: %d bytes, more than one file may be (%d).", p, len(data), gamesMaxFile)}
		}
		if p == "game.json" {
			if err := gamesCheckManifest(data); err != nil {
				return nil, err
			}
		}
		out = append(out, gamesWrite{p, data})
	}
	return out, nil
}

// sets files and takes paths away, within the game's limits
func (s *gamesService) change(ctx context.Context, g *gamesGame, writes []gamesWrite, remove []string) (map[string]any, error) {
	files := map[string]gamesFile{}
	for _, f := range g.files() {
		files[f.Path] = f
	}
	old := map[string]bool{}
	for _, f := range files {
		old[f.Sha] = true
	}
	removed := []string{}
	for _, p := range remove {
		if _, ok := files[p]; !ok {
			return nil, roomErr{"No file " + p + " in the game."}
		}
		if p == "game.json" || p == "game.tsx" {
			return nil, roomErr{p + " is the game itself; write it instead of deleting it."}
		}
		delete(files, p)
		removed = append(removed, p)
	}
	written := []string{}
	warnings := []string{}
	for _, w := range writes {
		warnings = append(warnings, gamesTextWarnings(w.path, w.data)...)
		sum := sha256.Sum256(w.data)
		sha := hex.EncodeToString(sum[:])
		if f, ok := files[w.path]; !ok || f.Sha != sha {
			if !old[sha] {
				if err := s.bucket.Save(ctx, s.blobPath(g.id, sha), "application/octet-stream", w.data, nil); err != nil {
					return nil, err
				}
			}
		}
		files[w.path] = gamesFile{w.path, sha, int64(len(w.data))}
		written = append(written, w.path)
	}
	if len(files) > gamesMaxFiles {
		return nil, roomErr{fmt.Sprintf("A game holds at most %d files.", gamesMaxFiles)}
	}
	var total int64
	l := []gamesFile{}
	keep := map[string]bool{}
	for _, f := range files {
		total += f.Size
		l = append(l, f)
		keep[f.Sha] = true
	}
	if total > gamesMaxTotal {
		return nil, roomErr{fmt.Sprintf("The game's files would be %d bytes, more than a game may hold (%d).", total, gamesMaxTotal)}
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Path < l[j].Path })
	if err := s.save(ctx, g, Doc{"files": gamesFilesDoc(l)}); err != nil {
		return nil, err
	}
	// blobs no file names any more
	if s.removePrefix != nil {
		for sha := range old {
			if !keep[sha] {
				s.removePrefix(ctx, s.blobPath(g.id, sha))
			}
		}
	}
	out := s.json(g, false)
	if len(written) > 0 {
		out["written"] = written
	}
	if len(removed) > 0 {
		out["deleted"] = removed
	}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

func (s *gamesService) deleteGame(ctx context.Context, g *gamesGame, a map[string]any) (any, error) {
	if c, _ := a["confirm_name"].(string); c != docStr(g.doc, "name") {
		return nil, roomErr{"confirm_name: the game's name exactly, to delete it for good."}
	}
	if err := s.db.Delete(ctx, gamesCol, g.id); err != nil {
		return nil, err
	}
	s.db.Delete(ctx, gamesSnapsCol, g.id)
	if s.removePrefix != nil {
		s.removePrefix(ctx, "games/"+g.id+"/")
	}
	return map[string]any{"deleted": g.id}, nil
}

func (s *gamesService) publish(ctx context.Context, g *gamesGame, a map[string]any) (any, error) {
	on := true
	if v, ok := a["published"].(bool); ok {
		on = v
	}
	set := Doc{"published": on}
	if v, _ := a["new_link"].(bool); v {
		set["link"] = gamesRandom(24)
	}
	if err := s.save(ctx, g, set); err != nil {
		return nil, err
	}
	out := s.json(g, false)
	if on {
		out["note"] = "Anyone with the link plays the game, without signing in. new_link: true makes a new link and the old one stops working."
	}
	return out, nil
}

// game.json: an object, its name and other fields as the engine reads them
func gamesCheckManifest(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return roomErr{"game.json: not a JSON object: " + err.Error()}
	}
	if n, _ := m["name"].(string); strings.TrimSpace(n) == "" {
		return roomErr{"game.json: needs a name."}
	}
	return nil
}

// ------------------------------------------------------------------ speech --

func gamesMonth(t time.Time) string { return t.UTC().Format("2006-01") }

func (s *gamesService) speak(ctx context.Context, who gamesWho, u gamesUser, g *gamesGame, a map[string]any) (any, error) {
	if s.tts == nil {
		return nil, roomErr{"Speech is not turned on on this server."}
	}
	text, _ := a["text"].(string)
	text = strings.TrimSpace(text)
	n := int64(utf8.RuneCountInString(text))
	if n == 0 || n > gamesMaxSpeech {
		return nil, roomErr{fmt.Sprintf("text: 1 to %d characters.", gamesMaxSpeech)}
	}
	p, _ := a["path"].(string)
	p, err := gamesCheckPath(p)
	if err != nil {
		return nil, err
	}
	if gamesExt(p) != ".wav" {
		return nil, roomErr{"path: a .wav file, e.g. sounds/hello.wav."}
	}
	lang, _ := a["lang"].(string)
	if lang == "" {
		lang = "fi"
	}
	voice, _ := a["voice"].(string)
	month := gamesMonth(s.now())
	if u.ttsMonth == 0 {
		return nil, roomErr{"Speech is not in your plan; the admin can give you an allowance."}
	}
	// everyone but the admins: the site's monthly budget for speech first
	if u.ttsMonth > 0 {
		c, err := s.ttsConfig(ctx)
		if err != nil {
			return nil, err
		}
		ok, err := s.db.IncrementCapped(ctx, gamesTTSCol, gamesTTSTotal, []string{month}, n, c.CapChars)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, roomErr{"This month's speech budget for the whole site is used up; it starts again next month (or the admin can raise it)."}
		}
		ok, err = s.db.IncrementCapped(ctx, gamesTTSCol, who.uid, []string{month}, n, u.ttsMonth)
		if err != nil || !ok {
			s.db.Increment(ctx, gamesTTSCol, gamesTTSTotal, Doc{month: -n})
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, roomErr{fmt.Sprintf("This month's speech allowance (%d characters) is used up.", u.ttsMonth)}
		}
	} else if err := s.db.Increment(ctx, gamesTTSCol, who.uid, Doc{month: n}); err != nil {
		return nil, err
	}
	wav, err := s.tts(ctx, text, lang, voice)
	if err != nil {
		// what was not made is not counted
		if u.ttsMonth > 0 {
			s.db.Increment(ctx, gamesTTSCol, who.uid, Doc{month: -n})
			s.db.Increment(ctx, gamesTTSCol, gamesTTSTotal, Doc{month: -n})
		}
		return nil, err
	}
	out, err := s.change(ctx, g, []gamesWrite{{p, wav}}, nil)
	if err != nil {
		return nil, err
	}
	out["sound"] = p
	out["play"] = `rg.sound("` + p + `")`
	return out, nil
}

// --------------------------------------------------------------- the pack --

// the game a play link names: the preview link always, the link when
// published
func (s *gamesService) byToken(ctx context.Context, token string) (*gamesGame, error) {
	if !gamesTokenRe.MatchString(token) {
		return nil, nil
	}
	for _, field := range []string{"preview", "link"} {
		docs, ids, err := s.db.WhereEq(ctx, gamesCol, field, token)
		if err != nil {
			return nil, err
		}
		for i, d := range docs {
			if field == "link" && d["published"] != true {
				continue
			}
			// a hidden game plays from neither link until it is shown again
			if d["hidden"] == true {
				continue
			}
			return &gamesGame{ids[i], d}, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------- calls --

// one MCP tool call
func (s *gamesService) call(ctx context.Context, who gamesWho, op string, a map[string]any) (any, error) {
	if a == nil {
		a = map[string]any{}
	}
	if op == "games_guide" {
		return gamesGuideTopic(a)
	}
	u, err := s.user(ctx, who)
	if err != nil {
		return nil, err
	}
	switch op {
	case "list_games":
		return s.listGames(ctx, who)
	case "create_game":
		return s.createGame(ctx, who, u, a)
	}
	id, _ := a["game_id"].(string)
	g, err := s.game(ctx, who, id)
	if err != nil {
		return nil, err
	}
	switch op {
	case "get_game":
		return s.getGame(ctx, g)
	case "read_file":
		return s.readFile(ctx, g, a)
	case "write_files":
		w, err := gamesWrites(a["files"])
		if err != nil {
			return nil, err
		}
		return s.change(ctx, g, w, nil)
	case "delete_files":
		list, _ := a["paths"].([]any)
		if len(list) == 0 {
			return nil, roomErr{"paths: the files to delete."}
		}
		ps := []string{}
		for _, x := range list {
			p, _ := x.(string)
			p, err := gamesCheckPath(p)
			if err != nil {
				return nil, err
			}
			ps = append(ps, p)
		}
		return s.change(ctx, g, nil, ps)
	case "rename_game":
		name, err := gamesName(a)
		if err != nil {
			return nil, err
		}
		if err := s.save(ctx, g, Doc{"name": name}); err != nil {
			return nil, err
		}
		return s.json(g, false), nil
	case "publish_game":
		return s.publish(ctx, g, a)
	case "delete_game":
		return s.deleteGame(ctx, g, a)
	case "speak":
		return s.speak(ctx, who, u, g, a)
	case "screenshot_game":
		return s.screenshot(ctx, g, a)
	case "play_game":
		return s.play(ctx, g, a, false)
	case "inspect_game":
		return s.play(ctx, g, a, true)
	}
	return nil, roomErr{"No such tool: " + op}
}

// ------------------------------------------------------------- the starter --

type gamesStarterFile struct {
	path string
	data []byte
}

// a new game's files: a small tapping game that runs as it is, for the
// assistant to change
func gamesStarter(name string) []gamesStarterFile {
	manifest, _ := json.MarshalIndent(map[string]any{
		"name": name, "description": "", "icon": "⭐", "color": "#3a86ff", "players": "1", "width": 360,
	}, "", "  ")
	return []gamesStarterFile{
		{"game.json", manifest},
		{"game.tsx", []byte(gamesStarterTSX)},
		{"style.css", []byte(gamesStarterCSS)},
	}
}

const gamesStarterTSX = `// A star to tap: each tap moves it and adds a point.
let score = 0;
let x = 180, y = 320;

function init() {
  x = width / 2;
  y = height / 2;
}

function tick(dt: number) {}

function hit() {
  score++;
  rg.sound("coin");
  rg.vibrate(30);
  if (score % 5 === 0) rg.celebrate(x, y, { big: true });
  x = rg.range(60, width - 60);
  y = rg.range(140, height - 80);
}

function view() {
  return (
    <div className="screen">
      <div className="score">{"⭐ " + score}</div>
      <div className="star" style={{ left: x - 50, top: y - 50 }} onClick={hit}>⭐</div>
    </div>
  );
}
`

const gamesStarterCSS = `.screen { width: 100%; height: 100%; align-items: center; background-color: #1b1f3b; }
.score { margin-top: 70px; font-size: 36px; font-weight: 700; color: #ffffff; }
.star { position: absolute; width: 100px; height: 100px; align-items: center; justify-content: center; font-size: 64px; }
`

// games_guide: the core guide, or one topic of it
func gamesGuideTopic(a map[string]any) (any, error) {
	t, _ := a["topic"].(string)
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "" || t == "core" || t == "guide":
		return map[string]any{"guide": gamesGuide}, nil
	case t == "3d":
		return map[string]any{"guide": gamesGuide3D}, nil
	case t == "menus" || t == "menu" || t == "screens":
		return map[string]any{"guide": gamesGuideMenus}, nil
	case t == "examples":
		return map[string]any{"guide": gamesExamplesText()}, nil
	}
	if x := gamesExampleOf(strings.TrimPrefix(t, "example:")); x != nil {
		return map[string]any{"guide": gamesExampleText(x)}, nil
	}
	return nil, roomErr{"No such topic: " + t + ". Topics: menus, 3d, examples, example:<name>."}
}

// speech for those not invited, and the site's monthly budget for everyone
// but the admins (games_config/tts; the admin page sets them)
const (
	gamesTTSTotal     = "_total" // games_tts: all non-admin characters a month
	gamesTTSOpenMonth = 20000    // characters a month for an account not invited
	gamesTTSCapChars  = 1700000  // all non-admin characters a month: about 20 € (Chirp 3 HD, 1M free a month)
)

type gamesTTSConfig struct {
	OpenMonth int64 `json:"open_month"`
	CapChars  int64 `json:"cap_chars"`
}

func (s *gamesService) ttsConfig(ctx context.Context) (gamesTTSConfig, error) {
	c := gamesTTSConfig{OpenMonth: gamesTTSOpenMonth, CapChars: gamesTTSCapChars}
	d, err := s.db.Get(ctx, gamesConfigCol, "tts")
	if err != nil || d == nil {
		return c, err
	}
	if v, ok := d["open_month"]; ok && v != nil {
		c.OpenMonth = docInt(v)
	}
	if v, ok := d["cap_chars"]; ok && v != nil {
		c.CapChars = docInt(v)
	}
	return c, nil
}

func (s *gamesService) ttsAdmin(ctx context.Context) (any, error) {
	c, err := s.ttsConfig(ctx)
	if err != nil {
		return nil, err
	}
	month := gamesMonth(s.now())
	used := int64(0)
	if d, err := s.db.Get(ctx, gamesTTSCol, gamesTTSTotal); err == nil && d != nil {
		used = docInt(d[month])
	}
	return map[string]any{"open_month": c.OpenMonth, "cap_chars": c.CapChars, "used": used, "month": month, "speech_on": s.tts != nil}, nil
}

func (s *gamesService) ttsAdminSet(ctx context.Context, a map[string]any) (any, error) {
	set := Doc{}
	for _, k := range []string{"open_month", "cap_chars"} {
		if v, ok := a[k].(float64); ok {
			if v < 0 || v > 100e6 {
				return nil, roomErr{"Characters: 0 to 100 000 000."}
			}
			set[k] = int64(v)
		}
	}
	d, err := s.db.Get(ctx, gamesConfigCol, "tts")
	if err != nil {
		return nil, err
	}
	if d == nil {
		err = s.db.Set(ctx, gamesConfigCol, "tts", set)
	} else {
		err = s.db.Update(ctx, gamesConfigCol, "tts", set)
	}
	if err != nil {
		return nil, err
	}
	return s.ttsAdmin(ctx)
}
