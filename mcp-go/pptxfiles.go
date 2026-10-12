// SPDX-License-Identifier: AGPL-3.0-or-later

// PowerPoint decks (.pptx) among the shared files (sharedfiles.go). A deck
// is read once, when it is put here, with RangerPPTX (the package pptxgo,
// rgr/PptxGo.rgr), and what is kept beside it is
//
//	shared/{id}/source.pptx       the deck as it is now
//	shared/{id}/index.json        its slides as DesignViewer's index lists
//	                              screens (a slide is screen "3", a shape on
//	                              it part "3:2"), its look ("style") and each
//	                              slide's words as Markdown and its shapes
//	shared/{id}/cut/{n}.dl.json   slide n laid out: "W H" and the display
//	                              list render.go paints
//	shared/{id}/img/{part}        the pictures the slides paint, by their
//	                              part in the package
//
// so a slide, or a shape on one, is drawn from its list alone, as a Figma
// screen is from its cut. A deck is changed in place (edit_pptx): the
// package is written over with RangerPPTX's own writer, which keeps every
// part it does not model, and read again. There is no history: the deck is
// what it is.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/pptxgo"
)

// one edit of a deck at a time, on any server (an edit reads, writes and
// reads again)
var pptxEditMu sync.Mutex

// cutPptx opens a deck and keeps its index, slide lists and pictures in
// place of the old ones
func (s *sharedFiles) cutPptx(ctx context.Context, id string, data []byte) (counts sharedCounts, err error) {
	// the reader is generated code: a deck it cannot read must fail the
	// file, not the server
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the deck could not be read (%v)", r)
		}
	}()
	d := pptxgo.PptxGo_static_open(data, pptxgo.Presets)
	if !pptxgo.PptxGo_static_ok(d) {
		msg := pptxgo.PptxGo_static_err(d)
		if msg == "" {
			msg = "not a PowerPoint deck"
		}
		return counts, fmt.Errorf("%s", msg)
	}
	return s.keepPptx(ctx, id, d)
}

// keepPptx writes what is kept of an opened deck
func (s *sharedFiles) keepPptx(ctx context.Context, id string, d *pptxgo.PptxGoDoc) (sharedCounts, error) {
	ix := pptxgo.PptxGo_static_indexJson(d)
	if !json.Valid([]byte(ix)) {
		return sharedCounts{}, fmt.Errorf("the index did not come out as JSON")
	}
	dir := sharedDir(id)
	for _, sub := range []string{"cut", "img", "png"} {
		if err := s.files.RemoveAll(dir + "/" + sub); err != nil {
			return sharedCounts{}, err
		}
	}
	n := int(pptxgo.PptxGo_static_slideCount(d))
	saved := map[string]bool{}
	for i := 0; i < n; i++ {
		view := pptxgo.PptxGo_static_view(d, int64(i))
		if view == "" {
			continue
		}
		if err := s.files.Save(ctx, dir+"/cut/"+strconv.Itoa(i+1)+".dl.json", "application/json", []byte(view), nil); err != nil {
			return sharedCounts{}, err
		}
		for _, m := range listSrc.FindAllStringSubmatch(view, -1) {
			var part string
			if json.Unmarshal([]byte(`"`+m[1]+`"`), &part) != nil || part == "" || saved[part] {
				continue
			}
			saved[part] = true
			b := pptxgo.PptxGo_static_partBytes(d, part)
			if len(b) == 0 {
				continue
			}
			if err := s.files.Save(ctx, dir+"/img/"+sharedNodeFile(part), sniffPicture(b), b, nil); err != nil {
				return sharedCounts{}, err
			}
		}
	}
	if err := s.files.Save(ctx, dir+"/index.json", "application/json", []byte(ix), nil); err != nil {
		return sharedCounts{}, err
	}
	return sharedCounts{1, n}, nil
}

