// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"testing/fstest"
)

// the Personal package's server has the viewer built in, not the editor:
// its front page is the list of decks and no link offers ?edit
func TestViewerOnly(t *testing.T) {
	viewer := fstest.MapFS{"index.html": {}, "view.js": {}}
	editor := fstest.MapFS{"index.html": {}, "pres_app.js": {}, "view.js": {}}
	eq(t, viewerOnly(viewer), true)
	eq(t, viewerOnly(editor), false)
	eq(t, viewerOnly(fstest.MapFS{"index.html": {}}), false)
	eq(t, viewerOnly(nil), false)
}
