// SPDX-License-Identifier: AGPL-3.0-or-later

package rdiff

import (
	"encoding/json"
	"testing"
	"unicode/utf16"
)

// RdOt itself is tested on Go in RangerDiff (npm run test:go); these check
// the wrapper: counts in UTF-16 units, the JSON form both ways, refusals.

func u16(s string) []uint16 { return utf16.Encode([]rune(s)) }
func str(u []uint16) string { return string(utf16.Decode(u)) }

func TestOtWrapper(t *testing.T) {
	s := TextDiff(u16("x😀y"), u16("x😁y"))
	if j, _ := json.Marshal(map[string]any{"ops": s.JSON()}); string(j) != `{"ops":[{"retain":1},{"insert":"😁"},{"delete":2}]}` {
		t.Fatalf("surrogate pair whole, UTF-16 counts: %s", j)
	}
	ab := u16("ab")
	a, b := TextDiff(ab, u16("aXb")), TextDiff(ab, u16("aYb"))
	a1, b1 := Transform(a, b)
	sa, _ := a.Apply(ab)
	sb, _ := b.Apply(ab)
	x, _ := b1.Apply(sa)
	y, _ := a1.Apply(sb)
	if str(x) != "aXYb" || str(y) != "aXYb" {
		t.Fatalf("transform: %q %q", str(x), str(y))
	}
	ins := TextDiff(u16("abc"), u16("aZZbc"))
	if ins.TransformIndex(1, false) != 1 || ins.TransformIndex(1, true) != 3 || ins.TransformIndex(2, false) != 4 {
		t.Fatal("TransformIndex around an insert")
	}
	built := Delta{}.Retain(2).Insert(u16("é😀")).Delete(1)
	if got, _ := built.Apply(u16("abcd")); str(got) != "abé😀d" {
		t.Fatalf("built: %q", str(got))
	}
	if !(Delta{}).Noop() || built.Noop() {
		t.Fatal("Noop")
	}
	if _, err := (Delta{}).Retain(10).Apply(u16("short")); err == nil {
		t.Fatal("a delta for a longer text applied")
	}

	var v any
	json.Unmarshal([]byte(`[{"retain":3},{"insert":"a\"b\n😀😁"},{"delete":2}]`), &v)
	d, err := ParseDelta(v)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Apply(u16("012345")); str(got) != "012a\"b\n😀😁5" {
		t.Fatalf("parse: %q", str(got))
	}
	for _, bad := range []string{`{}`, `[{"bogus":1}]`, `[{"retain":-1}]`, `[{"retain":1.5}]`, `[{"insert":3}]`, `[{"retain":1,"delete":1}]`} {
		json.Unmarshal([]byte(bad), &v)
		if _, err := ParseDelta(v); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}
