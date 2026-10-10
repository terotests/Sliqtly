// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"strings"
	"testing"
)

// `::: col` blocks inside a `::: columns` with as many colons: the first
// `:::` closes the outer container (as markdown-it does), and the last one
// is shown on the slide as text. create says so and names the form that
// nests (`:::: columns`); that form gets no such warning.
func TestColumnsFenceWarning(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	same := "# Deck\n\n## Two\n\n::: columns\n::: col A\nx\n:::\n::: col B\ny\n:::\n:::\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": same})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	w := fmt.Sprint(sc(c)["warnings"])
	match(t, w, "A `:::` line on slide \"Two\" closes no container.*`:::: columns`")
	four := strings.Replace(strings.Replace(same, "::: columns", ":::: columns", 1), ":::\n:::\n", ":::\n::::\n", 1)
	c4 := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": four})
	if strings.Contains(fmt.Sprint(sc(c4)["warnings"]), "closes no container") {
		t.Fatal("warned about :::: columns:", sc(c4)["warnings"])
	}
}
