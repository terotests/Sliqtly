package main

// Example games: small, complete games from RangerGames that show how a
// game is built (a state machine, a menu, saves, two players). games_guide
// shows their files (topic "example:<name>"), create_game with example
// starts a game of one's own from one, and sliqtly.com/games lists them to
// play at /games/x/<name> without signing in.

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

//go:embed assets/games-examples
var gamesExamplesFS embed.FS

type gamesExample struct {
	Name  string `json:"name"`  // its folder: create_game example, /games/x/<name>
	Title string `json:"title"` // game.json's name
	Icon  string `json:"icon"`
	Desc  string `json:"description"` // game.json's, for the page
	About string `json:"about"`       // what it shows a game maker
	files []gamesStarterFile
}

// what each example teaches; the order is the list's
var gamesExampleAbout = []struct{ name, about string }{
	{"nappikisa", "The smallest whole game (about 100 lines): two players on one phone, the top half turned 180°, a state machine (title, wait, go, point, won) timed in tick, onPointerDown and keys."},
	{"muistipeli", "A memory card game with a start menu (alone, against the phone, with a friend), a choice of size saved with rg.save, card-flip animation, a simple computer opponent, a results panel with Again and Back, and onBack."},
}

var gamesExamples = loadGamesExamples()

func loadGamesExamples() []gamesExample {
	var out []gamesExample
	for _, e := range gamesExampleAbout {
		dir := "assets/games-examples/" + e.name
		ents, err := gamesExamplesFS.ReadDir(dir)
		if err != nil {
			panic("games example " + e.name + ": " + err.Error())
		}
		x := gamesExample{Name: e.name, About: e.about}
		for _, f := range ents {
			b, _ := gamesExamplesFS.ReadFile(dir + "/" + f.Name())
			x.files = append(x.files, gamesStarterFile{f.Name(), b})
		}
		sort.Slice(x.files, func(i, j int) bool { return gamesFileOrder(x.files[i].path) < gamesFileOrder(x.files[j].path) })
		var m struct{ Name, Icon, Description string }
		for _, f := range x.files {
			if f.path == "game.json" {
				json.Unmarshal(f.data, &m)
			}
		}
		x.Title, x.Icon, x.Desc = m.Name, m.Icon, m.Description
		out = append(out, x)
	}
	return out
}

// game.json, game.tsx, style.css, then the rest by name
func gamesFileOrder(p string) string {
	switch p {
	case "game.json":
		return "0"
	case "game.tsx":
		return "1"
	case "style.css":
		return "2"
	}
	return "3" + p
}

func gamesExampleOf(name string) *gamesExample {
	for i := range gamesExamples {
		if gamesExamples[i].Name == name {
			return &gamesExamples[i]
		}
	}
	return nil
}

// the example's files for a new game: game.json takes the new name
func (x *gamesExample) filesNamed(name string) []gamesStarterFile {
	out := []gamesStarterFile{}
	for _, f := range x.files {
		if f.path == "game.json" {
			var m map[string]any
			if json.Unmarshal(f.data, &m) == nil {
				m["name"] = name
				delete(m, "i18n")
				if b, err := json.MarshalIndent(m, "", "  "); err == nil {
					f = gamesStarterFile{f.path, append(b, '\n')}
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// games_guide topic "examples" and "example:<name>"
func gamesExamplesText() string {
	var b strings.Builder
	b.WriteString("# Example games\n\nWhole, small games to learn from. Read one with games_guide topic \"example:<name>\"; start a game of the user's own from one with create_game example: \"<name>\" (then change the copy). Anyone can play them at https://sliqtly.com/games/x/<name>.\n\n")
	for _, x := range gamesExamples {
		b.WriteString("- **" + x.Name + "** (" + x.Icon + " " + x.Title + "): " + x.About + "\n")
	}
	return b.String()
}

func gamesExampleText(x *gamesExample) string {
	var b strings.Builder
	b.WriteString("# Example: " + x.Title + " (" + x.Name + ")\n\n" + x.About + "\n\nStart a game from it with create_game example: \"" + x.Name + "\". Its words come in three languages with rg.t(fi, en, es); a new game may write Finnish only.\n")
	for _, f := range x.files {
		lang := strings.TrimPrefix(f.path[strings.LastIndex(f.path, ".")+1:], ".")
		b.WriteString("\n## " + f.path + "\n\n```" + lang + "\n" + string(f.data))
		if !strings.HasSuffix(string(f.data), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}
	return b.String()
}

// the id an example's files have in a pack (a game id is g + 12 hex)
func gamesExampleID(name string) string { return "x-" + name }

// /games/x/<name>: the engine's page plays it; /games/x/<name>/game.rgpk
func serveGamesExample(env *Env, w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET only", 405)
		return
	}
	name, file, _ := strings.Cut(rest, "/")
	x := gamesExampleOf(name)
	if x == nil || (file != "" && file != "game.rgpk") {
		http.NotFound(w, r)
		return
	}
	if file == "" {
		q := url.Values{}
		q.Set("pack", gamesPath+"/x/"+name+"/game.rgpk")
		q.Set("game", gamesExampleID(name))
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, gamesEnginePath+"?"+q.Encode(), http.StatusFound)
		return
	}
	launcher := gamesLauncher()
	out := []byte("RGPK1\n" + strconv.Itoa(len(launcher)+len(x.files)) + "\n")
	for _, f := range launcher {
		out = append(out, "launcher/"+f.path+"\n"+strconv.Itoa(len(f.data))+"\n"...)
		out = append(out, f.data...)
	}
	for _, f := range x.files {
		out = append(out, gamesExampleID(name)+"/"+f.path+"\n"+strconv.Itoa(len(f.data))+"\n"...)
		out = append(out, f.data...)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.Write(out)
}
