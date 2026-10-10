// SPDX-License-Identifier: AGPL-3.0-or-later

package rdiff

import (
	"encoding/json"
	"errors"
)

// Delta is a text edit for real-time editing: RangerDiff's RdOt (src/RdOt.rgr,
// compiled into rdsmart.go), the code the page runs too. retain / insert /
// delete count UTF-16 code units; text is []uint16. The zero Delta is an
// empty edit.
type Delta struct{ d *RdOtDelta }

func (d Delta) delta() *RdOtDelta {
	if d.d == nil {
		return CreateNew_RdOtDelta()
	}
	return d.d
}

// Retain, Insert and Delete add to the edit (and return it).
func (d Delta) Retain(n int) Delta { x := d.delta(); x.retain(int64(n)); return Delta{x} }
func (d Delta) Insert(s []uint16) Delta {
	x := d.delta()
	x.insert(s)
	return Delta{x}
}
func (d Delta) Delete(n int) Delta { x := d.delta(); x.delete(int64(n)); return Delta{x} }

// Noop: the edit changes nothing.
func (d Delta) Noop() bool { return d.d == nil || d.d.isNoop() }

// Apply is text with the edit made.
func (d Delta) Apply(text []uint16) ([]uint16, error) {
	r := d.delta().tryApply(text)
	if !r.ok {
		return nil, errors.New(r.error)
	}
	return r.text, nil
}

// TransformIndex is where position at of the old text is in the new one;
// after: an insert exactly at it goes before it.
func (d Delta) TransformIndex(at int, after bool) int {
	return int(d.delta().transformIndex(int64(at), after))
}

// JSON is the Quill Delta form, without a trailing retain.
func (d Delta) JSON() json.RawMessage { return json.RawMessage(d.delta().toJson()) }

// Transform takes a and b made on one text to a' (after b) and b' (after a);
// a's insert goes first where both insert at one place.
func Transform(a, b Delta) (Delta, Delta) {
	p := RdOtDelta_static_transform(a.delta(), b.delta())
	return Delta{p.a}, Delta{p.b}
}

// TextDiff is b as one replace of a (the common start and end kept, never
// a surrogate pair cut).
func TextDiff(a, b []uint16) Delta { return Delta{RdOtDelta_static_diff(a, b, -1)} }

// ParseDelta reads an edit from its JSON form, decoded or not.
func ParseDelta(v any) (Delta, error) {
	raw, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return Delta{}, errors.New("ops: a list of {retain|insert|delete} is expected")
		}
	}
	p := CreateNew_RdOtJson(string(raw))
	d := p.delta()
	if !p.ok {
		return Delta{}, errors.New("ops: a list of {retain|insert|delete} is expected")
	}
	return Delta{d}, nil
}
