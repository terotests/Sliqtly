// SPDX-License-Identifier: AGPL-3.0-or-later

package agentsim

import (
	"context"
	"strings"
)

// DeckCheck is what one deck kept of the agents' work.
type DeckCheck struct {
	Deck  string `json:"deck_id"`
	Error string `json:"error,omitempty"`
	// updates the server said it saved, and of them the ones not in the
	// deck (lost) or in it more than once
	Saved      int      `json:"saved"`
	Lost       []string `json:"lost,omitempty"`
	Duplicated []string `json:"duplicated,omitempty"`
	// updates it refused that are in the deck all the same
	Refused int      `json:"refused"`
	Ghosts  []string `json:"ghosts,omitempty"`
	// calls that failed in transport: how many of them landed
	Unsure       int `json:"unsure"`
	UnsureLanded int `json:"unsure_landed"`
	// comments and session findings the server said it kept, and the
	// ones it does not have
	Comments        int      `json:"comments"`
	CommentsMissing []string `json:"comments_missing,omitempty"`
	Findings        int      `json:"findings"`
	FindingsMissing []string `json:"findings_missing,omitempty"`
	Slides          int      `json:"slides"`
	Bytes           int      `json:"markdown_bytes"`
}

// OK when nothing was lost, doubled, or kept although refused.
func (c DeckCheck) OK() bool {
	return c.Error == "" && len(c.Lost) == 0 && len(c.Duplicated) == 0 && len(c.Ghosts) == 0 &&
		len(c.CommentsMissing) == 0 && len(c.FindingsMissing) == 0
}

// CheckMarkdown compares the deck's Markdown with the lines the server said
// it saved and refused.
func CheckMarkdown(md string, saved, refused, unsure []string) (lost, dup, ghosts []string, landed int) {
	count := map[string]int{}
	for _, l := range strings.Split(md, "\n") {
		count[strings.TrimSpace(l)]++
	}
	for _, s := range saved {
		switch n := count[s]; {
		case n == 0:
			lost = append(lost, s)
		case n > 1:
			dup = append(dup, s)
		}
	}
	for _, s := range refused {
		if count[s] > 0 {
			ghosts = append(ghosts, s)
		}
	}
	for _, s := range unsure {
		if count[s] > 0 {
			landed++
		}
	}
	return
}

// Missing are the wanted texts not in have.
func Missing(want, have []string) []string {
	got := map[string]bool{}
	for _, h := range have {
		got[h] = true
	}
	var out []string
	for _, w := range want {
		if !got[w] {
			out = append(out, w)
		}
	}
	return out
}

func (s *sim) check(ctx context.Context, c *Client, d *deck) DeckCheck {
	d.mu.Lock()
	out := DeckCheck{Deck: d.id, Saved: len(d.saved), Refused: len(d.refuse), Unsure: len(d.unsure), Comments: len(d.notes), Findings: len(d.finds)}
	saved, refused, unsure := append([]string(nil), d.saved...), append([]string(nil), d.refuse...), append([]string(nil), d.unsure...)
	notes, finds := append([]string(nil), d.notes...), append([]string(nil), d.finds...)
	d.mu.Unlock()

	g, err := c.Call(ctx, "get_presentation", map[string]any{"deck_id": d.id})
	if err != nil || g.IsError {
		out.Error = "get_presentation: " + errText(g, err)
		return out
	}
	md, _ := g.Data["markdown"].(string)
	out.Bytes = len(md)
	out.Slides = strings.Count(md, "\n## ") + 1
	out.Lost, out.Duplicated, out.Ghosts, out.UnsureLanded = CheckMarkdown(md, saved, refused, unsure)

	lc, err := c.Call(ctx, "list_comments", map[string]any{"deck_id": d.id})
	if err != nil || lc.IsError {
		out.Error = "list_comments: " + errText(lc, err)
		return out
	}
	var have []string
	threads, _ := lc.Data["threads"].([]any)
	for _, t := range threads {
		tm, _ := t.(map[string]any)
		msgs, _ := tm["messages"].([]any)
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			if s, ok := mm["text"].(string); ok {
				have = append(have, s)
			}
		}
	}
	out.CommentsMissing = Missing(notes, have)

	var entries []string
	args := map[string]any{"deck_id": d.id, "limit": 500}
	for {
		r, err := c.Call(ctx, "get_session", args)
		if err != nil || r.IsError {
			out.Error = "get_session: " + errText(r, err)
			return out
		}
		es, _ := r.Data["entries"].([]any)
		for _, e := range es {
			em, _ := e.(map[string]any)
			if s, ok := em["text"].(string); ok {
				entries = append(entries, s)
			}
		}
		next, ok := r.Data["next_after"].(float64)
		if !ok || next == 0 {
			break
		}
		args = map[string]any{"deck_id": d.id, "limit": 500, "after": next}
	}
	out.FindingsMissing = Missing(finds, entries)
	return out
}

func errText(r Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return clip(r.Text, 300)
}
