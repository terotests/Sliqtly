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
	release, err := prepareData(dir, "1.2.3", "local", quiet)
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

func TestMigrateOneToCurrent(t *testing.T) {
	dir := formatOneFolder(t)
	n, _ := dataFormat(dir)
	eq(t, n, 1)
	if _, _, err := newFSStore(dir, "local"); err == nil {
		t.Fatal("format 1 was read as it is")
	}
	release, err := prepareData(dir, "1.1.3", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	f, _ := readFormat(dir)
	eq(t, f.Format, currentFormat)
	eq(t, len(f.History), 2)
	eq(t, f.History[len(f.History)-1].Server, "1.1.3")

	// every deck, file and type where the store looks for it
	db, bucket, err := newFSStore(dir, "local")
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
	release, err = prepareData(dir, "1.1.3", "local", quiet)
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
	release, err := prepareData(dir, "1.1.4", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	db, _, _ := newFSStore(dir, "local")
	_, ids, _ := db.WhereEq(context.Background(), "shares", "owner", "local")
	eq(t, len(ids), 3)
}

// a document in both places is not overwritten: the old one is set aside
func TestMigrationSetsAsideConflicts(t *testing.T) {
	dir := formatOneFolder(t)
	id := "bbbbbb2222"
	put(t, filepath.Join(dir, "db", "shares", shard(id), id+".json"), `{"name":"Newer","owner":"local"}`)
	release, err := prepareData(dir, "1.1.4", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	n, _ := countConflicts(dir)
	eq(t, n, 1)
	db, _, _ := newFSStore(dir, "local")
	d, _ := db.Get(context.Background(), "shares", id)
	eq(t, d["name"], "Newer")
}

// a folder as format 2 left it: decks of two owners, one naming none, and
// one already in a room
func formatTwoFolder(t *testing.T) string {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "format.json"), `{"format":2,"server":"1.2.0"}`)
	deck := func(id, body string) {
		put(t, filepath.Join(dir, "db", "shares", shard(id), id+".json"), body)
	}
	deck("aaaaaa1111", `{"name":"A","owner":"local","created":{"$ts":1700000000000}}`)
	deck("bbbbbb2222", `{"name":"B","owner":"mcp"}`)
	deck("cccccc3333", `{"name":"C"}`)
	deck("dddddd4444", `{"name":"D","owner":"local","room":"r1","inherit_room_files":true}`)
	put(t, filepath.Join(dir, "files", "shares", shard("aaaaaa1111"), "aaaaaa1111", "media", "a.png"), "png")
	return dir
}

func TestMigrateTwoToThree(t *testing.T) {
	dir := formatTwoFolder(t)
	if _, _, err := newFSStore(dir, "local"); err == nil {
		t.Fatal("format 2 was read as it is")
	}
	release, err := prepareData(dir, "1.3.0", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	f, _ := readFormat(dir)
	eq(t, f.Format, 3)

	db, bucket, err := newFSStore(dir, "local")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for id, want := range map[string][]any{
		"aaaaaa1111": {"home-local", false},
		"bbbbbb2222": {"home-mcp", false},
		"cccccc3333": {"home-local", false}, // no owner: the server's user
		"dddddd4444": {"r1", true},          // already in a room: stays
	} {
		d, _ := db.Get(ctx, "shares", id)
		eq(t, []any{d["room"], d["inherit_room_files"]}, want)
	}
	a, _ := db.Get(ctx, "shares", "aaaaaa1111")
	eq(t, []any{a["name"], a["created"].(time.Time).UnixMilli()}, []any{"A", int64(1700000000000)})
	if _, _, ok := bucket.Open("shares/aaaaaa1111/media/a.png"); !ok {
		t.Fatal("the deck's file was lost")
	}
	for _, owner := range []string{"local", "mcp"} {
		room, _ := db.Get(ctx, "rooms", "home-"+owner)
		eq(t, []any{room["kind"], room["tenant"], room["archived"]}, []any{"home", "local", false})
		m, _ := db.Get(ctx, "room_members", "home-"+owner+"~user-"+owner)
		eq(t, []any{m["room"], m["member"], m["role"]}, []any{"home-" + owner, "user:" + owner, "owner"})
	}

	// the backup has the decks as they were
	backups, _ := os.ReadDir(filepath.Join(dir, "backups"))
	eq(t, len(backups), 1)
	b, _ := os.ReadFile(filepath.Join(dir, "backups", backups[0].Name(), "db", "shares", shard("aaaaaa1111"), "aaaaaa1111.json"))
	if strings.Contains(string(b), "room") {
		t.Fatal("the backup was written into: " + string(b))
	}
}

// stopped after one deck: the second run makes what is missing only
func TestHomeRoomsResume(t *testing.T) {
	dir := formatTwoFolder(t)
	if _, err := homeRooms(dir, "local"); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "db", "shares", shard("eeeeee5555"), "eeeeee5555.json"), `{"name":"E","owner":"local"}`)
	n, err := homeRooms(dir, "local")
	eq(t, []any{n, err}, []any{0, nil})
	release, err := prepareData(dir, "1.3.0", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// after the migration every deck written has a home room, and a writer
// that replaces a deck keeps the room it is in
func TestDecksKeepTheirRoom(t *testing.T) {
	dir := formatTwoFolder(t)
	release, err := prepareData(dir, "1.3.0", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	release()
	db, _, _ := newFSStore(dir, "local")
	ctx := context.Background()
	if err := db.Set(ctx, "shares", "ffffff6666", Doc{"name": "New", "owner": "someone else"}); err != nil {
		t.Fatal(err)
	}
	d, _ := db.Get(ctx, "shares", "ffffff6666")
	room := d["room"].(string)
	match(t, room, `^home-h[0-9a-f]{24}$`)
	eq(t, d["inherit_room_files"], false)
	if r, _ := db.Get(ctx, "rooms", room); r == nil {
		t.Fatal("no home room for a new owner")
	}
	if err := db.Set(ctx, "shares", "dddddd4444", Doc{"name": "D2", "owner": "local"}); err != nil {
		t.Fatal(err)
	}
	d, _ = db.Get(ctx, "shares", "dddddd4444")
	eq(t, []any{d["name"], d["room"], d["inherit_room_files"]}, []any{"D2", "r1", true})
}

func TestNewerFormatIsRefused(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, "format.json"), `{"format":99,"server":"9.0.0"}`)
	_, err := prepareData(dir, "1.1.3", "local", quiet)
	if err == nil {
		t.Fatal("a newer format was accepted")
	}
	match(t, err.Error(), `newer server \(9\.0\.0\)`)
}

func TestFolderIsLocked(t *testing.T) {
	dir := t.TempDir()
	release, err := prepareData(dir, "1", "local", quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareData(dir, "1", "local", quiet); err == nil {
		t.Fatal("a second server got the folder")
	}
	release()
	release, err = prepareData(dir, "1", "local", quiet)
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