// drawPptx draws slide `node` ("3") of deck f, or one shape on it ("3:2",
// cut out of the slide by the box its index gives), `scale` pixels to a
// slide pixel (96 dpi) and at most maxSide on its long side
func (s *sharedFiles) drawPptx(ctx context.Context, f sharedFile, node string, scale float64, maxSide int) ([]byte, error) {
	slide, part, isPart := strings.Cut(node, ":")
	n, err := strconv.Atoi(slide)
	if err != nil || n < 1 {
		return nil, sharedErr{404, "no slide " + node + " in " + f.Name}
	}
	b, err := s.files.Read(ctx, sharedDir(f.ID)+"/cut/"+strconv.Itoa(n)+".dl.json", 64<<20)
	if err != nil {
		return nil, sharedErr{404, fmt.Sprintf("%s has no slide %d", f.Name, n)}
	}
	head, list, ok := strings.Cut(string(b), "\n")
	var cw, ch float64
	if _, err := fmt.Sscan(head, &cw, &ch); !ok || err != nil || cw <= 0 || ch <= 0 {
		return nil, fmt.Errorf("slide %d has no size", n)
	}
	bx, by, bw, bh := 0.0, 0.0, cw, ch
	if isPart {
		box, err := s.pptxPartBox(ctx, f, n, node)
		if err != nil {
			return nil, err
		}
		bx, by, bw, bh = box[0], box[1], box[2], box[3]
		if bw <= 0 || bh <= 0 {
			return nil, sharedErr{404, "shape " + part + " of slide " + slide + " has no size"}
		}
	}
	select {
	case s.draws <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.draws }()
	k := scale
	if long := math.Max(bw, bh) * k; long > float64(maxSide) {
		k = float64(maxSide) / math.Max(bw, bh)
	}
	pw, ph := int(math.Max(1, math.Round(bw*k))), int(math.Max(1, math.Round(bh*k)))
	pics := &renderPics{data: map[string][]byte{}, log: s.log}
	for _, m := range listSrc.FindAllStringSubmatch(list, -1) {
		var name string
		if json.Unmarshal([]byte(`"`+m[1]+`"`), &name) != nil || pics.data[name] != nil {
			continue
		}
		if b, err := s.files.Read(ctx, sharedDir(f.ID)+"/img/"+sharedNodeFile(utf8Of(name)), 64<<20); err == nil {
			pics.data[name] = b
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, pw, ph))
	ox, oy := int(math.Round(-bx*k)), int(math.Round(-by*k))
	area := image.Rect(ox, oy, ox+int(math.Round(cw*k)), oy+int(math.Round(ch*k)))
	if err := renderList(dst, list, cw, ch, area, pics, nil); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// the box (x, y, w, h in slide pixels) the index gives part `node` of slide n
func (s *sharedFiles) pptxPartBox(ctx context.Context, f sharedFile, n int, node string) ([4]float64, error) {
	var box [4]float64
	ix, err := s.index(ctx, f)
	if err != nil {
		return box, err
	}
	var head struct {
		Screens []struct {
			ID    string `json:"id"`
			Parts []struct {
				ID string  `json:"id"`
				X  float64 `json:"x"`
				Y  float64 `json:"y"`
				W  float64 `json:"w"`
				H  float64 `json:"h"`
			} `json:"parts"`
		} `json:"screens"`
	}
	if err := json.Unmarshal([]byte(ix), &head); err != nil {
		return box, err
	}
	for _, sc := range head.Screens {
		if sc.ID != strconv.Itoa(n) {
			continue
		}
		for _, p := range sc.Parts {
			if p.ID == node {
				return [4]float64{p.X, p.Y, p.W, p.H}, nil
			}
		}
	}
	return box, sharedErr{404, "no shape " + node + " in " + f.Name + " (get_pptx_slide lists them)"}
}

// one change edit_pptx makes: the words of a shape, or a slide taken out
type pptxEdit struct {
	Slide  int    `json:"slide"`
	Shape  string `json:"shape"`
	Text   string `json:"text"`
	Remove bool   `json:"remove"`
}

// editPptx makes the edits to deck id, in the order given (slide numbers
// as they are before any slide is taken out), writes it over its own
// package and reads it again → its card
func (s *sharedFiles) editPptx(ctx context.Context, id string, edits []pptxEdit) (sharedFile, error) {
	pptxEditMu.Lock()
	defer pptxEditMu.Unlock()
	f, err := s.card(ctx, id)
	if err != nil {
		return f, err
	}
	if f.Kind != "pptx" {
		return f, sharedErr{400, f.Name + " is not a PowerPoint deck"}
	}
	if f = s.seen(f); f.Status != "ready" {
		return f, sharedErr{409, f.Name + " is " + f.Status}
	}
	if len(edits) == 0 {
		return f, sharedErr{400, "say what to change in edits"}
	}
	data, err := s.files.Read(ctx, s.sourcePath(f), s.max+1)
	if err != nil {
		return f, err
	}
	var out []byte
	err = func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("the deck could not be changed (%v)", r)
			}
		}()
		d := pptxgo.PptxGo_static_open(data, pptxgo.Presets)
		if !pptxgo.PptxGo_static_ok(d) {
			return sharedErr{422, f.Name + " could not be read: " + pptxgo.PptxGo_static_err(d)}
		}
		count := int(pptxgo.PptxGo_static_slideCount(d))
		var gone []int
		for i, e := range edits {
			if e.Slide < 1 || e.Slide > count {
				return sharedErr{400, fmt.Sprintf("edit %d: there is no slide %d (1–%d)", i+1, e.Slide, count)}
			}
			if e.Remove {
				gone = append(gone, e.Slide)
				continue
			}
			if e.Shape == "" {
				return sharedErr{400, fmt.Sprintf("edit %d: say which shape (its \"shape\" from get_pptx_slide) or remove", i+1)}
			}
			if msg := pptxgo.PptxGo_static_setText(d, int64(e.Slide-1), e.Shape, e.Text); msg != "" {
				return sharedErr{400, fmt.Sprintf("edit %d: %s", i+1, msg)}
			}
		}
		// the last first, so the numbers said stay right
		sort.Sort(sort.Reverse(sort.IntSlice(gone)))
		for i, n := range gone {
			if i > 0 && gone[i-1] == n {
				continue
			}
			if msg := pptxgo.PptxGo_static_removeSlide(d, int64(n-1)); msg != "" {
				return sharedErr{400, msg}
			}
		}
		if int(pptxgo.PptxGo_static_slideCount(d)) == 0 {
			return sharedErr{400, "a deck keeps at least one slide"}
		}
		out = pptxgo.PptxGo_static_save(d)
		// what was written must read: a deck that would not open again is
		// not put over the one that did
		if !pptxgo.PptxGo_static_ok(pptxgo.PptxGo_static_open(out, "")) {
			return fmt.Errorf("the changed deck did not read back")
		}
		return nil
	}()
	if err != nil {
		return f, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := s.files.SaveFrom(ctx, s.sourcePath(f), "application/octet-stream", bytes.NewReader(out))
	if err != nil {
		return f, err
	}
	start := time.Now()
	n, err := s.cutPptx(ctx, id, out)
	out = nil
	debug.FreeOSMemory()
	if err != nil {
		f.Status, f.Error = "failed", err.Error()
		s.putCard(ctx, f)
		return f, err
	}
	f.Bytes, f.Sha = info.Size, fmt.Sprintf("%x", info.Hash[:])
	f.Pages, f.Screens, f.Status, f.Error = n.pages, n.screens, "ready", ""
	f.Index = s.indexURL(id)
	if err := s.putCard(ctx, f); err != nil {
		return f, err
	}
	s.forget(id)
	s.log(fmt.Sprintf("shared %s: %d edits, %d slides in %s", id, len(edits), n.screens, time.Since(start).Round(time.Millisecond)))
	return s.card(ctx, id)
}

