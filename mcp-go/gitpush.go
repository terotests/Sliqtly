// SPDX-License-Identifier: AGPL-3.0-or-later

// The decks' Git repository pushed to GitHub (git-storage DESIGN.md §8,
// phase 2), and Git turned on and off while the server runs.
//
// The repository named in git.json `remote` gets the branch's commits
// pushPeriod (5 min) after the newest, or at once on "Save version". The
// push uses a fine-grained token for that one repository (Contents: read
// and write), kept with the connectors' key (connectors/tokens.go) as the
// connector "git" of the person "server". Before every push the
// repository's `private` flag is read from GitHub's API: to a public
// repository nothing is pushed, since every deck would be readable by all.
//
// The server is its branch's only writer: when the remote branch has
// commits this server has not got (someone pushed or edited on GitHub),
// it stops pushing and says so; taking those changes in is phase 3.
//
// API (localServer.gitAPI):
//
//	GET  /api/git                 {enabled, head, push: {...}} for the editor
//	POST /api/git/save            {deck?, message} → a commit now, pushed now
//	GET  /api/settings/git        the settings, the push's state, the newest commits
//	PUT  /api/settings/git        {enabled, remote, token?, author, maxFileMB}
//	POST /api/settings/git/push   push now
//
// The settings are changed as the connectors are: on a server with
// sign-in only on its own computer or with the owner's credential.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/connectors"
	"github.com/terotests/sliqtly/mcp-go/rgit"
	"github.com/terotests/sliqtly/mcp-go/store"
)

// the push token's place among the connectors' tokens
const (
	gitTokenConnector = "git"
	gitTokenWho       = "server"
)

var githubRepoPath = regexp.MustCompile(`^/([A-Za-z0-9_.-]{1,100})/([A-Za-z0-9_.-]{1,100}?)(\.git)?/?$`)

// githubRepoOf: "https://github.com/o/r(.git)" → "o", "r"
func githubRepoOf(remote string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(remote))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" {
		return "", "", fmt.Errorf("%q is not a GitHub repository's address (https://github.com/owner/name)", remote)
	}
	m := githubRepoPath.FindStringSubmatch(u.Path)
	if m == nil || m[2] == "" {
		return "", "", fmt.Errorf("%q is not a GitHub repository's address (https://github.com/owner/name)", remote)
	}
	return m[1], m[2], nil
}

// --- pushing

// gitPushState is what the settings page and the editor show.
type gitPushState struct {
	Remote string `json:"remote"`
	// off (no remote), waiting, pushing, done, error, stopped (needs the
	// user: a public repository, a moved branch, a refused token)
	State string `json:"state"`
	// commits not on the remote yet
	Waiting  int    `json:"waiting"`
	Pushed   string `json:"pushed,omitempty"`
	PushedAt int64  `json:"pushedAt,omitempty"`
	Error    string `json:"error,omitempty"`
	// when the next push is tried (ms); 0: none planned
	Next int64 `json:"next,omitempty"`
}

// gitPusher pushes the mirror's branch to its remote.
type gitPusher struct {
	m      *gitMirror
	tokens *connectors.Tokens
	client *http.Client
	// GitHub's API, for the private flag, and its Git (tests: a fake)
	api   string
	host  string
	now   func() time.Time
	delay time.Duration

	kick chan bool // true: now

	mu      sync.Mutex
	st      gitPushState
	pushing sync.Mutex // one push at a time
}

func newGitPusher(m *gitMirror, tokens *connectors.Tokens, client *http.Client) *gitPusher {
	p := &gitPusher{m: m, tokens: tokens, client: client, api: "https://api.github.com", host: "https://github.com", now: time.Now,
		delay: time.Duration(m.cfg.PushSeconds) * time.Second, kick: make(chan bool, 1)}
	p.st.Remote = m.cfg.Remote
	p.st.State = "off"
	if m.cfg.Remote != "" {
		p.st.State = "waiting"
	}
	return p
}

func (p *gitPusher) state() gitPushState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

func (p *gitPusher) set(f func(*gitPushState)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(&p.st)
}

