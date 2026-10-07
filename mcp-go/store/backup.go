// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// BackupRepo is a folder of incremental backups of a data folder
// (sliqtly.db + blobs.db), on another disk or another machine's share:
//
//	<repo>/backup.json            what this folder is
//	<repo>/blobs.db               every byte kept, by SHA-256 (SQLiteBlobStore)
//	<repo>/snapshots/<id>.json    one per backup: what the data folder held
//
// A snapshot is a consistent copy of sliqtly.db (VACUUM INTO, beside the
// server that writes it), every kept file it names (file_refs) and a few
// small files of the folder (format.json). Blobs are kept by hash, so a
// file that did not change since the last snapshot takes no room: only new
// bytes are copied. The snapshot's manifest is written last and is its
// commit point; a stopped run leaves blobs no manifest names, which Prune
// removes.
//
// Older versions are kept as deltas against the newer ones (Deltify, the
// newest whole): sliqtly.db of the last snapshot against this one's, and
// each path's previous bytes against its new ones. A Markdown or XLSX file
// edited a little costs about the edit; a photo or a video costs itself.
//
// One process writes a repo at a time (the caller locks the folder); one
// BackupRepo is safe for concurrent use within it.
type BackupRepo struct {
	dir   string
	blobs *SQLiteBlobStore
	mu    sync.Mutex
}

// BackupSource is the data folder being backed up.
type BackupSource struct {
	Docs  *sql.DB   // sliqtly.db (read-only is enough)
	Blobs BlobStore // blobs.db (read-only is enough)
	// Extra are small files of the folder kept as they are, by name
	// (format.json)
	Extra map[string][]byte
	// Server names the build taking the backup
	Server string
}

// Snapshot is one backup's manifest.
type Snapshot struct {
	ID     string         `json:"id"`
	Time   time.Time      `json:"time"`
	Server string         `json:"server,omitempty"`
	Docs   BackupBlob     `json:"docs"`
	Extra  []BackupBlob   `json:"extra,omitempty"`
	Files  []BackupFile   `json:"files"`
	New    BackupNew      `json:"new"`
	Took   BackupDuration `json:"took"`
}