// --- the tools' answers (sharedtools.go)

// a deck's index as the tools read it
type pptxIndex struct {
	Style   map[string]any `json:"style"`
	Screens []pptxSlideIx  `json:"screens"`
}

type pptxSlideIx struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Title      string            `json:"title"`
	W          float64           `json:"w"`
	H          float64           `json:"h"`
	Markdown   string            `json:"markdown"`
	Layout     string            `json:"layout"`
	Background string            `json:"background"`
	Notes      []string          `json:"notes"`
	Shapes     []json.RawMessage `json:"shapes"`
}

// the card and the read index of ready deck id
func (s *sharedFiles) pptxOf(ctx context.Context, id string) (sharedFile, pptxIndex, error) {
	var ix pptxIndex
	if id == "" {
		return sharedFile{}, ix, roomErr{"give file_id (list_shared_files)"}
	}
	f, err := s.card(ctx, id)
	if err != nil {
		return f, ix, err
	}
	if f.Kind != "pptx" {
		return f, ix, roomErr{f.Name + " is not a PowerPoint deck"}
	}
	if f = s.seen(f); f.Status != "ready" {
		return f, ix, roomErr{f.Name + " is " + f.Status + " (list_shared_files)"}
	}
	raw, err := s.index(ctx, f)
	if err != nil {
		return f, ix, err
	}
	if err := json.Unmarshal([]byte(raw), &ix); err != nil {
		return f, ix, fmt.Errorf("the index of %s does not read: %v", f.Name, err)
	}
	return f, ix, nil
}

