// SPDX-License-Identifier: AGPL-3.0-or-later

package agentsim

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Outcome is what became of one call.
type Outcome string

const (
	OK        Outcome = "ok"
	Busy      Outcome = "busy"      // begin_work: another agent holds the slides
	Conflict  Outcome = "conflict"  // update refused: same lines changed, or claims kept changing
	Throttled Outcome = "throttled" // the server's write limit
	ToolError Outcome = "tool_error"
	Failed    Outcome = "failed"  // transport: HTTP status, connection, timeout
	Skipped   Outcome = "skipped" // not made: the run was over
)

// Classify names a call's outcome from its result.
func Classify(r Result, err error) Outcome {
	if err != nil {
		return Failed
	}
	if !r.IsError {
		if c, ok := r.Data["claimed"].(bool); ok && !c {
			return Busy
		}
		return OK
	}
	t := r.Text
	switch {
	case strings.HasPrefix(t, "Too many"), strings.Contains(t, "try again in a few minutes"):
		return Throttled
	case strings.HasPrefix(t, "Not claimed"), strings.HasPrefix(t, "Not saved"), strings.HasPrefix(t, "Not ended"),
		strings.Contains(t, "same lines"), strings.Contains(t, "kept changing"):
		return Conflict
	}
	return ToolError
}

// Stats gathers latencies and outcomes per tool, safe for many agents.
type Stats struct {
	mu    sync.Mutex
	tools map[string]*toolStats
	// the first few messages of each tool's errors, to see what went wrong
	samples map[string][]string
}

type toolStats struct {
	lat      []time.Duration
	outcomes map[Outcome]int
}

func NewStats() *Stats {
	return &Stats{tools: map[string]*toolStats{}, samples: map[string][]string{}}
}

func (s *Stats) Add(tool string, d time.Duration, o Outcome, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tools[tool]
	if t == nil {
		t = &toolStats{outcomes: map[Outcome]int{}}
		s.tools[tool] = t
	}
	t.lat = append(t.lat, d)
	t.outcomes[o]++
	if o == ToolError || o == Failed || o == Conflict {
		k := tool + " " + string(o)
		if len(s.samples[k]) < 3 {
			s.samples[k] = append(s.samples[k], clip(msg, 240))
		}
	}
}

// ToolSummary is one tool's line of the report.
type ToolSummary struct {
	Tool     string          `json:"tool"`
	Calls    int             `json:"calls"`
	Outcomes map[Outcome]int `json:"outcomes"`
	P50      float64         `json:"p50_ms"`
	P95      float64         `json:"p95_ms"`
	P99      float64         `json:"p99_ms"`
	Max      float64         `json:"max_ms"`
}

// Summary is every tool's line, the busiest first.
func (s *Stats) Summary() []ToolSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ToolSummary
	for name, t := range s.tools {
		lat := append([]time.Duration(nil), t.lat...)
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		oc := map[Outcome]int{}
		for k, v := range t.outcomes {
			oc[k] = v
		}
		out = append(out, ToolSummary{Tool: name, Calls: len(lat), Outcomes: oc,
			P50: ms(Percentile(lat, 50)), P95: ms(Percentile(lat, 95)), P99: ms(Percentile(lat, 99)), Max: ms(Percentile(lat, 100))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

// Samples are a few error messages per tool and outcome.
func (s *Stats) Samples() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]string{}
	for k, v := range s.samples {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Percentile of sorted durations, nearest rank; 0 for none.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted))*p/100+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Table is the summary as fixed-width text.
func Table(rows []ToolSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %7s %7s %6s %6s %6s %6s %7s %7s %7s %7s\n", "tool", "calls", "ok", "busy", "confl", "thrott", "error", "p50 ms", "p95 ms", "p99 ms", "max ms")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-22s %7d %7d %6d %6d %6d %6d %7.1f %7.1f %7.1f %7.1f\n", r.Tool, r.Calls,
			r.Outcomes[OK], r.Outcomes[Busy], r.Outcomes[Conflict], r.Outcomes[Throttled], r.Outcomes[ToolError]+r.Outcomes[Failed],
			r.P50, r.P95, r.P99, r.Max)
	}
	return b.String()
}
