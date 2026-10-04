// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// A SmartArt data model, sent like a picture (images, name *.xml) and
// referenced like one, is stored under its own type, laid out by the engine
// compiled into this server, and what it could not do comes back as warnings.
const STEPS = `<dgm:dataModel xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><dgm:ptLst><dgm:pt modelId="0" type="doc"><dgm:prSet loTypeId="urn:microsoft.com/office/officeart/2005/8/layout/process1"/></dgm:pt><dgm:pt modelId="1"><dgm:t><a:p><a:r><a:t>Suunnittelu</a:t></a:r></a:p></dgm:t></dgm:pt><dgm:pt modelId="2"><dgm:t><a:p><a:r><a:t>Toteutus</a:t></a:r></a:p></dgm:t></dgm:pt></dgm:ptLst><dgm:cxnLst><dgm:cxn srcId="0" destId="1"/><dgm:cxn srcId="0" destId="2"/></dgm:cxnLst></dgm:dataModel>`

const NOT_A_DIAGRAM = `<dgm:dataModel xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram"><dgm:ptLst/></dgm:dataModel>`

func TestSmartArtFile(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "## Steps\n\n![The steps](media/steps.xml)\n\n## Other\n\n![The steps again](media/steps.xml)\n{layout=gear1}\n\n## Broken\n\n![Broken](media/broken.xml)\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "SmartArt", "markdown": md,
		"images": []any{
			map[string]any{"name": "steps.xml", "data_base64": base64.StdEncoding.EncodeToString([]byte(STEPS))},
			map[string]any{"name": "broken.xml", "data_base64": base64.StdEncoding.EncodeToString([]byte(NOT_A_DIAGRAM))},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	id := out["deck_id"].(string)
	eq(t, out["slides"], 3)
	eq(t, f.bucket.saved["shares/"+id+"/media/steps.xml"].contentType, "application/vnd.openxmlformats-officedocument.drawingml.diagramData+xml")
	ws := strings.Join(toStrings(out["warnings"]), "\n")
	if !strings.Contains(ws, `The SmartArt media/steps.xml on slide "Other": the layout "gear1" is not one this engine has`) {
		t.Fatalf("no warning about the layout it does not have:\n%s", ws)
	}
	if !strings.Contains(ws, `The SmartArt media/broken.xml on slide "Broken" is not shown: there is no document point`) {
		t.Fatalf("no warning about the file that is not a diagram:\n%s", ws)
	}
	if strings.Contains(ws, `slide "Steps"`) {
		t.Fatalf("a warning about the diagram that is fine:\n%s", ws)
	}
	// drawn: the layout report has it as a diagram with text in it, set at
	// the size Open Sans (the server's font) fits, not the average-width
	// guess (81 px); and the message in the broken one's place reads on the
	// theme
	rep := textOf(c)
	if !strings.Contains(rep, "- diagram (steps.xml) at ") || !strings.Contains(rep, "2 labels, 115 shapes, smallest text 85 px") {
		t.Fatalf("the diagram is not in the layout report as one:\n%s", rep)
	}
	if strings.Contains(ws, "hard to read") {
		t.Fatalf("a SmartArt message that does not read:\n%s", ws)
	}
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1})
	if r.IsError {
		t.Fatal(textOf(r))
	}
}

func toStrings(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	if l, ok := v.([]string); ok {
		out = append(out, l...)
	}
	return out
}
