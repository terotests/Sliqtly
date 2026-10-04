// SPDX-License-Identifier: AGPL-3.0-or-later

// A server of one's own can require a form for presentations' names: for
// example a ticket key first,
//
//	^([A-Z][A-Z0-9]+-[0-9]+) +\S     "ABC-1234 Quarterly review"
//
// It is set on the server's settings page (/settings) and kept in its
// folder (settings/naming); it is off until turned on there, and the cloud
// server has none. The pattern is a Go regular expression matched against
// the name; its first group, or else the whole match, is the name's key.
// The tools say the rule in their descriptions and in sliqtly_guide, refuse
// a title that does not follow it with the rule and an example, and
// list_presentations gives each deck's key, so a key can be searched for
// and indexed apart from the words.
//
// The editor does not enforce it: a deck made there keeps the name it is
// given, and the settings page lists the ones that do not follow the rule.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type nameRule struct {
	re      *regexp.Regexp
	example string // a name that follows it
	text    string // the rule in words; "" for one made from the pattern
}

func newNameRule(pattern, example, text string) (*nameRule, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("the pattern is empty")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("the pattern is not a regular expression: %w", err)
	}
	r := &nameRule{re: re, example: strings.TrimSpace(example), text: strings.TrimSpace(text)}
	if r.example != "" && !re.MatchString(r.example) {
		return nil, fmt.Errorf("the example %q does not match the pattern", r.example)
	}
	return r, nil
}

func (r *nameRule) describe() string {
	if r == nil {
		return ""
	}
	s := r.text
	if s == "" {
		s = "The name must match the regular expression " + r.re.String() + "."
	}
	if r.example != "" {
		s += " For example: \"" + r.example + "\"."
	}
	return s
}

func (r *nameRule) check(name string) string {
	if r == nil || r.re.MatchString(name) {
		return ""
	}
	return fmt.Sprintf("the name %q does not follow this server's naming rule. %s", name, r.describe())
}

func (r *nameRule) key(name string) string {
	if r == nil {
		return ""
	}
	m := r.re.FindStringSubmatch(name)
	switch {
	case m == nil:
		return ""
	case len(m) > 1:
		return m[1]
	}
	return m[0]
}

// what the settings page sets, as kept in settings/naming
type nameSettings struct {
	Enabled bool   `json:"enabled"`
	Pattern string `json:"pattern"`
	Example string `json:"example"`
	Rule    string `json:"rule"`
}

// what the page offers before anything is set: a ticket key first, off
var defaultNames = nameSettings{
	Pattern: `^([A-Z][A-Z0-9]+-[0-9]+) +\S`,
	Example: "ABC-1234 Quarterly review",
	Rule:    "Start the name with its ticket key (like ABC-1234), then a space and the name.",
}

// the rule the settings make, nil when it is off
func (n nameSettings) rule() (*nameRule, error) {
	if !n.Enabled {
		return nil, nil
	}
	return newNameRule(n.Pattern, n.Example, n.Rule)
}

func loadNameSettings(ctx context.Context, db DB) (nameSettings, error) {
	d, err := db.Get(ctx, "settings", "naming")
	if err != nil || d == nil {
		return defaultNames, err
	}
	b, _ := json.Marshal(d)
	n := defaultNames
	err = json.Unmarshal(b, &n)
	return n, err
}

func saveNameSettings(ctx context.Context, db DB, n nameSettings) error {
	return db.Set(ctx, "settings", "naming", Doc{"enabled": n.Enabled, "pattern": n.Pattern, "example": n.Example, "rule": n.Rule})
}
