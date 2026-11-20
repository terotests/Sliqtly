// SPDX-License-Identifier: AGPL-3.0-or-later

// Games' play_game and inspect_game: the game played here step by step (a
// tap on a button by its text, a drag, a key, a wait) in the engine
// screenshot_game runs, with pictures on the way; inspect_game answers the
// game's variables and its laid-out elements instead of pictures.
//
// The engine is deterministic (a fixed seed, 60 frames a second), so a
// session is its steps: save_as keeps them under a name in games_snaps, and
// from plays them again before the new ones, which brings the game back to
// that point (a tap by text finds its button again after a change to the
// game's look).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	gamesSnapsCol       = "games_snaps"
	gamesPlayMaxFrames  = 3600 // a minute of game time, saved steps included
	gamesPlayMaxSteps   = 60
	gamesPlayMaxShots   = 4
	gamesPlayTimeout    = 40 * time.Second
	gamesSnapsMax       = 20
	gamesPlayWait       = 20 // frames after a tap, drag or key unless wait says
	gamesInspectMaxJSON = 30000
)

var gamesSnapNameRe = regexp.MustCompile(`^[\p{L}\p{N} _.-]{1,40}$`)

// the tools that run the engine (and count against gamesShotLimit)
func gamesRunsEngine(name string) bool {
	return name == "screenshot_game" || name == "play_game" || name == "inspect_game"
}

var gamesTargetSchema = map[string]any{"type": "object", "description": "Where: x and y in the game's coordinates, or the element by its text (the button that says it), its class (cls) or its id",
	"properties": map[string]any{
		"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"},
		"text": strProp("Text on the screen, e.g. Pelaa (exact match first, else the text that contains it)"),
		"cls":  strProp("A class name of the element"), "id": strProp("The element's id"),
	}}

var gamesPlayProps = map[string]any{
	"game_id": gamesGameID,
	"from":    strProp("A saved point (save_as) to continue from: its steps are played again first"),
	"steps": map[string]any{"type": "array", "description": "What to do, in order. Each step is one of tap, drag or key (or only a wait), then waits (20 frames, a third of a second, unless wait says)", "items": map[string]any{"type": "object", "properties": map[string]any{
		"tap":  gamesTargetSchema,
		"hold": map[string]any{"type": "integer", "description": "Frames the tap is held down (default 1)"},
		"drag": map[string]any{"type": "object", "description": "A drag from one place to another", "properties": map[string]any{
			"from": gamesTargetSchema, "to": gamesTargetSchema,
			"frames": map[string]any{"type": "integer", "description": "How long the drag takes (default 20)"}}},
		"key":  strProp("A key press, as a browser names it: ArrowLeft, Enter, a, Space…"),
		"wait": map[string]any{"type": "integer", "description": "Frames to run after the step (60 a second)"},
		"shot": map[string]any{"type": "boolean", "description": "A picture after this step (up to 4; the last frame always comes)"},
	}}},
	"save_as": strProp("Saves the whole session (from's steps and these) under this name, to continue from later"),
	"width":   map[string]any{"type": "integer", "description": "Screen width in CSS pixels, 160 to 1280 (default 360; a saved point keeps its own)"},
	"height":  map[string]any{"type": "integer", "description": "Screen height, 160 to 1280 (default 640)"},
}

var gamesPlayTools = []roomTool{
	{name: "play_game", title: "Play a game", readOnly: true,
		desc:     "Plays the game here in the RangerGames engine, step by step: tap a button by its text ({\"tap\": {\"text\": \"Pelaa\"}}) or at x, y, drag, press keys, wait, and take pictures on the way (shot). Returns the pictures (PNG), what is on the screen at the end (texts and tappable elements with their places) and the log. The game runs the same way every time, so save_as keeps the session under a name and from continues from it later, also after the game's files change. The saved points are listed in the answer.",
		props:    gamesPlayProps,
		required: []string{"game_id"}},
	{name: "inspect_game", title: "Inspect a game", readOnly: true,
		desc: "Like play_game, but answers the game's state as JSON instead of pictures: its top-level variables (game.tsx's let, const and var), what is on the screen, the laid-out element tree (tag, class, id, box in game pixels, text) and expr's value when given (a JavaScript expression run in the game, e.g. state.level).",
		props: func() map[string]any {
			m := map[string]any{"expr": strProp("A JavaScript expression run in the game after the steps; its value comes back as JSON")}
			for k, v := range gamesPlayProps {
				m[k] = v
			}
			return m
		}(),
		required: []string{"game_id"}},
}

