// SPDX-License-Identifier: AGPL-3.0-or-later

// agentsim: many AI agents working together on a Sliqtly server through MCP,
// to see how the server takes it and whether their work is kept
// (package agentsim).
//
//	go run ./cmd/agentsim -server dist/sliqtly-server -agents 2,10,100,1000 -duration 60s -out /tmp/sim
//	go run ./cmd/agentsim -url http://localhost:8080/mcp -agents 50
//
// With -server it starts that server binary on an empty data folder for
// each step and watches its memory and CPU; with -url it uses a running
// server (-pid to watch it).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/agentsim"
)

func main() {
	url := flag.String("url", "", "a running server's MCP address, e.g. http://localhost:8080/mcp")
	server := flag.String("server", "", "start this sliqtly-server binary on an empty data folder for each step")
	serverArgs := flag.String("server-args", "", "more arguments for -server, space separated")
	pid := flag.Int("pid", 0, "with -url: the server's process id, to watch its memory and CPU")
	token := flag.String("token", os.Getenv("SLIQTLY_TOKEN"), "Authorization: Bearer token, when the server wants one")
	agents := flag.String("agents", "10", "agents; a comma separated list runs one step for each, e.g. 2,10,100,1000")
	perDeck := flag.Int("per-deck", 10, "agents sharing one deck (decks = agents / per-deck, at least 1)")
	slides := flag.Int("slides", 12, "parts per deck (at most 99)")
	duration := flag.Duration("duration", 60*time.Second, "how long each step runs after the ramp")
	ramp := flag.Duration("ramp", 10*time.Second, "agents start spread over this time")
	think := flag.Duration("think", time.Second, "mean pause between an agent's steps")
	timeout := flag.Duration("timeout", 30*time.Second, "one call's time limit")
	mix := flag.String("mix", "", "weights claim,edit,merge,review,read (default 25,15,10,20,30)")
	seed := flag.Int64("seed", 1, "random seed")
	out := flag.String("out", "", "write report.txt, report.json and seconds.csv of each step here")
	quiet := flag.Bool("q", false, "no progress lines")
	flag.Parse()

	if (*url == "") == (*server == "") {
		fail("give -url or -server")
	}
	var steps []int
	for _, s := range strings.Split(*agents, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			fail("bad -agents %q", s)
		}
		steps = append(steps, n)
	}
	m := agentsim.DefaultMix
	if *mix != "" {
		var w [5]int
		parts := strings.Split(*mix, ",")
		if len(parts) != 5 {
			fail("-mix is five weights")
		}
		for i, p := range parts {
			w[i], _ = strconv.Atoi(strings.TrimSpace(p))
		}
		m = agentsim.Mix{Claim: w[0], Edit: w[1], Merge: w[2], Review: w[3], Read: w[4]}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	logf := func(f string, a ...any) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05 ")+f+"\n", a...)
		}
	}

	bad := false
	for _, n := range steps {
		cfg := agentsim.Config{URL: *url, Token: *token, Agents: n, Decks: max(1, n / *perDeck), Slides: *slides,
			Duration: *duration, Ramp: *ramp, Think: *think, Timeout: *timeout, Mix: m, Seed: *seed, Log: logf}
		var srv *exec.Cmd
		if *server != "" {
			var err error
			srv, cfg.URL, err = startServer(*server, strings.Fields(*serverArgs), logf)
			if err != nil {
				fail("start server: %v", err)
			}
			cfg.Watch = agentsim.ProcWatch(srv.Process.Pid)
		} else if *pid > 0 {
			cfg.Watch = agentsim.ProcWatch(*pid)
		}
		logf("step: %d agents", n)
		rep, err := agentsim.Run(ctx, cfg)
		if srv != nil {
			srv.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() { srv.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				srv.Process.Kill()
			}
		}
		if err != nil {
			fail("%d agents: %v", n, err)
		}
		text := rep.Text()
		fmt.Printf("\n== %d agents ==\n%s", n, text)
		if !rep.Kept() {
			bad = true
		}
		if *out != "" {
			dir := filepath.Join(*out, fmt.Sprintf("agents-%04d", n))
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "report.txt"), []byte(text), 0o644)
			j, _ := json.MarshalIndent(rep, "", "  ")
			os.WriteFile(filepath.Join(dir, "report.json"), j, 0o644)
			os.WriteFile(filepath.Join(dir, "seconds.csv"), []byte(rep.CSV()), 0o644)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if bad {
		fmt.Println("\nSome work was not kept as the server said (see \"work kept\").")
		os.Exit(1)
	}
}

// the server on a free port and an empty data folder, once it answers
func startServer(bin string, extra []string, logf func(string, ...any)) (*exec.Cmd, string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	data, err := os.MkdirTemp("", "agentsim-data-")
	if err != nil {
		return nil, "", err
	}
	args := append([]string{"-data", data, "-port", port}, extra...)
	cmd := exec.Command(bin, args...)
	logFile, _ := os.Create(filepath.Join(data, "server.log"))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	base := "http://127.0.0.1:" + port
	for i := 0; i < 200; i++ {
		if res, err := http.Get(base + "/api/status"); err == nil {
			res.Body.Close()
			logf("server pid %d on %s, data %s", cmd.Process.Pid, base, data)
			return cmd, base + "/mcp", nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	cmd.Process.Kill()
	return nil, "", fmt.Errorf("%s did not answer on %s", bin, base)
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "agentsim: "+f+"\n", a...)
	os.Exit(2)
}
