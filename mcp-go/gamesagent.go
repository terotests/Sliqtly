// SPDX-License-Identifier: AGPL-3.0-or-later

// Games' own assistant: on sliqtly.com/games/ an admin writes what the game
// should be, and Gemini makes or changes it with Games' MCP tools (the same
// calls an outside assistant makes). The page drives the run one model
// round at a time (agent_start, then agent_step until done), so that no one
// request waits long; a round's tool calls are run in the same request and
// their answers (a screenshot's picture too) go to the model in the next.
// A run's conversation is kept in the bucket; what each round cost is added
// to the month's spend, and no round starts once the month's cap is spent.

package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
)

const (
	gamesAgentCol       = "games_agent_spend" // {month: micro-USD}, "total" and per account
	gamesConfigCol      = "games_config"      // "agent": cap_usd, model, price_in, price_out
	gamesAgentPrefix    = "games_agent/"      // the runs, in the bucket
	gamesAgentRounds    = 40                  // model rounds for one message
	gamesAgentCapUSD    = 20.0                // the month's cap when none is set
	gamesAgentMaxOut    = 32768
	gamesAgentShots     = 2     // screenshots the model still sees (older ones: words only)
	gamesAgentResultMax = 40000 // characters of one tool's answer
	gamesAgentStepPath  = gamesPath + "/agent/step"
)

// the tools the assistant may call: not publish_game or delete_game (the
// person does those on the page), and the guide is in its instructions
var gamesAgentTools = []string{"list_games", "create_game", "get_game", "read_file", "write_files", "delete_files", "rename_game", "speak", "screenshot_game", "play_game", "inspect_game"}

// the model the assistant runs on: Gemini API with GEMINI_API_KEY, else
// Vertex AI in the service's project; SLIQTLY_GAMES_AI_MODEL names it
func newGamesAgentModel(ctx context.Context) *geminiModel {
	model := env("SLIQTLY_GAMES_AI_MODEL", "gemini-4-argon")
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return &geminiModel{http: &http.Client{Timeout: 9 * time.Minute}, model: model, key: key, maxOut: gamesAgentMaxOut}
	}
	c, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		log.Printf("games: no Google credentials for Gemini: %v; no assistant", err)
		return nil
	}
	c.Timeout = 9 * time.Minute
	return &geminiModel{http: c, model: model, project: env("SLIQTLY_AI_PROJECT", cloudProject()), region: env("SLIQTLY_GAMES_AI_REGION", "global"), maxOut: gamesAgentMaxOut}
}

// one run: a conversation about one game
type gamesAgentRun struct {
	ID       string          `json:"id"`
	Key      string          `json:"key"`
	UID      string          `json:"uid"`
	Name     string          `json:"name"`
	Email    string          `json:"email"`
	GameID   string          `json:"game_id"`
	Model    string          `json:"model"`
	Contents []geminiContent `json:"contents"`
	Rounds   int             `json:"rounds"` // for the latest message
	In       int64           `json:"in"`
	Out      int64           `json:"out"`
	Micros   int64           `json:"micros"` // what the run has cost, micro-USD
	Done     bool            `json:"done"`
	Started  int64           `json:"started"`
}

// the assistant's settings (games_config/agent)
type gamesAgentConfig struct {
	CapUSD            float64
	Model             string
	PriceIn, PriceOut float64 // USD per million tokens; 0: the list price
}

func (s *gamesService) agentConfig(ctx context.Context) (gamesAgentConfig, error) {
	c := gamesAgentConfig{CapUSD: gamesAgentCapUSD}
	d, err := s.db.Get(ctx, gamesConfigCol, "agent")
	if err != nil || d == nil {
		return c, err
	}
	if v, ok := d["cap_usd"].(float64); ok {
		c.CapUSD = v
	} else if v, ok := d["cap_usd"].(int64); ok {
		c.CapUSD = float64(v)
	}
	c.Model = docStr(d, "model")
	c.PriceIn, _ = d["price_in"].(float64)
	c.PriceOut, _ = d["price_out"].(float64)
	return c, nil
}