// poke asks for a push: at once, or after the delay
func (p *gitPusher) poke(now bool) {
	if p == nil || p.m.cfg.Remote == "" {
		return
	}
	select {
	case p.kick <- now:
	default:
		if now {
			// a later push is planned already: make it now
			select {
			case <-p.kick:
			default:
			}
			select {
			case p.kick <- true:
			default:
			}
		}
	}
}

// run pushes until ctx ends: delay after a commit, at once when asked,
// again after a failure (1 min, doubling to 30), not again after one the
// user has to fix until asked
func (p *gitPusher) run(ctx context.Context) {
	if p.m.cfg.Remote == "" {
		return
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	backoff := time.Minute
	plan := func(d time.Duration) {
		timer.Reset(d)
		p.set(func(s *gitPushState) { s.Next = p.now().Add(d).UnixMilli() })
	}
	// what was committed while the server was stopped
	plan(10 * time.Second)
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case now := <-p.kick:
			if !now {
				if st := p.state(); st.State == "stopped" {
					continue
				}
				plan(p.delay)
				continue
			}
		case <-timer.C:
		}
		timer.Stop()
		p.set(func(s *gitPushState) { s.Next = 0 })
		err := p.push(ctx)
		switch {
		case err == nil:
			backoff = time.Minute
		case ctx.Err() != nil:
			return
		case p.state().State == "stopped":
		default:
			plan(backoff)
			backoff = min(backoff*2, 30*time.Minute)
		}
	}
}

// errGitStopped: a push the user has to do something about
type errGitStopped struct{ why string }

func (e errGitStopped) Error() string { return e.why }

// push pushes the branch's head now
func (p *gitPusher) push(ctx context.Context) error {
	p.pushing.Lock()
	defer p.pushing.Unlock()
	head, err := p.m.Head()
	if err == nil && head == "" {
		err = errors.New("the repository has no commits")
	}
	if err == nil {
		p.set(func(s *gitPushState) { s.State = "pushing"; s.Error = "" })
		err = p.pushHead(ctx, head)
	}
	if err != nil {
		var stop errGitStopped
		stopped := errors.As(err, &stop) || errors.Is(err, rgit.ErrNotFastForward)
		p.set(func(s *gitPushState) {
			s.State = "error"
			if stopped {
				s.State = "stopped"
			}
			s.Error = err.Error()
		})
		if ctx.Err() == nil {
			p.m.logf("git: push to %s: %v", p.m.cfg.Remote, err)
		}
		return err
	}
	at := p.now().UnixMilli()
	p.set(func(s *gitPushState) {
		s.State, s.Error, s.Pushed, s.PushedAt, s.Waiting = "done", "", head, at, 0
	})
	return nil
}

func (p *gitPusher) pushHead(ctx context.Context, head string) error {
	owner, name, err := githubRepoOf(p.m.cfg.Remote)
	if err != nil {
		return errGitStopped{err.Error()}
	}
	tok, err := p.tokens.Get(gitTokenConnector, gitTokenWho)
	if err != nil {
		return err
	}
	if tok == nil || tok.AccessToken == "" {
		return errGitStopped{"no token: give one in the settings (Git)"}
	}
	if err := p.checkPrivate(ctx, owner, name, tok.AccessToken); err != nil {
		return err
	}
	rm := rgit.Remote{
		URL:    p.host + "/" + owner + "/" + name + ".git",
		Client: p.client,
		Agent:  "sliqtly/" + version,
		Auth:   func(r *http.Request) { r.SetBasicAuth("x-access-token", tok.AccessToken) },
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := p.m.repo.Push(ctx, rm, p.m.cfg.Branch, head)
	if err != nil {
		if errors.Is(err, rgit.ErrNotFastForward) {
			return fmt.Errorf("%w (someone pushed to %s/%s: this server does not take changes from GitHub yet, so it stops pushing until the branch is set back or another repository is chosen)", err, owner, name)
		}
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
			return errGitStopped{"GitHub refused the token: it needs Contents read and write on " + owner + "/" + name}
		}
		return err
	}
	if res.Objects > 0 {
		p.m.logf("git: pushed %s to %s/%s (%d objects)", short7(head), owner, name, res.Objects)
	}
	return nil
}

