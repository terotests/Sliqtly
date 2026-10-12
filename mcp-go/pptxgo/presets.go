// SPDX-License-Identifier: AGPL-3.0-or-later

package pptxgo

import _ "embed"

// Presets is Office's preset shape catalogue, as RangerPPTX reads it
// (copied from Ranger's gallery/office/geom/assets by gen.mjs): handed to
// PptxGo_static_open, so an ellipse is drawn as one and not as its box.
//
//go:embed presets.txt
var Presets string
