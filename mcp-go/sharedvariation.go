// SPDX-License-Identifier: AGPL-3.0-or-later

// Variations of a shared Figma file (sharedfiles.go): a changed copy that
// keeps only what changed. The original stays as it came; a variation is
//
//	shared/{id}/variation.json   the change: copies, field values by node,
//	                             removed nodes (DesignViewer's FigVariation,
//	                             "fig-delta/1")
//	sharedmeta/{id}.json         its card: kind "figma-variation", base =
//	                             the original's id, changes = a summary
//
// and nothing else is kept for it but what every shared file has: the
// index, the cuts and the drawn pictures, made by reading the file the
// variation makes (the original with the change applied, built in memory
// and dropped). So a variation is searched, looked at and put on slides
// (figma:<variation>/<node>) as any shared file is, and its .fig is built
// again whenever it is exported.
//
// One view of a file (a screen, or a part of one) can also be taken out as
// a shared file of its own (extract), with the components and styles it
// uses; that one is a whole .fig, not a variation.
//
// A variation of a variation is another variation of the same original:
// its change is copied, so every variation stays one step from the file it
// was made from.

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/figdv"
)

const variationKind = "figma-variation"

func sharedVariationPath(id string) string { return sharedDir(id) + "/variation.json" }

// the original a file is, or is a variation of: (its card, the variation's
// JSON or "" for the original itself)
func (s *sharedFiles) origin(ctx context.Context, f sharedFile) (sharedFile, string, error) {
	if f.Kind != variationKind {
		return f, "", nil
	}
	base, err := s.card(ctx, f.Base)
	if err != nil {
		return f, "", sharedErr{409, "the original of " + f.Name + " (" + f.Base + ") is gone"}
	}
	b, err := s.files.Read(ctx, sharedVariationPath(f.ID), 64<<20)
	if err != nil {
		return f, "", err
	}
	return base, string(b), nil
}

// the original's bytes, refused past the size this server edits in memory
func (s *sharedFiles) originBytes(ctx context.Context, base sharedFile) ([]byte, error) {
	if s.editMax > 0 && base.Bytes > s.editMax {
		return nil, sharedErr{413, fmt.Sprintf("%s is too large to change here (at most %d MB)", base.Name, s.editMax>>20)}
	}
	return s.files.Read(ctx, sharedDir(base.ID)+"/source.fig", s.max+1)
}

// sourceOf is the .fig file f is: the file as it came, or the one its
// variation makes
func (s *sharedFiles) sourceOf(ctx context.Context, f sharedFile) ([]byte, error) {
	if f.Kind != variationKind {
		return s.files.Read(ctx, sharedDir(f.ID)+"/source.fig", s.max+1)
	}
	base, delta, err := s.origin(ctx, f)
	if err != nil {
		return nil, err
	}
	data, err := s.files.Read(ctx, sharedDir(base.ID)+"/source.fig", s.max+1)
	if err != nil {
		return nil, err
	}
	var out []byte
	err = figdvCall(func() {
		v := figdv.FigGoEdit_static_file(data, base.Name, delta)
		if !figdv.FigGoEdit_static_ok(v) {
			err := figdv.FigGoEdit_static_err(v)
			panic(err)
		}
		out = figdv.FigGoEdit_static_bytes(v)
	})
	return out, err
}

// figdvCall runs generated code: a file it cannot handle fails the call,
// not the server
func figdvCall(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = sharedErr{422, fmt.Sprintf("the file could not be changed (%v)", r)}
		}
	}()
	f()
	return nil
}

