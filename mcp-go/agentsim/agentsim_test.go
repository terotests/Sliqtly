// SPDX-License-Identifier: AGPL-3.0-or-later

package agentsim

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		r    Result
		err  error
		want Outcome
	}{
		{Result{Text: "Updated"}, nil, OK},
		{Result{Data: map[string]any{"claimed": false}}, nil, Busy},
		{Result{Data: map[string]any{"claimed": true}}, nil, OK},
		{Result{IsError: true, Text: "Too many presentations from here in a short time; try again in a few minutes."}, nil, Throttled},
		{Result{IsError: true, Text: "Not saved: since version abc someone else changed the same lines on slide 3"}, nil, Conflict},
		{Result{IsError: true, Text: "Not claimed: the claims of this presentation kept changing; call begin_work again."}, nil, Conflict},
		{Result{IsError: true, Text: "slide is a number from 1 to 4."}, nil, ToolError},
		{Result{}, errors.New("connection refused"), Failed},
	} {
		if got := Classify(c.r, c.err); got != c.want {
			t.Errorf("%q %v: %s, want %s", c.r.Text, c.err, got, c.want)
		}
	}
}

func TestPercentile(t *testing.T) {
	var d []time.Duration
	for i := 1; i <= 100; i++ {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	for p, want := range map[float64]time.Duration{50: 50 * time.Millisecond, 95: 95 * time.Millisecond, 99: 99 * time.Millisecond, 100: 100 * time.Millisecond, 0: time.Millisecond} {
		if got := Percentile(d, p); got != want {
			t.Errorf("p%v = %v, want %v", p, got, want)
		}
	}
	if Percentile(nil, 50) != 0 {
		t.Error("none")
	}
}

func TestSeedAndInsert(t *testing.T) {
	md := SeedMarkdown(0, 12)
	if strings.Count(md, "\n## Part ") != 12 || !strings.Contains(md, "## Part 01\n") || !strings.Contains(md, "## Part 12\n") {
		t.Fatalf("seed:\n%s", md)
	}
	got, ok := InsertAfterHeading(md, 1, "- a note")
	if !ok || !strings.Contains(got, "## Part 01\n- a note\n\n- Started") {
		t.Fatalf("insert:\n%s", got)
	}
	// part 1 is not part 10's prefix
	got, _ = InsertAfterHeading(got, 10, "- ten")
	if !strings.Contains(got, "## Part 10\n- ten\n") || strings.Count(got, "- ten") != 1 {
		t.Fatalf("part 10:\n%s", got)
	}
	if _, ok := InsertAfterHeading(md, 13, "x"); ok {
		t.Fatal("no part 13")
	}
}

func TestCheckMarkdown(t *testing.T) {
	md := "# T\n\n## Part 01\n- a\n- b\n- b\n- r\n"
	lost, dup, ghosts, landed := CheckMarkdown(md, []string{"- a", "- b", "- c"}, []string{"- r", "- s"}, []string{"- a", "- z"})
	if !reflect.DeepEqual(lost, []string{"- c"}) || !reflect.DeepEqual(dup, []string{"- b"}) || !reflect.DeepEqual(ghosts, []string{"- r"}) || landed != 1 {
		t.Fatalf("lost %v dup %v ghosts %v landed %d", lost, dup, ghosts, landed)
	}
	if m := Missing([]string{"x", "y"}, []string{"y"}); !reflect.DeepEqual(m, []string{"x"}) {
		t.Fatalf("missing %v", m)
	}
}

func TestReportKept(t *testing.T) {
	r := &Report{Decks: []DeckCheck{{Deck: "a"}, {Deck: "b"}}}
	if !r.Kept() {
		t.Fatal("clean decks")
	}
	r.Decks[1].CommentsMissing = []string{"c"}
	if r.Kept() {
		t.Fatal("a missing comment")
	}
	s := NewStats()
	s.Add("update_presentation", 10*time.Millisecond, OK, "")
	s.Add("update_presentation", 30*time.Millisecond, Conflict, "Not saved")
	s.Add("get_presentation", time.Millisecond, Failed, "HTTP 502")
	r.Tools = s.Summary()
	if r.Failures() != 1 || r.Tools[0].Tool != "update_presentation" || r.Tools[0].P50 != 10 {
		t.Fatalf("%+v", r.Tools)
	}
	if !strings.Contains(r.Text(), "comments 0 missing 1") {
		t.Fatal(r.Text())
	}
}
