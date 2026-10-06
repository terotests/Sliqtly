// SPDX-License-Identifier: AGPL-3.0-or-later

// Backups of the data folder (store.BackupRepo): incremental snapshots of
// sliqtly.db and the kept files into a folder of their own, best on
// another disk.
//
// The server takes one when SLIQTLY_BACKUP names that folder: at start when
// the last is older than SLIQTLY_BACKUP_EVERY (24h), then every
// SLIQTLY_BACKUP_EVERY, and prunes by SLIQTLY_BACKUP_KEEP after each. The
// same, and checking and restoring, from the command line:
//
//	sliqtly-server backup run     [-data D] [-repo R] [-keep K]
//	sliqtly-server backup list    [-repo R]
//	sliqtly-server backup verify  [-repo R] [-deep]
//	sliqtly-server backup restore [-repo R] [-id latest] -into NEW
//	sliqtly-server backup prune   [-repo R] [-keep K]
//
// `run` reads the data folder only, so it may run beside the server. A
// restore writes a new data folder and checks it; a server started with
// -data on it serves the backup.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the server's backups (SLIQTLY_BACKUP…)
type backupConfig struct {
	Repo  string
	Every time.Duration
	Keep  store.BackupKeep
}

func backupFromEnv() (backupConfig, error) {
	c := backupConfig{Repo: os.Getenv("SLIQTLY_BACKUP"), Every: 24 * time.Hour, Keep: store.DefaultBackupKeep}
	if v := os.Getenv("SLIQTLY_BACKUP_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return c, fmt.Errorf("SLIQTLY_BACKUP_EVERY=%q: a duration of at least 1m, e.g. 24h", v)
		}
		c.Every = d
	}
	if v := os.Getenv("SLIQTLY_BACKUP_KEEP"); v != "" {
		k, err := parseBackupKeep(v)
		if err != nil {
			return c, fmt.Errorf("SLIQTLY_BACKUP_KEEP: %w", err)
		}
		c.Keep = k
	}
	return c, nil
}

// "last=3,daily=14,weekly=8"; "all" keeps every snapshot
func parseBackupKeep(s string) (store.BackupKeep, error) {
	var k store.BackupKeep
	if strings.TrimSpace(s) == "all" {
		return k, nil
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		name, val, ok := strings.Cut(part, "=")
		n, err := strconv.Atoi(val)
		if !ok || err != nil || n < 0 {
			return k, fmt.Errorf("%q: write last=N,daily=N,weekly=N or all", s)
		}
		switch name {
		case "last":
			k.Last = n
		case "daily":
			k.Daily = n
		case "weekly":
			k.Weekly = n
		default:
			return k, fmt.Errorf("%q: write last=N,daily=N,weekly=N or all", s)
		}
	}
	if k.Last == 0 && k.Daily == 0 && k.Weekly == 0 {
		return k, fmt.Errorf("%q keeps nothing; write all to keep every backup", s)
	}
	return k, nil
}

// a repo folder must not be in the data folder, nor the data folder in it
func checkBackupPlace(data, repo string) error {
	d, err1 := filepath.Abs(data)
	r, err2 := filepath.Abs(repo)
	if err1 != nil || err2 != nil {
		return errors.Join(err1, err2)
	}
	inside := func(a, b string) bool {
		rel, err := filepath.Rel(b, a)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if inside(r, d) || inside(d, r) {
		return fmt.Errorf("the backup folder %s and the data folder %s must be apart", r, d)
	}
	return nil
}

// openBackupRepo locks the repo folder for this process and opens it
func openBackupRepo(dir string) (*store.BackupRepo, func(), error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, nil, err
	}
	unlock, err := lockFolder(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, nil, fmt.Errorf("%s is being written by another backup (%v)", dir, err)
	}
	repo, err := store.OpenBackupRepo(dir)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return repo, func() { repo.Close(); sameOwner(dir); unlock() }, nil
}

