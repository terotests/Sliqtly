// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestStory(t *testing.T) {
	// a document from before stories: a story, active
	if got := StoryOf(Doc{}); !reflect.DeepEqual(got, Story{Type: "story", Phase: "active", Tags: []string{}}) {
		t.Fatal(got)
	}
	d := Doc{StoryField: map[string]any{"type": "nope", "phase": "done", "tags": []any{"A", " a ", 3, "B  c"}, "color": "#ABCDEF"}}
	if got := StoryOf(d); !reflect.DeepEqual(got, Story{Type: "story", Phase: "done", Tags: []string{"A", "B c"}, Color: "#abcdef"}) {
		t.Fatal(got)
	}
	ty, empty := "review", ""
	s, err := StoryChange{Type: &ty, Color: &empty}.Apply(StoryOf(d))
	if err != nil || s.Type != "review" || s.Color != "" || s.Phase != "done" {
		t.Fatal(s, err)
	}
	bad := "folder"
	if _, err := (StoryChange{Phase: &bad}).Apply(s); !errors.Is(err, ErrStory) {
		t.Fatal(err)
	}
	red := "red"
	if _, err := (StoryChange{Color: &red}).Apply(s); !errors.Is(err, ErrStory) {
		t.Fatal(err)
	}
	// kept and read back
	if got := StoryOf(Doc{StoryField: StoryDoc(s)}); !reflect.DeepEqual(got, s) {
		t.Fatal(got)
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = strings.Repeat("x", i+1)
	}
	if n := len(CleanTags(many)); n != MaxTags {
		t.Fatal(n)
	}
	if long := CleanTags([]string{strings.Repeat("é", 50)})[0]; len([]rune(long)) != MaxTagLen {
		t.Fatal(len([]rune(long)))
	}
}
