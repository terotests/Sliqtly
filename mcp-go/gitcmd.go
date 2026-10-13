// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// gitCmd is `sliqtly-server git …` (gitstore.go) → the exit code
//
//	init    turns Git on: git.json, the repository, the first commit,
//	        checked against the folder (the server stopped)
//	commit  commits what changed now (the server stopped)
//	log     the newest commits
//	verify  reads every object of the head back and compares it with the
//	        folder
//	off     turns Git off; the repository stays
func gitCmd(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: sliqtly-server git init|commit|log|verify|off [flags]")
		return 2
	}
	fs := flag.NewFlagSet("git "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	data := fs.String("data", os.Getenv("SLIQTLY_DATA"), "the data folder (SLIQTLY_DATA)")
	maxMB := fs.Int("max-file-mb", 0, "init: the largest file kept, in MB (default 20, at most 100)")
	author := fs.String("author", "", "init: whose name commits carry: editor (who edited) or server")
	msg := fs.String("m", "", "commit: the message (default: what changed)")
	n := fs.Int("n", 20, "log: how many commits")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(errOut, "git:", err)
		return 1
	}
	if *data == "" {
		return fail(errors.New("no data folder: give -data or set SLIQTLY_DATA"))
	}
	if have, err := dataFormat(*data); err != nil || have != currentFormat {
		if err == nil {
			err = fmt.Errorf("%s is in data format %d, this build reads %d: start the server on it once", *data, have, currentFormat)
		}
		return fail(err)
	}
	cfg, had, err := readGitConfig(*data)
	if err != nil {
		return fail(err)
	}
	ctx := context.Background()
	// init and commit write the repository: only with the folder to themselves
	writes := args[0] == "init" || args[0] == "commit" || args[0] == "off"
	if writes {
		abs, err := filepath.Abs(*data)
		if err != nil {
			return fail(err)
		}
		unlock, err := lockFolder(filepath.Join(abs, ".lock"))
		if err != nil {
			return fail(fmt.Errorf("%s is in use by a server: stop it first (%v)", abs, err))
		}
		defer unlock()
	}
	switch args[0] {
	case "init", "commit", "log", "verify":
	case "off":
		if !had || !cfg.Enabled {
			fmt.Fprintln(out, "Git is off already")
			return 0
		}
		cfg.Enabled = false
		if err := writeGitConfig(*data, cfg); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "Git is off; %s stays as it is\n", filepath.Join(*data, gitRepoDir))
		return 0
	default:
		fmt.Fprintf(errOut, "git: no command %q\n", args[0])
		return 2
	}
	if args[0] == "init" {
		if *maxMB != 0 {
			cfg.MaxFileMB = *maxMB
		}
		if *author != "" {
			cfg.Author = *author
		}
		cfg.Enabled = true
		if err := cfg.check(); err != nil {
			return fail(err)
		}
	} else if _, err := os.Stat(filepath.Join(*data, gitRepoDir, "HEAD")); err != nil {
		return fail(fmt.Errorf("no repository in %s: sliqtly-server git init", *data))
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
	m, err := openGitMirror(*data, cfg, docs, blobs)
	if err != nil {
		return fail(err)
	}
	m.logf = func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) }
	switch args[0] {
	case "init":
		// a dry run first: what would go in, and what is too big for it
		s, err := m.snapshot(ctx, false)
		if err != nil {
			return fail(err)
		}
		if len(s.TooBig) > 0 {
			fmt.Fprintf(errOut, "over %d MB, not kept in Git:\n  %s\n", cfg.MaxFileMB, strings.Join(s.TooBig, "\n  "))
			return fail(fmt.Errorf("%d files are too big: remove them or raise the limit (-max-file-mb, at most %d)", len(s.TooBig), gitMaxFileCeiling))
		}
		head, err := m.Head()
		if err != nil {
			return fail(err)
		}
		if head == "" {
			if err := m.start(ctx); err != nil {
				return fail(err)
			}
		} else {
			fmt.Fprintf(out, "%s has commits already (%s); Git is on again\n", m.repo.Dir, short7(head))
		}
		if err := writeGitConfig(*data, cfg); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "%d decks, %d rooms, %d files; the server commits to %s from its next start\n", len(s.Decks), len(s.Rooms), len(s.Files), cfg.Branch)
	case "commit":
		id, err := m.commit(ctx, *msg)
		if err != nil {
			return fail(err)
		}
		if id == "" {
			fmt.Fprintln(out, "nothing changed")
		} else {
			fmt.Fprintln(out, "committed", short7(id))
		}
	case "log":
		head, err := m.Head()
		if err != nil {
			return fail(err)
		}
		cs, err := m.repo.Log(head, *n)
		if err != nil {
			return fail(err)
		}
		for _, c := range cs {
			subject, _, _ := strings.Cut(c.Message, "\n")
			fmt.Fprintf(out, "%s  %s  %-20s  %s\n", short7(c.ID), c.Committer.When.Local().Format("2006-01-02 15:04"), c.Author.Name, subject)
		}
	case "verify":
		diff, err := m.verify(ctx)
		if err != nil {
			return fail(err)
		}
		for _, d := range diff {
			fmt.Fprintln(out, d)
		}
		if len(diff) > 0 {
			fmt.Fprintf(out, "%d paths differ from the folder: not committed yet (sliqtly-server git commit)\n", len(diff))
			return 1
		}
		fmt.Fprintln(out, "ok: the head is the folder as it is")
	}
	return 0
}
