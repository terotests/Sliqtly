// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rdiff is RangerDiff's RdSmart (terotests/RangerDiff) compiled to
// Go by gen.mjs (rdsmart.go, generated): a delta of one version of a file
// against another. It picks a byte delta, a ZIP-part delta (XLSX, DOCX,
// PPTX) or a PNG delta, whichever is smallest.
//
// Only a byte delta rebuilds the very bytes it was made from; the other two
// rebuild the same parts or pixels in a new wrapper. Exact makes a delta
// that does rebuild them, which is what content-addressed blobs need.
package rdiff

import "errors"

// Diff is target as a delta against base.
func Diff(base, target []byte) []byte {
	return RdSmart_static_diff(base, target)
}

// Apply rebuilds the target of delta from base.
func Apply(base, delta []byte) ([]byte, error) {
	r := RdSmart_static_apply(base, delta)
	if !r.ok {
		if r.error == "" {
			return nil, errors.New("rdiff: delta does not apply")
		}
		return nil, errors.New("rdiff: " + r.error)
	}
	return r.data, nil
}

// Exact is target as a delta against base that Apply turns back into
// target byte for byte: RdSmart's own when it does, else a byte delta.
func Exact(base, target []byte) []byte {
	d := Diff(base, target)
	if RdSmart_static_exact(d) {
		return d
	}
	if got, err := Apply(base, d); err == nil && string(got) == string(target) {
		return d
	}
	return RdDelta_static_diff(base, target)
}
