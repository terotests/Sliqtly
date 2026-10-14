// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agentsim simulates many AI agents working together on a Sliqtly
// server through MCP: each agent has its own MCP session and does what an
// assistant does (claims slides with begin_work, edits, merges from an old
// version, comments, logs its work to the Story's session, reads, forks),
// with a think time between calls. After the run it checks that the agents'
// work was kept: every edit the server said it saved is in the deck once,
// every edit it refused is not, every comment and session entry is there.
//
// The command is mcp-go/cmd/agentsim; the server's own test runs a small
// simulation (agentsim_test.go in mcp-go).
package agentsim

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Mix is how often agents do each kind of work, as weights.
type Mix struct {
	Claim  int // begin_work, get_presentation, update with base_version + work_id, end_work
	Edit   int // update_presentation with edits (find + replace), no claim
	Merge  int // get_presentation, think, update with markdown + an old base_version
	Review int // add_comment + log_session finding; now and then fork_story
	Read   int // get_presentation, list_presentations, get_session, list_files, list_comments, list_forks
}

// DefaultMix: a third of the work changes slides.
var DefaultMix = Mix{Claim: 25, Edit: 15, Merge: 10, Review: 20, Read: 30}

// Config of one run.
type Config struct {
	URL      string // the server's /mcp
	Token    string
	Agents   int
	Decks    int // the agents are spread over these decks
	Slides   int // parts per deck (at most 99)
	Duration time.Duration
	Ramp     time.Duration // agents start spread over this time
	Think    time.Duration // mean pause between an agent's steps
	Timeout  time.Duration // one call
	Mix      Mix
	Seed     int64
	// MaxForks per deck: forks make new decks, so they are kept few
	MaxForks int
	// Watch, when set, is sampled each second (the server's memory, CPU)
	Watch func() (ProcSample, bool)
	Log   func(format string, a ...any)
}

