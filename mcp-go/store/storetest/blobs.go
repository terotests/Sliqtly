// SPDX-License-Identifier: AGPL-3.0-or-later

package storetest

import (
	"bytes"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// RunBlobs tests the BlobStore that open makes; each test gets a new, empty one.
func RunBlobs(t *testing.T, open func(t *testing.T) store.BlobStore) {
	tests := []struct {
		name string
		fn   func(t *testing.T, b store.BlobStore)
	}{
		{"PutOpen", blobPutOpen},
		{"Empty", blobEmpty},
		{"Dedup", blobDedup},
		{"Ranges", blobRanges},
		{"Missing", blobMissing},
		{"DeleteEach", blobDeleteEach},
		{"VerifyCollect", blobVerifyCollect},
		{"Concurrent", blobConcurrent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := open(t)
			defer b.Close()
			tc.fn(t, b)
		})
	}
}

func randBytes(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func readAll(t *testing.T, b store.BlobStore, h store.Hash) []byte {
	t.Helper()
	r := must(b.Open(ctx, h))
	defer r.Close()
	return must(io.ReadAll(r))
}

func blobPutOpen(t *testing.T, b store.BlobStore) {
	data := randBytes(3*store.BlobChunk+12345, 1)
	info := must(b.Put(ctx, bytes.NewReader(data), "video/mp4"))
	if info.Hash != store.HashOf(data) || info.Size != int64(len(data)) || info.Mime != "video/mp4" {
		t.Fatalf("info %+v", info)
	}
	if !bytes.Equal(readAll(t, b, info.Hash), data) {
		t.Fatal("bytes differ")
	}
	st := must(b.Stat(ctx, info.Hash))
	if st.Size != info.Size || st.Mime != "video/mp4" || st.Created.IsZero() {
		t.Fatalf("stat %+v", st)
	}
}

func blobEmpty(t *testing.T, b store.BlobStore) {
	info := must(b.Put(ctx, bytes.NewReader(nil), "text/plain"))
	if info.Size != 0 || info.Hash != store.HashOf(nil) {
		t.Fatalf("info %+v", info)
	}
	if got := readAll(t, b, info.Hash); len(got) != 0 {
		t.Fatalf("read %d bytes", len(got))
	}
}

func blobDedup(t *testing.T, b store.BlobStore) {
	data := randBytes(store.BlobChunk+1, 2)
	a := must(b.Put(ctx, bytes.NewReader(data), "image/png"))
	time.Sleep(2 * time.Millisecond)
	c := must(b.Put(ctx, bytes.NewReader(data), "application/octet-stream"))
	if a.Hash != c.Hash || !c.Created.Equal(a.Created) || c.Mime != "image/png" {
		t.Fatalf("second put %+v, first %+v", c, a)
	}
	n := 0
	must0(b.Each(ctx, func(store.BlobInfo) error { n++; return nil }))
	if n != 1 {
		t.Fatalf("%d blobs", n)
	}
}

func blobRanges(t *testing.T, b store.BlobStore) {
	data := randBytes(2*store.BlobChunk+777, 3)
	info := must(b.Put(ctx, bytes.NewReader(data), ""))
	r := must(b.Open(ctx, info.Hash))
	defer r.Close()
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 200; i++ {
		off := rng.Int63n(int64(len(data)))
		n := rng.Intn(3 * store.BlobChunk / 2)
		p := make([]byte, n)
		k, err := r.ReadAt(p, off)
		want := data[off:min(off+int64(n), int64(len(data)))]
		if k != len(want) || !bytes.Equal(p[:k], want) {
			t.Fatalf("ReadAt(%d, %d) = %d, %v", n, off, k, err)
		}
		if k < n && err != io.EOF {
			t.Fatalf("short ReadAt without EOF: %v", err)
		}
	}
	// Seek then Read, as http.ServeContent does for a Range
	off := int64(store.BlobChunk - 10)
	must(r.Seek(off, io.SeekStart))
	p := make([]byte, 20)
	must(io.ReadFull(r, p))
	if !bytes.Equal(p, data[off:off+20]) {
		t.Fatal("seek and read across a chunk differ")
	}
	if end := must(r.Seek(0, io.SeekEnd)); end != int64(len(data)) {
		t.Fatalf("end %d", end)
	}
	if k, err := r.Read(p); k != 0 || err != io.EOF {
		t.Fatalf("read at end: %d %v", k, err)
	}
}

func blobMissing(t *testing.T, b store.BlobStore) {
	h := store.HashOf([]byte("never put"))
	if _, err := b.Stat(ctx, h); err != store.ErrNoBlob {
		t.Fatalf("stat: %v", err)
	}
	if _, err := b.Open(ctx, h); err != store.ErrNoBlob {
		t.Fatalf("open: %v", err)
	}
	must0(b.Delete(ctx, h))
}

func blobDeleteEach(t *testing.T, b store.BlobStore) {
	var hs []store.Hash
	for i := 0; i < 5; i++ {
		hs = append(hs, must(b.Put(ctx, bytes.NewReader(randBytes(100+i, int64(10+i))), "x")).Hash)
	}
	must0(b.Delete(ctx, hs[2]))
	var seen []store.Hash
	must0(b.Each(ctx, func(i store.BlobInfo) error { seen = append(seen, i.Hash); return nil }))
	if len(seen) != 4 {
		t.Fatalf("%d blobs after a delete", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1].String() >= seen[i].String() {
			t.Fatal("not in hash order")
		}
	}
	if _, err := b.Stat(ctx, hs[2]); err != store.ErrNoBlob {
		t.Fatalf("deleted blob: %v", err)
	}
}

func blobVerifyCollect(t *testing.T, b store.BlobStore) {
	keep := must(b.Put(ctx, bytes.NewReader([]byte("kept")), "")).Hash
	drop := must(b.Put(ctx, bytes.NewReader([]byte("dropped")), "")).Hash
	checked, bad := must2(store.VerifyBlobs(ctx, b))
	if checked != 2 || len(bad) != 0 {
		t.Fatalf("verify %d %v", checked, bad)
	}
	ref := func(h store.Hash) (bool, error) { return h == keep, nil }
	// too new to collect: its reference may be on its way
	if n := must(store.CollectBlobs(ctx, b, ref, time.Now().Add(-time.Hour))); n != 0 {
		t.Fatalf("collected %d new blobs", n)
	}
	if n := must(store.CollectBlobs(ctx, b, ref, time.Now().Add(time.Second))); n != 1 {
		t.Fatalf("collected %d", n)
	}
	if _, err := b.Stat(ctx, drop); err != store.ErrNoBlob {
		t.Fatal("unreferenced blob kept")
	}
	if _, err := b.Stat(ctx, keep); err != nil {
		t.Fatal("referenced blob collected")
	}
}

func blobConcurrent(t *testing.T, b store.BlobStore) {
	data := randBytes(store.BlobChunk*2+5, 7)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, d := range [][]byte{data, randBytes(5000, int64(100+g))} {
				info, err := b.Put(ctx, bytes.NewReader(d), "")
				if err == nil && info.Hash != store.HashOf(d) {
					err = io.ErrShortWrite
				}
				if err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	n := 0
	must0(b.Each(ctx, func(store.BlobInfo) error { n++; return nil }))
	if n != 9 {
		t.Fatalf("%d blobs, want 9", n)
	}
	if !bytes.Equal(readAll(t, b, store.HashOf(data)), data) {
		t.Fatal("shared blob differs")
	}
}

func must0(err error) {
	if err != nil {
		panic(err)
	}
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}
