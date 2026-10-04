// SPDX-License-Identifier: AGPL-3.0-or-later

// Text deltas for real-time editing, as RangerDiff's RdOt has them (the
// browser runs that one): retain / insert / delete in UTF-16 code units, the
// Quill Delta JSON form
//
//	[{"retain":5},{"insert":"x"},{"delete":2}]
//
// The server needs four of RdOt's operations: apply an edit to the room's
// text, transform an edit made on an older revision over the ones taken
// since, move a caret over an edit, and the delta between two texts (an
// assistant's write turned into an edit). otdelta_test.go checks them on
// random edits the way RangerDiff's tests check RdOt.

package main

import (
	"errors"
	"fmt"
	"unicode/utf16"
)

const (
	opRetain = 1
	opInsert = 2
	opDelete = 3
)

type dop struct {
	kind int
	n    int
	s    []uint16
}

func (o dop) len() int {
	if o.kind == opInsert {
		return len(o.s)
	}
	return o.n
}

type delta []dop

func toU16(s string) []uint16   { return utf16.Encode([]rune(s)) }
func fromU16(u []uint16) string { return string(utf16.Decode(u)) }

func (d delta) retain(n int) delta {
	if n <= 0 {
		return d
	}
	if k := len(d); k > 0 && d[k-1].kind == opRetain {
		d[k-1].n += n
		return d
	}
	return append(d, dop{kind: opRetain, n: n})
}

// an insert goes before a delete it follows, so one edit is one delta
func (d delta) insert(s []uint16) delta {
	if len(s) == 0 {
		return d
	}
	k := len(d)
	if k > 0 && d[k-1].kind == opInsert {
		d[k-1].s = append(append([]uint16{}, d[k-1].s...), s...)
		return d
	}
	if k > 0 && d[k-1].kind == opDelete {
		if k > 1 && d[k-2].kind == opInsert {
			d[k-2].s = append(append([]uint16{}, d[k-2].s...), s...)
			return d
		}
		del := d[k-1]
		d = append(d[:k-1], dop{kind: opInsert, s: s}, del)
		return d
	}
	return append(d, dop{kind: opInsert, s: s})
}

func (d delta) delete(n int) delta {
	if n <= 0 {
		return d
	}
	if k := len(d); k > 0 && d[k-1].kind == opDelete {
		d[k-1].n += n
		return d
	}
	return append(d, dop{kind: opDelete, n: n})
}

func (d delta) add(o dop) delta {
	switch o.kind {
	case opRetain:
		return d.retain(o.n)
	case opInsert:
		return d.insert(o.s)
	}
	return d.delete(o.n)
}

// the length of the text it applies to (without a trailing retain left out)
func (d delta) baseLen() int {
	n := 0
	for _, o := range d {
		if o.kind != opInsert {
			n += o.n
		}
	}
	return n
}

func (d delta) noop() bool {
	for _, o := range d {
		if o.kind != opRetain {
			return false
		}
	}
	return true
}

func (d delta) padTo(n int) delta {
	return d.retain(n - d.baseLen())
}

// the JSON form, without a trailing retain
func (d delta) json() []any {
	out := []any{}
	for i, o := range d {
		switch o.kind {
		case opRetain:
			if i < len(d)-1 {
				out = append(out, map[string]any{"retain": o.n})
			}
		case opInsert:
			out = append(out, map[string]any{"insert": fromU16(o.s)})
		case opDelete:
			out = append(out, map[string]any{"delete": o.n})
		}
	}
	return out
}

func count(v any) (int, bool) {
	switch x := v.(type) {
	case int64:
		return int(x), x >= 0
	case float64:
		return int(x), x >= 0 && x == float64(int(x))
	case int:
		return x, x >= 0
	}
	return 0, false
}

// a delta from its JSON form (decoded with decodeValue or plain)
func parseDelta(v any) (delta, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("ops: a list is expected")
	}
	var d delta
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok || len(m) != 1 {
			return nil, errors.New("ops: each one is {retain|insert|delete}")
		}
		if r, has := m["retain"]; has {
			n, ok := count(r)
			if !ok {
				return nil, errors.New("ops: retain is a count")
			}
			d = d.retain(n)
		} else if s, has := m["insert"]; has {
			str, ok := s.(string)
			if !ok {
				return nil, errors.New("ops: insert is a string")
			}
			d = d.insert(toU16(str))
		} else if r, has := m["delete"]; has {
			n, ok := count(r)
			if !ok {
				return nil, errors.New("ops: delete is a count")
			}
			d = d.delete(n)
		} else {
			return nil, errors.New("ops: each one is {retain|insert|delete}")
		}
	}
	return d, nil
}