func (c *Config) defaults() {
	if c.Agents < 1 {
		c.Agents = 1
	}
	if c.Decks < 1 {
		c.Decks = 1
	}
	if c.Slides < 1 {
		c.Slides = 8
	}
	if c.Slides > 99 {
		c.Slides = 99
	}
	if c.Duration <= 0 {
		c.Duration = 30 * time.Second
	}
	if c.Think <= 0 {
		c.Think = 500 * time.Millisecond
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.Mix == (Mix{}) {
		c.Mix = DefaultMix
	}
	if c.Seed == 0 {
		c.Seed = time.Now().UnixNano()
	}
	if c.MaxForks == 0 {
		c.MaxForks = 3
	}
	if c.Log == nil {
		c.Log = func(string, ...any) {}
	}
}

// Heading of part k: two digits, so "## Part 01" is not inside "## Part 011".
func Heading(k int) string { return fmt.Sprintf("## Part %02d", k) }

// SeedMarkdown is a deck the agents start from.
func SeedMarkdown(deck, slides int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Team deck %d\n\nWorked on by many agents at once.\n", deck+1)
	for k := 1; k <= slides; k++ {
		fmt.Fprintf(&b, "\n%s\n\n- Started\n", Heading(k))
	}
	return b.String()
}

// InsertAfterHeading puts line right under part k's heading; ok false when
// the heading is not in md (once only: headings are unique).
func InsertAfterHeading(md string, k int, line string) (string, bool) {
	h := Heading(k) + "\n"
	i := strings.Index(md, h)
	if i < 0 {
		return md, false
	}
	j := i + len(h)
	return md[:j] + line + "\n" + md[j:], true
}

// deck is one shared deck and what the server said it kept of it.
type deck struct {
	id     string
	forks  atomic.Int32
	mu     sync.Mutex
	saved  []string // marker lines of updates the server saved
	refuse []string // marker lines of updates it refused
	unsure []string // marker lines of calls that failed in transport (may or may not be there)
	notes  []string // comments it kept
	finds  []string // session findings it kept
}

func (d *deck) record(list *[]string, s string) {
	d.mu.Lock()
	*list = append(*list, s)
	d.mu.Unlock()
}

// Second is one second of the run.
type Second struct {
	T         int     `json:"t"`
	Agents    int     `json:"agents"`
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"` // failed + tool errors
	Conflicts int     `json:"conflicts"`
	Throttled int     `json:"throttled"`
	MeanMs    float64 `json:"mean_ms"`
	RSSMB     float64 `json:"rss_mb,omitempty"`
	CPU       float64 `json:"cpu,omitempty"` // cores busy
	Threads   int     `json:"threads,omitempty"`
}

type secAcc struct {
	calls, errors, conflicts, throttled int
	lat                                 time.Duration
}

// Run runs the simulation and checks what was kept.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	cfg.defaults()
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        cfg.Agents + 16,
		MaxIdleConnsPerHost: cfg.Agents + 16,
		IdleConnTimeout:     90 * time.Second,
	}
	hc := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	s := &sim{cfg: cfg, hc: hc, stats: NewStats(), start: time.Now()}

	// the decks
	boss := s.client()
	if err := boss.Init(ctx, "agentsim-setup"); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	defer boss.Close()
	for i := 0; i < cfg.Decks; i++ {
		r, err := boss.Call(ctx, "create_presentation", map[string]any{"title": fmt.Sprintf("Team deck %d", i+1), "markdown": SeedMarkdown(i, cfg.Slides)})
		if err != nil || r.IsError {
			return nil, fmt.Errorf("create deck %d: %v %s", i+1, err, clip(r.Text, 300))
		}
		id, _ := r.Data["deck_id"].(string)
		s.decks = append(s.decks, &deck{id: id})
	}
	cfg.Log("%d decks made, starting %d agents over %s for %s", len(s.decks), cfg.Agents, cfg.Ramp, cfg.Duration)

	// the agents
	s.start = time.Now()
	runCtx, stop := context.WithTimeout(ctx, cfg.Ramp+cfg.Duration)
	defer stop()
	var wg sync.WaitGroup
	watchDone := make(chan struct{})
	go s.watch(runCtx, watchDone)
	for i := 0; i < cfg.Agents; i++ {
		var delay time.Duration
		if cfg.Agents > 1 {
			delay = time.Duration(int64(cfg.Ramp) * int64(i) / int64(cfg.Agents))
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case <-time.After(delay):
			case <-runCtx.Done():
				return
			}
			s.agent(runCtx, i)
		}(i)
	}
	wg.Wait()
	stop()
	<-watchDone
	ran := time.Since(s.start)
	cfg.Log("agents done after %s, checking the decks", ran.Round(time.Millisecond))

	rep := &Report{Config: cfg, Ran: ran.Seconds(), Tools: s.stats.Summary(), Errors: s.stats.Samples(), Seconds: s.seconds, InitFailed: int(s.initFailed.Load())}
	for _, t := range rep.Tools {
		rep.Calls += t.Calls
	}
	if ran > 0 {
		rep.PerSecond = float64(rep.Calls) / ran.Seconds()
	}
	for _, sec := range s.seconds {
		if sec.RSSMB > rep.MaxRSSMB {
			rep.MaxRSSMB = sec.RSSMB
		}
		if sec.CPU > rep.MaxCPU {
			rep.MaxCPU = sec.CPU
		}
	}
	vctx, vstop := context.WithTimeout(ctx, 5*time.Minute)
	defer vstop()
	for _, d := range s.decks {
		rep.Decks = append(rep.Decks, s.check(vctx, boss, d))
	}
	return rep, nil
}

type sim struct {
	cfg        Config
	hc         *http.Client
	stats      *Stats
	decks      []*deck
	start      time.Time
	active     atomic.Int32
	initFailed atomic.Int32
	secMu      sync.Mutex
	acc        map[int]*secAcc
	seconds    []Second
}

func (s *sim) client() *Client { return &Client{URL: s.cfg.URL, Token: s.cfg.Token, HTTP: s.hc} }

