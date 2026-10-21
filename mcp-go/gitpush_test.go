// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubRepoOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/terotests/decks":      "terotests/decks",
		"https://github.com/terotests/decks.git":  "terotests/decks",
		"https://github.com/terotests/decks/":     "terotests/decks",
		" https://GitHub.com/a-b/c.d.git ":        "a-b/c.d",
		"http://github.com/a/b":                   "",
		"https://gitlab.com/a/b":                  "",
		"https://x:y@github.com/a/b":              "",
		"https://github.com/a":                    "",
		"https://github.com/a/b/c":                "",
		"https://github.com/a/b?x=1":              "",
		"git@github.com:a/b.git":                  "",
		"https://github.com.evil.example/a/b.git": "",
	} {
		o, r, err := githubRepoOf(in)
		got := ""
		if err == nil {
			got = o + "/" + r
		}
		if got != want {
			t.Errorf("%q → %q, want %q (%v)", in, got, want, err)
		}
	}
}

// fakeGitHub: the API's /repos/o/r (private as *private says) and Git's
// smart HTTP over bare repositories under root, with git's own http-backend
func fakeGitHub(t *testing.T, root string, private *atomic.Bool) *httptest.Server {
	t.Helper()
	gitTool(t, root, "--version")
	bin := strings.TrimSpace(gitTool(t, root, "--exec-path"))
	backend := filepath.Join(bin, "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("no git-http-backend to push to")
	}
	h := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=tester", "GIT_CONFIG_NOSYSTEM=1", "HOME=" + t.TempDir()}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			if r.Header.Get("Authorization") != "Bearer github_pat_test" {
				http.Error(w, `{"message":"Bad credentials"}`, 401)
				return
			}
			if r.URL.Path != "/repos/tero/decks" {
				http.Error(w, `{"message":"Not Found"}`, 404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"full_name": "tero/decks", "private": private.Load()})
			return
		}
		if _, pass, ok := r.BasicAuth(); !ok || pass != "github_pat_test" {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			http.Error(w, "no", 401)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gitJSONReq(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	code, text := req(t, method, url, "application/json", body)
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("%s %s: %d %s", method, url, code, text)
	}
	return code, m
}

