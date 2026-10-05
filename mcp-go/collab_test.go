// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// one page in a room, as the test sees it: its stream's events in order
type testPeer struct {
	t      *testing.T
	base   string
	id     string
	client string
	events chan map[string]any
	res    *http.Response
}

func joinTest(t *testing.T, base, id, client, name string, rev int) *testPeer {
	t.Helper()
	url := fmt.Sprintf("%s/api/events?room=%s&client=%s&who=%s&name=%s&color=%%23ea580c&rev=%d", base, id, client, "w"+client, name, rev)
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("events: %d", res.StatusCode)
	}
	p := &testPeer{t: t, base: base, id: id, client: client, events: make(chan map[string]any, 100000), res: res}
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			l := sc.Text()
			if !strings.HasPrefix(l, "data: ") {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(l[6:]), &m) == nil && m["t"] != nil {
				p.events <- m
			}
		}
		close(p.events)
	}()
	return p
}

func (p *testPeer) close() { p.res.Body.Close() }

// the next event of kind `t`, the others before it skipped
func (p *testPeer) next(t string) map[string]any {
	p.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-p.events:
			if !ok {
				p.t.Fatalf("%s: the stream ended", p.client)
			}
			if m["t"] == t {
				return m
			}
		case <-deadline:
			p.t.Fatalf("%s: no %s event", p.client, t)
		}
	}
}

func (p *testPeer) post(what string, body map[string]any) (int, map[string]any) {
	body["client"] = p.client
	b, _ := json.Marshal(body)
	code, out := req(p.t, "POST", p.base+"/api/collab/"+p.id+what, "application/json", string(b))
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	return code, m
}

func opsOf(t *testing.T, m map[string]any) delta {
	t.Helper()
	d, err := parseDelta(m["ops"])
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func snapshot(t *testing.T, base, id string) map[string]any {
	t.Helper()
	code, body := req(t, "GET", base+"/api/collab/"+id, "", "")
	if code != 200 {
		t.Fatalf("snapshot: %d %s", code, body)
	}
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	return m
}

func newDeck(t *testing.T, base, md string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"name": "Together", "md": md})
	code, body := req(t, "POST", base+"/api/shares", "application/json", string(b))
	eq(t, code, 201)
	var made map[string]string
	json.Unmarshal([]byte(body), &made)
	return made["id"]
}