// USD per million tokens in and out (thinking counts as out): Gemini 4
// Argon's list price, which doubles from December 2026
func (c gamesAgentConfig) prices(now time.Time) (float64, float64) {
	in, out := 2.0, 10.0
	if !now.Before(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		in, out = 4.0, 20.0
	}
	if c.PriceIn > 0 {
		in = c.PriceIn
	}
	if c.PriceOut > 0 {
		out = c.PriceOut
	}
	return in, out
}

// micro-USD: a price per million tokens is micro-USD per token
func gamesAgentCost(in, out int64, pin, pout float64) int64 {
	return int64(math.Round(float64(in)*pin + float64(out)*pout))
}

func (s *gamesService) agentSpent(ctx context.Context, id string) int64 {
	d, err := s.db.Get(ctx, gamesAgentCol, id)
	if err != nil || d == nil {
		return 0
	}
	return docInt(d[gamesMonth(s.now())])
}

func usd(micros int64) float64 { return math.Round(float64(micros)/1e4) / 100 }

func (s *gamesService) saveRun(ctx context.Context, r *gamesAgentRun) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.bucket.Save(ctx, gamesAgentPrefix+r.ID+".json", "application/json", b, nil)
}

func (s *gamesService) loadRun(ctx context.Context, id, key string) (*gamesAgentRun, error) {
	if id == "" || strings.ContainsAny(id, "/.") {
		return nil, roomErr{"No such run."}
	}
	b, err := s.bucket.Read(ctx, gamesAgentPrefix+id+".json", 64<<20)
	if err != nil {
		return nil, roomErr{"No such run."}
	}
	var r gamesAgentRun
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(r.Key), []byte(key)) != 1 {
		return nil, roomErr{"No such run."}
	}
	return &r, nil
}

const gamesAgentSystem = `You make and change games for young children's phones with the Games tools, for the person who writes to you (they speak Finnish; the children are about 3 to 9 years old). You work alone until the game is done: the person sees only your final answer and the game.

How to work:
- To change a game, read its files first (get_game, read_file). For a new one, create_game, then write its files.
- Write whole files with write_files (game.tsx, style.css, game.json and others).
- After every change, play the game with play_game and look at the pictures: tap through what you made (tap a button by its text: {"tap": {"text": "Pelaa"}}) to check that it works, that nothing is cut off or overlapping, and that the text fits. Save a point deep in the game (save_as) and continue from it (from) after the next change, instead of playing from the start again. inspect_game shows the game's variables and its laid-out elements when a picture is not enough. Fix what is wrong and look again.
- Keep the game simple, clear and kind: big touch targets, few words, Finnish text unless asked otherwise. No real people's names, photos or addresses.
- Don't ask the person questions: choose sensibly and say what you chose.
- When it is done, answer in Finnish in a few sentences: what the game is and what you made or changed. Don't paste code.

The guide to RangerGames games follows.

`

