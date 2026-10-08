// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

// A slide's own data in its heading — `## Revenue {jira=ACME-412}` — is kept
// with the slide, printed where a header or footer asks for it, drawn
// nowhere else, and shown in the layout report so an assistant revising the
// deck can see which slide is about which ticket. A key one character from
// one the engine knows is kept too, and said so.
func TestSlideMetadataInHeading(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "---\nslide-split-level: 2\njira: ACME-400\nowner: Tero\nfooter-right: \"{jira} · {page} / {pages}\"\n---\n\n" +
		"## Revenue {jira=ACME-412 owner=Tero}\n\nUp 18 %.\n\n" +
		"## Costs {jira=ACME-413}\n\nFlat.\n\n" +
		"## Outlook {transtion=fade}\n\nGood.\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Q3", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	eq(t, out["slides"], 3)

	// the presentation's own data, from the front matter keys Sliqtly does
	// not read; title and the footer's keys are not data
	eq(t, out["data"], "jira=ACME-400 owner=Tero")
	rep := textOf(c)
	if !strings.Contains(rep, "Presentation data: jira=ACME-400 owner=Tero") {
		t.Fatalf("the presentation's data is not in the result:\n%s", rep)
	}
	if !strings.Contains(rep, "- data: jira=ACME-412 owner=Tero") {
		t.Fatalf("the first slide's data is not in the report:\n%s", rep)
	}
	if !strings.Contains(rep, "- data: jira=ACME-413") {
		t.Fatalf("the second slide's data is not in the report:\n%s", rep)
	}
	// the report lists the slide's elements, not its header and footer; what
	// the footer prints is checked where the frames are (PresCheck.slideMeta)
	if strings.Contains(rep, "{jira}") {
		t.Fatalf("a placeholder was printed as itself:\n%s", rep)
	}

	ws := strings.Join(toStrings(out["warnings"]), "\n")
	if !strings.Contains(ws, "transtion is not a key Sliqtly knows") || !strings.Contains(ws, "Did you mean transition?") {
		t.Fatalf("no warning about the misspelt key:\n%s", ws)
	}
	if strings.Contains(ws, "jira") {
		t.Fatalf("a warning about a key that is plainly data:\n%s", ws)
	}
}
