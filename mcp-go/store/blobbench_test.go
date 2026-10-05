// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// TestBlobBench compares the blob stores on put, streamed read, random
// range reads and delete from 1 KB to 1 GB, document writes while a large
// blob is being written, backup and startup. It writes about 4 GB, so it
// runs only when asked:
//
//	SLIQTLY_BLOB_BENCH=1 go test ./store -run TestBlobBench -v -timeout 2h
//
// SLIQTLY_BLOB_BENCH_OUT=file also writes the tables to that file;
// SLIQTLY_BLOB_BENCH_DIR=dir puts the stores there (a real disk, not tmpfs).
func TestBlobBench(t *testing.T) {
	if os.Getenv("SLIQTLY_BLOB_BENCH") == "" {
		t.Skip("set SLIQTLY_BLOB_BENCH=1 to run")
	}
	if c, err := strconv.Atoi(os.Getenv("SLIQTLY_BLOB_CHUNK")); err == nil && c > 0 {
		store.BlobChunk = c
	}
	base := os.Getenv("SLIQTLY_BLOB_BENCH_DIR")
	if base == "" {
		base = t.TempDir()
	}
	type backend struct {
		name string
		open func(dir string) (store.BlobStore, error)
	}
	backends := []backend{
		{"SQLite blobs.db", func(dir string) (store.BlobStore, error) {
			return store.OpenSQLiteBlobStore(filepath.Join(dir, "blobs.db"))
		}},
		{"files", func(dir string) (store.BlobStore, error) { return store.NewFSBlobStore(filepath.Join(dir, "blobs")) }},
	}
	sizes := []struct {
		label string
		size  int64
		count int
	}{
		{"1 KB", 1 << 10, 1000},
		{"100 KB", 100 << 10, 200},
		{"1 MB", 1 << 20, 50},
		{"10 MB", 10 << 20, 10},
		{"100 MB", 100 << 20, 3},
		{"1 GB", 1 << 30, 1},
	}
	if os.Getenv("SLIQTLY_BLOB_BENCH_QUICK") != "" {
		sizes = sizes[2:5]
	}
	ctx := context.Background()
	var out strings.Builder
	fmt.Fprintf(&out, "Blob stores, %s, SQLite chunk %d KiB. Every write is durable (fsync) when it returns.\n\n", time.Now().UTC().Format("2006-01-02"), store.BlobChunk>>10)
	fmt.Fprintf(&out, "| size | store | put MB/s | put ms (p50) | read MB/s | 64 KB range read µs (p50 / p99) | delete ms (p50) |\n|---|---|---:|---:|---:|---:|---:|\n")
	dirs := map[string]string{}
	stores := map[string]store.BlobStore{}
	for _, b := range backends {
		dir := filepath.Join(base, strings.ReplaceAll(b.name, " ", "-"))
		os.RemoveAll(dir)
		os.MkdirAll(dir, 0o750)
		dirs[b.name] = dir
		s, err := b.open(dir)
		if err != nil {
			t.Fatal(err)
		}
		stores[b.name] = s
	}
	seed := uint64(1)
	for _, sz := range sizes {
		for _, b := range backends {
			s := stores[b.name]
			var puts, dels []time.Duration
			var ranges []time.Duration
			var hashes []store.Hash
			var putTotal, readTotal time.Duration
			for i := 0; i < sz.count; i++ {
				seed++
				t0 := time.Now()
				info, err := s.Put(ctx, &randReader{n: sz.size, r: rand.New(rand.NewPCG(seed, 7))}, "application/octet-stream")
				d := time.Since(t0)
				if err != nil {
					t.Fatal(err)
				}
				puts = append(puts, d)
				putTotal += d
				hashes = append(hashes, info.Hash)
			}
			for _, h := range hashes {
				r, err := s.Open(ctx, h)
				if err != nil {
					t.Fatal(err)
				}
				t0 := time.Now()
				if _, err := io.Copy(io.Discard, r); err != nil {
					t.Fatal(err)
				}
				readTotal += time.Since(t0)
				rng := rand.New(rand.NewPCG(9, 9))
				buf := make([]byte, 64<<10)
				for k := 0; k < 20; k++ {
					off := rng.Int64N(max(sz.size-int64(len(buf)), 1))
					t0 := time.Now()
					if _, err := r.ReadAt(buf[:min(int64(len(buf)), sz.size)], off); err != nil && err != io.EOF {
						t.Fatal(err)
					}
					ranges = append(ranges, time.Since(t0))
				}
				r.Close()
			}
			// half are deleted, half kept for backup and startup
			for _, h := range hashes[:max(len(hashes)/2, 0)] {
				t0 := time.Now()
				if err := s.Delete(ctx, h); err != nil {
					t.Fatal(err)
				}
				dels = append(dels, time.Since(t0))
			}
			mb := float64(sz.size) * float64(sz.count) / (1 << 20)
			delCol := "–"
			if len(dels) > 0 {
				delCol = fmt.Sprintf("%.2f", ms(pct(dels, 50)))
			}
			fmt.Fprintf(&out, "| %s × %d | %s | %.0f | %.2f | %.0f | %.0f / %.0f | %s |\n", sz.label, sz.count, b.name,
				mb/putTotal.Seconds(), ms(pct(puts, 50)), mb/readTotal.Seconds(),
				us(pct(ranges, 50)), us(pct(ranges, 99)), delCol)
			t.Logf("%s %s done", sz.label, b.name)
		}
	}

	// document writes in sliqtly.db while a large blob goes into blobs.db
	fmt.Fprintf(&out, "\nDocument writes to sliqtly.db while a %s blob is written to blobs.db (separate files, so the blob does not hold up the documents):\n\n| | doc writes | p50 ms | p99 ms |\n|---|---:|---:|---:|\n", sizes[len(sizes)-1].label)
	docs, err := store.OpenSQLiteStore(filepath.Join(base, "sliqtly.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"alone", "during the blob write"} {
		var wg sync.WaitGroup
		done := make(chan struct{})
		if phase != "alone" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer close(done)
				if _, err := stores["SQLite blobs.db"].Put(ctx, &randReader{n: sizes[len(sizes)-1].size, r: rand.New(rand.NewPCG(77, 7))}, ""); err != nil {
					t.Error(err)
				}
			}()
		} else {
			go func() { time.Sleep(3 * time.Second); close(done) }()
		}
		var lat []time.Duration
		for i := 0; ; i++ {
			select {
			case <-done:
			default:
				t0 := time.Now()
				if _, err := store.Put(ctx, docs, "shares", fmt.Sprint("d", i%500), store.Doc{"n": int64(i), "title": "deck"}, store.AnyRev); err != nil {
					t.Fatal(err)
				}
				lat = append(lat, time.Since(t0))
				continue
			}
			break
		}
		wg.Wait()
		fmt.Fprintf(&out, "| %s | %d | %.2f | %.2f |\n", phase, len(lat), ms(pct(lat, 50)), ms(pct(lat, 99)))
	}
	docs.Close()

	// backup and startup with what is kept
	fmt.Fprintf(&out, "\nBackup and startup with the blobs that were kept:\n\n| store | blobs | bytes kept | on disk | backup | backup method | open |\n|---|---:|---:|---:|---:|---|---:|\n")
	for _, b := range backends {
		s := stores[b.name]
		n, bytes := 0, int64(0)
		s.Each(ctx, func(i store.BlobInfo) error { n++; bytes += i.Size; return nil })
		if sh, ok := s.(interface{ Shrink(context.Context) error }); ok {
			if err := sh.Shrink(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var took time.Duration
		var how string
		switch x := s.(type) {
		case *store.SQLiteBlobStore:
			t0 := time.Now()
			if _, err := store.BackupSQLite(ctx, x.DB(), x.Path(), "bench"); err != nil {
				t.Fatal(err)
			}
			took, how = time.Since(t0), "VACUUM INTO (a copy)"
		default:
			t0 := time.Now()
			if err := linkTree(filepath.Join(dirs[b.name], "blobs"), filepath.Join(dirs[b.name], "backup")); err != nil {
				t.Fatal(err)
			}
			took, how = time.Since(t0), "hard links (no copy)"
		}
		s.Close()
		disk := dirSize(dirs[b.name], "backup", "backups")
		t0 := time.Now()
		s2, err := b.open(dirs[b.name])
		if err != nil {
			t.Fatal(err)
		}
		open := time.Since(t0)
		s2.Close()
		fmt.Fprintf(&out, "| %s | %d | %.0f MB | %.0f MB | %.2f s | %s | %.1f ms |\n", b.name, n, float64(bytes)/(1<<20), float64(disk)/(1<<20), took.Seconds(), how, ms(open))
	}
	t.Log("\n" + out.String())
	if p := os.Getenv("SLIQTLY_BLOB_BENCH_OUT"); p != "" {
		os.WriteFile(p, []byte(out.String()), 0o640)
	}
}

// randReader yields n pseudo-random bytes without holding them
type randReader struct {
	n int64
	r *rand.Rand
}

func (r *randReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	i := 0
	for ; i+8 <= len(p); i += 8 {
		v := r.r.Uint64()
		for k := 0; k < 8; k++ {
			p[i+k] = byte(v >> (8 * k))
		}
	}
	for ; i < len(p); i++ {
		p[i] = byte(r.r.Uint32())
	}
	r.n -= int64(len(p))
	return len(p), nil
}

func pct(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(len(s)-1, len(s)*p/100)]
}

func ms(d time.Duration) float64 { return float64(d) / 1e6 }
func us(d time.Duration) float64 { return float64(d) / 1e3 }

func linkTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		return os.Link(p, filepath.Join(dst, rel))
	})
}

// the bytes of the regular files under dir, leaving out the backups
func dirSize(dir string, skip ...string) int64 {
	var n int64
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		for _, s := range skip {
			if info.IsDir() && info.Name() == s {
				return filepath.SkipDir
			}
		}
		if info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}