// the page's call: a new run, or the next message in one
func (s *gamesService) agentStart(ctx context.Context, who gamesWho, a map[string]any) (any, error) {
	if !s.isAdmin(who) {
		return nil, roomErr{"The assistant is only for the site's admin for now."}
	}
	if s.agent == nil {
		return nil, roomErr{"The assistant is not set up on this server."}
	}
	prompt, _ := a["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || len([]rune(prompt)) > 8000 {
		return nil, roomErr{"Write what the game should be (at most 8000 characters)."}
	}
	cfg, err := s.agentConfig(ctx)
	if err != nil {
		return nil, err
	}
	if spent := s.agentSpent(ctx, "total"); float64(spent) >= cfg.CapUSD*1e6 {
		return nil, roomErr{fmt.Sprintf("This month's budget for the assistant ($%.2f) is spent; it can be raised on the admin page.", cfg.CapUSD)}
	}
	var r *gamesAgentRun
	if id, _ := a["run_id"].(string); id != "" {
		key, _ := a["key"].(string)
		if r, err = s.loadRun(ctx, id, key); err != nil {
			return nil, err
		}
		if r.UID != who.uid {
			return nil, roomErr{"No such run."}
		}
		r.Contents = append(r.Contents, geminiContent{Role: "user", Parts: []geminiPart{{Text: prompt}}})
	} else {
		text := prompt
		if id, _ := a["game_id"].(string); id != "" {
			g, err := s.game(ctx, who, id)
			if err != nil {
				return nil, err
			}
			text += fmt.Sprintf("\n\n(The game to work on: game_id %s, %q. Read its files first.)", g.id, docStr(g.doc, "name"))
		} else {
			text += "\n\n(Make a new game for this with create_game.)"
		}
		r = &gamesAgentRun{ID: gamesRandom(12), Key: gamesRandom(16), UID: who.uid, Name: who.name, Email: who.email,
			Started: s.now().UnixMilli(), Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: text}}}}}
		r.GameID, _ = a["game_id"].(string)
	}
	r.Rounds, r.Done = 0, false
	if err := s.saveRun(ctx, r); err != nil {
		return nil, err
	}
	step := gamesAgentStepPath
	if u := gamesSelfURL(); u != "" {
		step = u + gamesAgentStepPath
	}
	return map[string]any{"run_id": r.ID, "key": r.Key, "step_url": step, "cost_usd": usd(r.Micros), "cap_usd": cfg.CapUSD, "spent_usd": usd(s.agentSpent(ctx, "total"))}, nil
}

// runs being stepped on this instance: one request at a time for a run
var gamesAgentBusy sync.Map

