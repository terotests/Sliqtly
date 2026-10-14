// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCleanEntry(t *testing.T) {
	e, err := CleanEntry(SessionEntry{Kind: " tool ", Tool: " read_file ", Text: "  read it  ", Refs: []string{"file:data/x.csv", " file:data/x.csv", "url:https://a.example/b"}})
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != "tool" || e.Tool != "read_file" || e.Text != "read it" || !reflect.DeepEqual(e.Refs, []string{"file:data/x.csv", "url:https://a.example/b"}) {
		t.Fatalf("cleaned %+v", e)
	}
	for _, bad := range []SessionEntry{
		{Kind: "fork", Text: "x"},            // the server's own
		{Kind: "thought", Text: "x"},         // no such kind
		{Kind: "note"},                       // says nothing
		{Kind: "note", Tool: "x", Text: "y"}, // a tool on a note
		{Kind: "note", Refs: []string{"no"}}, // not kind:id
		{Kind: "note", Text: strings.Repeat("a", SessionMaxText+1)},
	} {
		if _, err := CleanEntry(bad); err == nil {
			t.Errorf("%+v was taken", bad)
		} else if !errors.As(err, new(ErrEntry)) {
			t.Errorf("%+v: %v is not an ErrEntry", bad, err)
		}
	}
	many := SessionEntry{Kind: "note", Text: "x"}
	for i := 0; i <= SessionMaxRefs; i++ {
		many.Refs = append(many.Refs, "entry:"+string(rune('a'+i)))
	}
	if _, err := CleanEntry(many); err == nil {
		t.Error("too many refs were taken")
	}
}

func TestForkDocRoundTrip(t *testing.T) {
	f := Fork{From: "abc", At: 7, Name: "security-review", By: "u1", Made: 99}
	if got := ForkOf(Doc{ForkField: ForkDoc(f)}); got != f {
		t.Fatalf("%+v → %+v", f, got)
	}
	// as a JSON store hands it back
	if got := ForkOf(Doc{ForkField: map[string]any{"from": "abc", "at": float64(7), "name": "n"}}); got.At != 7 || !got.IsFork() {
		t.Fatalf("from JSON: %+v", got)
	}
	if ForkOf(Doc{}).IsFork() {
		t.Fatal("a plain deck is a fork")
	}
	if CleanForkName("  ux   review ") != "ux review" || CleanForkName(strings.Repeat("é", 90)) != strings.Repeat("é", ForkNameMax) {
		t.Fatal("fork names")
	}
}

type forks map[string]Fork

func (fs forks) read(_ context.Context, deck string) (Fork, error) {
	f, ok := fs[deck]
	if !ok {
		return Fork{}, ErrNotFound
	}
	return f, nil
}

