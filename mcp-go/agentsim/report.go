// SPDX-License-Identifier: AGPL-3.0-or-later

package agentsim

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Report of one run.
type Report struct {
	Config     Config              `json:"-"`
	Ran        float64             `json:"ran_s"`
	Calls      int                 `json:"calls"`
	PerSecond  float64             `json:"calls_per_s"`
	InitFailed int                 `json:"init_failed"`
	MaxRSSMB   float64             `json:"max_rss_mb,omitempty"`
	MaxCPU     float64             `json:"max_cpu,omitempty"`
	Tools      []ToolSummary       `json:"tools"`
	Errors     map[string][]string `json:"errors,omitempty"`
	Decks      []DeckCheck         `json:"decks"`
	Seconds    []Second            `json:"seconds"`
}

// Kept is true when every deck kept the agents' work as the server said.
func (r *Report) Kept() bool {
	for _, d := range r.Decks {
		if !d.OK() {
			return false
		}
	}
	return true
}

// Failures counts transport failures and unexplained tool errors.
func (r *Report) Failures() int {
	n := r.InitFailed
	for _, t := range r.Tools {
		n += t.Outcomes[Failed] + t.Outcomes[ToolError]
	}
	return n
}

// Text is the report for a terminal or a Markdown file.
func (r *Report) Text() string {
	c := r.Config
	var b strings.Builder
	fmt.Fprintf(&b, "agents %d on %d decks of %d parts, %s (ramp %s), think %s, mix claim/edit/merge/review/read %d/%d/%d/%d/%d\n",
		c.Agents, c.Decks, c.Slides, c.Duration, c.Ramp, c.Think, c.Mix.Claim, c.Mix.Edit, c.Mix.Merge, c.Mix.Review, c.Mix.Read)
	fmt.Fprintf(&b, "ran %.1f s: %d calls, %.1f calls/s, %d failures", r.Ran, r.Calls, r.PerSecond, r.Failures())
	if r.InitFailed > 0 {
		fmt.Fprintf(&b, " (%d agents could not connect)", r.InitFailed)
	}
	if r.MaxRSSMB > 0 {
		fmt.Fprintf(&b, "; server max RSS %.0f MB, max CPU %.1f cores", r.MaxRSSMB, r.MaxCPU)
	}
	b.WriteString("\n\n")
	b.WriteString(Table(r.Tools))
	if len(r.Errors) > 0 {
		b.WriteString("\nerrors (first of each):\n")
		var keys []string
		for k := range r.Errors {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, m := range r.Errors[k] {
				fmt.Fprintf(&b, "  %s: %s\n", k, strings.ReplaceAll(m, "\n", " "))
			}
		}
	}
	b.WriteString("\nwork kept:\n")
	for _, d := range r.Decks {
		state := "ok"
		if !d.OK() {
			state = "PROBLEM"
		}
		fmt.Fprintf(&b, "  %s %s: %d slides, %d KB; updates saved %d, lost %d, doubled %d; refused %d, kept anyway %d; transport-failed %d (landed %d); comments %d missing %d; findings %d missing %d\n",
			d.Deck, state, d.Slides, d.Bytes/1024, d.Saved, len(d.Lost), len(d.Duplicated), d.Refused, len(d.Ghosts), d.Unsure, d.UnsureLanded, d.Comments, len(d.CommentsMissing), d.Findings, len(d.FindingsMissing))
		if d.Error != "" {
			fmt.Fprintf(&b, "    check failed: %s\n", d.Error)
		}
		for _, l := range first(d.Lost, 3) {
			fmt.Fprintf(&b, "    lost: %s\n", l)
		}
		for _, l := range first(d.Ghosts, 3) {
			fmt.Fprintf(&b, "    kept although refused: %s\n", l)
		}
	}
	return b.String()
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// CSV of the seconds, for a chart.
func (r *Report) CSV() string {
	var b strings.Builder
	b.WriteString("t,agents,calls,errors,conflicts,throttled,mean_ms,rss_mb,cpu,threads\n")
	for _, s := range r.Seconds {
		fmt.Fprintf(&b, "%d,%d,%d,%d,%d,%d,%.1f,%.1f,%.2f,%d\n", s.T, s.Agents, s.Calls, s.Errors, s.Conflicts, s.Throttled, s.MeanMs, s.RSSMB, s.CPU, s.Threads)
	}
	return b.String()
}

// ProcSample is a process's memory, CPU time and threads.
type ProcSample struct {
	RSS     int64 // bytes
	CPU     time.Duration
	Threads int
}

// ProcWatch reads a Linux process's /proc; ok false elsewhere or when it is gone.
func ProcWatch(pid int) func() (ProcSample, bool) {
	tick := int64(100) // USER_HZ on Linux
	return func() (ProcSample, bool) {
		var p ProcSample
		st, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return p, false
		}
		for _, l := range strings.Split(string(st), "\n") {
			f := strings.Fields(l)
			if len(f) < 2 {
				continue
			}
			switch f[0] {
			case "VmRSS:":
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				p.RSS = kb * 1024
			case "Threads:":
				p.Threads, _ = strconv.Atoi(f[1])
			}
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return p, true
		}
		// fields after the command, which is in parentheses
		s := string(stat)
		if i := strings.LastIndexByte(s, ')'); i >= 0 {
			f := strings.Fields(s[i+1:])
			if len(f) > 12 {
				ut, _ := strconv.ParseInt(f[11], 10, 64)
				stt, _ := strconv.ParseInt(f[12], 10, 64)
				p.CPU = time.Duration((ut + stt) * int64(time.Second) / tick)
			}
		}
		return p, true
	}
}