func TestCollabRoom(t *testing.T) {
	dir := t.TempDir()
	srv, session := startLocal(t, dir, "")
	defer srv.Close()
	defer session.Close()
	id := newDeck(t, srv.URL, "# Title\n\nHello\n")

	snap := snapshot(t, srv.URL, id)
	eq(t, []any{snap["rev"], snap["md"]}, []any{float64(0), "# Title\n\nHello\n"})
	a := joinTest(t, srv.URL, id, "pageA1", "AnonymousZebra", 0)
	defer a.close()
	a.next("peers")
	b := joinTest(t, srv.URL, id, "pageB1", "AnonymousOtter", 0)
	defer b.close()
	peers := b.next("peers")["peers"].([]any)
	eq(t, len(peers), 2, "both are here")
	eq(t, len(a.next("peers")["peers"].([]any)), 2, "A hears B come")

	// an edit reaches the other page at once, and its sender as the ack
	text := toU16("# Title\n\nHello\n")
	start := time.Now()
	code, out := a.post("/op", map[string]any{"rev": 0, "ops": diffU16(text, toU16("# Title\n\nHello world\n")).json()})
	eq(t, []any{code, out["rev"]}, []any{200, float64(1)})
	got := b.next("op")
	took := time.Since(start)
	eq(t, []any{got["rev"], got["client"]}, []any{float64(1), "pageA1"})
	text = mustApply(t, opsOf(t, got), text)
	eq(t, fromU16(text), "# Title\n\nHello world\n")
	eq(t, a.next("op")["client"], "pageA1", "the sender hears its own edit: its ack")
	if took > time.Second {
		t.Fatalf("an edit took %v to reach the other page", took)
	}
	t.Logf("an edit reached the other page in %v", took)

	// B typed at the start on revision 0, before A's edit reached it: taken
	// over A's edit, not refused
	code, out = b.post("/op", map[string]any{"rev": 0, "ops": delta{}.retain(2).insert(toU16("Big ")).json()})
	eq(t, []any{code, out["rev"]}, []any{200, float64(2)})
	got = a.next("op")
	text = mustApply(t, opsOf(t, got), text)
	eq(t, fromU16(text), "# Big Title\n\nHello world\n")
	eq(t, snapshot(t, srv.URL, id)["md"], fromU16(text))
	// an edit sent again after its answer was lost is not taken twice
	code, out = b.post("/op", map[string]any{"rev": 2, "seq": 1, "ops": delta{}.insert(toU16("!")).json()})
	eq(t, []any{code, out["rev"]}, []any{200, float64(3)})
	text = mustApply(t, opsOf(t, a.next("op")), text)
	code, out = b.post("/op", map[string]any{"rev": 2, "seq": 1, "ops": delta{}.insert(toU16("!")).json()})
	eq(t, []any{code, out["rev"], out["again"]}, []any{200, float64(3), true})

	// carets, moved to the room's revision
	code, _ = a.post("/presence", map[string]any{"rev": 1, "caret": 2, "anchor": 2})
	eq(t, code, 200)
	cur := b.next("cursor")
	eq(t, []any{cur["client"], cur["caret"]}, []any{"pageA1", float64(3)}, "an insert at the caret: the caret stays before it")
	// a new name, everyone told (B came in with A's colour and was given
	// another, so this one may be B's: A then gets a free one)
	code, _ = a.post("/presence", map[string]any{"name": "Ada", "color": "#7c3aed"})
	eq(t, code, 200)
	colors := map[string]any{}
	for _, p := range b.next("peers")["peers"].([]any) {
		m := p.(map[string]any)
		colors[m["client"].(string)] = m["color"]
		if m["client"] == "pageA1" {
			eq(t, m["name"], "Ada")
		}
	}
	if colors["pageA1"] == colors["pageB1"] || colors["pageB1"] == "#ea580c" {
		t.Fatalf("two people in one colour: %v", colors)
	}

	// the chat: to everyone, kept beside the deck
	code, msg := b.post("/chat", map[string]any{"text": "  Is the title final? 😀 "})
	eq(t, []any{code, msg["text"], msg["name"]}, []any{200, "Is the title final? 😀", "AnonymousOtter"})
	said := a.next("chat")["msg"].(map[string]any)
	eq(t, said["id"], msg["id"])
	code, _ = b.post("/chat", map[string]any{"text": "   "})
	eq(t, code, 400, "nothing to say")
	code, _ = (&testPeer{t: t, base: srv.URL, id: id, client: "stranger1"}).post("/chat", map[string]any{"text": "hi"})
	eq(t, code, 409, "only a page in the room")
	if _, err := os.Stat(filepath.Join(dir, "files", "shares", id, ".collab", "chat.jsonl")); err != nil {
		t.Fatal("the chat is not kept: ", err)
	}

	// the room writes the deck once edits pause
	time.Sleep(collabSaveWait + 300*time.Millisecond)
	code, body := req(t, "GET", srv.URL+"/api/shares/"+id, "", "")
	eq(t, code, 200)
	match(t, body, `"md":"!# Big Title\\n\\nHello world\\n"`)

	// a write from elsewhere (a page not in the room, an assistant) while
	// A has typed more that is not written yet: it comes in as an edit
	code, _ = a.post("/op", map[string]any{"rev": 3, "ops": delta{}.retain(len(text)).insert(toU16("A typed\n")).json()})
	eq(t, code, 200)
	text = mustApply(t, opsOf(t, b.next("op")), text)
	code, _ = req(t, "PATCH", srv.URL+"/api/shares/"+id, "application/json", `{"md":"!# Big Title\n\nHello world\n\n## From the assistant\n"}`)
	eq(t, code, 200)
	got = b.next("op")
	eq(t, got["client"], "server")
	text = mustApply(t, opsOf(t, got), text)
	// both at the end: the write from elsewhere goes first
	eq(t, fromU16(text), "!# Big Title\n\nHello world\n\n## From the assistant\nA typed\n", "both kept")
	eq(t, snapshot(t, srv.URL, id)["md"], fromU16(text))

	// a page leaving is heard
	b.close()
	left := a.next("peers")["peers"].([]any)
	eq(t, len(left), 1)

	// an edit for a revision not here, and one that does not fit
	code, _ = a.post("/op", map[string]any{"rev": 99, "ops": []any{}})
	eq(t, code, 409)
	code, _ = a.post("/op", map[string]any{"rev": 0, "ops": []any{map[string]any{"retain": 9999}, map[string]any{"insert": "x"}}})
	eq(t, code, 409)
}

// a page that lost its stream gets what it missed
func TestCollabReconnect(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	id := newDeck(t, srv.URL, "abc")
	a := joinTest(t, srv.URL, id, "pageA1", "A", 0)
	defer a.close()
	b := joinTest(t, srv.URL, id, "pageB1", "B", 0)
	b.next("peers")
	b.close()
	for i := 0; i < 3; i++ {
		a.post("/op", map[string]any{"rev": i, "ops": delta{}.insert(toU16("x")).json()})
	}
	r, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/events?room=%s&client=pageB1&name=B", srv.URL, id), nil)
	r.Header.Set("Last-Event-ID", "1")
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	var revs []string
	for sc.Scan() && len(revs) < 2 {
		if strings.HasPrefix(sc.Text(), "id: ") {
			revs = append(revs, sc.Text()[4:])
		}
	}
	eq(t, revs, []string{"2", "3"})
}

