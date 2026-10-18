// write_workbook's .xlsx: values and formulas, the first row bold, as
// mcp/src/xlsx.js writeWorkbook writes it; and what a sheet may say of its
// look: cells' fill, font colour, bold and italic (formats), column widths,
// header rows frozen and an autofilter over the header.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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

// one sheet as write_workbook gives it (rgr/Tools.rgr checks the kinds of
// formats, widths, freeze and filter; their content is checked here)
type xlsxSheet struct {
	Name    string           `json:"name"`
	Rows    [][]any          `json:"rows"`
	Formats []map[string]any `json:"formats"`
	Widths  []any            `json:"widths"`
	Freeze  int              `json:"freeze"`
	Filter  bool             `json:"filter"`
}

const (
	xlsxMaxRow = 1048576
	xlsxMaxCol = 16384
	// cells the formats of one workbook may cover, each written as a <c>
	xlsxMaxStyled = 200000
)

// how a cell looks: "" colours are the default
type xlsxLook struct {
	fill, color  string
	bold, italic bool
}

var xlsxColour = regexp.MustCompile(`^#?([0-9A-Fa-f]{6}|[0-9A-Fa-f]{3})$`)

// "#2e7d32" or "#2E7" → "FF2E7D32" (ARGB, as styles.xml has it)
func xlsxRGB(s string) (string, bool) {
	m := xlsxColour.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	h := strings.ToUpper(m[1])
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	return "FF" + h, true
}

var xlsxRef = regexp.MustCompile(`^([A-Z]*)([0-9]*)$`)

// "C" → 2, "" → -1
func xlsxColIndex(s string) int {
	if s == "" {
		return -1
	}
	n := 0
	for _, r := range s {
		n = n*26 + int(r-'A') + 1
		if n > xlsxMaxCol {
			return xlsxMaxCol
		}
	}
	return n - 1
}

// one end of a range: col and row from 0, -1 where the end leaves it out
func xlsxEnd(s string) (col, row int, ok bool) {
	m := xlsxRef.FindStringSubmatch(strings.ReplaceAll(s, "$", ""))
	if m == nil || (m[1] == "" && m[2] == "") {
		return 0, 0, false
	}
	col, row = xlsxColIndex(m[1]), -1
	if m[2] != "" {
		n, err := strconv.Atoi(m[2])
		if err != nil || n < 1 || n > xlsxMaxRow {
			return 0, 0, false
		}
		row = n - 1
	}
	if col >= xlsxMaxCol {
		return 0, 0, false
	}
	return col, row, true
}

// "A2:J2", "A2", whole rows "2:3", whole columns "C:E": the first and last
// row and column from 0; a whole row spans the sheet's lastCol columns, a
// whole column its lastRow rows
func xlsxRange(s string, lastRow, lastCol int) (r0, c0, r1, c1 int, ok bool) {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(s)), ":")
	if len(parts) > 2 {
		return 0, 0, 0, 0, false
	}
	if len(parts) == 1 {
		parts = append(parts, parts[0])
	}
	ca, ra, ok1 := xlsxEnd(parts[0])
	cb, rb, ok2 := xlsxEnd(parts[1])
	if !ok1 || !ok2 {
		return 0, 0, 0, 0, false
	}
	switch {
	case ca >= 0 && ra >= 0 && cb >= 0 && rb >= 0: // A2:J5
	case ca < 0 && cb < 0 && ra >= 0 && rb >= 0: // 2:5
		ca, cb = 0, lastCol
	case ra < 0 && rb < 0 && ca >= 0 && cb >= 0: // C:E
		ra, rb = 0, lastRow
	default:
		return 0, 0, 0, 0, false
	}
	if ra > rb {
		ra, rb = rb, ra
	}
	if ca > cb {
		ca, cb = cb, ca
	}
	return ra, ca, rb, cb, true
}

