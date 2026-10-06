// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/terotests/sliqtly/mcp-go/store"
)

// a folder server whose localServer the test can reach
func startLocalServer(t *testing.T, dir string) (*localServer, *testServer, func()) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	e, bucket, err := localEnv(dir, base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	ls := newLocalServer(e, bucket, "", nil).(*localServer)
	srv.Config.Handler = ls
	srv.Start()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{}}, nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	stop := func() {
		session.Close()
		srv.Close()
		bucket.blobs.Close()
		bucket.docs.Close()
	}
	return ls, &testServer{root: srv.URL, session: session}, stop
}

func runBackupCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := backupCmd(args, &out, &errOut); code != 0 {
		t.Fatalf("backup %v: exit %d\n%s%s", args, code, out.String(), errOut.String())
	}
	return out.String()
}

// The server's own backups and the command line's, then restores of each:
// a server on a restored folder serves the decks and pictures as they were.
func TestServerBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	repo := filepath.Join(t.TempDir(), "backup")
	ls, s, stop := startLocalServer(t, data)

	c := call(t, s, "create_presentation", map[string]any{
		"title": "Pilot", "markdown": DECK,
		"images": []any{
			map[string]any{"name": "cat.png", "url": "https://images.test/cat.png"},
			map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(PNG)},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	// the server's backup
	cfg := backupConfig{Repo: repo, Every: time.Hour, Keep: store.DefaultBackupKeep}
	if !ls.backupDue(cfg) {
		t.Fatal("no backup yet, and not due")
	}
	first, err := ls.backupNow(ctx, data, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ls.backupDue(cfg) {
		t.Fatal("due right after a backup")
	}

	// an edit, then a backup from the command line beside the running server
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": DECK + "\n## More\n\nText.\n"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	out := runBackupCmd(t, "run", "-data", data, "-repo", repo)
	if !strings.Contains(out, "backup ") {
		t.Fatalf("run said %q", out)
	}
	if out := runBackupCmd(t, "list", "-repo", repo); !strings.Contains(out, "2 backups") {
		t.Fatalf("list said %q", out)
	}
	if out := runBackupCmd(t, "verify", "-repo", repo, "-deep"); !strings.HasSuffix(out, "ok\n") {
		t.Fatalf("verify said %q", out)
	}
	stop()

	// the first backup: the deck as it was created
	r1 := filepath.Join(t.TempDir(), "r1")
	runBackupCmd(t, "restore", "-repo", repo, "-id", first.ID, "-into", r1)
	_, s1, stop1 := startLocalServer(t, r1)
	g := sc(call(t, s1, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, g["markdown"], DECK)
	eq(t, len(list(g["images"])), 2)
	code, ct, body := get(t, s1.root+"/files/shares/"+id+"/media/dot.png")
	eq(t, []any{code, ct, body == string(PNG)}, []any{200, "image/png", true})
	stop1()

	// the newest: the edit is there
	r2 := filepath.Join(t.TempDir(), "r2")
	runBackupCmd(t, "restore", "-repo", repo, "-into", r2)
	_, s2, stop2 := startLocalServer(t, r2)
	defer stop2()
	g = sc(call(t, s2, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, g["markdown"], DECK+"\n## More\n\nText.\n")
	code, _, body = get(t, s2.root+"/files/shares/"+id+"/media/cat.png")
	eq(t, []any{code, body == string(PNG)}, []any{200, true})
}

func TestBackupCommandRefuses(t *testing.T) {
	var out, errOut bytes.Buffer
	data := t.TempDir()
	// the backup inside the data folder
	if code := backupCmd([]string{"run", "-data", data, "-repo", filepath.Join(data, "b")}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "must be apart") {
		t.Fatalf("inside: %d %s", code, errOut.String())
	}
	// a folder that is not a backup
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "blobs.db"), []byte("x"), 0o640)
	errOut.Reset()
	if code := backupCmd([]string{"list", "-repo", other}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "not a backup folder and not empty") {
		t.Fatalf("not a backup: %d %s", code, errOut.String())
	}
	for _, k := range []string{"last=0", "daily=x", "hourly=3"} {
		if _, err := parseBackupKeep(k); err == nil {
			t.Fatalf("%q accepted", k)
		}
	}
	if k, err := parseBackupKeep("last=2, weekly=4"); err != nil || k != (store.BackupKeep{Last: 2, Weekly: 4}) {
		t.Fatalf("%+v %v", k, err)
	}
}
