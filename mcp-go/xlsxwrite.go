// write_workbook's .xlsx: values only, the first row bold, as
// mcp/src/xlsx.js writeWorkbook writes it.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var xlsxNumber = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$`)

func xlsxText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			// not allowed in XML 1.0
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func xlsxCol(i int) string {
	s := ""
	for n := i + 1; n > 0; n = (n - 1) / 26 {
		s = string(rune('A'+(n-1)%26)) + s
	}
	return s
}

// a cell's value: a number as it is written in <v>, ok false for text
func xlsxNum(v any) (string, bool) {
	switch x := v.(type) {
	case json.Number:
		return x.String(), true
	case string:
		t := strings.TrimSpace(x)
		if xlsxNumber.MatchString(t) {
			return t, true
		}
	}
	return "", false
}

func writeXlsx(sheets []struct {
	Name string  `json:"name"`
	Rows [][]any `json:"rows"`
}) ([]byte, error) {
	const head = "<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n"
	const ns = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	const rel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	var over, names, rels strings.Builder
	for i, s := range sheets {
		fmt.Fprintf(&over, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
		fmt.Fprintf(&names, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xlsxText(s.Name), i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%s/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, rel, i+1)
	}
	parts := [][2]string{
		{"[Content_Types].xml", head + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` + over.String() + "</Types>"},
		{"_rels/.rels", head + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="` + rel + `/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", head + `<workbook ` + ns + ` xmlns:r="` + rel + `"><sheets>` + names.String() + "</sheets></workbook>"},
		{"xl/_rels/workbook.xml.rels", head + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels.String() + fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1, rel)},
		{"xl/styles.xml", head + `<styleSheet ` + ns + `><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs></styleSheet>`},
	}
	for i, s := range sheets {
		var b strings.Builder
		b.WriteString(head + `<worksheet ` + ns + `><sheetData>`)
		for r, row := range s.Rows {
			fmt.Fprintf(&b, `<row r="%d">`, r+1)
			style := ""
			if r == 0 {
				style = ` s="1"`
			}
			for c, v := range row {
				if v == nil {
					continue
				}
				ref := xlsxCol(c) + strconv.Itoa(r+1)
				if n, ok := xlsxNum(v); ok {
					fmt.Fprintf(&b, `<c r="%s"%s><v>%s</v></c>`, ref, style, n)
					continue
				}
				t := fmt.Sprint(v)
				if t == "" {
					continue
				}
				fmt.Fprintf(&b, `<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xlsxText(t))
			}
			b.WriteString("</row>")
		}
		b.WriteString("</sheetData></worksheet>")
		parts = append(parts, [2]string{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), b.String()})
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, p := range parts {
		w, err := z.Create(p[0])
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(p[1])); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// XlsxWrite keeps the workbook of sheetsJSON ([{name, rows}]) as bytes:
// {"handle":n,"size":n}
func (h *McpHost) XlsxWrite(sheetsJSON string) string {
	var sheets []struct {
		Name string  `json:"name"`
		Rows [][]any `json:"rows"`
	}
	d := json.NewDecoder(strings.NewReader(sheetsJSON))
	d.UseNumber()
	if err := d.Decode(&sheets); err != nil {
		h.fail(err)
		return `{"handle":0,"size":0}`
	}
	b, err := writeXlsx(sheets)
	if err != nil {
		h.fail(err)
		return `{"handle":0,"size":0}`
	}
	return toJSON(map[string]any{"handle": h.keep(b), "size": len(b)})
}