// Git turned on in the settings, a GitHub repository and its token set:
// commits are pushed, "Save version" and end_work make commits of their
// own, a public repository gets nothing, and Git turns off again.
func TestGitSettingsAndPush(t *testing.T) {
	root := t.TempDir()
	gitTool(t, root, "init", "-q", "--bare", "-b", "main", "tero/decks.git")
	var private atomic.Bool
	private.Store(true)
	gh := fakeGitHub(t, root, &private)

	data := filepath.Join(t.TempDir(), "data")
	ls, s, stop := startLocalServer(t, data)
	defer stop()
	ls.gits = &gitService{data: data, docs: ls.bucket.docs, blobs: ls.bucket.blobs, feed: ls.env.Store, bucket: ls.bucket,
		client: gh.Client(), logf: t.Logf, githubAPI: gh.URL, githubGit: gh.URL}
	ls.env.agents.ended = ls.endWork
	defer ls.gits.stop()
	c := call(t, s, "create_presentation", map[string]any{"title": "Kvartaali", "markdown": DECK})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	code, st := gitJSONReq(t, "GET", s.root+"/api/git", "")
	if code != 200 || st["enabled"] != false {
		t.Fatalf("before: %d %v", code, st)
	}
	// a bad address is refused and nothing is turned on
	if code, st = gitJSONReq(t, "PUT", s.root+"/api/settings/git", `{"enabled":true,"remote":"https://gitlab.com/a/b"}`); code != 400 {
		t.Fatalf("gitlab: %d %v", code, st)
	}
	code, st = gitJSONReq(t, "PUT", s.root+"/api/settings/git", `{"enabled":true,"remote":"https://github.com/tero/decks","token":"github_pat_test"}`)
	if code != 200 || st["enabled"] != true {
		t.Fatalf("on: %d %v", code, st)
	}
	set := st["settings"].(map[string]any)
	if set["hasToken"] != true || set["remote"] != "https://github.com/tero/decks" {
		t.Fatalf("settings %v", set)
	}
	// the token is not in git.json
	if b, _ := os.ReadFile(filepath.Join(data, gitConfigFile)); strings.Contains(string(b), "github_pat_test") {
		t.Fatalf("git.json holds the token: %s", b)
	}
	remote := filepath.Join(root, "tero/decks.git")
	waitRemote := func(what string, ok func(head string) bool) string {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			out, _ := gitOut(remote, "rev-parse", "main")
			if head := strings.TrimSpace(out); ok(head) {
				return head
			}
			if time.Now().After(deadline) {
				_, st := gitJSONReq(t, "GET", s.root+"/api/settings/git", "")
				t.Fatalf("%s: remote not pushed; state %v", what, st["push"])
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	first := waitRemote("first", func(h string) bool { return len(h) == 40 })
	if md := gitTool(t, remote, "show", "main:decks/"+id+"/deck.md"); md != DECK {
		t.Fatalf("pushed deck.md %q", md)
	}

	// Save version: a commit with the message, pushed at once
	if err := ls.env.DB.Update(t.Context(), "shares", id, Doc{"md": "# Uusi\n"}); err != nil {
		t.Fatal(err)
	}
	code, st = gitJSONReq(t, "POST", s.root+"/api/git/save", `{"deck":"`+id+`","message":"Ennen hallituksen kokousta"}`)
	if code != 200 || st["commit"] == "" {
		t.Fatalf("save: %d %v", code, st)
	}
	second := waitRemote("save", func(h string) bool { return h != first })
	msg := gitTool(t, remote, "log", "-1", "--format=%B", second)
	if !strings.HasPrefix(msg, `Deck "Kvartaali": Ennen hallituksen kokousta`) || !strings.Contains(msg, "text changed") {
		t.Fatalf("save message %q", msg)
	}

	// an assistant's end_work: its note is the commit's message
	b := call(t, s, "begin_work", map[string]any{"deck_id": id, "note": "Lisää riskit-dia", "agent": "Testiavustaja"})
	if b.IsError {
		t.Fatal(textOf(b))
	}
	work := sc(b)["work_id"].(string)
	base := sc(b)["version"].(string)
	if u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": "# Uusi\n\n# Riskit\n", "base_version": base, "work_id": work}); u.IsError {
		t.Fatal(textOf(u))
	}
	if e := call(t, s, "end_work", map[string]any{"deck_id": id, "work_id": work}); e.IsError {
		t.Fatal(textOf(e))
	}
	third := waitRemote("end_work", func(h string) bool { return h != second })
	msg = gitTool(t, remote, "log", "-1", "--format=%B", third)
	if !strings.HasPrefix(msg, `Deck "Kvartaali": Lisää riskit-dia (Testiavustaja)`) {
		t.Fatalf("end_work message %q", msg)
	}
	gitTool(t, remote, "fsck", "--strict")

	// the repository turned public: nothing more is pushed
	private.Store(false)
	if err := ls.env.DB.Update(t.Context(), "shares", id, Doc{"md": "# Julkinen?\n"}); err != nil {
		t.Fatal(err)
	}
	code, st = gitJSONReq(t, "POST", s.root+"/api/git/save", `{"message":"x"}`)
	if code != 200 {
		t.Fatalf("save 2: %d %v", code, st)
	}
	code, st = gitJSONReq(t, "POST", s.root+"/api/settings/git/push", `{}`)
	push := st["push"].(map[string]any)
	if push["state"] != "stopped" || !strings.Contains(push["error"].(string), "public") {
		t.Fatalf("public repo: %v", push)
	}
	if h, _ := gitOut(remote, "rev-parse", "main"); strings.TrimSpace(h) != third {
		t.Fatal("pushed to a public repository")
	}

	// off: the repository stays, nothing is committed any more
	code, st = gitJSONReq(t, "PUT", s.root+"/api/settings/git", `{"enabled":false}`)
	if code != 200 || st["enabled"] != false {
		t.Fatalf("off: %d %v", code, st)
	}
	if ls.bucket.gitMax.Load() != 0 {
		t.Fatal("file limit left on")
	}
	if code, _ = gitJSONReq(t, "POST", s.root+"/api/git/save", `{"message":"x"}`); code != 409 {
		t.Fatalf("save when off: %d", code)
	}
	if _, err := os.Stat(filepath.Join(data, gitRepoDir, "HEAD")); err != nil {
		t.Fatal("repository removed")
	}
}

// git with its output, no test failure (the remote may have no branch yet)
func gitOut(dir string, args ...string) (string, error) {
	cmd := execGit(dir, args...)
	b, err := cmd.Output()
	return string(b), err
}

func execGit(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	return cmd
}