// checkPrivate reads the repository's private flag: a public one is not
// pushed to
func (p *gitPusher) checkPrivate(ctx context.Context, owner, name, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.api+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch res.StatusCode {
	case 200:
	case 401:
		return errGitStopped{"GitHub refused the token (expired or revoked): give a new one in the settings"}
	case 403, 404:
		return errGitStopped{fmt.Sprintf("the token cannot see %s/%s: make it for that repository", owner, name)}
	default:
		return fmt.Errorf("GitHub: %s", res.Status)
	}
	var repo struct {
		Private *bool `json:"private"`
	}
	if err := json.Unmarshal(b, &repo); err != nil || repo.Private == nil {
		return errors.New("GitHub did not say whether the repository is private")
	}
	if !*repo.Private {
		return errGitStopped{fmt.Sprintf("%s/%s is public: nothing is pushed to it, since every deck would be readable by anyone. Make it private on GitHub, then push again.", owner, name)}
	}
	return nil
}

// counts the commits not pushed yet, after a commit
func (p *gitPusher) committed(id string) {
	if p == nil || p.m.cfg.Remote == "" {
		return
	}
	pushed := p.state().Pushed
	n, err := p.m.repo.Ahead(pushed, id)
	if err != nil {
		n = 1
	}
	p.set(func(s *gitPushState) {
		s.Waiting = n
		if s.State == "done" {
			s.State = "waiting"
		}
	})
	p.poke(false)
}

// --- on and off while the server runs

// gitService holds the mirror and its pusher while Git is on.
type gitService struct {
	data   string
	docs   *sql.DB
	blobs  store.BlobStore
	feed   store.Engine
	bucket *localBucket
	client *http.Client
	logf   func(format string, args ...any)
	// GitHub's API and Git addresses; "": GitHub's own (tests: a fake)
	githubAPI, githubGit string

	mu     sync.Mutex
	m      *gitMirror
	p      *gitPusher
	tokens *connectors.Tokens
	cancel context.CancelFunc
	done   chan struct{}
}

func (g *gitService) mirror() *gitMirror {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m
}

func (g *gitService) pusher() *gitPusher {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.p
}

func (g *gitService) openTokens() (*connectors.Tokens, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tokens != nil {
		return g.tokens, nil
	}
	t, err := connectors.OpenTokens(filepath.Join(g.data, "connectors"))
	if err != nil {
		return nil, err
	}
	g.tokens = t
	return t, nil
}

// start turns the mirror on with cfg: the first commit when the
// repository has none (checked against the folder), then committing and
// pushing in the background until stop
func (g *gitService) start(ctx context.Context, cfg gitConfig) error {
	tokens, err := g.openTokens()
	if err != nil {
		return err
	}
	m, err := openGitMirror(g.data, cfg, g.docs, g.blobs)
	if err != nil {
		return err
	}
	if g.logf != nil {
		m.logf = g.logf
	}
	p := newGitPusher(m, tokens, g.client)
	if g.client == nil {
		p.client = http.DefaultClient
	}
	if g.githubAPI != "" {
		p.api = g.githubAPI
	}
	if g.githubGit != "" {
		p.host = g.githubGit
	}
	if err := m.start(ctx); err != nil {
		return err
	}
	m.onCommit = p.committed
	run, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	g.mu.Lock()
	g.m, g.p, g.cancel, g.done = m, p, cancel, done
	g.mu.Unlock()
	if g.bucket != nil {
		g.bucket.gitMax.Store(cfg.maxFile())
	}
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); m.run(run, g.feed) }()
		go func() { defer wg.Done(); p.run(run) }()
		wg.Wait()
	}()
	return nil
}

// stop commits what is left and turns the mirror off; the repository stays
func (g *gitService) stop() {
	if g == nil {
		return
	}
	g.mu.Lock()
	m, cancel, done := g.m, g.cancel, g.done
	g.m, g.p, g.cancel, g.done = nil, nil, nil, nil
	g.mu.Unlock()
	if m == nil {
		return
	}
	cancel()
	<-done
	m.stop()
	if g.bucket != nil {
		g.bucket.gitMax.Store(0)
	}
}

// save commits now with message and pushes at once. → the commit, "" when
// nothing changed since the last one
func (g *gitService) save(ctx context.Context, message string) (string, error) {
	m := g.mirror()
	if m == nil {
		return "", errors.New("this server does not keep its decks in Git")
	}
	id, err := m.commit(ctx, message)
	if err != nil {
		return "", err
	}
	g.pusher().poke(true)
	return id, nil
}