// the sheet's formats laid over its cells, later entries winning per
// property; the header row starts bold
func xlsxLooks(s xlsxSheet, budget *int) (map[[2]int]xlsxLook, error) {
	looks := map[[2]int]xlsxLook{}
	lastRow, lastCol := len(s.Rows)-1, 0
	for _, row := range s.Rows {
		if len(row)-1 > lastCol {
			lastCol = len(row) - 1
		}
	}
	if lastRow < 0 {
		lastRow = 0
	}
	for i, f := range s.Formats {
		where := fmt.Sprintf("Sheet %q, formats[%d]", s.Name, i)
		rs, isText := f["range"].(string)
		if !isText {
			return nil, fmt.Errorf(`%s: needs a range such as "A2:J2"`, where)
		}
		r0, c0, r1, c1, ok := xlsxRange(rs, lastRow, lastCol)
		if !ok {
			return nil, fmt.Errorf(`%s: range %q is not a cell range like "A2:J2", "B5", a whole row "2:2" or a whole column "C:C"`, where, rs)
		}
		var fill, color string
		var bold, italic *bool
		for k, v := range f {
			switch k {
			case "range":
			case "fill", "color":
				t, _ := v.(string)
				rgb, ok := xlsxRGB(t)
				if !ok {
					return nil, fmt.Errorf("%s: %s %q is not a colour like \"#RRGGBB\"", where, k, fmt.Sprint(v))
				}
				if k == "fill" {
					fill = rgb
				} else {
					color = rgb
				}
			case "bold", "italic":
				b, ok := v.(bool)
				if !ok {
					return nil, fmt.Errorf("%s: %s is true or false", where, k)
				}
				if k == "bold" {
					bold = &b
				} else {
					italic = &b
				}
			default:
				return nil, fmt.Errorf("%s: %q is not a format; use range, fill, color, bold and italic", where, k)
			}
		}
		*budget -= (r1 - r0 + 1) * (c1 - c0 + 1)
		if *budget < 0 {
			return nil, fmt.Errorf("%s: the formats cover more than %d cells; colour the rows and columns that have data", where, xlsxMaxStyled)
		}
		for r := r0; r <= r1; r++ {
			for c := c0; c <= c1; c++ {
				k := [2]int{r, c}
				l, had := looks[k]
				if !had && r == 0 {
					l.bold = true
				}
				if fill != "" {
					l.fill = fill
				}
				if color != "" {
					l.color = color
				}
				if bold != nil {
					l.bold = *bold
				}
				if italic != nil {
					l.italic = *italic
				}
				looks[k] = l
			}
		}
	}
	return looks, nil
}

// styles.xml's fonts, fills and cellXfs, each look once; xf 0 is the
// default and xf 1 the bold header, as before formats
type xlsxStyles struct {
	fonts, fills []string
	xfs          []string
	fontOf       map[string]int
	fillOf       map[string]int
	xfOf         map[xlsxLook]int
}

func newXlsxStyles() *xlsxStyles {
	st := &xlsxStyles{fontOf: map[string]int{}, fillOf: map[string]int{}, xfOf: map[xlsxLook]int{}}
	st.fills = []string{`<fill><patternFill patternType="none"/></fill>`, `<fill><patternFill patternType="gray125"/></fill>`}
	st.xf(xlsxLook{})
	st.xf(xlsxLook{bold: true})
	return st
}

func (st *xlsxStyles) xf(l xlsxLook) int {
	if i, ok := st.xfOf[l]; ok {
		return i
	}
	font := ""
	if l.bold {
		font += "<b/>"
	}
	if l.italic {
		font += "<i/>"
	}
	font += `<sz val="11"/>`
	if l.color != "" {
		font += `<color rgb="` + l.color + `"/>`
	}
	font = "<font>" + font + `<name val="Calibri"/></font>`
	fontID, ok := st.fontOf[font]
	if !ok {
		fontID = len(st.fonts)
		st.fonts = append(st.fonts, font)
		st.fontOf[font] = fontID
	}
	fillID := 0
	if l.fill != "" {
		fill := `<fill><patternFill patternType="solid"><fgColor rgb="` + l.fill + `"/><bgColor indexed="64"/></patternFill></fill>`
		if fillID, ok = st.fillOf[fill]; !ok {
			fillID = len(st.fills)
			st.fills = append(st.fills, fill)
			st.fillOf[fill] = fillID
		}
	}
	x := fmt.Sprintf(`<xf numFmtId="0" fontId="%d" fillId="%d" borderId="0" xfId="0"`, fontID, fillID)
	if fontID != 0 {
		x += ` applyFont="1"`
	}
	if fillID != 0 {
		x += ` applyFill="1"`
	}
	i := len(st.xfs)
	st.xfs = append(st.xfs, x+"/>")
	st.xfOf[l] = i
	return i
}