// a free id for a new file named `name`: its slug, or the slug with -2, -3…
func (s *sharedFiles) freeID(ctx context.Context, name string) string {
	base := sharedSlug(name)
	if len(base) > 40 {
		base = base[:40]
	}
	id := base
	for n := 2; ; n++ {
		if _, err := s.card(ctx, id); err != nil {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// vary makes a variation of file `of` called `name` ("" for "<original>
// variation"): empty, or a copy of `of`'s change when `of` is a variation
func (s *sharedFiles) vary(ctx context.Context, of, name string) (sharedFile, error) {
	src, err := s.card(ctx, of)
	if err != nil {
		return sharedFile{}, err
	}
	base, delta, err := s.origin(ctx, src)
	if err != nil {
		return sharedFile{}, err
	}
	if base.Status == "failed" {
		return sharedFile{}, sharedErr{422, base.Name + " could not be read, so it cannot be varied"}
	}
	if delta == "" {
		delta = `{"format":"fig-delta/1"}`
	}
	name = strings.TrimSpace(roomFileName(name))
	if name == "" {
		name = strings.TrimSuffix(src.Name, ".fig") + " variation"
	}
	if !strings.EqualFold(pathExt(name), ".fig") {
		name += ".fig"
	}
	s.mu.Lock()
	id := s.freeID(ctx, name)
	if err := s.files.Save(ctx, sharedVariationPath(id), "application/json", []byte(delta), nil); err != nil {
		s.mu.Unlock()
		return sharedFile{}, err
	}
	f := sharedFile{ID: id, Name: name, Kind: variationKind, Base: base.ID, Bytes: int64(len(delta)), Sha: variationSum(base, delta), Status: "indexing", Changes: src.Changes}
	if f.Changes == "" {
		f.Changes = "same as the original"
	}
	f.Added = time.Now().UnixMilli()
	err = s.putCard(ctx, f)
	s.mu.Unlock()
	if err != nil {
		return sharedFile{}, err
	}
	s.queue(id)
	return f, nil
}

// a variation's sum: its change and the original it changes
func variationSum(base sharedFile, delta string) string {
	h := sha256.Sum256([]byte(base.Sha + "\n" + delta))
	return fmt.Sprintf("%x", h[:])
}

// what an edit did, as the tool answers it
type variationEdit struct {
	File    sharedFile `json:"file"`
	Changes string     `json:"changes"`
	Created []string   `json:"created,omitempty"`
	Refused []string   `json:"refused,omitempty"`
	// nodes the variation names that the original no longer has
	Stale []string `json:"stale,omitempty"`
}

// edit makes `ops` (a JSON list) to variation `id`, keeps the new change
// and reads the file again
func (s *sharedFiles) edit(ctx context.Context, id, ops string) (variationEdit, error) {
	var out variationEdit
	f, err := s.card(ctx, id)
	if err != nil {
		return out, err
	}
	if f.Kind != variationKind {
		return out, sharedErr{400, f.Name + " is an original: make a variation of it first (create_figma_variation), so the original stays as it is"}
	}
	base, delta, err := s.origin(ctx, f)
	if err != nil {
		return out, err
	}
	data, err := s.originBytes(ctx, base)
	if err != nil {
		return out, err
	}
	var v *figdv.FigGoVary
	if err := figdvCall(func() { v = figdv.FigGoEdit_static_edit(data, base.Name, delta, ops) }); err != nil {
		return out, err
	}
	if !figdv.FigGoEdit_static_ok(v) {
		return out, sharedErr{400, figdv.FigGoEdit_static_err(v)}
	}
	out.Refused = figdv.FigGoEdit_static_errors(v)
	out.Created = figdv.FigGoEdit_static_created(v)
	out.Changes = figdv.FigGoEdit_static_summary(v)
	next := figdv.FigGoEdit_static_text(v)
	if next != delta {
		s.mu.Lock()
		err := s.files.Save(ctx, sharedVariationPath(id), "application/json", []byte(next), nil)
		if err == nil {
			f.Bytes, f.Sha, f.Changes, f.Status, f.Error = int64(len(next)), variationSum(base, next), out.Changes, "indexing", ""
			err = s.putCard(ctx, f)
		}
		s.mu.Unlock()
		if err != nil {
			return out, err
		}
		s.queue(id)
	}
	out.File = f
	return out, nil
}

// layers is the layer tree under `node` of file `id` as JSON (the pages
// for ""), `depth` levels; with raw, that node's own fields
func (s *sharedFiles) layers(ctx context.Context, id, node string, depth int, raw bool) (string, error) {
	f, err := s.card(ctx, id)
	if err != nil {
		return "", err
	}
	base, delta, err := s.origin(ctx, f)
	if err != nil {
		return "", err
	}
	data, err := s.originBytes(ctx, base)
	if err != nil {
		return "", err
	}
	var v *figdv.FigGoVary
	if err := figdvCall(func() { v = figdv.FigGoEdit_static_layers(data, base.Name, delta, node, int64(depth), raw) }); err != nil {
		return "", err
	}
	if !figdv.FigGoEdit_static_ok(v) {
		return "", sharedErr{404, figdv.FigGoEdit_static_err(v)}
	}
	return figdv.FigGoEdit_static_text(v), nil
}

// extract puts layer `node` of file `id` in the shared files as a .fig of
// its own called `name`
func (s *sharedFiles) extract(ctx context.Context, id, node, name string) (sharedFile, error) {
	f, err := s.card(ctx, id)
	if err != nil {
		return sharedFile{}, err
	}
	base, delta, err := s.origin(ctx, f)
	if err != nil {
		return sharedFile{}, err
	}
	data, err := s.originBytes(ctx, base)
	if err != nil {
		return sharedFile{}, err
	}
	var v *figdv.FigGoVary
	if err := figdvCall(func() { v = figdv.FigGoEdit_static_extract(data, base.Name, delta, node) }); err != nil {
		return sharedFile{}, err
	}
	if !figdv.FigGoEdit_static_ok(v) {
		return sharedFile{}, sharedErr{404, figdv.FigGoEdit_static_err(v)}
	}
	b := figdv.FigGoEdit_static_bytes(v)
	name = strings.TrimSpace(roomFileName(name))
	if name == "" {
		name = strings.TrimSuffix(f.Name, ".fig") + " " + strings.ReplaceAll(node, ":", "-")
	}
	if !strings.EqualFold(pathExt(name), ".fig") {
		name += ".fig"
	}
	return s.place(ctx, name, s.freeID(ctx, name), func(src string) (int64, string, error) {
		sum := sha256.Sum256(b)
		return int64(len(b)), fmt.Sprintf("%x", sum[:]), s.files.Save(ctx, src, "application/octet-stream", b, nil)
	})
}

// export keeps file `id` as a whole .fig at shared/{id}/export/{name} for
// a download link → that path, the name
func (s *sharedFiles) export(ctx context.Context, id string) (string, string, error) {
	f, err := s.card(ctx, id)
	if err != nil {
		return "", "", err
	}
	if f.Kind == variationKind {
		base, _, err := s.origin(ctx, f)
		if err != nil {
			return "", "", err
		}
		if s.editMax > 0 && base.Bytes > s.editMax {
			return "", "", sharedErr{413, fmt.Sprintf("%s is too large to build here (at most %d MB)", base.Name, s.editMax>>20)}
		}
	}
	b, err := s.sourceOf(ctx, f)
	if err != nil {
		return "", "", err
	}
	name := sharedName(f.Name)
	if !strings.EqualFold(pathExt(name), ".fig") {
		name += ".fig"
	}
	dir := sharedDir(id) + "/export"
	s.files.RemoveAll(dir)
	path := dir + "/" + name
	if err := s.files.Save(ctx, path, "application/octet-stream", b, nil); err != nil {
		return "", "", err
	}
	return path, name, nil
}

// the variations made of file `id`
func (s *sharedFiles) variationsOf(ctx context.Context, id string) []string {
	files, err := s.list(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		if f.Kind == variationKind && f.Base == id {
			out = append(out, f.ID)
		}
	}
	return out
}
