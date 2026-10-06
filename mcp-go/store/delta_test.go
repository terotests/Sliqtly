// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
	"github.com/terotests/sliqtly/mcp-go/store/storetest"
)

// a Markdown deck of about n kB; version v differs in a few lines
func deckText(n, v int) []byte {
	var b strings.Builder
	r := rand.New(rand.NewSource(7))
	words := []string{"revenue", "growth", "slide", "chart", "quarter", "team", "plan", "risk", "market", "Sliqtly"}
	for i := 0; b.Len() < n<<10; i++ {
		if i%40 == 0 {
			fmt.Fprintf(&b, "\n---\n\n# Slide %d\n\n", i/40)
		}
		fmt.Fprintf(&b, "- %s %s %d\n", words[r.Intn(len(words))], words[r.Intn(len(words))], r.Intn(1000))
	}
	s := b.String()
	for k := 1; k <= v; k++ {
		at := len(s) * k / (v + 2)
		s = s[:at] + fmt.Sprintf("\n- edited in version %d\n", k) + s[at:]
	}
	return []byte(s)
}

func openBlobs(t *testing.T) *store.SQLiteBlobStore {
	t.Helper()
	b, err := store.OpenSQLiteBlobStore(filepath.Join(t.TempDir(), "blobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func put(t *testing.T, b store.BlobStore, data []byte) store.Hash {
	t.Helper()
	info, err := b.Put(context.Background(), bytes.NewReader(data), "text/markdown")
	if err != nil {
		t.Fatal(err)
	}
	return info.Hash
}

func read(t *testing.T, b store.BlobStore, h store.Hash) []byte {
	t.Helper()
	r, err := b.Open(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// the delta store still passes the blob contract
func TestSQLiteBlobStoreWithDeltas(t *testing.T) {
	storetest.RunBlobs(t, func(t *testing.T) store.BlobStore { return openBlobs(t) })
}

// an older version kept as a delta against the newer: the same bytes from
// every offset, a fraction of the room, and whole again when the newer goes
func TestBlobDelta(t *testing.T) {
	ctx := context.Background()
	b := openBlobs(t)
	v1, v2 := deckText(300, 1), deckText(300, 2)
	h1, h2 := put(t, b, v1), put(t, b, v2)
	ok, err := b.Deltify(ctx, h1, h2)
	if err != nil || !ok {
		t.Fatalf("deltify: %v %v", ok, err)
	}
	u, _ := b.Usage(ctx)
	if u.Deltas != 1 || u.Kept > int64(len(v2))+2000 {
		t.Fatalf("usage %+v: v1 should take a few hundred bytes", u)
	}
	if !bytes.Equal(read(t, b, h1), v1) || !bytes.Equal(read(t, b, h2), v2) {
		t.Fatal("bytes changed")
	}
	// ranges, as http.ServeContent asks for them
	r, _ := b.Open(ctx, h1)
	for _, off := range []int64{0, 1, 100000, int64(len(v1)) - 10} {
		p := make([]byte, 10)
		n, err := r.ReadAt(p, off)
		if (err != nil && err != io.EOF) || !bytes.Equal(p[:n], v1[off:min(off+10, int64(len(v1)))]) {
			t.Fatalf("ReadAt %d: %v", off, err)
		}
	}
	r.Close()
	if n, bad, err := store.VerifyBlobs(ctx, b); err != nil || len(bad) != 0 || n != 2 {
		t.Fatalf("verify %d %v %v", n, bad, err)
	}
	// v1 against itself, or v2 against v1 (a cycle): no
	if ok, _ := b.Deltify(ctx, h1, h1); ok {
		t.Fatal("delta against itself")
	}
	if ok, _ := b.Deltify(ctx, h2, h1); ok {
		t.Fatal("a cycle was made")
	}
	// the base goes: v1 is kept whole first
	if err := b.Delete(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if u, _ := b.Usage(ctx); u.Deltas != 0 || u.Blobs != 1 {
		t.Fatalf("after delete %+v", u)
	}
	if !bytes.Equal(read(t, b, h1), v1) {
		t.Fatal("v1 lost with its base")
	}
}

// random bytes do not pay: kept whole
func TestBlobDeltaNotWorthIt(t *testing.T) {
	ctx := context.Background()
	b := openBlobs(t)
	r := rand.New(rand.NewSource(1))
	x, y := make([]byte, 50000), make([]byte, 50000)
	r.Read(x)
	r.Read(y)
	if ok, err := b.Deltify(ctx, put(t, b, x), put(t, b, y)); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

// many versions, each against the next: chains stop at MaxDeltaChain, all
// read back, and dropping a middle one keeps the rest readable
func TestBlobDeltaChain(t *testing.T) {
	ctx := context.Background()
	b := openBlobs(t)
	var hs []store.Hash
	var data [][]byte
	for v := 0; v < store.MaxDeltaChain+6; v++ {
		d := deckText(40, v)
		data = append(data, d)
		hs = append(hs, put(t, b, d))
	}
	// the newest is whole; the one that would make the chain longer than
	// MaxDeltaChain stays whole too, and a new chain starts on it
	var whole []int
	for i := len(hs) - 2; i >= 0; i-- {
		if ok, err := b.Deltify(ctx, hs[i], hs[i+1]); err != nil {
			t.Fatal(err)
		} else if !ok {
			whole = append(whole, i)
		}
	}
	if len(whole) != 1 || whole[0] != len(hs)-2-store.MaxDeltaChain {
		t.Fatalf("kept whole: %v", whole)
	}
	for i, h := range hs {
		if !bytes.Equal(read(t, b, h), data[i]) {
			t.Fatalf("version %d", i)
		}
	}
	mid := len(hs) - 5
	if err := b.Delete(ctx, hs[mid]); err != nil {
		t.Fatal(err)
	}
	for i, h := range hs {
		if i == mid {
			if _, err := b.Stat(ctx, h); err == nil {
				t.Fatal("deleted blob still there")
			}
			continue
		}
		if !bytes.Equal(read(t, b, h), data[i]) {
			t.Fatalf("version %d after deleting %d", i, mid)
		}
	}
	if _, bad, err := store.VerifyBlobs(ctx, b); err != nil || len(bad) != 0 {
		t.Fatalf("verify: %v %v", bad, err)
	}
}

// a reader opened before the body was rewritten goes on reading the same
// bytes
func TestBlobDeltaWhileReading(t *testing.T) {
	ctx := context.Background()
	b := openBlobs(t)
	v1, v2 := deckText(2000, 1), deckText(2000, 2)
	h1, h2 := put(t, b, v1), put(t, b, v2)
	r, err := b.Open(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	head := make([]byte, 1000)
	if _, err := io.ReadFull(r, head); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Deltify(ctx, h1, h2); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(head, rest...), v1) {
		t.Fatal("reader saw other bytes")
	}
}

// XLSX-like ZIPs: RangerDiff's part delta rebuilds the parts, not the ZIP;
// what is kept still gives the very bytes of the file back
func TestBlobDeltaZip(t *testing.T) {
	ctx := context.Background()
	b := openBlobs(t)
	mk := func(v int) []byte {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for _, name := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/worksheets/sheet1.xml"} {
			w, _ := z.Create(name)
			io.WriteString(w, `<?xml version="1.0"?><root>`)
			for i := 0; i < 3000; i++ {
				val := i
				if i == 1500 && name == "xl/worksheets/sheet1.xml" {
					val += v
				}
				fmt.Fprintf(w, `<row r="%d"><c r="A%d"><v>%d</v></c></row>`, i+1, i+1, val)
			}
			io.WriteString(w, `</root>`)
		}
		z.Close()
		return buf.Bytes()
	}
	x1, x2 := mk(1), mk(2)
	h1, h2 := put(t, b, x1), put(t, b, x2)
	if _, err := b.Deltify(ctx, h1, h2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(t, b, h1), x1) {
		t.Fatal("zip bytes changed")
	}
}

// a blobs.db of schema 1 (before deltas) is migrated in place, without a
// copy, and its blobs read as before
func TestBlobSchemaOneMigrates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "blobs.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, path, store.SQLiteBlobSchema[:1], "old"); err != nil {
		t.Fatal(err)
	}
	data := []byte("kept by an older server")
	h := store.HashOf(data)
	db.Exec(`INSERT INTO blobs (hash, body, size, mime, created, chunk) VALUES (?, 1, ?, 'text/plain', 0, 261120)`, h[:], len(data))
	db.Exec(`INSERT INTO chunks (body, n, data) VALUES (1, 0, ?)`, data)
	db.Close()
	b, err := store.OpenSQLiteBlobStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !bytes.Equal(read(t, b, h), data) {
		t.Fatal("old blob")
	}
	if v, _ := store.SchemaVersion(ctx, b.DB()); v != len(store.SQLiteBlobSchema) {
		t.Fatalf("schema %d", v)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "backups", "*")); len(m) != 0 {
		t.Fatalf("an additive migration made a copy: %v", m)
	}
}
