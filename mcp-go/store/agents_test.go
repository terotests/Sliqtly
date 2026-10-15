// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentsOf(t *testing.T) {
	for _, c := range []struct {
		d    Doc
		want string
	}{
		{Doc{}, AgentsMixed},
		{Doc{AgentsField: "read"}, AgentsRead},
		{Doc{AgentsField: "hidden"}, AgentsHidden},
		{Doc{AgentsField: "only"}, AgentsOnly},
		{Doc{AgentsField: "nobody"}, AgentsMixed},
		{Doc{AgentsField: 3}, AgentsMixed},
	} {
		if got := AgentsOf(c.d); got != c.want {
			t.Errorf("AgentsOf(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestAgentsSeeWrite(t *testing.T) {
	for _, c := range []struct {
		mode               string
		see, write, humans bool
	}{
		{AgentsMixed, true, true, true},
		{AgentsRead, true, false, true},
		{AgentsHidden, false, false, true},
		{AgentsOnly, true, true, false},
	} {
		d := Doc{AgentsField: c.mode}
		if AgentsSee(d) != c.see || AgentsWrite(d) != c.write || HumansWrite(d) != c.humans {
			t.Errorf("%s: see %v write %v humans %v", c.mode, AgentsSee(d), AgentsWrite(d), HumansWrite(d))
		}
	}
}

func TestAgentMayWrite(t *testing.T) {
	mixed := Doc{"md": "# a"}
	if !AgentMayWrite(mixed, Doc{"md": "# b"}) {
		t.Error("a mixed Story is the assistant's to change")
	}
	if AgentMayWrite(mixed, Doc{"md": "# a", AgentsField: AgentsOnly}) {
		t.Error("an assistant changed the agent access")
	}
	ro := Doc{"md": "# a", AgentsField: AgentsRead}
	if AgentMayWrite(ro, Doc{"md": "# b", AgentsField: AgentsRead}) || AgentMayWrite(ro, nil) {
		t.Error("an assistant changed or removed a read-only Story")
	}
	if AgentMayWrite(ro, Doc{"md": "# a", AgentsField: AgentsMixed}) {
		t.Error("an assistant let itself in")
	}
	if AgentMayWrite(Doc{AgentsField: AgentsHidden}, Doc{AgentsField: AgentsHidden, "md": "x"}) {
		t.Error("an assistant changed a hidden Story")
	}
	only := Doc{"md": "# a", AgentsField: AgentsOnly}
	if !AgentMayWrite(only, Doc{"md": "# b", AgentsField: AgentsOnly}) {
		t.Error("an agents only Story is the assistant's to change")
	}
	if !AgentMayWrite(nil, Doc{"md": "# new"}) || AgentMayWrite(nil, Doc{"md": "# new", AgentsField: AgentsHidden}) {
		t.Error("a new Story an assistant makes starts mixed")
	}
}

func TestHumanMayWrite(t *testing.T) {
	only := Doc{"md": "# a", "name": "A", AgentsField: AgentsOnly, StoryField: map[string]any{"type": "story"}}
	if HumanMayWrite(only, Doc{"md": "# b", "name": "A", AgentsField: AgentsOnly}) {
		t.Error("a person changed an agents only Story's text")
	}
	next := Clone(only)
	next[AgentsField] = AgentsMixed
	next[StoryField] = map[string]any{"type": "review"}
	next[RoomField] = "r1"
	if !HumanMayWrite(only, next) {
		t.Error("a person may change an agents only Story's settings")
	}
	gone := Clone(only)
	delete(gone, "md")
	if HumanMayWrite(only, gone) {
		t.Error("a person took away an agents only Story's text")
	}
	if !HumanMayWrite(Doc{"md": "x", AgentsField: AgentsRead}, Doc{"md": "y", AgentsField: AgentsRead}) {
		t.Error("a humans only Story is the person's to change")
	}
}

// RoomPolicy holds a Story's agent access for an assistant and a person
// with the same role
func TestRoomPolicyAgents(t *testing.T) {
	ctx := context.Background()
	s := New(NewMemStore(), RoomPolicy{Cols: map[string]bool{"shares": true}})
	person := Principal{UserID: "u", TenantID: "T", Rooms: map[string]Role{"r": Owner}}
	agent := person
	agent.Agent = true
	put := func(p Principal, id string, d Doc) error {
		_, err := s.Put(ctx, p, "shares", id, d, AnyRev)
		return err
	}
	deck := func(mode, md string) Doc {
		d := Doc{"tenant": "T", "room": "r", "md": md}
		if mode != "" {
			d[AgentsField] = mode
		}
		return d
	}
	for id, mode := range map[string]string{"mix": "", "ro": AgentsRead, "hid": AgentsHidden, "only": AgentsOnly} {
		if err := put(person, id, deck(mode, "# "+id)); err != nil {
			t.Fatal(id, err)
		}
	}
	items, err := s.Query(ctx, agent, Query{From: "shares"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.ID] = true
	}
	if seen["hid"] || !seen["mix"] || !seen["ro"] || !seen["only"] {
		t.Errorf("the assistant's list: %v", seen)
	}
	if _, _, err := s.Get(ctx, agent, "shares", "hid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the assistant read a hidden Story: %v", err)
	}
	if _, _, err := s.Get(ctx, person, "shares", "hid"); err != nil {
		t.Errorf("the person could not read a hidden Story: %v", err)
	}
	if err := put(agent, "ro", deck(AgentsRead, "# changed")); !errors.Is(err, ErrDenied) {
		t.Errorf("the assistant changed a read-only Story: %v", err)
	}
	if err := put(agent, "ro", deck(AgentsMixed, "# ro")); !errors.Is(err, ErrDenied) {
		t.Errorf("the assistant let itself in: %v", err)
	}
	if err := put(agent, "mix", deck("", "# changed")); err != nil {
		t.Errorf("the assistant could not change a mixed Story: %v", err)
	}
	if err := put(agent, "only", deck(AgentsOnly, "# changed")); err != nil {
		t.Errorf("the assistant could not change an agents only Story: %v", err)
	}
	if err := put(person, "only", deck(AgentsOnly, "# mine")); !errors.Is(err, ErrDenied) {
		t.Errorf("the person changed an agents only Story's text: %v", err)
	}
	if err := put(person, "only", deck(AgentsMixed, "# changed")); err != nil {
		t.Errorf("the person could not change its setting: %v", err)
	}
}
