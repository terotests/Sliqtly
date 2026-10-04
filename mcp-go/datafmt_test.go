// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
}

func quiet(string) {}

// a folder as the first servers left it: one folder per collection, one
// per deck's files
func formatOneFolder(t *testing.T) string {
	dir := t.TempDir()
	for _, id := range []string{"aaaaaa1111", "bbbbbb2222", "cccccc3333"} {
		put(t, filepath.Join(dir, "db", "shares", id+".json"), `{"name":"Deck `+id+`","owner":"local","created":{"$ts":1700000000000}}`)
		put(t, filepath.Join(dir, "files", "shares", id, "media", "a.png"), "png "+id)
		put(t, filepath.Join(dir, "files", "shares", id, "media", "a.png.type"), "image/png")
	}
	put(t, filepath.Join(dir, "db", "tokens", "0123abcd.json"), `{"uid":"local"}`)
	return dir
}

func TestNewFolderIsCurrentFormat(t *testing.T) {
	dir := t.TempDir()
	release, err := prepareData(dir, "1.2.3", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	f, err := readFormat(dir)
	if err != nil || f == nil {
		t.Fatal(f, err)
	}
	eq(t, []any{f.Format, f.Server}, []any{currentFormat, "1.2.3"})
	if _, err := os.Stat(filepath.Join(dir, "backups")); err == nil {
		t.Fatal("a new folder needs no backup")
	}
}

func TestMigrateOneToTwo(t *testing.T) {
	dir := formatOneFolder(t)
	n, _ := dataFormat(dir)
	eq(t, n, 1)
	if _, _, err := newFSStore(dir); err == nil {
		t.Fatal("format 1 was read as it is")
	}
	release, err := prepareData(dir, "1.1.3", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	f, _ := readFormat(dir)
	eq(t, f.Format, 2)
	eq(t, f.History[len(f.History)-1].Server, "1.1.3")

	// every deck, file and type where the store looks for it
	db, bucket, err := newFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	docs, ids, err := db.WhereEq(ctx, "shares", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(ids), 3)
	eq(t, docs[0]["created"].(time.Time).UnixMilli(), int64(1700000000000))
	for _, id := range []string{"aaaaaa1111", "bbbbbb2222", "cccccc3333"} {
		d, err := db.Get(ctx, "shares", id)
		if err != nil || d == nil {
			t.Fatal(id, d, err)
		}
		fh, ct, ok := bucket.Open("shares/" + id + "/media/a.png")
		if !ok {
			t.Fatal("no file for " + id)
		}
		b, _ := io.ReadAll(fh)
		fh.Close()
		eq(t, []any{string(b), ct}, []any{"png " + id, "image/png"})
	}
	if tok, _ := db.Get(ctx, "tokens", "0123abcd"); tok == nil {
		t.Fatal("the token was lost")
	}
	// nothing left in the old places
	if _, err := os.Stat(filepath.Join(dir, "db", "shares", "aaaaaa1111.json")); err == nil {
		t.Fatal("old document left behind")
	}

	// the backup has the folder as it was
	backups, _ := os.ReadDir(filepath.Join(dir, "backups"))
	eq(t, len(backups), 1)
	old := filepath.Join(dir, "backups", backups[0].Name())
	b, err := os.ReadFile(filepath.Join(old, "db", "shares", "aaaaaa1111.json"))
	if err != nil {
		t.Fatal(err)
	}
	match(t, string(b), `Deck aaaaaa1111`)

	// a write after the migration does not reach into the backup
	if err := db.Set(ctx, "shares", "aaaaaa1111", Doc{"name": "Changed", "owner": "local"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(old, "db", "shares", "aaaaaa1111.json"))
	match(t, string(b), `Deck aaaaaa1111`)

	// and a second start does nothing more
	release, err = prepareData(dir, "1.1.3", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	backups, _ = os.ReadDir(filepath.Join(dir, "backups"))
	eq(t, len(backups), 1)
}

// a migration stopped half way (a crash, a power cut) goes on where it was
func TestMigrationResumes(t *testing.T) {
	dir := formatOneFolder(t)
	// one deck and its files already moved, as a first run would have
	id := "aaaaaa1111"
	sh := shard(id)
	if err := moveInto(dir, filepath.Join(dir, "db", "shares", id+".json"), filepath.Join(dir, "db", "shares", sh, id+".json")); err != nil {
		t.Fatal(err)
	}
	if err := moveInto(dir, filepath.Join(dir, "files", "shares", id), filepath.Join(dir, "files", "shares", sh, id)); err != nil {
		t.Fatal(err)
	}
	release, err := prepareData(dir, "1.1.4", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	db, _, _ := newFSStore(dir)
	_, ids, _ := db.WhereEq(context.Background(), "shares", "owner", "local")
	eq(t, len(ids), 3)
}

// a document in both places is not overwritten: the old one is set aside
func TestMigrationSetsAsideConflicts(t *testing.T) {
	dir := formatOneFolder(t)
	id := "bbbbbb2222"
	put(t, filepath.Join(dir, "db", "shares", shard(id), id+".json"), `{"name":"Newer","owner":"local"}`)
	release, err := prepareData(dir, "1.1.4", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	n, _ := countConflicts(dir)
	eq(t, n, 1)
	db, _, _ := newFSStore(dir)
	d, _ := db.Get(context.Background(), "shares", id)
	eq(t, d["name"], "Newer")
}

func TestNewerFormatIsRefused(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "format.json"), `{"format":99,"server":"9.0.0"}`)
	_, err := prepareData(dir, "1.1.3", quiet)
	if err == nil {
		t.Fatal("a newer format was accepted")
	}
	match(t, err.Error(), `newer server \(9\.0\.0\)`)
}

func TestFolderIsLocked(t *testing.T) {
	dir := t.TempDir()
	release, err := prepareData(dir, "1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareData(dir, "1", quiet); err == nil {
		t.Fatal("a second server got the folder")
	}
	release()
	release, err = prepareData(dir, "1", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestBackupsAreKeptToThree(t *testing.T) {
	dir := formatOneFolder(t)
	for i := 0; i < 5; i++ {
		put(t, filepath.Join(dir, "backups", "2020010"+string(rune('1'+i))+"T000000Z-format-1", "x"), "x")
	}
	if _, err := backupFolder(dir, 1); err != nil {
		t.Fatal(err)
	}
	backups, _ := os.ReadDir(filepath.Join(dir, "backups"))
	eq(t, len(backups), keepBackups)
}

// while the folder is being migrated the port answers, and says so
func TestMaintenance(t *testing.T) {
	board := newStatusBoard("migrating", "1.1.3")
	srv := httptest.NewServer(maintenance(board))
	defer srv.Close()

	code, body := req(t, "GET", srv.URL+"/api/shares", "", "")
	eq(t, code, 503)
	match(t, body, `"code":"maintenance"`)
	code, body = req(t, "POST", srv.URL+"/mcp", "application/json", `{}`)
	eq(t, code, 503)
	code, _, page := get(t, srv.URL+"/")
	eq(t, code, 503)
	match(t, page, `being updated`)
	code, body = req(t, "GET", srv.URL+"/api/status", "", "")
	var st serverStatus
	json.Unmarshal([]byte(body), &st)
	eq(t, []any{code, st.State, st.Version}, []any{200, "migrating", "1.1.3"})

	// a page listening hears "migrating", then "ready"
	res, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	var data []string
	go func() {
		time.Sleep(100 * time.Millisecond)
		board.set("ready", "")
	}()
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "data: ") {
			data = append(data, l)
		}
	}
	eq(t, data, []string{`data: {"state":"migrating","version":"1.1.3"}`, `data: {"state":"ready","version":"1.1.3"}`})
}

// the server proper says what it is when a page connects, and that it is
// stopping before it closes the stream
func TestLocalStatusEvents(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	ls := srv.Config.Handler.(*localServer)
	res, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	var data []string
	go func() {
		time.Sleep(100 * time.Millisecond)
		ls.board.set("stopping", "")
	}()
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "data: ") {
			data = append(data, l)
		}
	}
	eq(t, data, []string{`data: {"state":"ready","version":"` + version + `"}`, `data: {"state":"stopping","version":"` + version + `"}`})
}
