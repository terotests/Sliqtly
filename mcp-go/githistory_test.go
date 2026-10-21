// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
)

// A deck's history from the server's Git: only the commits that changed
// it, the deck as one of them had it (by a whole id, a short one, or the
// branch), what it changed and its files.
func TestGitDeckHistory(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	ls, s, stop := startLocalServer(t, data)
	defer stop()
	ls.gits = &gitService{data: data, docs: ls.bucket.docs, blobs: ls.bucket.blobs, feed: ls.env.Store, bucket: ls.bucket, logf: t.Logf}
	defer ls.gits.stop()

	// before Git is on: said so
	if code, _ := gitJSONReq(t, "GET", s.root+"/api/history/abcdefgh", ""); code != 404 {
		t.Fatalf("no git: %d", code)
	}
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Mallit", "markdown": "# Mallit\n",
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(PNG)}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	o := call(t, s, "create_presentation", map[string]any{"title": "Muu", "markdown": "# Muu\n"})
	other := sc(o)["deck_id"].(string)
	if code, st := gitJSONReq(t, "PUT", s.root+"/api/settings/git", `{"enabled":true}`); code != 200 {
		t.Fatalf("on: %d %v", code, st)
	}
	first, _ := ls.gits.mirror().Head()

	// a version of the deck, one of the other deck, and one more of it
	if err := ls.env.DB.Update(ctx, "shares", id, Doc{"md": "# Mallit\n\n## Malli A\n"}); err != nil {
		t.Fatal(err)
	}
	_, saved := gitJSONReq(t, "POST", s.root+"/api/git/save", `{"deck":"`+id+`","message":"Malli A"}`)
	second := saved["commit"].(string)
	if err := ls.env.DB.Update(ctx, "shares", other, Doc{"md": "# Muu 2\n"}); err != nil {
		t.Fatal(err)
	}
	gitJSONReq(t, "POST", s.root+"/api/git/save", `{"message":"muu"}`)
	if err := ls.env.DB.Update(ctx, "shares", id, Doc{"md": "# Mallit\n\n## Malli B\n"}); err != nil {
		t.Fatal(err)
	}
	_, saved = gitJSONReq(t, "POST", s.root+"/api/git/save", `{"deck":"`+id+`","message":"Malli B"}`)
	third := saved["commit"].(string)

	code, log := gitJSONReq(t, "GET", s.root+"/api/history/"+id, "")
	if code != 200 {
		t.Fatalf("log %d %v", code, log)
	}
	var ids []string
	for _, x := range log["commits"].([]any) {
		ids = append(ids, x.(map[string]any)["id"].(string))
	}
	if len(ids) != 3 || ids[0] != third || ids[1] != second || ids[2] != first {
		t.Fatalf("log %v, want %s %s %s", ids, third, second, first)
	}
	if subj := log["commits"].([]any)[0].(map[string]any)["subject"]; subj != `Deck "Mallit": Malli B` {
		t.Fatalf("subject %v", subj)
	}

	// the deck as the second commit has it, by a short id
	code, v := gitJSONReq(t, "GET", s.root+"/api/history/"+id+"/"+second[:8], "")
	if code != 200 || v["md"] != "# Mallit\n\n## Malli A\n" || v["name"] != "Mallit" || v["id"] != second {
		t.Fatalf("version %d %v", code, v)
	}
	files, _ := json.Marshal(v["files"])
	if string(files) != `[{"path":"media/dot.png","size":`+itoa(int64(len(PNG)))+`,"type":"image/png"}]` {
		t.Fatalf("files %s", files)
	}
	// the branch is the newest
	if _, v = gitJSONReq(t, "GET", s.root+"/api/history/"+id+"/main", ""); v["md"] != "# Mallit\n\n## Malli B\n" {
		t.Fatalf("main %v", v)
	}
	// what the second changed; the first added everything
	_, ch := gitJSONReq(t, "GET", s.root+"/api/history/"+id+"/"+second+"/changes", "")
	if b, _ := json.Marshal(ch["changes"]); string(b) != `[{"kind":"changed","path":"deck.md","text":true}]` {
		t.Fatalf("changes %s", b)
	}
	_, ch = gitJSONReq(t, "GET", s.root+"/api/history/"+id+"/"+first+"/changes", "")
	if n := len(ch["changes"].([]any)); n != 2 {
		t.Fatalf("first changes %v", ch)
	}
	// a file of a version, as it was
	code, png := req(t, "GET", s.root+"/api/history/"+id+"/"+first+"/file/media/dot.png", "", "")
	if code != 200 || png != string(PNG) {
		t.Fatalf("file %d", code)
	}
	code, md := req(t, "GET", s.root+"/api/history/"+id+"/"+second+"/file/deck.md", "", "")
	if code != 200 || md != "# Mallit\n\n## Malli A\n" {
		t.Fatalf("deck.md %d %q", code, md)
	}
	for _, bad := range []string{
		"/api/history/" + id + "/" + second + "/file/deck.json",
		"/api/history/" + id + "/" + second + "/file/../../" + other + "/deck.md",
		"/api/history/" + id + "/0000000",
		"/api/history/" + id + "/nosuchbranch",
		"/api/history/nosuchdeck1",
	} {
		if code, _ := req(t, "GET", s.root+bad, "", ""); code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}
