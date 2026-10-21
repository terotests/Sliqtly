// SPDX-License-Identifier: AGPL-3.0-or-later

package rgit

import (
	"context"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitServer serves the bare repositories under root over smart HTTP with
// git's own http-backend; receive-pack is on (REMOTE_USER is set) and the
// Authorization the client sent is kept in *auth
func gitServer(t *testing.T, root string, auth *string) *httptest.Server {
	t.Helper()
	haveGit(t)
	bin := strings.TrimSpace(gitCmd(t, root, "--exec-path"))
	backend := filepath.Join(bin, "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("no git-http-backend to push to")
	}
	h := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=tester", "GIT_CONFIG_NOSYSTEM=1", "HOME=" + t.TempDir()}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPushToGit(t *testing.T) {
	haveGit(t)
	root := t.TempDir()
	gitCmd(t, root, "init", "-q", "--bare", "-b", "main", "remote.git")
	var auth string
	srv := gitServer(t, root, &auth)
	rm := Remote{URL: srv.URL + "/remote.git", Agent: "sliqtly-test", Auth: func(r *http.Request) { r.SetBasicAuth("x-access-token", "t0ken") }}

	r, err := Init(filepath.Join(t.TempDir(), "repo.git"), "main")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	c1 := commitFiles(t, r, map[string]string{"decks/a/deck.md": "# Yksi\n", "README.md": "hello\n"}, "", "first\n", when)
	ctx := context.Background()

	// into an empty repository: everything goes
	res, err := r.Push(ctx, rm, "main", c1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Old != "" || res.Objects < 4 {
		t.Fatalf("first push %+v", res)
	}
	if !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("no token sent: %q", auth)
	}
	if got := strings.TrimSpace(gitCmd(t, filepath.Join(root, "remote.git"), "rev-parse", "main")); got != c1 {
		t.Fatalf("remote main %s, want %s", got, c1)
	}

	// a second commit: only what is new
	c2 := commitFiles(t, r, map[string]string{"decks/a/deck.md": "# Yksi\n\n# Kaksi\n", "README.md": "hello\n"}, c1, "second\n", when.Add(time.Minute))
	res, err = r.Push(ctx, rm, "main", c2)
	if err != nil {
		t.Fatal(err)
	}
	// the commit, three trees (/, decks, decks/a) and the one new blob
	if res.Old != c1 || res.Objects != 5 {
		t.Fatalf("second push %+v", res)
	}
	gitCmd(t, filepath.Join(root, "remote.git"), "fsck", "--strict")
	if got := gitCmd(t, filepath.Join(root, "remote.git"), "show", "main:decks/a/deck.md"); got != "# Yksi\n\n# Kaksi\n" {
		t.Fatalf("remote file %q", got)
	}

	// pushed again: nothing to do
	if res, err = r.Push(ctx, rm, "main", c2); err != nil || res.Objects != 0 {
		t.Fatalf("again %+v %v", res, err)
	}

	// someone pushes to the remote: the server does not overwrite it
	work := t.TempDir()
	gitCmd(t, work, "clone", "-q", srv.URL+"/remote.git", "w")
	w := filepath.Join(work, "w")
	os.WriteFile(filepath.Join(w, "other.txt"), []byte("x\n"), 0o644)
	gitCmd(t, w, "add", "other.txt")
	gitCmd(t, w, "-c", "user.name=O", "-c", "user.email=o@example.com", "commit", "-q", "-m", "other")
	gitCmd(t, w, "push", "-q", "origin", "main")
	c3 := commitFiles(t, r, map[string]string{"decks/a/deck.md": "# Kolme\n", "README.md": "hello\n"}, c2, "third\n", when.Add(2*time.Minute))
	if _, err = r.Push(ctx, rm, "main", c3); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("over someone else's commit: %v", err)
	}
}

func TestPushRefused(t *testing.T) {
	haveGit(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid username or token", http.StatusUnauthorized)
	}))
	defer srv.Close()
	r, err := Init(filepath.Join(t.TempDir(), "repo.git"), "main")
	if err != nil {
		t.Fatal(err)
	}
	c1 := commitFiles(t, r, map[string]string{"a": "1"}, "", "m\n", time.Now())
	_, err = r.Push(context.Background(), Remote{URL: "https://x:secret@" + strings.TrimPrefix(srv.URL, "http://")}, "main", c1)
	if err == nil {
		t.Fatal("pushed without a token")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error tells the password: %v", err)
	}
}

func TestAncestry(t *testing.T) {
	r, err := Init(filepath.Join(t.TempDir(), "repo.git"), "main")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c1 := commitFiles(t, r, map[string]string{"a": "1"}, "", "1\n", now)
	c2 := commitFiles(t, r, map[string]string{"a": "2"}, c1, "2\n", now)
	c3 := commitFiles(t, r, map[string]string{"a": "3"}, c2, "3\n", now)
	if ok, _ := r.IsAncestor(c1, c3); !ok {
		t.Fatal("c1 is behind c3")
	}
	if ok, _ := r.IsAncestor(c3, c1); ok {
		t.Fatal("c3 is not behind c1")
	}
	if n, _ := r.Ahead(c1, c3); n != 2 {
		t.Fatalf("ahead %d", n)
	}
	if n, _ := r.Ahead("", c3); n != 3 {
		t.Fatalf("all %d", n)
	}
}