// one model round of a run and the tools it called
func (s *gamesService) agentStep(ctx context.Context, id, key string) (map[string]any, error) {
	r, err := s.loadRun(ctx, id, key)
	if err != nil {
		return nil, err
	}
	who := gamesWho{uid: r.UID, name: r.Name, email: r.Email}
	if !s.isAdmin(who) || s.agent == nil {
		return nil, roomErr{"The assistant is only for the site's admin for now."}
	}
	out := map[string]any{"done": r.Done, "game_id": r.GameID}
	if r.Done {
		return out, nil
	}
	if _, busy := gamesAgentBusy.LoadOrStore(id, true); busy {
		return nil, roomErr{"This run is already working."}
	}
	defer gamesAgentBusy.Delete(id)
	cfg, err := s.agentConfig(ctx)
	if err != nil {
		return nil, err
	}
	if spent := s.agentSpent(ctx, "total"); float64(spent) >= cfg.CapUSD*1e6 {
		return nil, roomErr{fmt.Sprintf("This month's budget for the assistant ($%.2f) is spent; it can be raised on the admin page.", cfg.CapUSD)}
	}
	m := *s.agent
	if cfg.Model != "" {
		m.model = cfg.Model
	}
	r.Model = m.model
	decls := []map[string]any{}
	for _, name := range gamesAgentTools {
		if t := findGamesTool(name); t != nil {
			decls = append(decls, geminiDecl(gamesAITool(*t)))
		}
	}
	gamesAgentForget(r.Contents, gamesAgentShots)
	system := gamesAgentSystem + gamesGuide
	res, err := m.generate(ctx, system, r.Contents, decls)
	var bad *geminiError
	if errors.As(err, &bad) && bad.status == http.StatusBadRequest && geminiHasPictures(r.Contents) {
		// a model that takes no pictures in a function's answer
		geminiDropPictures(r.Contents)
		res, err = m.generate(ctx, system, r.Contents, decls)
	}
	if err != nil {
		return nil, err
	}
	pin, pout := cfg.prices(s.now())
	cost := gamesAgentCost(res.in, res.out, pin, pout)
	month := gamesMonth(s.now())
	for _, k := range []string{"total", r.UID} {
		if err := s.db.Increment(ctx, gamesAgentCol, k, Doc{month: cost}); err != nil {
			log.Printf("games agent spend: %v", err)
		}
	}
	r.In += res.in
	r.Out += res.out
	r.Micros += cost
	r.Rounds++
	out["round"] = r.Rounds
	out["in"], out["out"] = res.in, res.out
	out["step_usd"] = usd(cost)
	if res.declined {
		r.Done = true
		out["text"] = "Gemini declined to answer this."
	} else {
		r.Contents = append(r.Contents, res.content)
		var text strings.Builder
		var answers []geminiPart
		var logs []any
		for _, p := range res.content.Parts {
			if c := p.FunctionCall; c != nil {
				fr, entry := s.agentTool(ctx, who, r, c)
				answers = append(answers, geminiPart{FunctionResponse: fr})
				logs = append(logs, entry)
			} else if !p.Thought {
				text.WriteString(p.Text)
			}
		}
		out["text"] = strings.TrimSpace(text.String())
		out["log"] = logs
		if len(answers) == 0 {
			r.Done = true
		} else {
			r.Contents = append(r.Contents, geminiContent{Role: "user", Parts: answers})
			if r.Rounds >= gamesAgentRounds {
				r.Done = true
				out["stopped"] = true
			}
		}
	}
	if err := s.saveRun(ctx, r); err != nil {
		return nil, err
	}
	out["done"] = r.Done
	out["game_id"] = r.GameID
	out["cost_usd"] = usd(r.Micros)
	out["spent_usd"] = usd(s.agentSpent(ctx, "total"))
	out["cap_usd"] = cfg.CapUSD
	out["model"] = r.Model
	if r.GameID != "" {
		if g, err := s.game(ctx, who, r.GameID); err == nil {
			out["preview_url"] = s.playURL(docStr(g.doc, "preview"))
			out["game_name"] = docStr(g.doc, "name")
		}
	}
	return out, nil
}

// a Games tool as the model's function
func gamesAITool(t roomTool) aiTool {
	props := t.props
	if props == nil {
		props = map[string]any{}
	}
	p := map[string]any{"type": "object", "properties": props}
	if len(t.required) > 0 {
		p["required"] = t.required
	}
	return aiTool{Name: t.name, Description: t.desc, Params: p}
}

// one tool call of the model's: its answer, and a line for the page
func (s *gamesService) agentTool(ctx context.Context, who gamesWho, r *gamesAgentRun, c *geminiCall) (*geminiResponse, map[string]any) {
	start := time.Now()
	entry := map[string]any{"tool": c.Name, "about": gamesAgentAbout(c)}
	fail := func(msg string) (*geminiResponse, map[string]any) {
		entry["error"] = msg
		entry["ms"] = time.Since(start).Milliseconds()
		return &geminiResponse{Name: c.Name, Response: map[string]any{"error": msg}}, entry
	}
	t := findGamesTool(c.Name)
	allowed := false
	for _, n := range gamesAgentTools {
		allowed = allowed || n == c.Name
	}
	if t == nil || !allowed {
		return fail("No such tool here: " + c.Name)
	}
	if !t.readOnly {
		if why := gamesLimit(who.uid); why != "" {
			return fail("Too many changes in a short time.")
		}
	}
	if gamesRunsEngine(c.Name) {
		if why := gamesShotLimit(who.uid); why != "" {
			return fail("Too many screenshots in a short time.")
		}
	}
	a := c.Args
	if a == nil {
		a = map[string]any{}
	}
	if id, _ := a["game_id"].(string); id != "" && r.GameID == "" {
		r.GameID = id
	}
	res, err := s.call(ctx, who, c.Name, a)
	if err != nil {
		var re roomErr
		if errors.As(err, &re) {
			return fail(re.msg)
		}
		log.Printf("games agent %s: %v", c.Name, err)
		return fail("The tool failed on the server; try again or another way.")
	}
	entry["ms"] = time.Since(start).Milliseconds()
	if shot, ok := res.(*gamesShotResult); ok {
		text := shot.text()
		if shot.fail != "" {
			entry["error"] = "pelin koodi kaatui"
		}
		fr := &geminiResponse{Name: c.Name, Response: map[string]any{"content": text}}
		for _, p := range shot.pictures() {
			fr.Parts = append(fr.Parts, geminiInline{InlineData: &geminiData{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(p)}})
		}
		if len(shot.png) > 0 {
			entry["png"] = base64.StdEncoding.EncodeToString(shot.png)
		}
		return fr, entry
	}
	if c.Name == "create_game" {
		if m, ok := res.(map[string]any); ok {
			if id, _ := m["game_id"].(string); id != "" {
				r.GameID = id
			}
		}
	}
	b, _ := json.Marshal(res)
	text := string(b)
	if len(text) > gamesAgentResultMax {
		text = text[:gamesAgentResultMax] + "… (cut here: the answer was too long)"
	}
	return &geminiResponse{Name: c.Name, Response: map[string]any{"content": text}}, entry
}