// one second's numbers, and the server's memory and CPU when watched
func (s *sim) watch(ctx context.Context, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var last ProcSample
	var lastAt time.Time
	if s.cfg.Watch != nil {
		last, _ = s.cfg.Watch()
		lastAt = time.Now()
	}
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sec := Second{T: n, Agents: int(s.active.Load())}
		s.secMu.Lock()
		if a := s.acc[n-1]; a != nil {
			sec.Calls, sec.Errors, sec.Conflicts, sec.Throttled = a.calls, a.errors, a.conflicts, a.throttled
			if a.calls > 0 {
				sec.MeanMs = ms(a.lat / time.Duration(a.calls))
			}
			delete(s.acc, n-1)
		}
		s.secMu.Unlock()
		if s.cfg.Watch != nil {
			if p, ok := s.cfg.Watch(); ok {
				now := time.Now()
				sec.RSSMB = float64(p.RSS) / (1 << 20)
				sec.Threads = p.Threads
				if el := now.Sub(lastAt).Seconds(); el > 0 && last.CPU > 0 {
					sec.CPU = (p.CPU - last.CPU).Seconds() / el
				}
				last, lastAt = p, now
			}
		}
		s.seconds = append(s.seconds, sec)
		if n%10 == 0 {
			s.cfg.Log("t=%3ds agents=%d calls/s=%d errors=%d conflicts=%d mean=%.0fms rss=%.0fMB cpu=%.1f", n, sec.Agents, sec.Calls, sec.Errors, sec.Conflicts, sec.MeanMs, sec.RSSMB, sec.CPU)
		}
	}
}

// a call into the second it ended in
func (s *sim) count(at time.Time, d time.Duration, o Outcome) {
	n := int(at.Sub(s.start) / time.Second)
	s.secMu.Lock()
	if s.acc == nil {
		s.acc = map[int]*secAcc{}
	}
	a := s.acc[n]
	if a == nil {
		a = &secAcc{}
		s.acc[n] = a
	}
	a.calls++
	a.lat += d
	switch o {
	case Failed, ToolError:
		a.errors++
	case Conflict:
		a.conflicts++
	case Throttled:
		a.throttled++
	}
	s.secMu.Unlock()
}

type agent struct {
	s    *sim
	name string
	c    *Client
	d    *deck
	rng  *rand.Rand
	n    int
}

func (s *sim) agent(ctx context.Context, i int) {
	a := &agent{s: s, name: fmt.Sprintf("agent-%03d", i+1), c: s.client(), d: s.decks[i%len(s.decks)], rng: rand.New(rand.NewSource(s.cfg.Seed + int64(i)))}
	ictx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	err := a.c.Init(ictx, a.name)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			s.initFailed.Add(1)
			s.stats.Add("initialize", 0, Failed, err.Error())
		}
		return
	}
	defer a.c.Close()
	s.active.Add(1)
	defer s.active.Add(-1)
	m := s.cfg.Mix
	total := m.Claim + m.Edit + m.Merge + m.Review + m.Read
	for ctx.Err() == nil {
		if !a.pause(ctx) {
			return
		}
		x := a.rng.Intn(total)
		switch {
		case x < m.Claim:
			a.claim(ctx)
		case x < m.Claim+m.Edit:
			a.edit(ctx)
		case x < m.Claim+m.Edit+m.Merge:
			a.merge(ctx)
		case x < m.Claim+m.Edit+m.Merge+m.Review:
			a.review(ctx)
		default:
			a.read(ctx)
		}
	}
}

// a think time, exponential around the mean; false when the run is over
func (a *agent) pause(ctx context.Context) bool {
	d := time.Duration(a.rng.ExpFloat64() * float64(a.s.cfg.Think))
	if d > 10*a.s.cfg.Think {
		d = 10 * a.s.cfg.Think
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *agent) call(ctx context.Context, tool string, args map[string]any) (Result, Outcome) {
	if ctx.Err() != nil {
		return Result{}, Skipped
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.s.cfg.Timeout)
	defer cancel()
	t0 := time.Now()
	r, err := a.c.Call(cctx, tool, args)
	d := time.Since(t0)
	o := Classify(r, err)
	msg := r.Text
	if err != nil {
		msg = err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "timeout after " + a.s.cfg.Timeout.String()
		}
	}
	a.s.stats.Add(tool, d, o, msg)
	a.s.count(t0.Add(d), d, o)
	return r, o
}

func (a *agent) marker() string {
	a.n++
	return fmt.Sprintf("- %s note %d", a.name, a.n)
}

func (a *agent) part() int { return a.rng.Intn(a.s.cfg.Slides) + 1 }

// part k is slide k+1: the title slide is 1
func slideOf(k int) int { return k + 1 }

func (a *agent) keep(o Outcome, line string) {
	switch o {
	case Skipped:
	case OK:
		a.d.record(&a.d.saved, line)
	case Failed:
		a.d.record(&a.d.unsure, line)
	default:
		a.d.record(&a.d.refuse, line)
	}
}

