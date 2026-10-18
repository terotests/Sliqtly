package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/presdata"
)

// the sheets as write_workbook hands them to the writer
func xlsxSheets(t *testing.T, j string) []xlsxSheet {
	t.Helper()
	var sheets []xlsxSheet
	d := json.NewDecoder(strings.NewReader(j))
	d.UseNumber()
	if err := d.Decode(&sheets); err != nil {
		t.Fatal(err)
	}
	return sheets
}

// every part of the package, checked to be well-formed XML
func xlsxParts(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		x, _ := io.ReadAll(rc)
		rc.Close()
		d := xml.NewDecoder(bytes.NewReader(x))
		for {
			if _, err := d.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s: %v", zf.Name, err)
			}
		}
		parts[zf.Name] = string(x)
	}
	return parts
}

type lookCell struct {
	R, C         int
	Fill, Color  string
	Bold, Italic bool
}

type lookSheet struct {
	Name      string
	Freeze    int
	FilterCol int
	Widths    []int
	Cells     []lookCell
}

// the workbook as the editor's reader (datagrid's XlsxLoader, which
// EVGSheets is built on) sees its look
func editorLooks(t *testing.T, b []byte) []lookSheet {
	t.Helper()
	var out struct {
		Error  string
		Sheets []lookSheet
	}
	if err := json.Unmarshal([]byte(presdata.PresDataLook_static_looks(b)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	return out.Sheets
}

func cellAt(t *testing.T, s lookSheet, r, c int) lookCell {
	t.Helper()
	for _, x := range s.Cells {
		if x.R == r && x.C == c {
			return x
		}
	}
	t.Fatalf("no cell %d,%d in %s", r, c, s.Name)
	return lookCell{}
}

func TestWriteXlsxWithoutFormatsKeepsTheBoldHeaderOnly(t *testing.T) {
	b, err := writeXlsx(xlsxSheets(t, `[{"name":"A","rows":[["x","y"],[1,"b"]]}]`))
	if err != nil {
		t.Fatal(err)
	}
	p := xlsxParts(t, b)
	match(t, p["xl/styles.xml"], `<fonts count="2">.*<fills count="2">.*<cellXfs count="2">`)
	match(t, p["xl/worksheets/sheet1.xml"], `<worksheet [^>]*><sheetData><row r="1"><c r="A1" s="1" t="inlineStr">`)
	if strings.Contains(p["xl/worksheets/sheet1.xml"], "<cols>") || strings.Contains(p["xl/worksheets/sheet1.xml"], "autoFilter") {
		t.Fatal(p["xl/worksheets/sheet1.xml"])
	}
	s := editorLooks(t, b)[0]
	eq(t, cellAt(t, s, 0, 0).Bold, true)
	eq(t, cellAt(t, s, 1, 0).Bold, false)
	eq(t, cellAt(t, s, 1, 0).Fill, "")
}

func TestWriteXlsxFormatsRoundTripThroughTheEditorsReader(t *testing.T) {
	sheets := xlsxSheets(t, `[{"name":"Tasks","rows":[
		["Task","Priority","Status","Owner"],
		["Ship","High","Done","Ann"],
		["Test","High","Open",null],
		["Docs","Low","Open","Bo"]],
	 "formats":[
		{"range":"1:1","fill":"#1F4E79","color":"#ffffff"},
		{"range":"A2:D2","fill":"#C8E6C9","color":"#1B5E20"},
		{"range":"A3:D3","fill":"#FFCDD2","bold":true},
		{"range":"a4:d4","fill":"#FFF3E0","italic":true},
		{"range":"B:B","bold":true},
		{"range":"A4","fill":"#abc"},
		{"range":"F2","fill":"#C8E6C9"}],
	 "widths":[30,null,12,0],
	 "freeze":1,
	 "filter":true},
	 {"name":"Other's","rows":[["a"]],"formats":[{"range":"A1","fill":"#C8E6C9"}],"filter":true}]`)
	b, err := writeXlsx(sheets)
	if err != nil {
		t.Fatal(err)
	}
	p := xlsxParts(t, b)
	st := p["xl/styles.xml"]
	// one fill per colour, shared by both sheets; fonts once each
	eq(t, strings.Count(st, `<fgColor rgb="FFC8E6C9"/>`), 1)
	eq(t, strings.Count(st, `<font><sz val="11"/><color rgb="FF1B5E20"/><name val="Calibri"/></font>`), 1)
	match(t, st, `<fill><patternFill patternType="solid"><fgColor rgb="FF1F4E79"/><bgColor indexed="64"/></patternFill></fill>`)
	match(t, st, `<font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Calibri"/></font>`)
	sh := p["xl/worksheets/sheet1.xml"]
	match(t, sh, `<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>`)
	match(t, sh, `</sheetViews><cols><col min="1" max="1" width="30" customWidth="1"/><col min="3" max="3" width="12" customWidth="1"/></cols><sheetData>`)
	match(t, sh, `</sheetData><autoFilter ref="A1:D4"/></worksheet>`)
	// the empty owner cell and F2, past the data, are written for their fill
	match(t, sh, `<c r="D3" s="\d+"/></row>`)
	match(t, sh, `<c r="F2" s="\d+"/></row>`)
	match(t, p["xl/workbook.xml"], `<definedNames><definedName name="_xlnm._FilterDatabase" localSheetId="0" hidden="1">'Tasks'!\$A\$1:\$D\$4</definedName><definedName name="_xlnm._FilterDatabase" localSheetId="1" hidden="1">'Other''s'!\$A\$1:\$A\$1</definedName></definedNames>`)

	looks := editorLooks(t, b)
	s := looks[0]
	eq(t, s.Freeze, 1)
	eq(t, s.FilterCol, 0)
	if s.Widths[0] <= s.Widths[1] || s.Widths[2] <= s.Widths[1] || s.Widths[3] != s.Widths[1] {
		t.Fatalf("widths %v", s.Widths)
	}
	upper := func(c lookCell) lookCell {
		c.Fill, c.Color = strings.ToUpper(strings.TrimPrefix(c.Fill, "#")), strings.ToUpper(strings.TrimPrefix(c.Color, "#"))
		return c
	}
	for _, want := range []lookCell{
		{0, 0, "1F4E79", "FFFFFF", true, false},
		{0, 1, "1F4E79", "FFFFFF", true, false},
		{1, 0, "C8E6C9", "1B5E20", false, false},
		{1, 1, "C8E6C9", "1B5E20", true, false},
		{2, 3, "FFCDD2", "", true, false},
		{3, 0, "AABBCC", "", false, true},
		{3, 1, "FFF3E0", "", true, true},
		{3, 2, "FFF3E0", "", false, true},
	} {
		eq(t, upper(cellAt(t, s, want.R, want.C)), want)
	}
	eq(t, upper(cellAt(t, looks[1], 0, 0)), lookCell{0, 0, "C8E6C9", "", true, false})
}

func TestWriteXlsxNamesTheSheetAndEntryOfABadFormat(t *testing.T) {
	for _, bad := range []struct{ sheets, why string }{
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A1"},{"range":"A2","fill":"green"}]}]`, `Sheet "Tasks", formats\[1\]: fill "green" is not a colour like "#RRGGBB"`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A1","color":"#12345"}]}]`, `Sheet "Tasks", formats\[0\]: color "#12345" is not a colour`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A1:B","fill":"#ffffff"}]}]`, `Sheet "Tasks", formats\[0\]: range "A1:B" is not a cell range`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"ZZZZ1"}]}]`, `range "ZZZZ1" is not a cell range`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A0"}]}]`, `range "A0" is not a cell range`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"fill":"#ffffff"}]}]`, `formats\[0\]: needs a range`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A1","bold":"yes"}]}]`, `formats\[0\]: bold is true or false`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A1","background":"#ffffff"}]}]`, `"background" is not a format`},
		{`[{"name":"Tasks","rows":[["a"]],"formats":[{"range":"A1:Z100000","fill":"#ffffff"}]}]`, `formats\[0\]: the formats cover more than 200000 cells`},
		{`[{"name":"Tasks","rows":[["a"]],"widths":[10,"wide"]}]`, `Sheet "Tasks", widths\[1\]: a column width is a number`},
		{`[{"name":"Tasks","rows":[["a"]],"widths":[300]}]`, `widths\[0\]`},
		{`[{"name":"Tasks","rows":[["a"]],"freeze":-1}]`, `Sheet "Tasks": freeze is`},
	} {
		_, err := writeXlsx(xlsxSheets(t, bad.sheets))
		if err == nil {
			t.Fatalf("%s was written", bad.sheets)
		}
		match(t, err.Error(), bad.why)
	}
}