// BackupBlob is a file kept whole in the repo: sliqtly.db or an extra
type BackupBlob struct {
	Name string `json:"name,omitempty"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// BackupFile is a kept file of the folder (a file_refs row)
type BackupFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	Mime string `json:"mime,omitempty"`
}

// BackupNew is what a snapshot added to the repo
type BackupNew struct {
	Blobs int   `json:"blobs"`
	Bytes int64 `json:"bytes"`
}

// BackupDuration is a time.Duration written as text ("1.2s")
type BackupDuration time.Duration

func (d BackupDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).Round(time.Millisecond).String())
}

func (d *BackupDuration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = BackupDuration(v)
	return err
}

type backupHead struct {
	Kind    string    `json:"kind"`
	Format  int       `json:"format"`
	Created time.Time `json:"created"`
}

const (
	backupKind   = "sliqtly-backup"
	backupFormat = 1
	// the name of sliqtly.db's copy in the repo
	docsMime = "application/vnd.sqlite3"
)

// ErrBackupSourceChanged: a file the copy of sliqtly.db names was removed
// from blobs.db before it was copied (the server collected it). Taking the
// snapshot again gives a consistent one.
var ErrBackupSourceChanged = errors.New("store: a kept file went while it was being backed up")

// OpenBackupRepo opens the repo at dir, making it when the folder is new
// or empty. The caller holds the folder's lock.
func OpenBackupRepo(dir string) (*BackupRepo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "snapshots"), 0o750); err != nil {
		return nil, err
	}
	// WriteAtomic's temp files a killed process left behind (a stop
	// while backup.json was being written leaves the folder with only a
	// .tmp-* file in it). The caller holds the folder's lock, so no
	// write of ours is going on.
	if err := removeTempFiles(abs); err != nil {
		return nil, err
	}
	headPath := filepath.Join(abs, "backup.json")
	b, err := os.ReadFile(headPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// a new repo only in an empty folder (a fresh disk may have
		// lost+found; the caller's lock file and our snapshots/ are ours)
		ents, err := os.ReadDir(abs)
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			if n := e.Name(); n != "snapshots" && n != ".lock" && n != "lost+found" {
				return nil, fmt.Errorf("%s is not a backup folder and not empty (it has %s): give an empty folder", abs, n)
			}
		}
		h, _ := json.MarshalIndent(backupHead{Kind: backupKind, Format: backupFormat, Created: time.Now().UTC()}, "", "  ")
		if err := WriteAtomic(headPath, append(h, '\n')); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		var h backupHead
		if err := json.Unmarshal(b, &h); err != nil || h.Kind != backupKind {
			return nil, fmt.Errorf("%s: not a Sliqtly backup folder", abs)
		}
		if h.Format > backupFormat {
			return nil, fmt.Errorf("%s is in backup format %d, written by a newer server; this one reads %d", abs, h.Format, backupFormat)
		}
	}
	blobs, err := OpenSQLiteBlobStore(filepath.Join(abs, "blobs.db"))
	if err != nil {
		return nil, err
	}
	return &BackupRepo{dir: abs, blobs: blobs}, nil
}

// removeTempFiles removes the .tmp-* files WriteAtomic left in dir
func removeTempFiles(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), ".tmp-") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (r *BackupRepo) Dir() string { return r.dir }

// Blobs is the repo's blob store (for checks).
func (r *BackupRepo) Blobs() *SQLiteBlobStore { return r.blobs }

func (r *BackupRepo) Close() error { return r.blobs.Close() }

// Snapshots are the repo's snapshots, oldest first.
func (r *BackupRepo) Snapshots() ([]Snapshot, error) {
	ents, err := os.ReadDir(filepath.Join(r.dir, "snapshots"))
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		s, err := r.readSnapshot(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Snapshot is the snapshot id ("latest" or "" for the newest).
func (r *BackupRepo) Snapshot(id string) (Snapshot, error) {
	if id == "" || id == "latest" {
		all, err := r.Snapshots()
		if err != nil {
			return Snapshot{}, err
		}
		if len(all) == 0 {
			return Snapshot{}, fmt.Errorf("%s has no backups", r.dir)
		}
		return all[len(all)-1], nil
	}
	return r.readSnapshot(id)
}

func (r *BackupRepo) readSnapshot(id string) (Snapshot, error) {
	var s Snapshot
	if strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return s, fmt.Errorf("bad backup id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(r.dir, "snapshots", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, fmt.Errorf("no backup %q in %s", id, r.dir)
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("backup %s: %w", id, err)
	}
	return s, nil
}

// Take makes a snapshot of src. It reads src only, so it may run beside
// the server that writes the folder.
func (r *BackupRepo) Take(ctx context.Context, src BackupSource) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := time.Now()
	snap := Snapshot{Time: start.UTC().Truncate(time.Second), Server: src.Server}
	snap.ID = snap.Time.Format("20060102T150405Z")
	// two in one second: the later gets a suffix
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(r.dir, "snapshots", snap.ID+".json")); errors.Is(err, os.ErrNotExist) {
			break
		}
		snap.ID = fmt.Sprintf("%s-%d", snap.Time.Format("20060102T150405Z"), i)
	}
	prev, _ := r.Snapshot("latest")

	// 1. a consistent copy of sliqtly.db
	tmpDir := filepath.Join(r.dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return snap, err
	}
	copyPath := filepath.Join(tmpDir, snap.ID+".db")
	os.Remove(copyPath)
	defer os.Remove(copyPath)
	if _, err := src.Docs.ExecContext(ctx, `VACUUM INTO ?`, copyPath); err != nil {
		return snap, fmt.Errorf("copy of sliqtly.db: %w", err)
	}
	files, err := readFileRefs(ctx, copyPath)
	if err != nil {
		return snap, err
	}
	snap.Files = files

	// 2. the copy itself
	f, err := os.Open(copyPath)
	if err != nil {
		return snap, err
	}
	info, err := r.put(ctx, f, docsMime, &snap.New)
	f.Close()
	if err != nil {
		return snap, err
	}
	snap.Docs = BackupBlob{Name: "sliqtly.db", Hash: info.Hash.String(), Size: info.Size}

	// 3. the files it names that the repo does not have yet
	for _, file := range files {
		h, _ := ParseHash(file.Hash)
		if _, err := r.blobs.Stat(ctx, h); err == nil {
			continue
		}
		rd, err := src.Blobs.Open(ctx, h)
		if errors.Is(err, ErrNoBlob) {
			return snap, fmt.Errorf("%s: %w", file.Path, ErrBackupSourceChanged)
		}
		if err != nil {
			return snap, fmt.Errorf("%s: %w", file.Path, err)
		}
		got, err := r.put(ctx, rd, file.Mime, &snap.New)
		rd.Close()
		if err != nil {
			return snap, fmt.Errorf("%s: %w", file.Path, err)
		}
		if got.Hash != h || got.Size != file.Size {
			return snap, fmt.Errorf("%s: blobs.db gave other bytes than its hash %s says (%s, %d bytes)", file.Path, h, got.Hash, got.Size)
		}
	}

	// 4. the small files
	names := make([]string, 0, len(src.Extra))
	for n := range src.Extra {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		info, err := r.put(ctx, strings.NewReader(string(src.Extra[n])), "application/octet-stream", &snap.New)
		if err != nil {
			return snap, err
		}
		snap.Extra = append(snap.Extra, BackupBlob{Name: n, Hash: info.Hash.String(), Size: info.Size})
	}

	// 5. the manifest: the snapshot is there when it is
	snap.Took = BackupDuration(time.Since(start))
	b, err := json.MarshalIndent(snap, "", " ")
	if err != nil {
		return snap, err
	}
	if err := WriteAtomic(filepath.Join(r.dir, "snapshots", snap.ID+".json"), append(b, '\n')); err != nil {
		return snap, err
	}

	// 6. the previous versions as deltas against these (only space: a
	// failure here leaves them whole)
	if prev.ID != "" {
		r.deltifyPrevious(ctx, prev, snap)
	}
	return snap, nil
}

// put keeps the bytes of rd, counting them in n when they are new
func (r *BackupRepo) put(ctx context.Context, rd io.Reader, mime string, n *BackupNew) (BlobInfo, error) {
	before, _ := r.blobs.Usage(ctx)
	info, err := r.blobs.Put(ctx, rd, mime)
	if err != nil {
		return info, err
	}
	if after, _ := r.blobs.Usage(ctx); after.Blobs > before.Blobs {
		n.Blobs++
		n.Bytes += info.Size
	}
	return info, nil
}

func (r *BackupRepo) deltifyPrevious(ctx context.Context, prev, now Snapshot) {
	current := map[string]bool{now.Docs.Hash: true}
	byPath := map[string]string{}
	for _, f := range now.Files {
		current[f.Hash] = true
		byPath[f.Path] = f.Hash
	}
	pairs := [][2]string{{prev.Docs.Hash, now.Docs.Hash}}
	for _, f := range prev.Files {
		if nh, ok := byPath[f.Path]; ok && nh != f.Hash {
			pairs = append(pairs, [2]string{f.Hash, nh})
		}
	}
	for _, p := range pairs {
		// a blob this snapshot still names stays whole
		if current[p[0]] {
			continue
		}
		old, err1 := ParseHash(p[0])
		nw, err2 := ParseHash(p[1])
		if err1 == nil && err2 == nil {
			r.blobs.Deltify(ctx, old, nw)
		}
	}
}

// readFileRefs lists file_refs of the sliqtly.db at path
func readFileRefs(ctx context.Context, path string) ([]BackupFile, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT path, hash, size, mime FROM file_refs ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("copy of sliqtly.db: %w", err)
	}
	defer rows.Close()
	out := []BackupFile{}
	for rows.Next() {
		var f BackupFile
		var hb []byte
		if err := rows.Scan(&f.Path, &hb, &f.Size, &f.Mime); err != nil {
			return nil, err
		}
		var h Hash
		copy(h[:], hb)
		f.Hash = h.String()
		out = append(out, f)
	}
	return out, rows.Err()
}

// BackupKeep says which snapshots Prune keeps: the newest Last, the newest
// of each of the last Daily days and of the last Weekly weeks (ISO weeks).
// A zero BackupKeep keeps everything.
type BackupKeep struct {
	Last, Daily, Weekly int
}

// DefaultBackupKeep: three, two weeks of days, two months of weeks
var DefaultBackupKeep = BackupKeep{Last: 3, Daily: 14, Weekly: 8}

func (k BackupKeep) all() bool { return k.Last <= 0 && k.Daily <= 0 && k.Weekly <= 0 }

// kept picks the snapshots k keeps (snaps oldest first), by id
func (k BackupKeep) kept(snaps []Snapshot) map[string]bool {
	keep := map[string]bool{}
	if k.all() {
		for _, s := range snaps {
			keep[s.ID] = true
		}
		return keep
	}
	days, weeks := map[string]bool{}, map[string]bool{}
	for i := len(snaps) - 1; i >= 0; i-- {
		s := snaps[i]
		if len(snaps)-1-i < k.Last {
			keep[s.ID] = true
		}
		day := s.Time.UTC().Format("2006-01-02")
		if !days[day] && len(days) < k.Daily {
			days[day] = true
			keep[s.ID] = true
		}
		y, w := s.Time.UTC().ISOWeek()
		week := fmt.Sprintf("%d-%02d", y, w)
		if !weeks[week] && len(weeks) < k.Weekly {
			weeks[week] = true
			keep[s.ID] = true
		}
	}
	return keep
}

// PruneReport says what Prune removed
type PruneReport struct {
	Snapshots []string // the ids removed
	Blobs     int      // blobs no kept snapshot names
}

// Prune removes the snapshots keep does not keep, then the blobs no
// remaining snapshot names (those of removed snapshots and of runs that
// stopped before their manifest), and gives the space back.
func (r *BackupRepo) Prune(ctx context.Context, keep BackupKeep) (PruneReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rep PruneReport
	snaps, err := r.Snapshots()
	if err != nil {
		return rep, err
	}
	kept := keep.kept(snaps)
	named := map[Hash]bool{}
	for _, s := range snaps {
		if !kept[s.ID] {
			continue
		}
		for _, h := range s.hashes() {
			if p, err := ParseHash(h); err == nil {
				named[p] = true
			}
		}
	}
	for _, s := range snaps {
		if kept[s.ID] {
			continue
		}
		if err := os.Remove(filepath.Join(r.dir, "snapshots", s.ID+".json")); err != nil {
			return rep, err
		}
		rep.Snapshots = append(rep.Snapshots, s.ID)
	}
	// the oldest versions are the leaves of the delta chains: removed
	// first, nothing has to be rewritten
	var gone []BlobInfo
	if err := r.blobs.Each(ctx, func(b BlobInfo) error {
		if !named[b.Hash] {
			gone = append(gone, b)
		}
		return nil
	}); err != nil {
		return rep, err
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i].Created.Before(gone[j].Created) })
	for _, b := range gone {
		if err := r.blobs.Delete(ctx, b.Hash); err != nil {
			return rep, err
		}
		rep.Blobs++
	}
	if rep.Blobs > 0 {
		if err := r.blobs.Shrink(ctx); err != nil {
			return rep, err
		}
	}
	os.RemoveAll(filepath.Join(r.dir, "tmp"))
	return rep, nil
}

// hashes are every blob the snapshot names
func (s Snapshot) hashes() []string {
	out := []string{s.Docs.Hash}
	for _, e := range s.Extra {
		out = append(out, e.Hash)
	}
	for _, f := range s.Files {
		out = append(out, f.Hash)
	}
	return out
}

// VerifyReport says what Verify read
type VerifyReport struct {
	Snapshots int
	Blobs     int   // blobs read back and checked against their hashes
	Bytes     int64 // their size
	Problems  []string
}

func (v VerifyReport) OK() bool { return len(v.Problems) == 0 }

// Verify reads every blob of the repo back, through its deltas, against its
// hash; checks that each snapshot's blobs are there; and opens each
// snapshot's sliqtly.db: SQLite's integrity check, and its file_refs equal
// to the manifest's files. deep false checks only the newest snapshot's
// sliqtly.db.
func (r *BackupRepo) Verify(ctx context.Context, deep bool) (VerifyReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rep VerifyReport
	snaps, err := r.Snapshots()
	if err != nil {
		return rep, err
	}
	rep.Snapshots = len(snaps)
	err = r.blobs.Each(ctx, func(info BlobInfo) error {
		b, err := r.blobs.bytesOrStream(ctx, info)
		if err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("blob %s: %v", info.Hash, err))
			return nil
		}
		rep.Blobs++
		rep.Bytes += b
		return nil
	})
	if err != nil {
		return rep, err
	}
	for i, s := range snaps {
		for _, h := range s.hashes() {
			p, err := ParseHash(h)
			if err != nil {
				rep.Problems = append(rep.Problems, fmt.Sprintf("backup %s: %v", s.ID, err))
				continue
			}
			if _, err := r.blobs.Stat(ctx, p); err != nil {
				rep.Problems = append(rep.Problems, fmt.Sprintf("backup %s: blob %s: %v", s.ID, h, err))
			}
		}
		if !deep && i != len(snaps)-1 {
			continue
		}
		if err := r.checkDocs(ctx, s); err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("backup %s: %v", s.ID, err))
		}
	}
	return rep, nil
}

// bytesOrStream checks a blob against its hash → its size
func (s *SQLiteBlobStore) bytesOrStream(ctx context.Context, info BlobInfo) (int64, error) {
	_, bad, err := verifyOne(ctx, s, info)
	if err != nil {
		return 0, err
	}
	if bad {
		return 0, errors.New("its bytes do not match its hash")
	}
	return info.Size, nil
}

func verifyOne(ctx context.Context, b BlobStore, info BlobInfo) (int64, bool, error) {
	rd, err := b.Open(ctx, info.Hash)
	if err != nil {
		return 0, false, err
	}
	defer rd.Close()
	h := newHasher()
	n, err := io.Copy(h, rd)
	if err != nil {
		return n, false, err
	}
	return n, h.sum() != info.Hash || n != info.Size, nil
}

// checkDocs writes the snapshot's sliqtly.db to a temporary file and checks it
func (r *BackupRepo) checkDocs(ctx context.Context, s Snapshot) error {
	tmpDir := filepath.Join(r.dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(tmpDir, "verify-"+s.ID+".db")
	defer os.Remove(path)
	if err := r.writeBlob(ctx, s.Docs.Hash, path); err != nil {
		return fmt.Errorf("sliqtly.db: %w", err)
	}
	return checkDocsFile(ctx, path, s.Files)
}

// checkDocsFile: SQLite's integrity check of the sliqtly.db at path, and
// its file_refs equal to files
func checkDocsFile(ctx context.Context, path string, files []BackupFile) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var ok string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ok); err != nil {
		return fmt.Errorf("sliqtly.db: %w", err)
	}
	if ok != "ok" {
		return fmt.Errorf("sliqtly.db integrity check: %s", ok)
	}
	got, err := readFileRefs(ctx, path)
	if err != nil {
		return err
	}
	if len(got) != len(files) {
		return fmt.Errorf("sliqtly.db names %d files, the manifest %d", len(got), len(files))
	}
	for i := range got {
		if got[i].Path != files[i].Path || got[i].Hash != files[i].Hash {
			return fmt.Errorf("sliqtly.db and the manifest differ at %s", got[i].Path)
		}
	}
	return nil
}

// writeBlob writes blob h of the repo to a new file at path, synced
func (r *BackupRepo) writeBlob(ctx context.Context, hash, path string) error {
	h, err := ParseHash(hash)
	if err != nil {
		return err
	}
	rd, err := r.blobs.Open(ctx, h)
	if err != nil {
		return err
	}
	defer rd.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	hs := newHasher()
	_, err = io.Copy(io.MultiWriter(f, hs), rd)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hs.sum() != h {
		err = fmt.Errorf("blob %s read back as other bytes", h)
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// RestoreReport says what Restore wrote
type RestoreReport struct {
	Snapshot string
	Files    int   // file references in sliqtly.db
	Blobs    int   // blobs written to blobs.db
	Bytes    int64 // their size
}

// Restore writes snapshot id ("latest" for the newest) as a data folder at
// to, which must not exist or be empty: sliqtly.db, blobs.db with every
// file it names, and the extras (format.json). Then it checks what it
// wrote: SQLite's integrity check, every file reference's blob present, and
// every blob read back against its hash. On any failure the folder is
// removed again. A server started with -data on it serves the backup.
func (r *BackupRepo) Restore(ctx context.Context, id, to string) (RestoreReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rep RestoreReport
	s, err := r.Snapshot(id)
	if err != nil {
		return rep, err
	}
	rep.Snapshot = s.ID
	abs, err := filepath.Abs(to)
	if err != nil {
		return rep, err
	}
	if ents, err := os.ReadDir(abs); err == nil && len(ents) > 0 {
		return rep, fmt.Errorf("%s is not empty: restore into a new folder", abs)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return rep, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(abs)
		}
	}()
	for _, e := range s.Extra {
		if e.Name == "" || strings.ContainsAny(e.Name, `/\`) || strings.HasPrefix(e.Name, ".") {
			return rep, fmt.Errorf("bad file name %q in backup %s", e.Name, s.ID)
		}
		if err := r.writeBlob(ctx, e.Hash, filepath.Join(abs, e.Name)); err != nil {
			return rep, fmt.Errorf("%s: %w", e.Name, err)
		}
	}
	docs := filepath.Join(abs, "sliqtly.db")
	if err := r.writeBlob(ctx, s.Docs.Hash, docs); err != nil {
		return rep, fmt.Errorf("sliqtly.db: %w", err)
	}
	if err := checkDocsFile(ctx, docs, s.Files); err != nil {
		return rep, err
	}
	rep.Files = len(s.Files)
	blobs, err := OpenSQLiteBlobStore(filepath.Join(abs, "blobs.db"))
	if err != nil {
		return rep, err
	}
	defer blobs.Close()
	seen := map[string]bool{}
	for _, f := range s.Files {
		if seen[f.Hash] {
			continue
		}
		seen[f.Hash] = true
		h, err := ParseHash(f.Hash)
		if err != nil {
			return rep, err
		}
		rd, err := r.blobs.Open(ctx, h)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", f.Path, err)
		}
		info, err := blobs.Put(ctx, rd, f.Mime)
		rd.Close()
		if err != nil {
			return rep, fmt.Errorf("%s: %w", f.Path, err)
		}
		if info.Hash != h {
			return rep, fmt.Errorf("%s: backup gave other bytes than its hash", f.Path)
		}
		rep.Blobs++
		rep.Bytes += info.Size
	}
	// read back what was written
	for _, f := range s.Files {
		h, _ := ParseHash(f.Hash)
		if _, err := blobs.Stat(ctx, h); err != nil {
			return rep, fmt.Errorf("%s after restore: %w", f.Path, err)
		}
	}
	if _, bad, err := VerifyBlobs(ctx, blobs); err != nil || len(bad) > 0 {
		if err == nil {
			err = fmt.Errorf("%d blobs do not match their hashes", len(bad))
		}
		return rep, fmt.Errorf("blobs.db after restore: %w", err)
	}
	ok = true
	return rep, nil
}

type hasher struct{ hash.Hash }

func newHasher() hasher { return hasher{sha256.New()} }

func (h hasher) sum() Hash {
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}
