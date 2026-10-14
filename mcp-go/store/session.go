// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

// A Story's session is the visible history of the work on it: what people
// asked, what the assistants answered, the tools they called, the files and
// searches they used, what they made, and the decisions and findings on the
// way (docs/story-workspace.md). Nothing in it is an assistant's hidden
// reasoning: only what was said and done where others could see it.
//
// It is kept as a ChatLog of its own per Story (the room SessionRoom(deck)),
// each entry a message whose Act says its kind, the tool and what it used
// (Refs), so its order, numbers and paging are the chat's.
//
// A fork is a Story made from another at one entry of its history. Its
// timeline is its parent's first At entries, read through and never copied,
// followed by its own; so every fork has the same context up to its fork
// point. A fork of a fork reads through both, at most ForkDepth levels.

// SessionRoomPrefix starts the ChatLog room of a Story's session.
const SessionRoomPrefix = "story-"

// SessionRoom is the ChatLog room deck's session is kept in.
func SessionRoom(deck string) string { return SessionRoomPrefix + deck }

// IsSessionRoom: room is a Story's session, not a room's chat.
func IsSessionRoom(room string) bool { return strings.HasPrefix(room, SessionRoomPrefix) }

// The kinds of entry. KindFork is the server's own: written in the parent's
// session when someone forks it.
const (
	KindPrompt   = "prompt"
	KindReply    = "reply"
	KindTool     = "tool"
	KindSearch   = "search"
	KindFile     = "file"
	KindArtifact = "artifact"
	KindDecision = "decision"
	KindFinding  = "finding"
	KindNote     = "note"
	KindFork     = "fork"
)

// SessionKinds a caller may write (KindFork is not one).
var SessionKinds = []string{KindPrompt, KindReply, KindTool, KindSearch, KindFile, KindArtifact, KindDecision, KindFinding, KindNote}

const (
	// an entry's words; a longer text belongs in a file the entry refers to
	SessionMaxText = 20000
	SessionMaxTool = 80
	SessionMaxRefs = 20
	// a fork's name, in runes
	ForkNameMax = 80
	// how deep a fork reads through its parents
	ForkDepth = 8
	// the most entries one timeline is read with
	SessionMaxEntries = 20000
)

// ForkField holds a fork's Fork on its share document.
const ForkField = "fork"

// SessionEntry is one entry of a Story's timeline.
type SessionEntry struct {
	// its place in the timeline it is read in, 1 the first
	N int64 `json:"n"`
	// its number and id in the session it was written in (Origin)
	Seq int64  `json:"seq"`
	ID  string `json:"id"`
	// the Story it was written in: the one read, or a parent for an
	// entry a fork inherits
	Origin string   `json:"origin"`
	At     int64    `json:"at"` // ms since 1970
	By     ChatFrom `json:"by"`
	Kind   string   `json:"kind"`
	Text   string   `json:"text,omitempty"`
	Tool   string   `json:"tool,omitempty"`
	// what it used or made, each kind:id: file:data/x.csv, deck:<id>,
	// url:https://…, entry:<n>
	Refs []string `json:"refs,omitempty"`
}

// ErrEntry: an entry a caller may not write as it is; the text says why.
type ErrEntry struct{ Why string }

func (e ErrEntry) Error() string { return e.Why }

// CleanEntry is e as kept: its kind one a caller may write, its words
// trimmed and at most SessionMaxText runes, a tool's name only with a tool
// entry, each ref a kind:id (Ref) at most SessionMaxRefs of them, repeats
// left out. An entry with neither words nor refs says nothing: refused.
func CleanEntry(e SessionEntry) (SessionEntry, error) {
	e.Kind = strings.TrimSpace(e.Kind)
	if !oneOf(SessionKinds, e.Kind) {
		return e, ErrEntry{fmt.Sprintf("kind is one of %s", strings.Join(SessionKinds, ", "))}
	}
	e.Text = strings.TrimSpace(e.Text)
	if utf8.RuneCountInString(e.Text) > SessionMaxText {
		return e, ErrEntry{fmt.Sprintf("text is longer than %d characters: put it in a file and refer to it (refs file:<path>)", SessionMaxText)}
	}
	e.Tool = strings.TrimSpace(e.Tool)
	if e.Tool != "" && e.Kind != KindTool {
		return e, ErrEntry{"tool is the name of the tool a tool entry called"}
	}
	if utf8.RuneCountInString(e.Tool) > SessionMaxTool {
		return e, ErrEntry{fmt.Sprintf("tool is at most %d characters", SessionMaxTool)}
	}
	if len(e.Refs) > SessionMaxRefs {
		return e, ErrEntry{fmt.Sprintf("at most %d refs", SessionMaxRefs)}
	}
	var refs []string
	seen := map[string]bool{}
	for _, r := range e.Refs {
		ref, err := ParseRef(strings.TrimSpace(r))
		if err != nil {
			return e, ErrEntry{fmt.Sprintf("ref %q is not kind:id (file:data/x.csv, deck:<id>, url:https://…, entry:<n>)", r)}
		}
		if s := ref.String(); !seen[s] {
			seen[s] = true
			refs = append(refs, s)
		}
	}
	e.Refs = refs
	if e.Text == "" && len(e.Refs) == 0 && e.Tool == "" {
		return e, ErrEntry{"an entry needs text, a tool or refs"}
	}
	return e, nil
}

