// SPDX-License-Identifier: AGPL-3.0-or-later

// Searching the presentations' text: the Rooms panel's search and the
// assistant's search_presentations. A deck's words are read by the parser
// that draws it (RangerMarkdown's MdSearchText, compiled in from rgr/), so
// front matter, {attrs}, fence markers, link and picture addresses, CSS and
// diagram or chart source are not searched. Only the decks given are
// searched: the caller picks them by who may read them (roomsapi.go through
// the room policy, editor.go the signed-in user's own).

package main

import (
	"hash/fnv"
	"log"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// one presentation found
type deckHit struct {
	DeckID  string `json:"deck_id"`
	Name    string `json:"name"`
	RoomID  string `json:"room_id,omitempty"`
	Room    string `json:"room,omitempty"`
	Folder  string `json:"folder_id,omitempty"`
	Snippet string `json:"snippet"`
	Updated int64  `json:"updated"`
}

// a deck to search: its id and its shares document
type deckDoc struct {
	ID  string
	Doc Doc
}

const (
	// presentations a search answers with at most
	searchMax = 50
	// characters of text around the first word found
	snippetWidth = 90
	// decks whose text is kept between searches; past it the cache starts over
	searchCacheMax = 4000
)

// The decks' searchable text, kept while their Markdown is the same: a
// search is made on every pause in typing, and parsing a deck again each
// time would cost more than the search.
type searchTexts struct {
	mu sync.Mutex
	m  map[string]searchText
}

type searchText struct {
	sum  uint64
	text string
}

var deckTexts = &searchTexts{m: map[string]searchText{}}

func (c *searchTexts) of(id, md string) string {
	h := fnv.New64a()
	h.Write([]byte(md))
	sum := h.Sum64()
	c.mu.Lock()
	t, ok := c.m[id]
	c.mu.Unlock()
	if ok && t.sum == sum {
		return t.text
	}
	text := plainText(md)
	c.mu.Lock()
	if len(c.m) >= searchCacheMax {
		c.m = map[string]searchText{}
	}
	c.m[id] = searchText{sum, text}
	c.mu.Unlock()
	return text
}

// a deck's searchable text; "" for one the parser cannot read
func plainText(md string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("search: a deck's text could not be read: %v", r)
			out = ""
		}
	}()
	return MdSearchText_static_of(md)
}

// The decks whose name and text hold every word of the query (any case),
// last changed first, at most max; each with the text around the first
// word, "" when only its name matched. room names a room's title by id.
func searchDecks(docs []deckDoc, query string, max int, room func(id string) string) []deckHit {
	query = strings.Join(strings.Fields(query), " ")
	hits := []deckHit{}
	if query == "" {
		return hits
	}
	for _, d := range docs {
		name, _ := d.Doc["name"].(string)
		md, _ := d.Doc["md"].(string)
		text := deckTexts.of(d.ID, md)
		if MdSearchText_static_find(name+"\n"+text, query) < 0 {
			continue
		}
		at := millis(d.Doc["updated"])
		if at == 0 {
			at = millis(d.Doc["created"])
		}
		h := deckHit{DeckID: d.ID, Name: name, Updated: at,
			Snippet: strings.ToValidUTF8(MdSearchText_static_snippet(text, query, snippetWidth), "")}
		if r, _ := d.Doc[store.RoomField].(string); r != "" && room != nil {
			h.RoomID, h.Room = r, room(r)
			h.Folder, _ = d.Doc[store.FolderField].(string)
		}
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Updated > hits[j].Updated })
	if len(hits) > max {
		hits = hits[:max]
	}
	return hits
}

// the query a caller gave, at most 200 characters
func searchQuery(q string) string {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) > 200 {
		q = string([]rune(q)[:200])
	}
	return q
}
