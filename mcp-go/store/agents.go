// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
)

// Who works on a Story: people, assistants (MCP), or both. Its owner sets
// it in the presentation's Document settings; it is kept on the share
// document under AgentsField, beside its story. A document without it is
// Mixed, what every Story was before the setting.
//
//	mixed   people and assistants read and change it (the default)
//	read    humans only: assistants read it and change nothing
//	hidden  humans only: assistants do not see it at all, not in lists
//	        either; to them it is not there
//	only    agents only: assistants change it, people view it (and may
//	        still change its settings: this one, its room, its story)
//
// Only a person changes it: an assistant's change of it is refused
// (AgentMayWrite), so an assistant cannot let itself in.

// AgentsField holds a document's agent access.
const AgentsField = "agents"

const (
	AgentsMixed  = "mixed"
	AgentsRead   = "read"
	AgentsHidden = "hidden"
	AgentsOnly   = "only"
)

// AgentModes in the order the page offers them.
var AgentModes = []string{AgentsMixed, AgentsRead, AgentsHidden, AgentsOnly}

// ErrAgents: not one of AgentModes.
var ErrAgents = errors.New("store: not an agent access mode")

// AgentsOf is the document's agent access; what it does not say (or says
// wrong) is Mixed.
func AgentsOf(d Doc) string {
	if v, _ := d[AgentsField].(string); oneOf(AgentModes, v) {
		return v
	}
	return AgentsMixed
}

// ValidAgents: mode is one of AgentModes.
func ValidAgents(mode string) bool { return oneOf(AgentModes, mode) }

// AgentsSee: an assistant may know the document is there and read it.
func AgentsSee(d Doc) bool { return AgentsOf(d) != AgentsHidden }

// AgentsWrite: an assistant may change the document.
func AgentsWrite(d Doc) bool {
	m := AgentsOf(d)
	return m == AgentsMixed || m == AgentsOnly
}

// HumansWrite: a person may change the document's content (its text,
// theme, pictures and files); its settings they always may.
func HumansWrite(d Doc) bool { return AgentsOf(d) != AgentsOnly }

// AgentMayWrite: may an assistant change the document from old to next
// (old nil: it makes it; next nil: it removes it). It changes only what it
// may change, and never the agent access itself: a new one starts Mixed.
func AgentMayWrite(old, next Doc) bool {
	if old == nil {
		return next == nil || AgentsOf(next) == AgentsMixed
	}
	if !AgentsWrite(old) {
		return false
	}
	return next == nil || AgentsOf(next) == AgentsOf(old)
}

// settingFields are what a person may still change on an agents only
// Story: its settings, not its content
var settingFields = map[string]bool{
	AgentsField: true, StoryField: true, RoomField: true, FolderField: true,
	InheritField: true, "visibility": true, "editors": true, "updated": true,
}

// HumanMayWrite: may a person change the document from old to next. On an
// agents only Story only its settings; anything else as before.
func HumanMayWrite(old, next Doc) bool {
	if old == nil || next == nil || HumansWrite(old) {
		return true
	}
	return OnlySettings(old, next)
}

// OnlySettings: old and next differ in settingFields only.
func OnlySettings(old, next Doc) bool {
	for k, v := range next {
		if !settingFields[k] && !reflect.DeepEqual(old[k], v) {
			return false
		}
	}
	for k := range old {
		if _, ok := next[k]; !ok && !settingFields[k] {
			return false
		}
	}
	return true
}
