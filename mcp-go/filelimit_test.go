package main

import (
	"strings"
	"testing"
)

// A server of one's own takes files up to Env.MaxFile (50 MB unless
// SLIQTLY_MAX_FILE_MB says otherwise), past sliqtly.com's 10 MB data
// files and 20 MB deck files; a smaller setting is kept to.
func TestLocalFileLimit(t *testing.T) {
	big := strings.Repeat("0123456789abcdef\n", 25<<20/17) // 25 MB

	srv, session := startLocal(t, t.TempDir(), "")
	s := &testServer{root: srv.URL, session: session}
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Big", "markdown": "# One\n",
		"files": []any{map[string]any{"name": "log.txt", "text": big[:12<<20]}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	code, body := req(t, "PUT", srv.URL+"/api/files/shares/"+id+"/media/big.bin", "application/octet-stream", big)
	eq(t, code, 200, body)

	srv, session = startLocalWith(t, t.TempDir(), "", func(e *Env) { e.MaxFile = 1 << 20 })
	s = &testServer{root: srv.URL, session: session}
	c = call(t, s, "create_presentation", map[string]any{
		"title": "Big", "markdown": "# One\n",
		"files": []any{map[string]any{"name": "log.txt", "text": big[:2<<20]}},
	})
	eq(t, c.IsError, true)
	match(t, textOf(c), `File log\.txt is larger than 1 MB\.`)
	c = call(t, s, "create_presentation", map[string]any{"title": "Small", "markdown": "# One\n"})
	id = sc(c)["deck_id"].(string)
	code, body = req(t, "PUT", srv.URL+"/api/files/shares/"+id+"/media/big.bin", "application/octet-stream", big[:2<<20])
	eq(t, code, 413)
	match(t, body, `a file is at most 1 MB`)
}

func TestFileLimitDefaults(t *testing.T) {
	cloud := &Env{}
	eq(t, []any{cloud.fileLimit(5 << 20), cloud.bodyMax()}, []any{5 << 20, int64(40 << 20)}, "sliqtly.com keeps its limits")
	own := &Env{MaxFile: localMaxFileMB << 20}
	eq(t, own.fileLimit(5<<20), 50<<20)
	if own.bodyMax() < (50<<20)/3*4 {
		t.Fatalf("a 50 MB file as base64 does not fit a request of %d bytes", own.bodyMax())
	}
}