func init() { gamesTools = append(gamesTools, gamesPlayTools...) }

// ------------------------------------------------------------------ steps --

type gamesTarget struct {
	xy              bool
	x, y            float64
	text, cls, elID string
}

func (t gamesTarget) String() string {
	switch {
	case t.text != "":
		return fmt.Sprintf("%q", t.text)
	case t.cls != "":
		return "." + t.cls
	case t.elID != "":
		return "#" + t.elID
	}
	return fmt.Sprintf("(%g, %g)", t.x, t.y)
}

type gamesStep struct {
	raw        map[string]any
	tap        *gamesTarget
	hold       int
	from, to   *gamesTarget
	dragFrames int
	key        string
	wait       int
	shot       bool
}

func gamesTargetOf(v any, what string) (*gamesTarget, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, roomErr{what + ": {x, y} or {text}, {cls} or {id}."}
	}
	t := &gamesTarget{}
	t.text, _ = m["text"].(string)
	t.cls, _ = m["cls"].(string)
	t.elID, _ = m["id"].(string)
	x, okx := m["x"].(float64)
	y, oky := m["y"].(float64)
	if okx && oky {
		if math.IsNaN(x) || math.IsNaN(y) || math.Abs(x) > 1e5 || math.Abs(y) > 1e5 {
			return nil, roomErr{what + ": x and y in the game's coordinates."}
		}
		t.xy, t.x, t.y = true, x, y
		return t, nil
	}
	if strings.TrimSpace(t.text) == "" && t.cls == "" && t.elID == "" {
		return nil, roomErr{what + ": {x, y} or {text}, {cls} or {id}."}
	}
	return t, nil
}

func gamesFrames(v any, def, lo, hi int, what string) (int, error) {
	if v == nil {
		return def, nil
	}
	f, ok := v.(float64)
	if !ok || f < float64(lo) || f > float64(hi) {
		return 0, roomErr{fmt.Sprintf("%s: a whole number from %d to %d.", what, lo, hi)}
	}
	return int(f), nil
}

func gamesSteps(v any) ([]gamesStep, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, roomErr{"steps: a list of steps."}
	}
	if len(list) > gamesPlayMaxSteps {
		return nil, roomErr{fmt.Sprintf("steps: up to %d a call (save_as and continue with from).", gamesPlayMaxSteps)}
	}
	out := []gamesStep{}
	for i, x := range list {
		m, ok := x.(map[string]any)
		what := fmt.Sprintf("steps[%d]", i)
		if !ok {
			return nil, roomErr{what + ": an object with tap, drag, key or wait."}
		}
		s := gamesStep{raw: m}
		n := 0
		var err error
		if t, ok := m["tap"]; ok {
			n++
			if s.tap, err = gamesTargetOf(t, what+".tap"); err != nil {
				return nil, err
			}
			if s.hold, err = gamesFrames(m["hold"], 1, 1, 600, what+".hold"); err != nil {
				return nil, err
			}
		}
		if d, ok := m["drag"]; ok {
			n++
			dm, _ := d.(map[string]any)
			if s.from, err = gamesTargetOf(dm["from"], what+".drag.from"); err != nil {
				return nil, err
			}
			if s.to, err = gamesTargetOf(dm["to"], what+".drag.to"); err != nil {
				return nil, err
			}
			if s.dragFrames, err = gamesFrames(dm["frames"], 20, 2, 600, what+".drag.frames"); err != nil {
				return nil, err
			}
		}
		if k, ok := m["key"]; ok {
			n++
			s.key, _ = k.(string)
			if s.key == "" || len(s.key) > 32 {
				return nil, roomErr{what + ".key: a key such as ArrowLeft, Enter or a."}
			}
		}
		if n > 1 {
			return nil, roomErr{what + ": one of tap, drag or key a step."}
		}
		def := gamesPlayWait
		if n == 0 {
			def = 0
		}
		if s.wait, err = gamesFrames(m["wait"], def, 0, gamesShotMaxFrames, what+".wait"); err != nil {
			return nil, err
		}
		s.shot = m["shot"] == true
		if n == 0 && s.wait == 0 && !s.shot {
			return nil, roomErr{what + ": tap, drag, key or wait."}
		}
		out = append(out, s)
	}
	return out, nil
}

