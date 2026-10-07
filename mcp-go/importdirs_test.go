package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// an album folder with pictures, a secret beside it and links out of it
func importFixture(t *testing.T) (album, outside string) {
	t.Helper()
	root := t.TempDir()
	album = filepath.Join(root, "photo album")
	outside = filepath.Join(root, "private")
	for _, d := range []string{album, filepath.Join(album, "trip"), outside, filepath.Join(root, "photo album2")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string, b []byte) {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(album, "trip", "cover.png"), PNG)
	write(filepath.Join(album, "trip", "sales.csv"), []byte("month,sales\nJan,10\nFeb,12\n"))
	write(filepath.Join(album, "trip", "big.png"), make([]byte, 6<<20))
	write(filepath.Join(outside, "secret.png"), PNG)
	write(filepath.Join(root, "photo album2", "other.png"), PNG)
	link := func(target, name string) {
		if err := os.Symlink(target, name); err != nil {
			t.Skip("no symbolic links here:", err)
		}
	}
	// a link to a file outside, a link to a folder outside, a link inside
	link(filepath.Join(outside, "secret.png"), filepath.Join(album, "trip", "secret.png"))
	link(outside, filepath.Join(album, "elsewhere"))
	link(filepath.Join(album, "trip", "cover.png"), filepath.Join(album, "same.png"))
	return album, outside
}

func TestParseImportDirs(t *testing.T) {
	album, outside := importFixture(t)
	dirs, err := parseImportDirs(album + " , " + outside + "\n")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, dirs.list(), []string{album, outside})
	none, err := parseImportDirs("")
	if err != nil || len(none) != 0 {
		t.Fatal("an empty setting is no folders", err)
	}
	for _, bad := range []string{"photos", filepath.Join(album, "nothere"), filepath.Join(album, "trip", "cover.png"), "/"} {
		if _, err := parseImportDirs(bad); err == nil {
			t.Errorf("%q was taken as an import folder", bad)
		}
	}
}

func TestImportDirsRead(t *testing.T) {
	album, outside := importFixture(t)
	dirs, err := parseImportDirs(album)
	if err != nil {
		t.Fatal(err)
	}
	const limit = 5 << 20
	b, err := dirs.read(filepath.Join(album, "trip", "cover.png"), limit)
	if err != nil || string(b) != string(PNG) {
		t.Fatalf("the picture inside was not read: %v", err)
	}
	if _, err := dirs.read(filepath.Join(album, "same.png"), limit); err != nil {
		t.Errorf("a link that stays inside was refused: %v", err)
	}
	refused := map[string]string{
		filepath.Join(album, "..", "private", "secret.png"):               "not inside",
		filepath.Join(album, "trip", "..", "..", "private", "secret.png"): "not inside",
		album + "2/other.png":                           "not inside", // a name that only starts like the folder
		filepath.Join(album, "trip", "secret.png"):      "not inside", // a link to a file outside
		filepath.Join(album, "elsewhere", "secret.png"): "not inside", // through a link to a folder outside
		filepath.Join(outside, "secret.png"):            "not inside",
		"trip/cover.png":                                "not an absolute path",
		filepath.Join(album, "trip", "nothere.png"):     "no such file",
		filepath.Join(album, "trip"):                    "not a file",
		album:                                           "not a file", // the folder itself
		filepath.Join(album, "trip", "big.png"):         "larger than 5 MB",
	}
	for p, want := range refused {
		_, err := dirs.read(p, limit)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", p, err, want)
		}
	}
	// the folder named through a link: both spellings of a path are found,
	// and a link out of it is still refused
	linked := filepath.Join(t.TempDir(), "album-link")
	if err := os.Symlink(album, linked); err != nil {
		t.Fatal(err)
	}
	dirs2, err := parseImportDirs(linked)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(linked, "trip", "cover.png"), filepath.Join(album, "trip", "cover.png")} {
		if _, err := dirs2.read(p, limit); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := dirs2.read(filepath.Join(linked, "elsewhere", "secret.png"), limit); err == nil {
		t.Error("a link out of a linked folder was followed")
	}
	if _, err := (importDirs{}).read(filepath.Join(album, "trip", "cover.png"), limit); err == nil {
		t.Error("read without import folders")
	}
}

// the tool's input schema as JSON
func toolSchema(t *testing.T, session *mcp.ClientSession, name string) string {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range tools.Tools {
		if x.Name == name {
			b, _ := json.Marshal(x.InputSchema)
			return string(b)
		}
	}
	t.Fatalf("no tool %s", name)
	return ""
}