// a few words on what a call was about, for the page
func gamesAgentAbout(c *geminiCall) string {
	a := c.Args
	switch c.Name {
	case "write_files":
		files, _ := a["files"].([]any)
		ps := []string{}
		for _, f := range files {
			if m, ok := f.(map[string]any); ok {
				p, _ := m["path"].(string)
				ps = append(ps, p)
			}
		}
		return strings.Join(ps, ", ")
	case "delete_files":
		ps, _ := a["paths"].([]any)
		return fmt.Sprint(ps...)
	case "read_file", "speak":
		p, _ := a["path"].(string)
		return p
	case "create_game", "rename_game":
		n, _ := a["name"].(string)
		return n
	case "play_game", "inspect_game":
		steps, _ := a["steps"].([]any)
		parts := []string{}
		if f, _ := a["from"].(string); f != "" {
			parts = append(parts, "jatkaa: "+f)
		}
		if len(steps) > 0 {
			parts = append(parts, fmt.Sprintf("%d askelta", len(steps)))
		}
		if n, _ := a["save_as"].(string); n != "" {
			parts = append(parts, "tallentaa: "+n)
		}
		return strings.Join(parts, ", ")
	case "screenshot_game":
		taps, _ := a["taps"].([]any)
		f := docInt(a["frames"])
		if f == 0 {
			f = 30
		}
		if len(taps) > 0 {
			return fmt.Sprintf("%d kuvaa, %d napautusta", f, len(taps))
		}
		return fmt.Sprintf("%d kuvaa", f)
	}
	return ""
}

// pictures stay in the latest keep screenshot answers only: older ones
// become words, so the conversation (and what each round costs) stays small
func gamesAgentForget(contents []geminiContent, keep int) {
	seen := 0
	for i := len(contents) - 1; i >= 0; i-- {
		for _, p := range contents[i].Parts {
			fr := p.FunctionResponse
			if fr == nil || len(fr.Parts) == 0 {
				continue
			}
			if seen++; seen > keep {
				fr.Parts = nil
				fr.Response["content"] = fmt.Sprint(fr.Response["content"]) + "\n(An older screenshot: no longer shown.)"
			}
		}
	}
}