// frames a step runs
func (s gamesStep) frames() int {
	switch {
	case s.tap != nil:
		return s.hold + 1 + s.wait
	case s.from != nil:
		return s.dragFrames + 1 + s.wait
	case s.key != "":
		return 1 + s.wait
	}
	return s.wait
}

// -------------------------------------------------------------- the tree --

type gamesNode struct {
	T    string       `json:"t"`
	ID   string       `json:"id,omitempty"`
	Cls  string       `json:"cls,omitempty"`
	X    float64      `json:"x"`
	Y    float64      `json:"y"`
	W    float64      `json:"w"`
	H    float64      `json:"h"`
	Text string       `json:"text,omitempty"`
	C    []*gamesNode `json:"c,omitempty"`
}

// the last frame's laid-out tree, and the ids that have handlers
func (r *gamesRunner) tree() (*gamesNode, map[string]bool, error) {
	if r.mod.ExportedFunction("rg_tree") == nil {
		return nil, nil, errGamesOldEngine
	}
	if _, err := r.v.call("rg_tree"); err != nil {
		return nil, nil, err
	}
	var root *gamesNode
	if err := json.Unmarshal(r.v.bytesOf("rg_result_ptr", "rg_result_len"), &root); err != nil {
		return nil, nil, err
	}
	gamesFold(root)
	// the engine's own close button (✕) goes back to the menu
	taps := map[string]bool{"__close": true}
	if v, ok, _ := r.eval(`JSON.stringify(Object.keys(typeof __handlers === "object" ? __handlers : {}).map(function (k) { return k.slice(0, k.lastIndexOf(":")); }))`); ok {
		var ids []string
		json.Unmarshal([]byte(v), &ids)
		for _, id := range ids {
			taps[id] = true
		}
	}
	return root, taps, nil
}

// a box whose only child is its text run takes the text: a smaller tree
func gamesFold(n *gamesNode) {
	if n == nil {
		return
	}
	for _, c := range n.C {
		gamesFold(c)
	}
	if len(n.C) == 1 && n.C[0].T == "#text" && n.Text == "" && len(n.C[0].C) == 0 {
		n.Text = n.C[0].Text
		n.C = nil
	}
}

var errGamesOldEngine = roomErr{"This needs a newer engine (rg_tree); give x and y instead."}

// runs JavaScript in the game: its value as text, or the error
func (r *gamesRunner) eval(src string) (string, bool, error) {
	if r.mod.ExportedFunction("rg_eval") == nil {
		return "", false, roomErr{"This needs a newer engine (rg_eval)."}
	}
	res, err := r.v.withText("rg_eval", []byte(src))
	if err != nil {
		return "", false, err
	}
	out := string(r.v.bytesOf("rg_result_ptr", "rg_result_len"))
	r.takeLog()
	return out, res == 0, nil
}