// --- the API

type gitCommitRow struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	When    int64  `json:"when"`
}

func (g *gitService) recent(n int) []gitCommitRow {
	m := g.mirror()
	if m == nil {
		return nil
	}
	head, err := m.Head()
	if err != nil || head == "" {
		return nil
	}
	cs, _ := m.repo.Log(head, n)
	out := make([]gitCommitRow, 0, len(cs))
	for _, c := range cs {
		subject, _, _ := strings.Cut(c.Message, "\n")
		out = append(out, gitCommitRow{ID: c.ID, Subject: subject, Author: c.Author.Name, When: c.Committer.When.UnixMilli()})
	}
	return out
}

func (s *localServer) gitState(full bool) map[string]any {
	g := s.gits
	cfg, _, err := readGitConfig(g.data)
	out := map[string]any{"enabled": false}
	if err != nil {
		out["error"] = err.Error()
	}
	has := false
	if full {
		if tokens, _ := g.openTokens(); tokens != nil {
			if t, _ := tokens.Get(gitTokenConnector, gitTokenWho); t != nil && t.AccessToken != "" {
				has = true
			}
		}
		out["settings"] = gitSettingsOf(cfg, has)
	}
	m := g.mirror()
	if m == nil {
		return out
	}
	out["enabled"] = true
	head, _ := m.Head()
	out["head"] = head
	out["branch"] = m.cfg.Branch
	if p := g.pusher(); p != nil {
		out["push"] = p.state()
	}
	if full {
		out["commits"] = g.recent(10)
		out["repository"] = m.repo.Dir
	}
	return out
}

type gitSettings struct {
	Enabled   bool   `json:"enabled"`
	Remote    string `json:"remote"`
	HasToken  bool   `json:"hasToken"`
	Author    string `json:"author"`
	MaxFileMB int    `json:"maxFileMB"`
	Branch    string `json:"branch"`
}

func gitSettingsOf(c gitConfig, hasToken bool) gitSettings {
	return gitSettings{Enabled: c.Enabled, Remote: c.Remote, HasToken: hasToken, Author: c.Author, MaxFileMB: c.MaxFileMB, Branch: c.Branch}
}

