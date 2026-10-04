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
	md := "## Steps\n\n![The steps](media/steps.xml)\n\n## Other\n\n![The steps again](media/steps.xml)\n{layout=noSuchLayout9}\n\n## Broken\n\n![Broken](media/broken.xml)\n"
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
	if !strings.Contains(ws, `The SmartArt media/steps.xml on slide "Other": the layout "noSuchLayout9" is not one this engine has`) {
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
	// read back from the deck's storage, where a file has no type with it:
	// still a diagram, drawn (it used to be taken for a broken PNG)
	if rr := lastText(r); !strings.Contains(rr, "- diagram (steps.xml) at ") || strings.Contains(rr, "picture (steps.xml)") {
		t.Fatalf("render_slide does not draw the stored SmartArt as a diagram:\n%s", rr)
	}
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": out["edit_key"], "markdown": md + "\n## More\n\nText.\n"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	if ur := textOf(u); !strings.Contains(ur, "- diagram (steps.xml) at ") {
		t.Fatalf("update_presentation does not see the stored SmartArt as a diagram:\n%s", ur)
	}
}

// A SmartArt file written as a link shows only the link's text: the server
// says so, and how to show it.
func TestSmartArtLinked(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Linked", "markdown": "## Steps\n\n[The steps](media/steps.xml)\n\n## Shown\n\n![The steps](media/steps.xml)\n",
		"images": []any{map[string]any{"name": "steps.xml", "data_base64": base64.StdEncoding.EncodeToString([]byte(STEPS))}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	ws := strings.Join(toStrings(sc(c)["warnings"]), "\n")
	if !strings.Contains(ws, "media/steps.xml is written as a link, [text](media/steps.xml), so the slide shows only its text. A SmartArt file is shown like a picture: ![text](media/steps.xml).") {
		t.Fatalf("no warning about the linked SmartArt:\n%s", ws)
	}
	if strings.Count(ws, "is written as a link") != 1 {
		t.Fatalf("the picture reference was taken for a link too:\n%s", ws)
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

// Phase 2's layouts through the server: a cycle, a pyramid, an organisation
// chart with an assistant, a radial and a block list, each laid out with
// nothing to warn about, reported as a diagram and rendered.
const ORG = `<dgm:dataModel xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><dgm:ptLst><dgm:pt modelId="0" type="doc"><dgm:prSet loTypeId="urn:microsoft.com/office/officeart/2005/8/layout/orgChart1"/></dgm:pt><dgm:pt modelId="a"><dgm:t><a:p><a:r><a:t>CEO</a:t></a:r></a:p></dgm:t></dgm:pt><dgm:pt modelId="s" type="asst"><dgm:t><a:p><a:r><a:t>Assistant</a:t></a:r></a:p></dgm:t></dgm:pt><dgm:pt modelId="b"><dgm:t><a:p><a:r><a:t>Sales</a:t></a:r></a:p></dgm:t></dgm:pt><dgm:pt modelId="c"><dgm:t><a:p><a:r><a:t>Finance</a:t></a:r></a:p></dgm:t></dgm:pt></dgm:ptLst><dgm:cxnLst><dgm:cxn srcId="0" destId="a"/><dgm:cxn srcId="a" destId="s"/><dgm:cxn srcId="a" destId="b"/><dgm:cxn srcId="a" destId="c"/></dgm:cxnLst></dgm:dataModel>`

func TestSmartArtLayouts(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "## Cycle\n\n![Cycle](media/steps.xml)\n{layout=cycle2}\n\n" +
		"## Pyramid\n\n![Pyramid](media/steps.xml)\n{layout=pyramid1}\n\n" +
		"## Blocks\n\n![Blocks](media/steps.xml)\n{layout=default}\n\n" +
		"## Org\n\n![Org](media/org.xml)\n\n" +
		"## Hierarchy\n\n![Hierarchy](media/org.xml)\n{layout=hierarchy1}\n\n" +
		"## Radial\n\n![Radial](media/org.xml)\n{layout=radial1}\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "SmartArt layouts", "markdown": md,
		"images": []any{
			map[string]any{"name": "steps.xml", "data_base64": base64.StdEncoding.EncodeToString([]byte(STEPS))},
			map[string]any{"name": "org.xml", "data_base64": base64.StdEncoding.EncodeToString([]byte(ORG))},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	eq(t, out["slides"], 6)
	ws := strings.Join(toStrings(out["warnings"]), "\n")
	if strings.Contains(ws, "SmartArt") {
		t.Fatalf("a SmartArt warning for a layout the engine has:\n%s", ws)
	}
	rep := textOf(c)
	if n := strings.Count(rep, "- diagram ("); n != 6 {
		t.Fatalf("%d diagrams in the layout report, want 6:\n%s", n, rep)
	}
	id := out["deck_id"].(string)
	for i := 1; i <= 6; i++ {
		r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": i})
		if r.IsError {
			t.Fatalf("slide %d: %s", i, textOf(r))
		}
	}
}

// A whole PowerPoint SmartArt as one file (Flat OPC: data, colours,
// drawing): taken, drawn as PowerPoint drew it, reported as a diagram, and
// read back from storage the same way.
func TestSmartArtWholeFile(t *testing.T) {
	part := func(ct, body string) string {
		return `<pkg:part pkg:name="/x.xml" pkg:contentType="application/vnd.` + ct + `+xml"><pkg:xmlData>` + body + `</pkg:xmlData></pkg:part>`
	}
	colors := `<dgm:colorsDef xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" uniqueId="urn:example/house"><dgm:styleLbl name="node1"><dgm:fillClrLst><a:srgbClr val="336699"/></dgm:fillClrLst><dgm:linClrLst/><dgm:txFillClrLst><a:schemeClr val="lt1"/></dgm:txFillClrLst></dgm:styleLbl></dgm:colorsDef>`
	sp := func(id, x, text string) string {
		return `<dsp:sp modelId="` + id + `"><dsp:nvSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvSpPr/></dsp:nvSpPr><dsp:spPr><a:xfrm><a:off x="` + x + `" y="0"/><a:ext cx="2540000" cy="1270000"/></a:xfrm><a:prstGeom prst="roundRect"><a:avLst/></a:prstGeom><a:solidFill><a:srgbClr val="336699"/></a:solidFill></dsp:spPr><dsp:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="2400"/><a:t>` + text + `</a:t></a:r></a:p></dsp:txBody></dsp:sp>`
	}
	drawing := `<dsp:drawing xmlns:dsp="http://schemas.microsoft.com/office/drawing/2008/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><dsp:spTree><dsp:nvGrpSpPr><dsp:cNvPr id="0" name=""/><dsp:cNvGrpSpPr/></dsp:nvGrpSpPr><dsp:grpSpPr/>` + sp("1", "0", "Suunnittelu") + sp("2", "3810000", "Toteutus") + `</dsp:spTree></dsp:drawing>`
	whole := `<?xml version="1.0" encoding="UTF-8"?><pkg:package xmlns:pkg="http://schemas.microsoft.com/office/2006/xmlPackage">` +
		part("openxmlformats-officedocument.drawingml.diagramData", STEPS) +
		part("openxmlformats-officedocument.drawingml.diagramColors", colors) +
		part("ms-office.drawingml.diagramDrawing", drawing) + `</pkg:package>`
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "## As drawn\n\n![The steps](media/whole.xml)\n\n## Laid out\n\n![The steps](media/whole.xml)\n{layout=chevron1}\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Whole", "markdown": md,
		"images": []any{map[string]any{"name": "whole.xml", "data_base64": base64.StdEncoding.EncodeToString([]byte(whole))}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	ws := strings.Join(toStrings(out["warnings"]), "\n")
	if strings.Contains(ws, "SmartArt") {
		t.Fatalf("a warning about a whole file that is fine:\n%s", ws)
	}
	rep := textOf(c)
	if strings.Count(rep, "- diagram (whole.xml) at ") != 2 {
		t.Fatalf("the whole file is not reported as a diagram on both slides:\n%s", rep)
	}
	id := out["deck_id"].(string)
	for i := 1; i <= 2; i++ {
		r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": i})
		if r.IsError {
			t.Fatal(textOf(r))
		}
		if rr := lastText(r); !strings.Contains(rr, "- diagram (whole.xml) at ") {
			t.Fatalf("slide %d read back is not a diagram:\n%s", i, rr)
		}
	}
}