// claim a slide, read the deck, write the slide with base_version and
// work_id, release the claim
func (a *agent) claim(ctx context.Context) {
	k := a.part()
	r, o := a.call(ctx, "begin_work", map[string]any{"deck_id": a.d.id, "agent": a.name, "slides": []any{slideOf(k)}, "minutes": 2, "note": "adding a note"})
	if o == Busy {
		k = a.part()
		r, o = a.call(ctx, "begin_work", map[string]any{"deck_id": a.d.id, "agent": a.name, "slides": []any{slideOf(k)}, "minutes": 2, "note": "adding a note"})
	}
	if o != OK {
		return
	}
	work, _ := r.Data["work_id"].(string)
	defer a.call(ctx, "end_work", map[string]any{"deck_id": a.d.id, "work_id": work})
	g, o := a.call(ctx, "get_presentation", map[string]any{"deck_id": a.d.id})
	if o != OK {
		return
	}
	md, _ := g.Data["markdown"].(string)
	base, _ := g.Data["version"].(string)
	line := a.marker()
	md2, ok := InsertAfterHeading(md, k, line)
	if !ok {
		a.s.stats.Add("lost heading", 0, ToolError, Heading(k)+" not in the deck")
		return
	}
	_, o = a.call(ctx, "update_presentation", map[string]any{"deck_id": a.d.id, "markdown": md2, "base_version": base, "work_id": work})
	a.keep(o, line)
}

// a find + replace edit, no claim
func (a *agent) edit(ctx context.Context) {
	k := a.part()
	line := a.marker()
	_, o := a.call(ctx, "update_presentation", map[string]any{"deck_id": a.d.id, "edits": []any{map[string]any{"find": Heading(k) + "\n", "replace": Heading(k) + "\n" + line + "\n"}}})
	a.keep(o, line)
}

// read, think, write the whole deck from that version: the server merges
func (a *agent) merge(ctx context.Context) {
	g, o := a.call(ctx, "get_presentation", map[string]any{"deck_id": a.d.id})
	if o != OK {
		return
	}
	md, _ := g.Data["markdown"].(string)
	base, _ := g.Data["version"].(string)
	if !a.pause(ctx) {
		return
	}
	k := a.part()
	line := a.marker()
	md2, ok := InsertAfterHeading(md, k, line)
	if !ok {
		a.s.stats.Add("lost heading", 0, ToolError, Heading(k)+" not in the deck")
		return
	}
	_, o = a.call(ctx, "update_presentation", map[string]any{"deck_id": a.d.id, "markdown": md2, "base_version": base})
	a.keep(o, line)
}

func (a *agent) review(ctx context.Context) {
	k := a.part()
	a.n++
	text := fmt.Sprintf("%s comment %d on %s", a.name, a.n, Heading(k)[3:])
	if _, o := a.call(ctx, "add_comment", map[string]any{"deck_id": a.d.id, "slide": slideOf(k), "text": text}); o == OK {
		a.d.record(&a.d.notes, text)
	}
	find := fmt.Sprintf("%s finding %d", a.name, a.n)
	if _, o := a.call(ctx, "log_session", map[string]any{"deck_id": a.d.id, "agent": a.name, "entries": []any{map[string]any{"kind": "finding", "text": find}}}); o == OK {
		a.d.record(&a.d.finds, find)
	}
	if a.rng.Intn(50) == 0 && int(a.d.forks.Add(1)) <= a.s.cfg.MaxForks {
		a.call(ctx, "fork_story", map[string]any{"deck_id": a.d.id, "name": "review by " + a.name, "agent": a.name})
	}
}

func (a *agent) read(ctx context.Context) {
	switch a.rng.Intn(6) {
	case 0, 1:
		a.call(ctx, "get_presentation", map[string]any{"deck_id": a.d.id})
	case 2:
		a.call(ctx, "list_presentations", map[string]any{})
	case 3:
		a.call(ctx, "get_session", map[string]any{"deck_id": a.d.id, "limit": 50})
	case 4:
		a.call(ctx, "list_comments", map[string]any{"deck_id": a.d.id})
	default:
		a.call(ctx, "list_forks", map[string]any{"deck_id": a.d.id})
	}
}