// gitAPI answers /api/git and /api/settings/git (see the top of the file)
func (s *localServer) gitAPI(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if s.gits == nil {
		writeJSON(w, 404, map[string]string{"error": "this server has no data folder"})
		return
	}
	if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, 415, map[string]string{"error": "send JSON"})
		return
	}
	settings := strings.HasPrefix(p, "/api/settings/git")
	if settings && r.Method != http.MethodGet && s.authRequired() && !fromHere(r) && !s.ownerCredential(r) {
		writeJSON(w, 403, map[string]string{"error": "on a server with sign-in, Git is set on the server's own computer or with the owner's credential"})
		return
	}
	body := func(v any) bool {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := json.Unmarshal(b, v); err != nil {
			writeJSON(w, 400, map[string]string{"error": "not JSON: " + err.Error()})
			return false
		}
		return true
	}
	switch {
	case p == "/api/git" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.gitState(false))
	case p == "/api/git/save" && r.Method == http.MethodPost:
		var b struct {
			Deck    string `json:"deck"`
			Message string `json:"message"`
		}
		if !body(&b) {
			return
		}
		msg := strings.TrimSpace(b.Message)
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		if b.Deck != "" && msg != "" {
			if d, err := s.env.DB.Get(r.Context(), "shares", b.Deck); err == nil && d != nil {
				if name, _ := d["name"].(string); name != "" {
					msg = fmt.Sprintf("Deck %q: %s", name, msg)
				}
			}
		}
		if msg == "" {
			msg = "Saved version"
		}
		// the rooms' text first: what the editors see is what is committed
		s.flushRooms()
		id, err := s.gits.save(r.Context(), msg)
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		out := s.gitState(false)
		out["commit"] = id
		writeJSON(w, 200, out)
	case p == "/api/settings/git" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.gitState(true))
	case p == "/api/settings/git" && r.Method == http.MethodPut:
		var b struct {
			Enabled   *bool   `json:"enabled"`
			Remote    *string `json:"remote"`
			Token     *string `json:"token"`
			Author    *string `json:"author"`
			MaxFileMB *int    `json:"maxFileMB"`
		}
		if !body(&b) {
			return
		}
		if err := s.setGit(r.Context(), b.Enabled, b.Remote, b.Token, b.Author, b.MaxFileMB); err != nil {
			code := 400
			var big gitTooBig
			if errors.As(err, &big) {
				writeJSON(w, 409, map[string]any{"error": err.Error(), "tooBig": big.files})
				return
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, s.gitState(true))
	case p == "/api/settings/git/push" && r.Method == http.MethodPost:
		pu := s.gits.pusher()
		if pu == nil || pu.m.cfg.Remote == "" {
			writeJSON(w, 409, map[string]string{"error": "no GitHub repository is set"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		pu.push(ctx)
		writeJSON(w, 200, s.gitState(true))
	default:
		writeJSON(w, 404, map[string]string{"error": "no such call"})
	}
}

// gitTooBig: files over the limit keep Git from being turned on
type gitTooBig struct {
	files []string
	mb    int
}

func (e gitTooBig) Error() string {
	return fmt.Sprintf("%d files are over %d MB: remove them or raise the limit (at most %d)", len(e.files), e.mb, gitMaxFileCeiling)
}

// setGit changes git.json and the token, turning the mirror on, off or
// over to the new settings
func (s *localServer) setGit(ctx context.Context, enabled *bool, remote, token, author *string, maxMB *int) error {
	g := s.gits
	cfg, _, err := readGitConfig(g.data)
	if err != nil {
		return err
	}
	if enabled != nil {
		cfg.Enabled = *enabled
	}
	if remote != nil {
		cfg.Remote = strings.TrimSpace(*remote)
	}
	if author != nil {
		cfg.Author = *author
	}
	if maxMB != nil {
		cfg.MaxFileMB = *maxMB
		cfg.defaults()
	}
	if err := cfg.check(); err != nil {
		return err
	}
	if token != nil {
		tokens, err := g.openTokens()
		if err != nil {
			return err
		}
		t := strings.TrimSpace(*token)
		if t == "" {
			err = tokens.Delete(gitTokenConnector, gitTokenWho)
		} else if strings.ContainsAny(t, " \t\r\n") || len(t) > 400 {
			return errors.New("that is not a GitHub token")
		} else {
			err = tokens.Put(gitTokenConnector, gitTokenWho, &connectors.Token{AccessToken: t, TokenType: "bearer", At: time.Now().UnixMilli()})
		}
		if err != nil {
			return err
		}
	}
	if cfg.Enabled && g.mirror() == nil {
		// a dry run first: what is too big for Git
		m, err := openGitMirror(g.data, cfg, g.docs, g.blobs)
		if err != nil {
			return err
		}
		snap, err := m.snapshot(ctx, false)
		if err != nil {
			return err
		}
		if len(snap.TooBig) > 0 {
			return gitTooBig{files: snap.TooBig, mb: cfg.MaxFileMB}
		}
	}
	if err := writeGitConfig(g.data, cfg); err != nil {
		return err
	}
	// the mirror runs with the settings it was started with: started again
	g.stop()
	if cfg.Enabled {
		if err := g.start(context.Background(), cfg); err != nil {
			// left off, so the server's next start does not stop on it
			cfg.Enabled = false
			writeGitConfig(g.data, cfg)
			return err
		}
		g.pusher().poke(true)
	}
	return nil
}

// endWork is told an assistant's end_work (presence.go): its work is a
// version of its own, and is pushed
func (s *localServer) endWork(deck, note, agent string) {
	if s.gits.mirror() == nil {
		return
	}
	msg := strings.TrimSpace(note)
	if msg == "" {
		msg = "Assistant's work"
	}
	if agent != "" {
		msg += " (" + agent + ")"
	}
	if d, err := s.env.DB.Get(context.Background(), "shares", deck); err == nil && d != nil {
		if name, _ := d["name"].(string); name != "" {
			msg = fmt.Sprintf("Deck %q: %s", name, msg)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := s.gits.save(ctx, msg); err != nil {
		s.gits.mirror().logf("git: end_work commit: %v", err)
	}
}
