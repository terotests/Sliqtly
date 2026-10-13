// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A presentation is a story of its room (its topic): what kind of story it
// is (Type), where it is in its life (Phase), tags of one's own, and a
// colour over the type's. They are kept on the share document under
// StoryField ({"type", "phase", "tags", "color"}), beside its room and
// folder; none of it is in the deck's own text. A document with none is
// what every presentation was before stories: type "story", phase
// "active". A new one starts as a draft (HomeRooms). The page's model is
// web/stories.js; the lists here are the same.

// StoryField holds a document's story: type, phase, tags and colour.
const StoryField = "story"

// StoryTypes and StoryPhases as the page and the assistant name them.
var (
	StoryTypes  = []string{"review", "problem", "task", "pitch", "report", "lesson", "demo", "decision", "survey", "story"}
	StoryPhases = []string{"draft", "active", "review", "done", "archived"}
)

// What a story with nothing said is, and what a new one starts as.
const (
	DefaultStoryType  = "story"
	DefaultStoryPhase = "active"
	NewStoryPhase     = "draft"
)

// MaxTags a story has, each at most MaxTagLen runes.
const (
	MaxTags   = 20
	MaxTagLen = 40
)

// Story is a document's story.
type Story struct {
	Type  string   `json:"type"`
	Phase string   `json:"phase"`
	Tags  []string `json:"tags"`
	Color string   `json:"color,omitempty"`
}

// ErrStory: a type or phase not in the lists, or a colour that is not #rrggbb.
var ErrStory = errors.New("store: not a story type, phase or colour")

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func oneOf(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// StoryOf is the document's story; what it does not say (or says wrong) is
// the default's.
func StoryOf(d Doc) Story {
	s := Story{Type: DefaultStoryType, Phase: DefaultStoryPhase, Tags: []string{}}
	m, _ := d[StoryField].(map[string]any)
	if v, _ := m["type"].(string); oneOf(StoryTypes, v) {
		s.Type = v
	}
	if v, _ := m["phase"].(string); oneOf(StoryPhases, v) {
		s.Phase = v
	}
	if v, _ := m["color"].(string); hexColor.MatchString(v) {
		s.Color = strings.ToLower(v)
	}
	var tags []string
	switch list := m["tags"].(type) {
	case []any:
		for _, t := range list {
			if t, ok := t.(string); ok {
				tags = append(tags, t)
			}
		}
	case []string:
		tags = list
	}
	s.Tags = CleanTags(tags)
	return s
}

// CleanTags: each tag with one space between words and at most MaxTagLen
// runes, the empty ones and repeats (any case) left out, at most MaxTags.
func CleanTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.Join(strings.Fields(t), " ")
		if utf8.RuneCountInString(t) > MaxTagLen {
			t = strings.TrimSpace(string([]rune(t)[:MaxTagLen]))
		}
		k := strings.ToLower(t)
		if t == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
		if len(out) == MaxTags {
			break
		}
	}
	return out
}

// StoryChange is what set_story changes; nil leaves that part as it is, and
// an empty Color goes back to the type's.
type StoryChange struct {
	Type, Phase, Color *string
	Tags               *[]string
}

// Apply is s with the change, or ErrStory for a type, phase or colour that
// is not one.
func (c StoryChange) Apply(s Story) (Story, error) {
	if c.Type != nil {
		if !oneOf(StoryTypes, *c.Type) {
			return s, ErrStory
		}
		s.Type = *c.Type
	}
	if c.Phase != nil {
		if !oneOf(StoryPhases, *c.Phase) {
			return s, ErrStory
		}
		s.Phase = *c.Phase
	}
	if c.Color != nil {
		if *c.Color != "" && !hexColor.MatchString(*c.Color) {
			return s, ErrStory
		}
		s.Color = strings.ToLower(*c.Color)
	}
	if c.Tags != nil {
		s.Tags = CleanTags(*c.Tags)
	}
	return s, nil
}

// StoryDoc is the story as the document keeps it.
func StoryDoc(s Story) map[string]any {
	tags := make([]any, 0, len(s.Tags))
	for _, t := range s.Tags {
		tags = append(tags, t)
	}
	m := map[string]any{"type": s.Type, "phase": s.Phase, "tags": tags}
	if s.Color != "" {
		m["color"] = s.Color
	}
	return m
}
