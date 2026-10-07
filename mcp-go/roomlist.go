// SPDX-License-Identifier: AGPL-3.0-or-later

// list_rooms' choosing and paging, apart from where the rooms come from:
// an assistant looks for the room (shown to people as a project) a new
// presentation belongs to, by a ticket code or words of its name or
// description, among the rooms worked in lately or among all of them.

package main

import (
	"context"
	"sort"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	// rooms on one page of list_rooms, and at most
	roomPageDefault = 1000
	roomPageMax     = 1000
)

// roomQuery is what list_rooms was asked for.
type roomQuery struct {
	// "created" (General and Playground first, then the newest; the
	// default and the page's own order), "active" (the latest worked in
	// first) or "title"
	Order string
	// words that each must be in the title or the description, any case
	Text   string
	Limit  int
	Offset int
}

func roomQueryOf(a map[string]any) (roomQuery, error) {
	q := roomQuery{Order: argStr(a, "order"), Text: argStr(a, "query"), Limit: int(argInt(a, "limit")), Offset: int(argInt(a, "offset"))}
	switch q.Order {
	case "":
		q.Order = "created"
	case "created", "active", "title":
	default:
		return q, roomErr{"order is created, active or title"}
	}
	if q.Limit <= 0 {
		q.Limit = roomPageDefault
	}
	if q.Limit > roomPageMax {
		q.Limit = roomPageMax
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return q, nil
}

// the rooms' place before the others in the "created" order
func starterFirst(id string) int {
	switch id {
	case store.GeneralRoom:
		return 0
	case store.PlaygroundRoom:
		return 1
	}
	return 2
}

// matches: every word of text is in the room's title or description
func (q roomQuery) matches(r roomRow) bool {
	hay := strings.ToLower(r.Title + "\n" + r.About)
	for _, w := range strings.Fields(strings.ToLower(q.Text)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// pick is the page of rows q asks for, and how many there are in all.
func (q roomQuery) pick(rows []roomRow) ([]roomRow, int) {
	out := []roomRow{}
	for _, r := range rows {
		if q.matches(r) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch q.Order {
		case "active":
			if a.Active != b.Active {
				return a.Active > b.Active
			}
		case "title":
			ta, tb := strings.ToLower(a.Title), strings.ToLower(b.Title)
			if ta != tb {
				return ta < tb
			}
		default:
			if fa, fb := starterFirst(a.RoomID), starterFirst(b.RoomID); fa != fb {
				return fa < fb
			}
		}
		if a.Created != b.Created {
			return a.Created > b.Created
		}
		return a.RoomID < b.RoomID
	})
	total := len(out)
	if q.Offset >= total {
		return []roomRow{}, total
	}
	out = out[q.Offset:]
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, total
}

// chatLatest is when the room's chat last had a message or a reply (of
// the newest top-level messages), 0 when it has none or none is kept.
func (s *roomService) chatLatest(ctx context.Context, tenant, room string) int64 {
	if s.chat == nil {
		return 0
	}
	ms, err := s.chat.Page(ctx, tenant, room, store.ChatPage{})
	if err != nil {
		return 0
	}
	var at int64
	for _, m := range ms {
		at = max(at, m.At, m.LastReply)
	}
	return at
}
