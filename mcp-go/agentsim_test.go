// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/agentsim"
)

// Agents working together on a folder server (package agentsim): they
// claim slides, edit, merge from old versions, comment and log their work at
// once, and the server keeps every change it says it saved, keeps none it
// refused, and loses no comment or session entry. Small by default;
// AGENTSIM_AGENTS and AGENTSIM_SECONDS make it bigger (and -cpuprofile shows
// where the server spends its time).
func TestAgentsWorkTogether(t *testing.T) {
	agents, secs := 12, 6
	if n, err := strconv.Atoi(os.Getenv("AGENTSIM_AGENTS")); err == nil && n > 0 {
		agents = n
	}
	if n, err := strconv.Atoi(os.Getenv("AGENTSIM_SECONDS")); err == nil && n > 0 {
		secs = n
	}
	if testing.Short() {
		secs = 2
	}
	srv, session := startLocalWith(t, t.TempDir(), "", func(e *Env) {
		// the agents all come from here: no write limit in this test
		e.Limiter = func(string) string { return "" }
	})
	defer srv.Close()
	session.Close()
	rep, err := agentsim.Run(context.Background(), agentsim.Config{
		URL: srv.URL + "/mcp", Agents: agents, Decks: max(1, agents/6), Slides: 6,
		Duration: time.Duration(secs) * time.Second, Ramp: time.Second, Think: 150 * time.Millisecond, Seed: 7,
		Log: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + rep.Text())
	if !rep.Kept() {
		t.Fatal("work was lost, doubled or kept although refused")
	}
	if n := rep.Failures(); n > 0 {
		t.Fatalf("%d calls failed", n)
	}
	saved := 0
	for _, d := range rep.Decks {
		saved += d.Saved
	}
	if saved == 0 {
		t.Fatal("no update was saved")
	}
}
