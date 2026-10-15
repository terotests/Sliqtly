// SPDX-License-Identifier: AGPL-3.0-or-later

// Who is working on a Story, on sliqtly.com: the board of presence.go,
// whose calls may come to any of the server's instances, told to Firestore
// so the editor's Stories page reads it from there, as it changes
// (web/cloudpresence.js, onSnapshot). One document an assistant on a Story:
//
//	presence/{deck}/agents/{agent}  { id, name, client, tool, calls,
//	                                  first, last (ms), slides, whole, note,
//	                                  expires }
//
// written when what the page shows of it changes (its name, the slides it
// holds) and at most every presenceRefresh while it keeps calling, so the
// page sees it working; removed when its session ends, else by Firestore's
// TTL policy on `expires` (firestore.indexes.json). The page counts it as
// working, idle or gone from `last`, with the times presence.go uses. Only
// the Story's owner and the editors it invited read them (firestore.rules).
//
// A call that failed is not told: an assistant shows up only on a Story
// its call could work on.

package main

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// how often an assistant that keeps calling is written again (below
// agentWorking, so the page does not see it idle meanwhile)
const presenceRefresh = 30 * time.Second

// what the cloud keeps presence in (firebase.go; the tests a map)
type presenceStore interface {
	// put merges d into the document: a field left out stays as it was
	put(ctx context.Context, deck, agent string, d Doc) error
	drop(ctx context.Context, deck, agent string) error
}

type presencePub struct {
	st presenceStore
	mu sync.Mutex
	// deck~agent → what was last written, and when
	sent      map[string]pubSent
	lastPrune time.Time
}

type pubSent struct {
	shape string
	at    time.Time
}

func newPresencePub(st presenceStore) *presencePub {
	return &presencePub{st: st, sent: map[string]pubSent{}}
}

// a deck id Firestore takes as a document's
var presenceDeckID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// the document of an assistant on a Story, and what of it the page shows
// beyond its times
func presenceDoc(o agentOn) (Doc, string) {
	r := o.row
	slides := make([]any, 0, len(r.Slides))
	for _, s := range r.Slides {
		slides = append(slides, int64(s))
	}
	d := Doc{
		"id": r.ID, "tool": r.Tool, "calls": int64(r.Calls),
		"first": o.first.UnixMilli(), "last": o.last.UnixMilli(),
		"slides": slides, "whole": r.Whole, "note": r.Note,
		"expires": o.last.Add(agentGone + time.Minute),
	}
	// a name not known here (the session began on another instance) is
	// left as the document has it
	if o.name != "" {
		d["name"] = o.name
	}
	if r.Client != "" {
		d["client"] = r.Client
	}
	shape := strings.Join([]string{o.name, r.Client, fmtInts(r.Slides), boolStr(r.Whole), r.Note}, "\x00")
	return d, shape
}

func fmtInts(ns []int) string {
	var b strings.Builder
	for _, n := range ns {
		b.WriteString(strconv.Itoa(n))
		b.WriteByte(',')
	}
	return b.String()
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// tell writes the assistant's document on the Story when it is due
func (p *presencePub) tell(ctx context.Context, b *agentBoard, agent, deck string, now time.Time) {
	if p == nil || agent == "" || !presenceDeckID.MatchString(deck) {
		return
	}
	o, ok := b.on(agent, deck, now)
	if !ok {
		return
	}
	d, shape := presenceDoc(o)
	k := deck + "~" + agent
	p.mu.Lock()
	s, had := p.sent[k]
	due := !had || s.shape != shape || now.Sub(s.at) >= presenceRefresh
	if due {
		p.sent[k] = pubSent{shape, now}
	}
	p.pruneLocked(now)
	p.mu.Unlock()
	if !due {
		return
	}
	if err := p.st.put(ctx, deck, agent, d); err != nil {
		log.Printf("presence: %v", err)
		p.mu.Lock()
		delete(p.sent, k) // told again on its next call
		p.mu.Unlock()
	}
}

// gone: the assistant's session ended; its documents go
func (p *presencePub) gone(ctx context.Context, agent string, decks []string) {
	if p == nil {
		return
	}
	for _, deck := range decks {
		if !presenceDeckID.MatchString(deck) {
			continue
		}
		p.mu.Lock()
		_, had := p.sent[deck+"~"+agent]
		delete(p.sent, deck+"~"+agent)
		p.mu.Unlock()
		if !had {
			continue // never written from here; its TTL takes it
		}
		if err := p.st.drop(ctx, deck, agent); err != nil {
			log.Printf("presence: %v", err)
		}
	}
}

// (locked) what was written agentGone ago is forgotten, at most every 30 s
func (p *presencePub) pruneLocked(now time.Time) {
	if now.Sub(p.lastPrune) < 30*time.Second {
		return
	}
	p.lastPrune = now
	for k, s := range p.sent {
		if now.Sub(s.at) >= agentGone {
			delete(p.sent, k)
		}
	}
}
