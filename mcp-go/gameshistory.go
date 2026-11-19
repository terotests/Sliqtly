// SPDX-License-Identifier: AGPL-3.0-or-later

// Games: earlier versions of a game's text files (code, CSS, JSON), kept
// for a week, so a file another agent or a slip wrote over can be found
// and put back.
//
//	games_history/{id}  entries [{path, sha, size, at, op}]: the version a
//	                    write replaced or a delete took away ("write",
//	                    "delete"), newest last; the bytes stay in the
//	                    game's blobs (games/{id}/blobs/{sha}) while an
//	                    entry names them
//
// change() records the versions it replaces; versions older than
// gamesHistoryDays go, and the oldest go first past gamesHistoryMaxEntries
// or gamesHistoryMaxBytes. Pictures, sounds and models are not kept, nor a
// text file larger than gamesHistoryMaxFile.

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	gamesHistoryCol        = "games_history"
	gamesHistoryDays       = 7
	gamesHistoryMaxFile    = 512 << 10 // a version larger than this is not kept
	gamesHistoryMaxBytes   = 20 << 20  // a game's kept versions together
	gamesHistoryMaxEntries = 400
)

type gamesVersion struct {
	Path string
	Sha  string
	Size int64
	At   int64 // ms
	Op   string
}

// a version worth keeping: a text file not too large
func gamesKeepsVersion(f gamesFile) bool {
	return gamesTypes[gamesExt(f.Path)].text && f.Size <= gamesHistoryMaxFile
}

func (s *gamesService) history(ctx context.Context, id string) ([]gamesVersion, error) {
	d, err := s.db.Get(ctx, gamesHistoryCol, id)
	if err != nil || d == nil {
		return nil, err
	}
	l, _ := d["entries"].([]any)
	out := []gamesVersion{}
	for _, x := range l {
		m, _ := x.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, gamesVersion{docStr(m, "path"), docStr(m, "sha"), docInt(m["size"]), docInt(m["at"]), docStr(m, "op")})
	}
	return out, nil
}

// adds the versions a change replaced, drops the old ones, and says which
// blobs the kept versions still need and which dropped ones may go
func (s *gamesService) recordHistory(ctx context.Context, id string, add []gamesVersion) (keep map[string]bool, dropped []string) {
	keep = map[string]bool{}
	l, err := s.history(ctx, id)
	if err != nil {
		// unread history: keep every blob it might name
		return nil, nil
	}
	l = append(l, add...)
	cut := s.now().Add(-gamesHistoryDays * 24 * time.Hour).UnixMilli()
	var total int64
	kept := []gamesVersion{}
	// newest first, so the limits drop the oldest
	for i := len(l) - 1; i >= 0; i-- {
		v := l[i]
		if v.At < cut || len(kept) >= gamesHistoryMaxEntries || total+v.Size > gamesHistoryMaxBytes {
			dropped = append(dropped, v.Sha)
			continue
		}
		total += v.Size
		kept = append(kept, v)
	}
	slices.Reverse(kept)
	if len(add) == 0 && len(dropped) == 0 {
		for _, v := range kept {
			keep[v.Sha] = true
		}
		return keep, nil
	}
	docs := []any{}
	for _, v := range kept {
		docs = append(docs, map[string]any{"path": v.Path, "sha": v.Sha, "size": v.Size, "at": v.At, "op": v.Op})
		keep[v.Sha] = true
	}
	if err := s.db.Set(ctx, gamesHistoryCol, id, Doc{"entries": docs}); err != nil {
		// not saved: the added versions' blobs stay (their entries may
		// still be read later from a retry), nothing is dropped
		for _, v := range add {
			keep[v.Sha] = true
		}
		return keep, nil
	}
	return keep, dropped
}

func gamesVersionID(v gamesVersion) string {
	return fmt.Sprintf("%d-%s", v.At, v.Sha[:min(8, len(v.Sha))])
}

// file_history: the kept versions (newest first), of one path or all; with
// version, that version's text
func (s *gamesService) fileHistory(ctx context.Context, g *gamesGame, a map[string]any) (any, error) {
	l, err := s.history(ctx, g.id)
	if err != nil {
		return nil, err
	}
	p, _ := a["path"].(string)
	if p != "" {
		if p, err = gamesCheckPath(p); err != nil {
			return nil, err
		}
	}
	if ver, _ := a["version"].(string); ver != "" {
		v, err := gamesFindVersion(l, p, ver)
		if err != nil {
			return nil, err
		}
		b, err := s.bucket.Read(ctx, s.blobPath(g.id, v.Sha), gamesHistoryMaxFile)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"path": v.Path, "version": gamesVersionID(v), "size": len(b), "saved": time.UnixMilli(v.At).UTC().Format(time.RFC3339)}
		if utf8.Valid(b) {
			out["text"] = string(b)
		}
		return out, nil
	}
	vs := []any{}
	for i := len(l) - 1; i >= 0; i-- {
		v := l[i]
		if p != "" && v.Path != p {
			continue
		}
		what := "replaced by a write"
		if v.Op == "delete" {
			what = "deleted"
		}
		vs = append(vs, map[string]any{"path": v.Path, "version": gamesVersionID(v), "size": v.Size,
			"until": time.UnixMilli(v.At).UTC().Format(time.RFC3339), "then": what})
	}
	return map[string]any{"game_id": g.id, "versions": vs,
		"note": fmt.Sprintf("Earlier versions of text files, kept %d days. 'until' is when the version was replaced or deleted. restore_file puts one back.", gamesHistoryDays)}, nil
}

func gamesFindVersion(l []gamesVersion, p, ver string) (gamesVersion, error) {
	for i := len(l) - 1; i >= 0; i-- {
		if gamesVersionID(l[i]) == ver && (p == "" || l[i].Path == p) {
			return l[i], nil
		}
	}
	return gamesVersion{}, roomErr{"No such version: file_history lists them."}
}

// restore_file: a kept version written back (to its own path, or to_path);
// the file it replaces is kept in turn
func (s *gamesService) restoreFile(ctx context.Context, g *gamesGame, a map[string]any) (any, error) {
	l, err := s.history(ctx, g.id)
	if err != nil {
		return nil, err
	}
	ver, _ := a["version"].(string)
	p, _ := a["path"].(string)
	if p != "" {
		if p, err = gamesCheckPath(p); err != nil {
			return nil, err
		}
	}
	v, err := gamesFindVersion(l, p, strings.TrimSpace(ver))
	if err != nil {
		return nil, err
	}
	to := v.Path
	if t, _ := a["to_path"].(string); t != "" {
		if to, err = gamesCheckPath(t); err != nil {
			return nil, err
		}
	}
	b, err := s.bucket.Read(ctx, s.blobPath(g.id, v.Sha), gamesHistoryMaxFile)
	if err != nil {
		return nil, err
	}
	out, err := s.change(ctx, g, []gamesWrite{{path: to, data: b}}, nil)
	if err != nil {
		return nil, err
	}
	out["restored"] = map[string]any{"path": to, "version": gamesVersionID(v)}
	return out, nil
}