// EntryMsg is e as the session's ChatLog keeps it.
func EntryMsg(tenant, deck string, e SessionEntry) ChatMsg {
	return ChatMsg{Tenant: tenant, Room: SessionRoom(deck), From: e.By, Text: e.Text,
		Act: &ChatAct{Kind: e.Kind, Tool: e.Tool, Refs: e.Refs}}
}

// EntryOf is the entry a message of deck's session is (N not yet given).
func EntryOf(deck string, m ChatMsg) SessionEntry {
	e := SessionEntry{Seq: m.Seq, ID: m.ID, Origin: deck, At: m.At, By: m.From, Text: m.Text, Kind: KindNote}
	if m.Act != nil {
		e.Kind, e.Tool, e.Refs = m.Act.Kind, m.Act.Tool, append([]string(nil), m.Act.Refs...)
		if len(e.Refs) == 0 {
			e.Refs = nil
		}
	}
	if m.Deleted {
		e.Text, e.Refs = "", nil
	}
	return e
}

// Fork is where a fork came from: the Story, the place in its timeline
// (the fork has its first At entries), the fork's name, who made it, when.
type Fork struct {
	From string `json:"from"`
	At   int64  `json:"at"`
	Name string `json:"name"`
	By   string `json:"by,omitempty"`
	Made int64  `json:"made,omitempty"`
}

// IsFork: the Story was forked from another.
func (f Fork) IsFork() bool { return f.From != "" }

// ForkOf is a share document's fork, the zero Fork when it is none.
func ForkOf(d Doc) Fork {
	m, _ := d[ForkField].(map[string]any)
	var f Fork
	f.From, _ = m["from"].(string)
	f.Name, _ = m["name"].(string)
	f.By, _ = m["by"].(string)
	f.At = asInt(m["at"])
	f.Made = asInt(m["made"])
	if f.At < 0 {
		f.At = 0
	}
	return f
}

// ForkDoc is f as the share document keeps it.
func ForkDoc(f Fork) map[string]any {
	m := map[string]any{"from": f.From, "at": f.At, "name": f.Name}
	if f.By != "" {
		m["by"] = f.By
	}
	if f.Made != 0 {
		m["made"] = f.Made
	}
	return m
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// CleanForkName is a fork's name with one space between words, at most
// ForkNameMax runes; "" when nothing is left.
func CleanForkName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > ForkNameMax {
		s = strings.TrimSpace(string([]rune(s)[:ForkNameMax]))
	}
	return s
}

// Sessions keeps Stories' sessions in a ChatLog.
type Sessions struct {
	Log ChatLog
}

// Append writes e (cleaned with CleanEntry) at the end of deck's own
// session; the server's own kinds (KindFork) pass with server true.
// → the entry as kept (its N is its Seq: the place in the own session)
func (s Sessions) Append(ctx context.Context, tenant, deck string, e SessionEntry, server bool) (SessionEntry, error) {
	if server && e.Kind == KindFork {
		e.Text = strings.TrimSpace(e.Text)
	} else {
		var err error
		if e, err = CleanEntry(e); err != nil {
			return e, err
		}
	}
	m, err := s.Log.Append(ctx, EntryMsg(tenant, deck, e))
	if err != nil {
		return e, err
	}
	out := EntryOf(deck, m)
	out.N = out.Seq
	return out, nil
}

// Own is deck's own session, oldest first, at most max entries (max <= 0:
// SessionMaxEntries).
func (s Sessions) Own(ctx context.Context, tenant, deck string, max int64) ([]SessionEntry, error) {
	if max <= 0 || max > SessionMaxEntries {
		max = SessionMaxEntries
	}
	// a session's numbers run 1, 2, 3 … (it has no threads), so the
	// entries numbered lo+1 … hi are the newest hi-lo below hi+1
	var out []SessionEntry
	for lo := int64(0); lo < max; lo += chatPageMax {
		hi := min(lo+chatPageMax, max)
		page, err := s.Log.Page(ctx, tenant, SessionRoom(deck), ChatPage{Before: hi + 1, Limit: int(hi - lo)})
		if err != nil {
			return nil, err
		}
		for _, m := range page {
			if m.Seq > lo {
				out = append(out, EntryOf(deck, m))
			}
		}
		if int64(len(page)) < hi-lo {
			break
		}
	}
	return out, nil
}

// ForkReader says a Story's fork; ErrNotFound when there is no such Story
// (or not one the reader may see).
type ForkReader func(ctx context.Context, deck string) (Fork, error)