func (s *sharedFiles) pptxSlide(ctx context.Context, id string, n int) (map[string]any, error) {
	f, ix, err := s.pptxOf(ctx, id)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > len(ix.Screens) {
		return nil, roomErr{fmt.Sprintf("%s has slides 1–%d", f.Name, len(ix.Screens))}
	}
	sl := ix.Screens[n-1]
	shapes := sl.Shapes
	if shapes == nil {
		shapes = []json.RawMessage{}
	}
	return map[string]any{
		"file_id": f.ID, "file": f.Name, "slide": n, "slides": len(ix.Screens),
		"title": sl.Title, "markdown_text": sl.Markdown, "notes": orEmpty(sl.Notes),
		"layout": sl.Layout, "background": sl.Background,
		"width": sl.W, "height": sl.H, "shapes": shapes,
		"markdown": FigmaRef_static_markdownIn("pptx", f.ID, sl.ID, sl.Name), "note": sharedDataNote,
	}, nil
}

// a text style the slides use and how often
type pptxTextStyle struct {
	Font   string  `json:"font"`
	SizePt float64 `json:"size_pt"`
	Bold   bool    `json:"bold"`
	Italic bool    `json:"italic"`
	Color  string  `json:"color"`
	Count  int     `json:"count"`
}

type pptxShapeIx struct {
	Placeholder string         `json:"placeholder"`
	Style       *pptxTextStyle `json:"style"`
	Children    []pptxShapeIx  `json:"children"`
}

// the most used text styles of the shapes: titles (title placeholders) and
// the rest, most used first, at most five of each
func pptxTextStyles(ix pptxIndex) (titles, body []pptxTextStyle) {
	tally := map[bool]map[pptxTextStyle]int{true: {}, false: {}}
	var walk func(sh pptxShapeIx)
	walk = func(sh pptxShapeIx) {
		if sh.Style != nil {
			k := *sh.Style
			k.Count = 0
			title := sh.Placeholder == "title" || sh.Placeholder == "ctrTitle"
			tally[title][k]++
		}
		for _, c := range sh.Children {
			walk(c)
		}
	}
	for _, sl := range ix.Screens {
		for _, raw := range sl.Shapes {
			var sh pptxShapeIx
			if json.Unmarshal(raw, &sh) == nil {
				walk(sh)
			}
		}
	}
	top := func(m map[pptxTextStyle]int) []pptxTextStyle {
		out := []pptxTextStyle{}
		for k, n := range m {
			k.Count = n
			out = append(out, k)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].SizePt > out[j].SizePt
		})
		return out[:min(len(out), 5)]
	}
	return top(tally[true]), top(tally[false])
}

func (s *sharedFiles) pptxStyle(ctx context.Context, id string) (map[string]any, error) {
	f, ix, err := s.pptxOf(ctx, id)
	if err != nil {
		return nil, err
	}
	titles, body := pptxTextStyles(ix)
	out := map[string]any{"file_id": f.ID, "file": f.Name, "slides": len(ix.Screens), "title_styles": titles, "text_styles": body, "note": sharedDataNote}
	for k, v := range ix.Style {
		out[k] = v
	}
	return out, nil
}

func (s *sharedFiles) pptxEditForTool(ctx context.Context, id string, edits []pptxEdit) (map[string]any, error) {
	if id == "" {
		return nil, roomErr{"give file_id (list_shared_files)"}
	}
	f, err := s.editPptx(ctx, id, edits)
	if err != nil {
		return nil, err
	}
	_, ix, err := s.pptxOf(ctx, id)
	if err != nil {
		return nil, err
	}
	changed := []map[string]any{}
	seen := map[int]bool{}
	for _, e := range edits {
		if e.Remove || seen[e.Slide] {
			continue
		}
		seen[e.Slide] = true
		// slides taken out before it move it up
		n := e.Slide
		for _, r := range edits {
			if r.Remove && r.Slide < e.Slide {
				n--
			}
		}
		if n >= 1 && n <= len(ix.Screens) {
			changed = append(changed, map[string]any{"slide": n, "markdown_text": ix.Screens[n-1].Markdown})
		}
	}
	return map[string]any{"file": f, "slides": len(ix.Screens), "changed": changed,
		"note": "Saved over the deck itself; there is no undo. Download it from the editor's Files place."}, nil
}