func (d delta) apply(text []uint16) ([]uint16, error) {
	if d.baseLen() > len(text) {
		return nil, fmt.Errorf("the edit is for a text of %d or more, this one has %d", d.baseLen(), len(text))
	}
	out := make([]uint16, 0, len(text))
	pos := 0
	for _, o := range d {
		switch o.kind {
		case opRetain:
			out = append(out, text[pos:pos+o.n]...)
			pos += o.n
		case opInsert:
			out = append(out, o.s...)
		case opDelete:
			pos += o.n
		}
	}
	return append(out, text[pos:]...), nil
}

// reads a delta's ops a piece at a time
type dreader struct {
	d   delta
	i   int
	off int
}

func (r *dreader) more() bool { return r.i < len(r.d) }
func (r *dreader) kind() int {
	if r.i >= len(r.d) {
		return 0
	}
	return r.d[r.i].kind
}
func (r *dreader) left() int {
	if r.i >= len(r.d) {
		return 0
	}
	return r.d[r.i].len() - r.off
}
func (r *dreader) take(n int) dop {
	o := r.d[r.i]
	if n > o.len()-r.off {
		n = o.len() - r.off
	}
	p := dop{kind: o.kind, n: n}
	if o.kind == opInsert {
		p.s = o.s[r.off : r.off+n]
		p.n = 0
	}
	r.off += n
	if r.off >= o.len() {
		r.i++
		r.off = 0
	}
	return p
}

// a and b on one text -> a' (after b) and b' (after a); a's insert goes
// first where both insert at one place
func transform(a, b delta) (delta, delta) {
	n := max(a.baseLen(), b.baseLen())
	a = append(delta{}, a...).padTo(n)
	b = append(delta{}, b...).padTo(n)
	var a1, b1 delta
	ra, rb := &dreader{d: a}, &dreader{d: b}
	for ra.more() || rb.more() {
		if ra.kind() == opInsert {
			s := ra.take(ra.left()).s
			a1 = a1.insert(s)
			b1 = b1.retain(len(s))
			continue
		}
		if rb.kind() == opInsert {
			s := rb.take(rb.left()).s
			a1 = a1.retain(len(s))
			b1 = b1.insert(s)
			continue
		}
		if !ra.more() || !rb.more() {
			break
		}
		k := min(ra.left(), rb.left())
		x, y := ra.take(k), rb.take(k)
		switch {
		case x.kind == opRetain && y.kind == opRetain:
			a1 = a1.retain(k)
			b1 = b1.retain(k)
		case x.kind == opDelete && y.kind == opRetain:
			a1 = a1.delete(k)
		case x.kind == opRetain && y.kind == opDelete:
			b1 = b1.delete(k)
		}
	}
	return a1, b1
}

// where position `at` of the old text is in the new one; `after`: an insert
// exactly at it goes before it
func (d delta) transformIndex(at int, after bool) int {
	pos, out := 0, at
	for _, o := range d {
		if pos > at {
			break
		}
		switch o.kind {
		case opRetain:
			pos += o.n
		case opInsert:
			if pos < at || (pos == at && after) {
				out += len(o.s)
			}
		case opDelete:
			if pos < at {
				out -= min(o.n, at-pos)
			}
			pos += o.n
		}
	}
	return out
}

func lowSurrogate(c uint16) bool { return c >= 0xDC00 && c <= 0xDFFF }

// one replace: the common start and end kept, never a surrogate pair cut
func diffU16(a, b []uint16) delta {
	most := min(len(a), len(b))
	p := 0
	for p < most && a[p] == b[p] {
		p++
	}
	if p > 0 && ((p < len(a) && lowSurrogate(a[p])) || (p < len(b) && lowSurrogate(b[p]))) {
		p--
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	for s > 0 && lowSurrogate(a[len(a)-s]) {
		s--
	}
	var d delta
	d = d.retain(p)
	d = d.insert(b[p : len(b)-s])
	d = d.delete(len(a) - p - s)
	return d.retain(s)
}