func say(t *testing.T, s Sessions, deck, text string) SessionEntry {
	t.Helper()
	e, err := s.Append(context.Background(), "t", deck, SessionEntry{Kind: KindNote, Text: text, By: ChatFrom{ID: "p-1", Name: "Tero", Kind: "person"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func texts(es []SessionEntry) string {
	var out []string
	for _, e := range es {
		out = append(out, e.Origin+":"+e.Text)
	}
	return strings.Join(out, " ")
}

func TestTimelineReadsThroughForks(t *testing.T) {
	ctx := context.Background()
	s := Sessions{Log: NewMemChat()}
	for _, w := range []string{"m1", "m2", "m3"} {
		say(t, s, "main", w)
	}
	// security forked at 2, then main went on; ux forked security at 3
	fs := forks{"main": {}, "sec": {From: "main", At: 2, Name: "security"}, "ux": {From: "sec", At: 3, Name: "ux"}}
	say(t, s, "sec", "s1")
	say(t, s, "sec", "s2")
	say(t, s, "main", "m4")
	say(t, s, "ux", "u1")

	tl, err := s.Timeline(ctx, "t", "sec", fs.read)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(tl); got != "main:m1 main:m2 sec:s1 sec:s2" {
		t.Fatalf("security's timeline: %s", got)
	}
	for i, e := range tl {
		if e.N != int64(i)+1 {
			t.Fatalf("entry %d numbered %d", i, e.N)
		}
	}
	if tl[1].Seq != 2 || tl[2].Seq != 1 {
		t.Fatalf("seq is the place in the session it was written in: %+v", tl)
	}
	tl, _ = s.Timeline(ctx, "t", "ux", fs.read)
	if got := texts(tl); got != "main:m1 main:m2 sec:s1 ux:u1" {
		t.Fatalf("ux's timeline: %s", got)
	}
	tl, _ = s.Timeline(ctx, "t", "main", fs.read)
	if got := texts(tl); got != "main:m1 main:m2 main:m3 main:m4" {
		t.Fatalf("main's timeline: %s", got)
	}
	// the parent gone: the fork keeps its own
	delete(fs, "main")
	tl, _ = s.Timeline(ctx, "t", "sec", fs.read)
	if got := texts(tl); got != "sec:s1 sec:s2" {
		t.Fatalf("orphan: %s", got)
	}
	// a Story nobody may see has no timeline
	if _, err := s.Timeline(ctx, "t", "nope", fs.read); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown story: %v", err)
	}
}

func TestTimelineStopsOnCycles(t *testing.T) {
	s := Sessions{Log: NewMemChat()}
	say(t, s, "a", "a1")
	say(t, s, "b", "b1")
	fs := forks{"a": {From: "b", At: 5}, "b": {From: "a", At: 5}}
	tl, err := s.Timeline(context.Background(), "t", "a", fs.read)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) > 2*(ForkDepth+1) {
		t.Fatalf("a cycle read %d entries", len(tl))
	}
}

func TestOwnPagesThroughLongSessions(t *testing.T) {
	s := Sessions{Log: NewMemChat()}
	for i := 0; i < chatPageMax+3; i++ {
		say(t, s, "d", "x")
	}
	own, err := s.Own(context.Background(), "t", "d", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != chatPageMax+3 || own[len(own)-1].Seq != int64(chatPageMax+3) {
		t.Fatalf("read %d", len(own))
	}
	part, _ := s.Own(context.Background(), "t", "d", 4)
	if len(part) != 4 {
		t.Fatalf("max 4 read %d", len(part))
	}
}

func TestForkCopiesFile(t *testing.T) {
	for p, want := range map[string]bool{
		"media/cat.png":         true,
		"data/sales.xlsx":       true,
		"charts/a.json":         true,
		"review/comments.json":  false,
		".collab/chat.jsonl":    false,
		"data/.env":             false,
		"keys/server.pem":       false,
		"secrets/token.txt":     false,
		"media/id_rsa":          false,
		"data/credentials.json": false,
		"../other/x.png":        false,
		"":                      false,
	} {
		if got := ForkCopiesFile(p); got != want {
			t.Errorf("%q: %v", p, got)
		}
	}
}

func TestPlanForkTakesOnlyTheDeck(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	parent := Doc{
		"name": "Passkey support", "md": "# Hi", "theme": "aurora", "css": nil,
		"room": "r1", "folder": "f1", "visibility": "private",
		"owner": "anna", "editKey": "secret", "head": "abc", "links": []any{"x"},
		StoryField: map[string]any{"type": "problem", "phase": "review", "tags": []any{"auth"}},
		"files": []any{
			map[string]any{"path": "media/a.png", "type": "image/png", "size": int64(3), "url": "https://old", "token": "t"},
			map[string]any{"path": "review/comments.json", "type": "application/json"},
			map[string]any{"path": "keys/x.pem"},
		},
	}
	p := PlanFork(parent, "main1", 4, "security-review", "tero", now)
	d := p.Doc
	if d["name"] != "Passkey support · security-review" || d["md"] != "# Hi" || d["room"] != "r1" || d["folder"] != "f1" || d["visibility"] != "private" {
		t.Fatalf("deck fields: %+v", d)
	}
	for _, k := range []string{"editKey", "head", "links", "files"} {
		if _, ok := d[k]; ok {
			t.Errorf("%s was copied", k)
		}
	}
	if d["owner"] != "tero" {
		t.Errorf("owner %v", d["owner"])
	}
	if s := StoryOf(d); s.Type != "problem" || s.Phase != NewStoryPhase || !reflect.DeepEqual(s.Tags, []string{"auth", "security-review"}) {
		t.Errorf("story %+v", s)
	}
	if f := ForkOf(d); f != (Fork{From: "main1", At: 4, Name: "security-review", By: "tero", Made: now.UnixMilli()}) {
		t.Errorf("fork %+v", f)
	}
	if len(p.Files) != 1 || p.Files[0]["path"] != "media/a.png" || p.Files[0]["url"] != nil || p.Files[0]["token"] != nil || p.Files[0]["size"] != int64(3) {
		t.Errorf("files %+v", p.Files)
	}
	if !reflect.DeepEqual(p.Skipped, []string{"review/comments.json", "keys/x.pem"}) {
		t.Errorf("skipped %v", p.Skipped)
	}
	// a link deck stays one; a parent's story-less doc forks as a draft story
	q := PlanFork(Doc{"md": "x"}, "p", 0, "n", "u", now)
	if q.Doc["visibility"] != "link" || q.Doc["name"] != "Untitled · n" || StoryOf(q.Doc).Phase != NewStoryPhase {
		t.Errorf("plain: %+v", q.Doc)
	}
}

func TestSessionAppendKeepsAct(t *testing.T) {
	s := Sessions{Log: NewMemChat()}
	e, err := s.Append(context.Background(), "t", "d", SessionEntry{Kind: KindTool, Tool: "update_presentation", Text: "slides 2-3", Refs: []string{"deck:d"}, By: ChatFrom{ID: "bot-claude", Name: "Claude", Kind: "bot"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if e.N != 1 || e.Kind != KindTool || e.Tool != "update_presentation" || e.Origin != "d" || e.By.Name != "Claude" {
		t.Fatalf("%+v", e)
	}
	if _, err := s.Append(context.Background(), "t", "d", SessionEntry{Kind: KindFork, Text: "forked"}, false); err == nil {
		t.Fatal("a caller wrote a fork entry")
	}
	if f, err := s.Append(context.Background(), "t", "d", SessionEntry{Kind: KindFork, Text: "forked", Refs: []string{"deck:f"}, By: ChatFrom{ID: "p-1", Name: "Anna", Kind: "person"}}, true); err != nil || f.Kind != KindFork {
		t.Fatalf("the server's fork entry: %+v %v", f, err)
	}
	if !IsSessionRoom(SessionRoom("d")) || IsSessionRoom("r1") {
		t.Fatal("session rooms")
	}
}