// backupSource is the data folder's files to back up: format.json beside
// the two databases
func backupSource(data string, b *localBucket) store.BackupSource {
	src := store.BackupSource{Docs: b.docs, Blobs: b.blobs, Server: version, Extra: map[string][]byte{}}
	if f, err := os.ReadFile(filepath.Join(data, "format.json")); err == nil {
		src.Extra["format.json"] = f
	}
	return src
}

// takeBackup takes a snapshot of src into repo and prunes it. A file
// collected while it was being copied makes it take the snapshot again.
func takeBackup(ctx context.Context, repo *store.BackupRepo, src store.BackupSource, keep store.BackupKeep) (store.Snapshot, store.PruneReport, error) {
	snap, err := repo.Take(ctx, src)
	if errors.Is(err, store.ErrBackupSourceChanged) {
		snap, err = repo.Take(ctx, src)
	}
	if err != nil {
		return snap, store.PruneReport{}, err
	}
	pr, err := repo.Prune(ctx, keep)
	return snap, pr, err
}

func describeSnapshot(s store.Snapshot) string {
	return fmt.Sprintf("backup %s: %d files, %s new in %d blobs, took %s", s.ID, len(s.Files), sizeText(s.New.Bytes), s.New.Blobs, time.Duration(s.Took).Round(time.Millisecond))
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// backupLoop takes the server's backups until ctx ends
func (s *localServer) backupLoop(ctx context.Context, data string, c backupConfig) {
	check := min(c.Every, time.Hour)
	for {
		if s.backupDue(c) {
			s.backupNow(ctx, data, c)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(check):
		}
	}
}

// due when the newest snapshot is older than Every (or there is none)
func (s *localServer) backupDue(c backupConfig) bool {
	ents, err := os.ReadDir(filepath.Join(c.Repo, "snapshots"))
	if err != nil {
		return true
	}
	var newest time.Time
	for _, e := range ents {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".json") && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return time.Since(newest) >= c.Every
}

func (s *localServer) backupNow(ctx context.Context, data string, c backupConfig) (store.Snapshot, error) {
	repo, done, err := openBackupRepo(c.Repo)
	if err != nil {
		log.Printf("backup: %v", err)
		return store.Snapshot{}, err
	}
	defer done()
	// what the rooms took is written first, so the backup has it
	s.flushRooms()
	snap, pr, err := takeBackup(ctx, repo, backupSource(data, s.bucket), c.Keep)
	if err != nil {
		log.Printf("backup into %s: %v", c.Repo, err)
		return snap, err
	}
	msg := describeSnapshot(snap)
	if len(pr.Snapshots) > 0 {
		msg += fmt.Sprintf("; removed %d older backups", len(pr.Snapshots))
	}
	log.Print(msg)
	return snap, nil
}

// ------------------------------------------------------------- command --

// backupCmd is `sliqtly-server backup …` → the exit code
func backupCmd(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: sliqtly-server backup run|list|verify|restore|prune [flags]")
		return 2
	}
	fs := flag.NewFlagSet("backup "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	repoDir := fs.String("repo", os.Getenv("SLIQTLY_BACKUP"), "the backup folder (SLIQTLY_BACKUP)")
	data := fs.String("data", os.Getenv("SLIQTLY_DATA"), "the data folder (SLIQTLY_DATA), for run")
	keepText := fs.String("keep", env("SLIQTLY_BACKUP_KEEP", "last=3,daily=14,weekly=8"), "which backups to keep: last=N,daily=N,weekly=N or all (SLIQTLY_BACKUP_KEEP)")
	deep := fs.Bool("deep", false, "verify: open every backup's sliqtly.db, not only the newest")
	id := fs.String("id", "latest", "restore: the backup to restore")
	into := fs.String("into", "", "restore: a new folder to write the data folder to")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(errOut, "backup:", err)
		return 1
	}
	if *repoDir == "" {
		return fail(errors.New("no backup folder: give -repo or set SLIQTLY_BACKUP"))
	}
	keep, err := parseBackupKeep(*keepText)
	if err != nil {
		return fail(err)
	}
	ctx := context.Background()
	switch args[0] {
	case "run":
		if *data == "" {
			return fail(errors.New("no data folder: give -data or set SLIQTLY_DATA"))
		}
		if err := checkBackupPlace(*data, *repoDir); err != nil {
			return fail(err)
		}
		if have, err := dataFormat(*data); err != nil || have != currentFormat {
			if err == nil {
				err = fmt.Errorf("%s is in data format %d, this build backs up %d: start the server on it once", *data, have, currentFormat)
			}
			return fail(err)
		}
		docs, err := store.OpenSQLiteReadOnly(filepath.Join(*data, docsFile))
		if err != nil {
			return fail(err)
		}
		defer docs.Close()
		blobs, err := store.OpenSQLiteBlobStoreReadOnly(filepath.Join(*data, blobsFile))
		if err != nil {
			return fail(err)
		}
		defer blobs.Close()
		repo, done, err := openBackupRepo(*repoDir)
		if err != nil {
			return fail(err)
		}
		defer done()
		snap, pr, err := takeBackup(ctx, repo, backupSource(*data, &localBucket{docs: docs, blobs: blobs}), keep)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(out, describeSnapshot(snap))
		if len(pr.Snapshots) > 0 {
			fmt.Fprintf(out, "removed %d older backups (%s), %d blobs\n", len(pr.Snapshots), strings.Join(pr.Snapshots, ", "), pr.Blobs)
		}
	case "list":
		repo, done, err := openBackupRepo(*repoDir)
		if err != nil {
			return fail(err)
		}
		defer done()
		snaps, err := repo.Snapshots()
		if err != nil {
			return fail(err)
		}
		for _, s := range snaps {
			var size int64
			for _, f := range s.Files {
				size += f.Size
			}
			fmt.Fprintf(out, "%s  %s  %5d files  %9s  sliqtly.db %s  +%s\n", s.ID, s.Time.Local().Format("2006-01-02 15:04"), len(s.Files), sizeText(size), sizeText(s.Docs.Size), sizeText(s.New.Bytes))
		}
		u, err := repo.Blobs().Usage(ctx)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "%d backups; %d blobs of %s kept in %s (%d as deltas)\n", len(snaps), u.Blobs, sizeText(u.Size), sizeText(u.Kept), u.Deltas)
	case "verify":
		repo, done, err := openBackupRepo(*repoDir)
		if err != nil {
			return fail(err)
		}
		defer done()
		rep, err := repo.Verify(ctx, *deep)
		if err != nil {
			return fail(err)
		}
		for _, p := range rep.Problems {
			fmt.Fprintln(out, "PROBLEM", p)
		}
		fmt.Fprintf(out, "%d backups, %d blobs (%s) read back and checked\n", rep.Snapshots, rep.Blobs, sizeText(rep.Bytes))
		if !rep.OK() {
			return fail(fmt.Errorf("%d problems", len(rep.Problems)))
		}
		fmt.Fprintln(out, "ok")
	case "restore":
		if *into == "" {
			return fail(errors.New("give -into, a new folder to restore into"))
		}
		repo, done, err := openBackupRepo(*repoDir)
		if err != nil {
			return fail(err)
		}
		defer done()
		rep, err := repo.Restore(ctx, *id, *into)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "restored backup %s into %s: %d files, %d blobs (%s), checked\n", rep.Snapshot, *into, rep.Files, rep.Blobs, sizeText(rep.Bytes))
		fmt.Fprintf(out, "serve it with: sliqtly-server -data %s\n", *into)
	case "prune":
		repo, done, err := openBackupRepo(*repoDir)
		if err != nil {
			return fail(err)
		}
		defer done()
		pr, err := repo.Prune(ctx, keep)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "removed %d backups, %d blobs\n", len(pr.Snapshots), pr.Blobs)
	default:
		fmt.Fprintf(errOut, "backup: no command %q (run, list, verify, restore, prune)\n", args[0])
		return 2
	}
	return 0
}