// many pages typing at once, each sending on the revision it has: all end
// with the room's text, and so does the deck's file
func TestCollabManyPages(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	start := "# Title\n\nSome text 😀 here.\n"
	id := newDeck(t, srv.URL, start)
	const pages = 12
	const edits = 15
	var wg sync.WaitGroup
	finals := make([]string, pages)
	peers := make([]*testPeer, pages)
	for i := range pages {
		peers[i] = joinTest(t, srv.URL, id, fmt.Sprintf("page%02d", i), "P", 0)
	}
	for i := range pages {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := peers[i]
			r := rand.New(rand.NewSource(int64(i)))
			text := toU16(start)
			rev := 0
			// one edit out at a time, the others' edits taken as they come
			for k := 0; k < edits; k++ {
				out := otRandomDelta(r, text)
				text = mustApply(t, out, text)
				b, _ := json.Marshal(map[string]any{"client": p.client, "rev": rev, "ops": out.json()})
				res, err := http.Post(p.base+"/api/collab/"+id+"/op", "application/json", strings.NewReader(string(b)))
				if err != nil || res.StatusCode != 200 {
					t.Errorf("%s: send failed", p.client)
					return
				}
				res.Body.Close()
				for {
					m := <-p.events
					if m["t"] != "op" {
						continue
					}
					rev = int(m["rev"].(float64))
					if m["client"] == p.client {
						break
					}
					o, _ := parseDelta(m["ops"])
					var o1 delta
					out, o1 = transform(out, o)
					text = mustApply(t, o1, text)
				}
			}
			// then everyone else's, to the last
			for rev < pages*edits {
				m, ok := <-p.events
				if !ok {
					t.Errorf("%s: the stream ended", p.client)
					return
				}
				if m["t"] == "op" {
					o, _ := parseDelta(m["ops"])
					text = mustApply(t, o, text)
					rev = int(m["rev"].(float64))
				}
			}
			finals[i] = fromU16(text)
		}(i)
	}
	wg.Wait()
	room := snapshot(t, srv.URL, id)
	want := room["md"].(string)
	eq(t, room["rev"], float64(pages*edits))
	for i, p := range peers {
		eq(t, finals[i], want, p.client+" has the room's text")
		p.close()
	}
	// and the room's text is what its edits in order give
	rm := srv.Config.Handler.(*localServer).openRoom(id)
	rm.mu.Lock()
	replay := toU16(start)
	for _, e := range rm.log {
		replay = mustApply(t, e.d, replay)
	}
	rm.mu.Unlock()
	eq(t, fromU16(replay), want)
	rm.flush()
	code, body := req(t, "GET", srv.URL+"/api/shares/"+id, "", "")
	eq(t, code, 200)
	var doc map[string]any
	json.Unmarshal([]byte(body), &doc)
	eq(t, doc["md"], want, "the file has the room's text")
}

// the chat is read back when the room opens again
func TestCollabChatKept(t *testing.T) {
	dir := t.TempDir()
	srv, session := startLocal(t, dir, "")
	id := newDeck(t, srv.URL, "x")
	a := joinTest(t, srv.URL, id, "pageA1", "AnonymousZebra", 0)
	a.next("peers")
	a.post("/chat", map[string]any{"text": "first"})
	a.post("/chat", map[string]any{"text": "second"})
	a.close()
	session.Close()
	srv.Close()
	srv2, session2 := startLocal(t, dir, "")
	defer srv2.Close()
	defer session2.Close()
	chat := snapshot(t, srv2.URL, id)["chat"].([]any)
	eq(t, len(chat), 2)
	eq(t, chat[1].(map[string]any)["text"], "second")
	eq(t, chat[0].(map[string]any)["name"], "AnonymousZebra")
}