func TestLocalImportByPath(t *testing.T) {
	album, outside := importFixture(t)
	dirs, err := parseImportDirs(album)
	if err != nil {
		t.Fatal(err)
	}
	srv, session := startLocalWith(t, t.TempDir(), "", func(e *Env) { e.ImportDirs = dirs })
	defer srv.Close()
	defer session.Close()
	s := &testServer{root: srv.URL, session: session}

	schema := toolSchema(t, session, "create_presentation")
	match(t, schema, `"path":\{"description":"Instead of url or data_base64: the absolute path of the picture on this server's computer, inside the import folders `+album)
	match(t, schema, `"path":\{"description":"Instead of text, data_base64 or url`)
	match(t, toolSchema(t, session, "update_presentation"), `import folders`)
	match(t, toolSchema(t, session, "vectorize_image"), `"image_path"`)

	md := "# Trip\n\n## Cover\n\n![](media/cover.png)\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Trip", "markdown": md,
		"images": []any{map[string]any{"name": "cover.png", "path": filepath.Join(album, "trip", "cover.png")}},
		"files":  []any{map[string]any{"name": "sales.csv", "path": filepath.Join(album, "trip", "sales.csv")}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	code, ct, body := get(t, srv.URL+"/files/shares/"+id+"/media/cover.png")
	eq(t, []any{code, ct, body == string(PNG)}, []any{200, "image/png", true})
	code, _, body = get(t, srv.URL+"/files/shares/"+id+"/data/sales.csv")
	eq(t, []any{code, body}, []any{200, "month,sales\nJan,10\nFeb,12\n"})

	// refused: outside the folder, through links out, the wrong kind, two sources
	bad := []struct {
		img  map[string]any
		want string
	}{
		{map[string]any{"name": "s.png", "path": filepath.Join(album, "..", "private", "secret.png")}, `not inside the server's import folders\)\. Import folders: ` + album},
		{map[string]any{"name": "s.png", "path": filepath.Join(album, "trip", "secret.png")}, `not inside`},
		{map[string]any{"name": "s.png", "path": filepath.Join(album, "elsewhere", "secret.png")}, `not inside`},
		{map[string]any{"name": "s.png", "path": filepath.Join(outside, "secret.png")}, `not inside`},
		{map[string]any{"name": "s.png", "path": "trip/cover.png"}, `not an absolute path`},
		{map[string]any{"name": "s.png", "path": filepath.Join(album, "trip", "big.png")}, `larger than 5 MB`},
		{map[string]any{"name": "s.png", "path": filepath.Join(album, "trip", "sales.csv")}, `is not a \.png, \.jpg`},
		{map[string]any{"name": "s.jpg", "path": filepath.Join(album, "trip", "cover.png")}, `different kinds of picture`},
		{map[string]any{"name": "s.png", "path": filepath.Join(album, "trip", "cover.png"), "url": "https://images.test/cat.png"}, `give one of text, data_base64, path or url`},
	}
	for _, b := range bad {
		u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "images": []any{b.img}})
		if !u.IsError {
			t.Fatalf("%v was taken", b.img)
		}
		match(t, textOf(u), b.want)
	}
	f := call(t, s, "update_presentation", map[string]any{"deck_id": id, "files": []any{map[string]any{"name": "x.csv", "path": filepath.Join(outside, "secret.png")}}})
	match(t, textOf(f), `not the same kind of file`)

	v := call(t, s, "vectorize_image", map[string]any{"image_path": filepath.Join(album, "trip", "cover.png")})
	if v.IsError {
		t.Fatal(textOf(v))
	}
	match(t, textOf(v), `<svg`)
	v = call(t, s, "vectorize_image", map[string]any{"image_path": filepath.Join(outside, "secret.png")})
	match(t, textOf(v), `not inside`)
}

// without import folders no path is offered or read: a local server not
// given any, and sliqtly.com
func TestImportPathOff(t *testing.T) {
	album, _ := importFixture(t)
	img := []any{map[string]any{"name": "cover.png", "path": filepath.Join(album, "trip", "cover.png")}}
	md := "# Trip\n\n## Cover\n\n![](media/cover.png)\n"

	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	notMatch(t, toolSchema(t, session, "create_presentation"), `"path"`)
	r := call(t, &testServer{root: srv.URL, session: session}, "create_presentation", map[string]any{"title": "T", "markdown": md, "images": img})
	match(t, textOf(r), `path is read only by a Sliqtly server on your own computer started with import folders`)

	f := fakeFirebase()
	e := withSignIn(testEnv(&f, nil))
	// even set, the cloud server reads nothing by path (it has no LocalUser)
	e.ImportDirs, _ = parseImportDirs(album)
	s := start(t, e, signIn(f))
	defer s.close()
	notMatch(t, toolSchema(t, s.session, "create_presentation"), `import folders`)
	r = call(t, s, "create_presentation", map[string]any{"title": "T", "markdown": md, "images": img})
	if !r.IsError {
		t.Fatal("the cloud server read a file by path")
	}
	match(t, textOf(r), `path is read only by a Sliqtly server on your own computer`)
	r = call(t, s, "vectorize_image", map[string]any{"image_path": filepath.Join(album, "trip", "cover.png")})
	match(t, textOf(r), `image_path is read only`)
}