// Timeline is deck's whole history, oldest first, each entry numbered by
// its place (N, from 1): what its parents had up to its fork point, then
// its own. A parent that is gone, or one the reader may not see, leaves
// only what the Story has of its own.
func (s Sessions) Timeline(ctx context.Context, tenant, deck string, forkOf ForkReader) ([]SessionEntry, error) {
	f, err := forkOf(ctx, deck)
	if err != nil {
		return nil, err
	}
	tl, err := s.timeline(ctx, tenant, deck, f, SessionMaxEntries, 0, forkOf)
	if err != nil {
		return nil, err
	}
	for i := range tl {
		tl[i].N = int64(i) + 1
	}
	return tl, nil
}

// the first max entries of deck's timeline (f: its fork)
func (s Sessions) timeline(ctx context.Context, tenant, deck string, f Fork, max int64, depth int, forkOf ForkReader) ([]SessionEntry, error) {
	var out []SessionEntry
	if f.IsFork() && f.At > 0 && depth < ForkDepth && f.From != deck {
		pf, err := forkOf(ctx, f.From)
		switch {
		case errors.Is(err, ErrNotFound):
			// the parent is gone: the fork keeps its own
		case err != nil:
			return nil, err
		default:
			inherited, err := s.timeline(ctx, tenant, f.From, pf, min(f.At, max), depth+1, forkOf)
			if err != nil {
				return nil, err
			}
			out = inherited
		}
	}
	if int64(len(out)) >= max {
		return out[:max], nil
	}
	own, err := s.Own(ctx, tenant, deck, max-int64(len(out)))
	if err != nil {
		return nil, err
	}
	return append(out, own...), nil
}

// ForkPlan is what a fork takes from its parent: the new share document
// (without its files' addresses, which the copy gives) and the files to
// copy; Skipped names the parent's files left behind.
type ForkPlan struct {
	Doc     Doc
	Files   []Doc
	Skipped []string
}

// forkFields are what a fork's share takes from its parent's: the deck and
// where it lives. Everything else (owner, keys, links, connectors, live
// data, chat, versions, exports) stays the parent's.
var forkFields = []string{"name", "md", "theme", "css", "visibility", RoomField, FolderField, InheritField}

// ForkCopiesFile says whether a fork takes the parent's file at p: not a
// hidden one (a part starting with "."), nothing that looks like a key or
// credentials, and not the review comments (a fork's review is its own;
// findings come back by merge).
func ForkCopiesFile(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "..") {
		return false
	}
	low := strings.ToLower(p)
	if low == "review/comments.json" {
		return false
	}
	for _, part := range strings.Split(low, "/") {
		if strings.HasPrefix(part, ".") || part == "secrets" || part == "credentials" {
			return false
		}
	}
	base := path.Base(low)
	for _, pre := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "credentials", "secret"} {
		if strings.HasPrefix(base, pre) {
			return false
		}
	}
	switch path.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx", ".env", ".keystore", ".jks", ".kdbx":
		return false
	}
	return true
}

// PlanFork plans the fork of parent (its share document, id parentID) at
// place at of its timeline, named name, made by owner (a principal's user
// id) at now. The fork is a draft of the parent's type and tags in the
// parent's room and folder, as private as the parent, named after it and
// tagged with its name.
func PlanFork(parent Doc, parentID string, at int64, name, owner string, now time.Time) ForkPlan {
	d := Doc{}
	for _, k := range forkFields {
		if v, ok := parent[k]; ok {
			d[k] = cloneValue(v)
		}
	}
	base, _ := parent["name"].(string)
	if base = strings.TrimSpace(base); base == "" {
		base = "Untitled"
	}
	d["name"] = base + " · " + name
	if v, _ := d["visibility"].(string); v != "private" {
		d["visibility"] = "link"
	}
	s := StoryOf(parent)
	s.Phase = NewStoryPhase
	// the editor names a deck by its first heading, so the fork's name
	// travels as a tag too: its card and search tell the forks apart
	s.Tags = CleanTags(append(s.Tags, name))
	d[StoryField] = StoryDoc(s)
	d[ForkField] = ForkDoc(Fork{From: parentID, At: at, Name: name, By: owner, Made: now.UnixMilli()})
	d["owner"] = owner
	d["source"] = "fork"
	d["created"] = now
	d["updated"] = now
	plan := ForkPlan{Doc: d}
	files, _ := parent["files"].([]any)
	for _, f := range files {
		m, _ := f.(map[string]any)
		p, _ := m["path"].(string)
		if !ForkCopiesFile(p) {
			if p != "" {
				plan.Skipped = append(plan.Skipped, p)
			}
			continue
		}
		// all it says of itself but where it is read from: the copy has
		// an address of its own
		c := Doc{}
		for k, v := range m {
			if k != "url" && k != "token" {
				c[k] = cloneValue(v)
			}
		}
		plan.Files = append(plan.Files, c)
	}
	return plan
}