func gamesNorm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// the element a target names: the best text match (exact, then contained),
// the topmost (last drawn) when several are as good
func gamesFind(root *gamesNode, t *gamesTarget) *gamesNode {
	var best *gamesNode
	bestScore := 0
	want := gamesNorm(t.text)
	var walk func(n *gamesNode)
	walk = func(n *gamesNode) {
		score := 0
		switch {
		case t.text != "":
			have := gamesNorm(n.Text)
			if have == want {
				score = 2
			} else if have != "" && strings.Contains(have, want) {
				score = 1
			}
		case t.cls != "":
			for _, c := range strings.Fields(n.Cls) {
				if c == t.cls {
					score = 1
				}
			}
		case t.elID != "":
			if n.ID == t.elID {
				score = 1
			}
		}
		if score > 0 && score >= bestScore && n.W > 0 && n.H > 0 {
			best, bestScore = n, score
		}
		for _, c := range n.C {
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	return best
}

// a target's place in game coordinates
func (r *gamesRunner) resolve(t *gamesTarget) (float64, float64, error) {
	if t.xy {
		return t.x, t.y, nil
	}
	root, taps, err := r.tree()
	if err != nil {
		return 0, 0, err
	}
	n := gamesFind(root, t)
	if n == nil {
		lines := gamesOnScreen(root, taps, 20)
		msg := "Nothing on the screen matches " + t.String() + "."
		if len(lines) > 0 {
			msg += " On the screen:\n" + strings.Join(lines, "\n")
		}
		return 0, 0, roomErr{msg}
	}
	return n.X + n.W/2, n.Y + n.H/2, nil
}

// what is on the screen, one line each: texts and tappable elements with
// their middles and sizes
func gamesOnScreen(root *gamesNode, taps map[string]bool, max int) []string {
	out := []string{}
	var walk func(n *gamesNode, tappable bool)
	walk = func(n *gamesNode, tappable bool) {
		if len(out) >= max {
			return
		}
		own := n.ID != "" && taps[n.ID]
		text := strings.Join(strings.Fields(n.Text), " ")
		mid := fmt.Sprintf("x=%g y=%g (%g×%g)", math.Round(n.X+n.W/2), math.Round(n.Y+n.H/2), math.Round(n.W), math.Round(n.H))
		switch {
		case text != "" && n.W > 0:
			tag := ""
			if own || tappable {
				tag = " [tap]"
			}
			if len(text) > 80 {
				text = text[:80] + "…"
			}
			out = append(out, fmt.Sprintf("%q%s %s", text, tag, mid))
		case own && !gamesHasText(n):
			name := n.T
			if n.Cls != "" {
				name += "." + strings.Join(strings.Fields(n.Cls), ".")
			}
			out = append(out, fmt.Sprintf("[tap] %s %s", name, mid))
		}
		for _, c := range n.C {
			walk(c, tappable || own)
		}
	}
	if root != nil {
		walk(root, false)
	}
	return out
}

func gamesHasText(n *gamesNode) bool {
	if strings.TrimSpace(n.Text) != "" {
		return true
	}
	for _, c := range n.C {
		if gamesHasText(c) {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------- variables --

// game.tsx's top-level let, const, var, function and class names: the
// source read for brackets, strings and comments only
func gamesTopNames(src string) []string {
	rs := []rune(src)
	n := len(rs)
	depth := 0
	seen := map[string]bool{}
	out := []string{}
	isID := func(c rune) bool { return c == '_' || c == '$' || unicode.IsLetter(c) || unicode.IsDigit(c) }
	skipSpace := func(i int) int {
		for i < n && unicode.IsSpace(rs[i]) {
			i++
		}
		return i
	}
	word := func(i int) (string, int) {
		j := i
		for j < n && isID(rs[j]) {
			j++
		}
		return string(rs[i:j]), j
	}
	add := func(name string) {
		if name != "" && !unicode.IsDigit([]rune(name)[0]) && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	// a quote after a word is JSX text (Don't), unless the word is a
	// keyword that an expression follows (return 'x')
	quoteOpens := func(i int) bool {
		j := i - 1
		for j >= 0 && (rs[j] == ' ' || rs[j] == '\t') {
			j--
		}
		if j < 0 || !isID(rs[j]) {
			return true
		}
		k := j
		for k >= 0 && isID(rs[k]) {
			k--
		}
		switch string(rs[k+1 : j+1]) {
		case "return", "case", "typeof", "in", "of", "yield", "await", "void", "delete", "else", "do", "instanceof":
			return true
		}
		return false
	}
	for i := 0; i < n; {
		c := rs[i]
		switch {
		case c == '/' && i+1 < n && rs[i+1] == '/':
			for i < n && rs[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && rs[i+1] == '*':
			i += 2
			for i+1 < n && !(rs[i] == '*' && rs[i+1] == '/') {
				i++
			}
			i += 2
		case (c == '"' || c == '\'') && quoteOpens(i):
			// a string ends at its quote or the line's end
			i++
			for i < n && rs[i] != c && rs[i] != '\n' {
				if rs[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case c == '`':
			i++
			for i < n && rs[i] != '`' {
				if rs[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case c == '{' || c == '(' || c == '[':
			depth++
			i++
		case c == '}' || c == ')' || c == ']':
			if depth > 0 {
				depth--
			}
			i++
		case isID(c) && (i == 0 || (!isID(rs[i-1]) && rs[i-1] != '.')):
			w, j := word(i)
			i = j
			if depth != 0 {
				continue
			}
			switch w {
			case "let", "const", "var", "class":
				name, k := word(skipSpace(j))
				add(name)
				i = k
			case "function":
				k := skipSpace(j)
				if k < n && rs[k] == '*' {
					k = skipSpace(k + 1)
				}
				name, k2 := word(k)
				add(name)
				i = k2
			}
		default:
			i++
		}
	}
	return out
}

var gamesJSIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// a JavaScript value as JSON: functions named, repeats and cycles cut
const gamesJSJSON = `function (v) { var seen = []; return JSON.stringify(v, function (k, x) { if (typeof x === "function") return "[function]"; if (typeof x === "object" && x !== null) { if (seen.indexOf(x) >= 0) return "[seen above]"; seen.push(x); } return x; }); }`

// the expression that answers the names' values as one JSON object
func gamesGlobalsExpr(names []string) string {
	var b strings.Builder
	b.WriteString("(function () { var o = {};\n")
	for _, name := range names {
		if !gamesJSIdentRe.MatchString(name) {
			continue
		}
		fmt.Fprintf(&b, "try { if (typeof %s !== \"function\") o[%q] = %s; } catch (e) {}\n", name, name, name)
	}
	b.WriteString("return (" + gamesJSJSON + ")(o); })()")
	return b.String()
}

// --------------------------------------------------------------- snapshots --

type gamesSnap struct {
	steps  []any
	w, h   float64
	frames int
	at     int64
}

func (s *gamesService) snaps(ctx context.Context, gameID string) (map[string]gamesSnap, error) {
	d, err := s.db.Get(ctx, gamesSnapsCol, gameID)
	if err != nil {
		return nil, err
	}
	out := map[string]gamesSnap{}
	m, _ := d["snaps"].(map[string]any)
	for name, v := range m {
		sm, _ := v.(map[string]any)
		var steps []any
		if json.Unmarshal([]byte(docStr(sm, "steps")), &steps) != nil {
			continue
		}
		w, h := float64(docInt(sm["w"])), float64(docInt(sm["h"]))
		out[name] = gamesSnap{steps, w, h, int(docInt(sm["frames"])), docInt(sm["at"])}
	}
	return out, nil
}

func (s *gamesService) saveSnap(ctx context.Context, gameID, name string, all map[string]gamesSnap, sn gamesSnap) error {
	all[name] = sn
	// the oldest go when there are too many
	for len(all) > gamesSnapsMax {
		oldest := ""
		for k, v := range all {
			if k != name && (oldest == "" || v.at < all[oldest].at) {
				oldest = k
			}
		}
		delete(all, oldest)
	}
	m := map[string]any{}
	for k, v := range all {
		b, _ := json.Marshal(v.steps)
		m[k] = map[string]any{"steps": string(b), "w": v.w, "h": v.h, "frames": v.frames, "at": v.at}
	}
	return s.db.Set(ctx, gamesSnapsCol, gameID, Doc{"snaps": m})
}

func gamesSnapNames(all map[string]gamesSnap) []string {
	out := []string{}
	for k := range all {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ------------------------------------------------------------------- play --

// one step played; its pictures go to shots
func (r *gamesRunner) step(ctx context.Context, s gamesStep) (string, error) {
	need := s.frames()
	if r.frame == 0 && (s.tap != nil || s.from != nil) {
		need++
	}
	if r.frame+need > gamesPlayMaxFrames {
		return "", roomErr{fmt.Sprintf("Too long: a session runs up to %d frames (a minute of game time) in all.", gamesPlayMaxFrames)}
	}
	// a target by text needs a laid-out frame
	if r.frame == 0 && (s.tap != nil || s.from != nil) {
		if err := r.run(ctx, 1); err != nil {
			return "", err
		}
	}
	f0 := r.frame + 1
	var what string
	switch {
	case s.tap != nil:
		x, y, err := r.resolve(s.tap)
		if err != nil {
			return "", err
		}
		r.ptrs[f0] = append(r.ptrs[f0], gamesPtr{0, 0, x, y})
		r.ptrs[f0+s.hold] = append(r.ptrs[f0+s.hold], gamesPtr{2, 0, x, y})
		if err := r.run(ctx, s.hold+1); err != nil {
			return "", err
		}
		what = fmt.Sprintf("tap %s at (%g, %g)", s.tap, math.Round(x), math.Round(y))
	case s.from != nil:
		x0, y0, err := r.resolve(s.from)
		if err != nil {
			return "", err
		}
		x1, y1, err := r.resolve(s.to)
		if err != nil {
			return "", err
		}
		nf := s.dragFrames
		r.ptrs[f0] = append(r.ptrs[f0], gamesPtr{0, 0, x0, y0})
		for i := 1; i < nf; i++ {
			k := float64(i) / float64(nf)
			r.ptrs[f0+i] = append(r.ptrs[f0+i], gamesPtr{1, 0, x0 + (x1-x0)*k, y0 + (y1-y0)*k})
		}
		r.ptrs[f0+nf] = append(r.ptrs[f0+nf], gamesPtr{2, 0, x1, y1})
		if err := r.run(ctx, nf+1); err != nil {
			return "", err
		}
		what = fmt.Sprintf("drag %s → %s", s.from, s.to)
	case s.key != "":
		r.keys[f0] = append(r.keys[f0], s.key)
		if err := r.run(ctx, 1); err != nil {
			return "", err
		}
		what = "key " + s.key
	default:
		what = "wait"
	}
	if err := r.run(ctx, s.wait); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s → frame %d", what, r.frame), nil
}

func gamesSize(a map[string]any, k string, def float64) (float64, error) {
	v, ok := a[k]
	if !ok || v == nil {
		return def, nil
	}
	f, ok := v.(float64)
	if !ok || f < 160 || f > 1280 {
		return 0, roomErr{k + ": a number from 160 to 1280."}
	}
	return math.Round(f), nil
}

// play_game (pictures) and inspect_game (the state as JSON)
func (s *gamesService) play(ctx context.Context, g *gamesGame, a map[string]any, inspect bool) (any, error) {
	steps, err := gamesSteps(a["steps"])
	if err != nil {
		return nil, err
	}
	w, err := gamesSize(a, "width", 360)
	if err != nil {
		return nil, err
	}
	h, err := gamesSize(a, "height", 640)
	if err != nil {
		return nil, err
	}
	from, _ := a["from"].(string)
	saveAs, _ := a["save_as"].(string)
	saveAs = strings.TrimSpace(saveAs)
	if saveAs != "" && !gamesSnapNameRe.MatchString(saveAs) {
		return nil, roomErr{"save_as: a name of up to 40 letters, digits, spaces, dots, dashes or underscores."}
	}
	expr, _ := a["expr"].(string)
	if len(expr) > 4000 {
		return nil, roomErr{"expr: up to 4000 characters."}
	}
	all, err := s.snaps(ctx, g.id)
	if err != nil {
		return nil, err
	}
	var base []gamesStep
	if from != "" {
		sn, ok := all[from]
		if !ok {
			names := gamesSnapNames(all)
			if len(names) == 0 {
				return nil, roomErr{"from: the game has no saved points yet (save one with save_as)."}
			}
			return nil, roomErr{"from: no saved point " + fmt.Sprintf("%q", from) + "; the saved ones: " + strings.Join(names, ", ") + "."}
		}
		if base, err = gamesSteps(sn.steps); err != nil {
			return nil, roomErr{"from " + from + ": " + err.Error()}
		}
		if sn.w > 0 && sn.h > 0 {
			w, h = sn.w, sn.h
		}
	}
	pack, err := s.pack(ctx, g, gamesPackOpts{launcher: gamesLauncher(), fit: gamesRunnerFit})
	if err != nil {
		return nil, err
	}
	if s.engine == nil {
		return nil, roomErr{"Playing games is not available here."}
	}
	wasm, err := s.engine.get(ctx, s.engineSrc, "rg_web.wasm")
	if err != nil {
		return nil, err
	}
	select {
	case gamesShots.slots <- struct{}{}:
		defer func() { <-gamesShots.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	rctx, cancel := context.WithTimeout(ctx, gamesPlayTimeout)
	defer cancel()
	r, err := gamesOpen(rctx, wasm, pack, g.id, w, h)
	if err != nil {
		return nil, err
	}
	defer r.done()
	slow := func(err error) error {
		if errors.Is(err, errGamesShotSlow) {
			return roomErr{"The game took too long to run; play fewer frames at a time."}
		}
		return err
	}
	for i, st := range base {
		if _, err := r.step(rctx, st); err != nil {
			var re roomErr
			if errors.As(err, &re) {
				return nil, roomErr{fmt.Sprintf("from %s, step %d (did the game change?): %s", from, i+1, re.msg)}
			}
			return nil, slow(err)
		}
	}
	var lines []string
	if from != "" {
		lines = append(lines, fmt.Sprintf("Continued from %q (frame %d).", from, r.frame))
	}
	var shots [][]byte
	for i, st := range steps {
		line, err := r.step(rctx, st)
		if err != nil {
			var re roomErr
			if errors.As(err, &re) {
				return nil, roomErr{fmt.Sprintf("steps[%d]: %s", i, re.msg)}
			}
			return nil, slow(err)
		}
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, line))
		if st.shot && !inspect && len(shots) < gamesPlayMaxShots {
			pic, err := r.picture()
			if err != nil {
				return nil, err
			}
			shots = append(shots, pic)
			lines[len(lines)-1] += fmt.Sprintf(" (picture %d)", len(shots))
		}
	}
	if r.frame == 0 {
		if err := r.run(rctx, 30); err != nil {
			return nil, slow(err)
		}
	}
	if saveAs != "" {
		raw := []any{}
		for _, st := range append(append([]gamesStep{}, base...), steps...) {
			raw = append(raw, st.raw)
		}
		if err := s.saveSnap(ctx, g.id, saveAs, all, gamesSnap{raw, w, h, r.frame, s.now().UnixMilli()}); err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("Saved as %q: continue with from.", saveAs))
	}
	root, taps, treeErr := r.tree()
	screen := gamesOnScreen(root, taps, 60)
	if inspect {
		out := map[string]any{"frame": r.frame, "steps": lines, "on_screen": screen}
		names := gamesTopNames(gamesSource(s, ctx, g))
		v, ok, err := r.eval(gamesGlobalsExpr(names))
		switch {
		case err != nil:
			out["variables_error"] = err.Error()
		case !ok:
			out["variables_error"] = v
		case len(v) > gamesInspectMaxJSON:
			out["variables"] = "(too long to show: " + fmt.Sprint(len(v)) + " characters; ask for parts with expr)"
		case json.Valid([]byte(v)):
			out["variables"] = json.RawMessage(v)
		default:
			out["variables"] = v
		}
		if expr != "" {
			v, ok, err := r.eval("(" + gamesJSJSON + ")((" + expr + "\n))")
			switch {
			case err != nil:
				out["expr_error"] = err.Error()
			case !ok:
				out["expr_error"] = v
			case v == "undefined" || v == "":
				out["value"] = nil
			case json.Valid([]byte(v)) && len(v) <= gamesInspectMaxJSON:
				out["value"] = json.RawMessage(v)
			default:
				if len(v) > gamesInspectMaxJSON {
					v = v[:gamesInspectMaxJSON] + "…"
				}
				out["value"] = v
			}
		}
		if treeErr == nil && root != nil {
			if b, _ := json.Marshal(root); len(b) <= gamesInspectMaxJSON {
				out["tree"] = root
			} else {
				out["tree"] = "(too big to show; on_screen lists the texts and tappable elements)"
			}
		}
		if r.fail != "" {
			out["error"] = (&gamesShotResult{fail: r.fail}).text()
		}
		if l := r.logText(); l != "" {
			out["log"] = l
		}
		if names := gamesSnapNames(all); len(names) > 0 {
			out["saved_points"] = names
		}
		return out, nil
	}
	pic, err := r.picture()
	if err != nil {
		return nil, err
	}
	if len(screen) > 0 {
		lines = append(lines, fmt.Sprintf("On the screen at frame %d (game coordinates):", r.frame))
		lines = append(lines, screen...)
	}
	if names := gamesSnapNames(all); len(names) > 0 {
		lines = append(lines, "Saved points: "+strings.Join(names, ", "))
	}
	return &gamesShotResult{png: pic, shots: shots, log: r.logText(), fail: r.fail, info: strings.Join(lines, "\n")}, nil
}

// the game's entry script as text, for its top-level names
func gamesSource(s *gamesService, ctx context.Context, g *gamesGame) string {
	res, err := s.readFile(ctx, g, map[string]any{"path": "game.tsx"})
	if err != nil {
		return ""
	}
	if m, ok := res.(map[string]any); ok {
		t, _ := m["text"].(string)
		return t
	}
	return ""
}