func (st *xlsxStyles) xml() string {
	return fmt.Sprintf(`<fonts count="%d">%s</fonts><fills count="%d">%s</fills>`, len(st.fonts), strings.Join(st.fonts, ""), len(st.fills), strings.Join(st.fills, "")) +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		fmt.Sprintf(`<cellXfs count="%d">%s</cellXfs>`, len(st.xfs), strings.Join(st.xfs, "")) +
		`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>`
}

// the sheet's <cols>: widths in characters, null or 0 for the default
func xlsxCols(s xlsxSheet) (string, error) {
	if len(s.Widths) > xlsxMaxCol {
		return "", fmt.Errorf("Sheet %q: widths has more than %d columns", s.Name, xlsxMaxCol)
	}
	var b strings.Builder
	for i, v := range s.Widths {
		if v == nil {
			continue
		}
		n, ok := v.(json.Number)
		w, err := n.Float64()
		if !ok || err != nil || w < 0 || w > 255 {
			return "", fmt.Errorf("Sheet %q, widths[%d]: a column width is a number of characters from 0 to 255 (or null)", s.Name, i)
		}
		if w == 0 {
			continue
		}
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="%s" customWidth="1"/>`, i+1, i+1, strconv.FormatFloat(w, 'f', -1, 64))
	}
	if b.Len() == 0 {
		return "", nil
	}
	return "<cols>" + b.String() + "</cols>", nil
}

func writeXlsx(sheets []xlsxSheet) ([]byte, error) {
	const head = "<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n"
	const ns = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	const rel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	var over, names, rels, defined strings.Builder
	for i, s := range sheets {
		fmt.Fprintf(&over, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
		fmt.Fprintf(&names, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xlsxText(s.Name), i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%s/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, rel, i+1)
	}
	styles := newXlsxStyles()
	budget := xlsxMaxStyled
	var sheetParts [][2]string
	for i, s := range sheets {
		if s.Freeze < 0 || s.Freeze > 1000 {
			return nil, fmt.Errorf("Sheet %q: freeze is the number of header rows to keep in view, 0 to 1000", s.Name)
		}
		looks, err := xlsxLooks(s, &budget)
		if err != nil {
			return nil, err
		}
		cols, err := xlsxCols(s)
		if err != nil {
			return nil, err
		}
		// the rows and, in each, the columns written: those with a value
		// and those a format covers
		lastRow := len(s.Rows) - 1
		styledCols := map[int][]int{}
		for k := range looks {
			styledCols[k[0]] = append(styledCols[k[0]], k[1])
			if k[0] > lastRow {
				lastRow = k[0]
			}
		}
		var b strings.Builder
		b.WriteString(head + `<worksheet ` + ns + `>`)
		if s.Freeze > 0 {
			fmt.Fprintf(&b, `<sheetViews><sheetView workbookViewId="0"><pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/><selection pane="bottomLeft" activeCell="A%d" sqref="A%d"/></sheetView></sheetViews>`, s.Freeze, s.Freeze+1, s.Freeze+1, s.Freeze+1)
		}
		b.WriteString(cols + "<sheetData>")
		for r := 0; r <= lastRow; r++ {
			var row []any
			if r < len(s.Rows) {
				row = s.Rows[r]
			}
			sc := styledCols[r]
			if len(row) == 0 && len(sc) == 0 {
				if r < len(s.Rows) {
					fmt.Fprintf(&b, `<row r="%d"></row>`, r+1)
				}
				continue
			}
			sort.Ints(sc)
			order := make([]int, 0, len(row)+len(sc))
			for c := range row {
				order = append(order, c)
			}
			for _, c := range sc {
				if c >= len(row) {
					order = append(order, c)
				}
			}
			fmt.Fprintf(&b, `<row r="%d">`, r+1)
			for _, c := range order {
				var v any
				if c < len(row) {
					v = row[c]
				}
				look, styled := looks[[2]int{r, c}]
				style := ""
				if styled {
					if x := styles.xf(look); x != 0 {
						style = ` s="` + strconv.Itoa(x) + `"`
					}
				} else if r == 0 {
					style = ` s="1"`
				}
				ref := xlsxCol(c) + strconv.Itoa(r+1)
				empty := func() {
					if styled {
						fmt.Fprintf(&b, `<c r="%s"%s/>`, ref, style)
					}
				}
				if v == nil {
					empty()
					continue
				}
				// a formula, and the value it gives if the writer knows it
				// (Excel works it out on opening; Sliqtly reads the value)
				if m, ok := v.(map[string]any); ok {
					f, _ := m["f"].(string)
					f = xlsxText(strings.TrimPrefix(strings.TrimSpace(f), "="))
					known, cached, kind := m["v"], "", ""
					if n, ok := xlsxNum(known); ok {
						cached = "<v>" + n + "</v>"
					} else if t, ok := known.(string); ok && t != "" {
						cached, kind = "<v>"+xlsxText(t)+"</v>", ` t="str"`
					}
					fmt.Fprintf(&b, `<c r="%s"%s%s><f>%s</f>%s</c>`, ref, style, kind, f, cached)
					continue
				}
				if n, ok := xlsxNum(v); ok {
					fmt.Fprintf(&b, `<c r="%s"%s><v>%s</v></c>`, ref, style, n)
					continue
				}
				t := fmt.Sprint(v)
				if t == "" {
					empty()
					continue
				}
				fmt.Fprintf(&b, `<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xlsxText(t))
			}
			b.WriteString("</row>")
		}
		b.WriteString("</sheetData>")
		if s.Filter {
			// over the header row and the data under it
			width := 1
			for _, row := range s.Rows {
				if len(row) > width {
					width = len(row)
				}
			}
			height := len(s.Rows)
			if height < 1 {
				height = 1
			}
			ref := "A1:" + xlsxCol(width-1) + strconv.Itoa(height)
			fmt.Fprintf(&b, `<autoFilter ref="%s"/>`, ref)
			abs := "$A$1:$" + xlsxCol(width-1) + "$" + strconv.Itoa(height)
			fmt.Fprintf(&defined, `<definedName name="_xlnm._FilterDatabase" localSheetId="%d" hidden="1">'%s'!%s</definedName>`, i, xlsxText(strings.ReplaceAll(s.Name, "'", "''")), abs)
		}
		b.WriteString("</worksheet>")
		sheetParts = append(sheetParts, [2]string{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), b.String()})
	}
	definedNames := ""
	if defined.Len() > 0 {
		definedNames = "<definedNames>" + defined.String() + "</definedNames>"
	}
	parts := [][2]string{
		{"[Content_Types].xml", head + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` + over.String() + "</Types>"},
		{"_rels/.rels", head + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="` + rel + `/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", head + `<workbook ` + ns + ` xmlns:r="` + rel + `"><bookViews><workbookView/></bookViews><sheets>` + names.String() + `</sheets>` + definedNames + `<calcPr fullCalcOnLoad="1"/></workbook>`},
		{"xl/_rels/workbook.xml.rels", head + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels.String() + fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1, rel)},
		{"xl/styles.xml", head + `<styleSheet ` + ns + `>` + styles.xml() + `</styleSheet>`},
	}
	parts = append(parts, sheetParts...)
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

// XlsxWrite keeps the workbook of sheetsJSON ([{name, rows, formats,
// widths, freeze, filter}]) as bytes: {"handle":n,"size":n}
func (h *McpHost) XlsxWrite(sheetsJSON string) string {
	var sheets []xlsxSheet
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
