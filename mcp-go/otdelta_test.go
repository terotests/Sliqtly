// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"math/rand"
	"testing"
)

var otPieces = []string{"a", "b", "xy", " ", "\n", "## ", "ä", "😀", "👨‍👩‍👧", "\"q\"", "\\"}

func otRandomString(r *rand.Rand, n int) []uint16 {
	s := ""
	for i := 0; i < n; i++ {
		s += otPieces[r.Intn(len(otPieces))]
	}
	return toU16(s)
}

func otWhole(s []uint16, at int) int {
	if at > 0 && at < len(s) && lowSurrogate(s[at]) {
		return at - 1
	}
	return at
}

// a few replaces at random places, cut at whole characters
func otRandomDelta(r *rand.Rand, s []uint16) delta {
	n := len(s)
	var d delta
	pos := 0
	for k, edits := 0, 1+r.Intn(3); k < edits; k++ {
		at := otWhole(s, pos+r.Intn(n-pos+1))
		if at < pos {
			at = pos
		}
		del := min(r.Intn(4), n-at)
		del = max(otWhole(s, at+del)-at, 0)
		d = d.retain(at - pos)
		if r.Intn(3) > 0 {
			d = d.insert(otRandomString(r, 1+r.Intn(3)))
		}
		d = d.delete(del)
		pos = at + del
	}
	return d.retain(n - pos)
}

func mustApply(t *testing.T, d delta, s []uint16) []uint16 {
	t.Helper()
	out, err := d.apply(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOtBasics(t *testing.T) {
	ab := toU16("ab")
	a := diffU16(ab, toU16("aXb"))
	b := diffU16(ab, toU16("aYb"))
	a1, b1 := transform(a, b)
	if got := fromU16(mustApply(t, b1, mustApply(t, a, ab))); got != "aXYb" {
		t.Fatalf("a first after a: %q", got)
	}
	if got := fromU16(mustApply(t, a1, mustApply(t, b, ab))); got != "aXYb" {
		t.Fatalf("a first after b: %q", got)
	}
	ins := diffU16(toU16("abc"), toU16("aZZbc"))
	if ins.transformIndex(1, false) != 1 || ins.transformIndex(1, true) != 3 || ins.transformIndex(2, false) != 4 {
		t.Fatal("transformIndex around an insert")
	}
	del := diffU16(toU16("abcdef"), toU16("af"))
	if del.transformIndex(3, false) != 1 || del.transformIndex(6, false) != 2 {
		t.Fatal("transformIndex around a delete")
	}
	s := diffU16(toU16("x😀y"), toU16("x😁y"))
	j, _ := json.Marshal(s.json())
	if string(j) != `[{"retain":1},{"insert":"😁"},{"delete":2}]` {
		t.Fatalf("surrogate pair whole: %s", j)
	}
	// the JSON RdOt writes, read back
	var v any
	json.Unmarshal([]byte(`[{"retain":3},{"insert":"a\"b\n😀"},{"delete":2}]`), &v)
	d, err := parseDelta(v)
	if err != nil || len(d) != 3 || fromU16(d[1].s) != "a\"b\n😀" {
		t.Fatalf("parse: %v %v", d, err)
	}
	for _, bad := range []string{`{}`, `[{"bogus":1}]`, `[{"retain":-1}]`, `[{"retain":1.5}]`, `[{"insert":3}]`, `[{"retain":1,"delete":1}]`} {
		json.Unmarshal([]byte(bad), &v)
		if _, err := parseDelta(v); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	long := delta{}.retain(10)
	if _, err := long.apply(toU16("short")); err == nil {
		t.Fatal("a delta for a longer text applied")
	}
}

func TestOtProperties(t *testing.T) {
	r := rand.New(rand.NewSource(4242))
	for round := 0; round < 3000; round++ {
		s := otRandomString(r, r.Intn(30))
		a, b := otRandomDelta(r, s), otRandomDelta(r, s)
		sa, sb := mustApply(t, a, s), mustApply(t, b, s)
		a1, b1 := transform(a, b)
		ab, ba := fromU16(mustApply(t, b1, sa)), fromU16(mustApply(t, a1, sb))
		if ab != ba {
			t.Fatalf("TP1 on %q: %q vs %q", fromU16(s), ab, ba)
		}
		if fromU16(mustApply(t, diffU16(s, sa), s)) != fromU16(sa) {
			t.Fatal("diff")
		}
		// JSON round trip
		j, _ := json.Marshal(a.json())
		var v any
		json.Unmarshal(j, &v)
		back, err := parseDelta(v)
		if err != nil || fromU16(mustApply(t, back, s)) != fromU16(sa) {
			t.Fatalf("JSON %s: %v", j, err)
		}
		// a character outside every delete keeps its place
		if len(s) > 0 {
			at := r.Intn(len(s))
			survives, pos := true, 0
			for _, o := range a {
				if o.kind == opDelete && at >= pos && at < pos+o.n {
					survives = false
				}
				if o.kind != opInsert {
					pos += o.n
				}
			}
			if survives && sa[a.transformIndex(at, true)] != s[at] {
				t.Fatal("index transform lost the character")
			}
		}
	}
}

// edits made on an old revision, transformed over the ones taken since,
// give every client the server's text (the server's half of the protocol)
func TestOtStaleChain(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for round := 0; round < 300; round++ {
		base := otRandomString(r, 10+r.Intn(20))
		text := base
		var log []delta
		for i := 0; i < 1+r.Intn(5); i++ {
			d := otRandomDelta(r, text)
			text = mustApply(t, d, text)
			log = append(log, d)
		}
		stale := otRandomDelta(r, base)
		for _, l := range log {
			stale, _ = transform(stale, l)
		}
		if _, err := stale.apply(text); err != nil {
			t.Fatalf("a transformed stale edit does not apply: %v", err)
		}
	}
}
