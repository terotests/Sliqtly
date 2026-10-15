// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// A Story's agent access (store/agents.go) for an assistant's tool call:
// every tool, Ranger's and the Go side's, passes rgr/Tools.rgr Tools.call,
// which asks AgentGate before it runs one. The rooms' operations hold it
// once more in their store (RoomPolicy, Principal.Agent), and the lists
// leave a hidden Story out (AgentsSee).

// the tools that may run on a Story assistants only read, besides those
// that only read (readOnlyHint): an export reads it into a file, and a
// claim made before the Story became read only is always let go of
var agentReadTools = map[string]bool{"export_presentation": true, "end_work": true}

// agentDecks: the presentations a tool's arguments name: deck_id, and
// the deck:<id> ends of a link (from, to, ref)
func agentDecks(a map[string]any) []string {
	var ids []string
	if id, _ := a["deck_id"].(string); strings.TrimSpace(id) != "" {
		ids = append(ids, strings.TrimSpace(id))
	}
	for _, k := range []string{"from", "to", "ref"} {
		s, _ := a[k].(string)
		if r, err := store.ParseRef(strings.TrimSpace(s)); err == nil && r.Kind == "deck" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// agentRefusal: why an assistant may not run tool name (readOnly: it
// only reads) on the Story d (id), "" when it may
func agentRefusal(name string, readOnly bool, id string, d store.Doc) string {
	if !store.AgentsSee(d) {
		return "No presentation " + id + "."
	}
	if !readOnly && !agentReadTools[name] && !store.AgentsWrite(d) {
		return "Not changed: presentation " + id + " is read only for assistants (its owner set it to Humans only in Document settings). You may read it. Tell the user, and do not make a copy unasked; only they can change the setting."
	}
	return ""
}

// AgentGate is the refusal for an assistant's call of tool name with args
// (JSON) on a presentation whose agent access keeps it from that, "" when
// nothing does. A storage failure is the host's error.
func (h *McpHost) AgentGate(name, args string, readOnly bool) string {
	if h.env.DB == nil {
		return ""
	}
	a := map[string]any{}
	if json.Unmarshal([]byte(args), &a) != nil {
		return ""
	}
	for _, id := range agentDecks(a) {
		if !shareID.MatchString(id) {
			continue
		}
		d, err := h.env.DB.Get(h.ctx, "shares", id)
		if err != nil {
			h.fail(err)
			return ""
		}
		if d == nil {
			continue
		}
		if why := agentRefusal(name, readOnly, id, store.Doc(d)); why != "" {
			return why
		}
	}
	return ""
}

// AgentsSee: an assistant may see the share doc (its JSON) in a list
func (h *McpHost) AgentsSee(doc string) bool {
	d := store.Doc{}
	if json.Unmarshal([]byte(doc), &d) != nil {
		return true
	}
	return store.AgentsSee(d)
}