// POST /games/agent/step {run_id, key}: one round. The page calls it at
// the service's own address (gamesSelfURL) when it knows it, so that a
// long round is not cut by Hosting's 60 seconds; the run's key is the
// permission, so no cookie is needed there.
func serveGamesAgentStep(env *Env, w http.ResponseWriter, r *http.Request) {
	s := env.games
	w.Header().Set("Access-Control-Allow-Origin", s.origin())
	w.Header().Set("Vary", "Origin")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		editorJSON(w, 405, map[string]string{"error": "POST only."})
		return
	}
	var a struct {
		RunID string `json:"run_id"`
		Key   string `json:"key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&a); err != nil {
		editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()
	out, err := s.agentStep(ctx, a.RunID, a.Key)
	var re roomErr
	switch {
	case errors.As(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case errors.Is(err, errAINotSetUp):
		log.Printf("games agent: %v", err)
		editorJSON(w, 500, map[string]string{"error": "Gemini is not reachable with this server's settings (the model or its permission): " + err.Error()})
	case err != nil:
		log.Printf("games agent step: %v", err)
		editorJSON(w, 500, map[string]string{"error": "The round failed: " + err.Error()})
	default:
		editorJSON(w, 200, out)
	}
}

var (
	gamesSelfOnce sync.Once
	gamesSelf     string
)

// the service's own run.app address on Cloud Run (K_SERVICE set), from the
// metadata server: https://<service>-<project number>.<region>.run.app;
// "" elsewhere
func gamesSelfURL() string {
	gamesSelfOnce.Do(func() {
		svc := os.Getenv("K_SERVICE")
		if v := os.Getenv("SLIQTLY_SELF_URL"); v != "" {
			gamesSelf = strings.TrimRight(v, "/")
			return
		}
		if svc == "" {
			return
		}
		get := func(p string) string {
			req, _ := http.NewRequest(http.MethodGet, "http://metadata.google.internal/computeMetadata/v1/"+p, nil)
			req.Header.Set("Metadata-Flavor", "Google")
			c := &http.Client{Timeout: 2 * time.Second}
			res, err := c.Do(req)
			if err != nil {
				return ""
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<10))
			if res.StatusCode != 200 {
				return ""
			}
			return strings.TrimSpace(string(b))
		}
		num := get("project/numeric-project-id")
		region := get("instance/region") // projects/<number>/regions/<region>
		region = region[strings.LastIndex(region, "/")+1:]
		if num != "" && region != "" {
			gamesSelf = fmt.Sprintf("https://%s-%s.%s.run.app", svc, num, region)
		}
	})
	return gamesSelf
}

// the admin page's view of the assistant
func (s *gamesService) agentAdmin(ctx context.Context) (any, error) {
	cfg, err := s.agentConfig(ctx)
	if err != nil {
		return nil, err
	}
	pin, pout := cfg.prices(s.now())
	model := cfg.Model
	if model == "" && s.agent != nil {
		model = s.agent.model
	}
	return map[string]any{"on": s.agent != nil, "cap_usd": cfg.CapUSD, "model": model, "model_set": cfg.Model,
		"price_in": pin, "price_out": pout, "spent_usd": usd(s.agentSpent(ctx, "total")), "month": gamesMonth(s.now())}, nil
}

func (s *gamesService) agentAdminSet(ctx context.Context, a map[string]any) (any, error) {
	set := Doc{}
	if v, ok := a["cap_usd"].(float64); ok {
		if v < 0 || v > 1000 {
			return nil, roomErr{"Budget: 0 to 1000 USD a month."}
		}
		set["cap_usd"] = v
	}
	if v, ok := a["model"].(string); ok {
		v = strings.TrimSpace(v)
		if len(v) > 80 || strings.ContainsAny(v, "/ ") {
			return nil, roomErr{"A Gemini model id, e.g. gemini-4-argon."}
		}
		set["model"] = v
	}
	d, err := s.db.Get(ctx, gamesConfigCol, "agent")
	if err != nil {
		return nil, err
	}
	if d == nil {
		err = s.db.Set(ctx, gamesConfigCol, "agent", set)
	} else {
		err = s.db.Update(ctx, gamesConfigCol, "agent", set)
	}
	if err != nil {
		return nil, err
	}
	return s.agentAdmin(ctx)
}