// no two people in a room have one name or one colour
func TestCollabNamesFree(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	id := newDeck(t, srv.URL, "# Names\n")
	names := func(ev map[string]any) map[string]string {
		out := map[string]string{}
		for _, p := range ev["peers"].([]any) {
			m := p.(map[string]any)
			out[m["client"].(string)] = m["name"].(string) + " " + m["color"].(string)
		}
		return out
	}
	// everyone comes in as AnonymousPanda: the animals first, then numbers
	n := len(collabAnimals) + 3
	pages := make([]*testPeer, n)
	var last map[string]any
	for i := range pages {
		pages[i] = joinTest(t, srv.URL, id, fmt.Sprintf("page%02d", i), "AnonymousPanda", 0)
		defer pages[i].close()
		last = pages[i].next("peers")
	}
	seen := map[string]bool{}
	colors := map[string]int{}
	for client, nc := range names(last) {
		name, color, _ := strings.Cut(nc, " #")
		if seen[strings.ToLower(name)] {
			t.Fatalf("%s: the name %q twice", client, name)
		}
		seen[strings.ToLower(name)] = true
		colors[color]++
	}
	eq(t, len(seen), n)
	eq(t, names(last)["page00"][:len("AnonymousPanda #")], "AnonymousPanda #", "the first keeps its name")
	for i := 2; i <= 4; i++ {
		if !seen[fmt.Sprintf("anonymouspanda %d", i)] {
			t.Fatalf("no AnonymousPanda %d once the animals ran out: %v", i, seen)
		}
	}
	eq(t, len(colors), len(collabColors), "every colour in use before one is used twice")

	// one's own other page keeps the name; a rename to someone's name is numbered
	again := joinTest(t, srv.URL, id, "page00", "Whatever", 0)
	defer again.close()
	got := names(again.next("peers"))
	eq(t, strings.HasPrefix(got["page00"], "AnonymousPanda #"), true)
	other := &testPeer{t: t, base: srv.URL, id: id, client: "page01"}
	code, _ := other.post("/presence", map[string]any{"name": "anonymouspanda"})
	eq(t, code, 200)
	got = names(snapshot(t, srv.URL, id))
	name, _, _ := strings.Cut(got["page01"], " #")
	if strings.EqualFold(name, "AnonymousPanda") {
		t.Fatalf("renamed into another's name: %v", got)
	}

	// the lists are the page's (web/collab.js)
	js, err := os.ReadFile(filepath.Join("..", "web", "collab.js"))
	if err != nil {
		t.Fatal(err)
	}
	list := func(name string) []string {
		m := regexp.MustCompile(`(?s)export const ` + name + ` = \[(.*?)\];`).FindStringSubmatch(string(js))
		if m == nil {
			t.Fatalf("no %s in web/collab.js", name)
		}
		var out []string
		for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1) {
			out = append(out, q[1])
		}
		return out
	}
	eq(t, list("ANIMALS"), collabAnimals)
	eq(t, list("COLORS"), collabColors)
}

// a page that comes back to a room opened again (restart, idle close) with
// its old revision and epoch is told to read the deck again: the new room
// counts its revisions from 0, and its log is not the page's
func TestCollabEpochReset(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	ls := srv.Config.Handler.(*localServer)
	id := newDeck(t, srv.URL, "abc")
	epoch := snapshot(t, srv.URL, id)["epoch"].(string)
	if epoch == "" {
		t.Fatal("no epoch")
	}
	a := joinTest(t, srv.URL, id, "pageA1", "A", 0)
	a.next("peers")
	code, _ := a.post("/op", map[string]any{"rev": 0, "ops": delta{}.insert(toU16("x")).json(), "epoch": epoch})
	eq(t, code, 200)
	a.close()

	// the room goes as when nobody has been there for a while
	rm := ls.openRoom(id)
	for i := 0; ls.openRoom(id) != nil && i < 100; i++ {
		rm.close()
		time.Sleep(20 * time.Millisecond)
	}
	if ls.openRoom(id) != nil {
		t.Fatal("the room is still open")
	}
	epoch2 := snapshot(t, srv.URL, id)["epoch"].(string)
	if epoch2 == epoch {
		t.Fatal("the room opened again has the old epoch")
	}

	code, out := a.post("/op", map[string]any{"rev": 1, "ops": delta{}.retain(1).insert(toU16("y")).json(), "epoch": epoch})
	eq(t, code, 409, "an op from the old room")
	eq(t, out["code"], "reset")

	url := fmt.Sprintf("%s/api/events?room=%s&client=pageA1&name=A&rev=1&epoch=%s", srv.URL, id, epoch)
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	p := &testPeer{t: t, client: "pageA1", events: make(chan map[string]any, 100)}
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			var m map[string]any
			if l := sc.Text(); strings.HasPrefix(l, "data: ") && json.Unmarshal([]byte(l[6:]), &m) == nil {
				p.events <- m
			}
		}
		close(p.events)
	}()
	p.next("reset")
	eq(t, snapshot(t, srv.URL, id)["md"], "xabc")
}

// at shutdown the rooms write what they took, before their save timers
func TestCollabFlushRooms(t *testing.T) {
	dir := t.TempDir()
	srv, session := startLocal(t, dir, "")
	ls := srv.Config.Handler.(*localServer)
	id := newDeck(t, srv.URL, "abc")
	a := joinTest(t, srv.URL, id, "pageA1", "A", 0)
	a.next("peers")
	code, _ := a.post("/op", map[string]any{"rev": 0, "ops": delta{}.insert(toU16("x")).json()})
	eq(t, code, 200)
	ls.flushRooms()
	d, err := ls.env.DB.Get(context.Background(), "shares", id)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, d["md"], "xabc")
	a.close()
	session.Close()
	srv.Close()
}
