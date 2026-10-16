package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// run_script runs a program on a deck's files in CErXes with Node.js's fs,
// path and process (web/node-runtime.js): what it writes under media/,
// data/, code/ and presentation.md is kept, anything else said and dropped.
func TestRunScript(t *testing.T) {
	dir := os.Getenv("SLIQTLY_CERXES_DIR")
	if dir == "" {
		dir = "../web/dist"
	}
	if _, err := os.Stat(dir + "/cerxes.wasm"); err != nil {
		t.Skip("no web/dist/cerxes.wasm (npm run build with Rust's wasm32-wasip1 target)")
	}
	if _, err := os.Stat(dir + "/node-runtime.js"); err != nil {
		t.Skip("no node-runtime.js beside cerxes.wasm (npm run build)")
	}
	t.Setenv("SLIQTLY_CERXES_DIR", dir)
	cerxes = cerxesEngine{}
	defer func() { cerxes = cerxesEngine{} }()
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Sales", "markdown": "# Sales\n\n## Q1\n\n- Up\n",
		"files": []any{
			map[string]any{"name": "sales.csv", "text": "month,value\nJan,40\nFeb,90\nMar,60\n"},
			map[string]any{"name": "code/scripts/top.js", "text": "const fs = require(\"fs\");\nconst min = Number(process.argv[2]);\nconst rows = fs.readFileSync(\"data/sales.csv\", \"utf8\").trim().split(\"\\n\").slice(1).filter(l => +l.split(\",\")[1] >= min);\nfs.writeFileSync(\"data/top.csv\", \"month,value\\n\" + rows.join(\"\\n\") + \"\\n\");\nconsole.log(rows.length, \"months\");\n"},
		}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	prog := `import fs from "node:fs";
import * as path from "path";
const rows = fs.readFileSync("data/sales.csv", "utf8").trim().split("\n").slice(1).map(l => l.split(","));
console.log(fs.readdirSync("/").join(" "), process.cwd());
const max = Math.max(...rows.map(r => +r[1]));
const svg = renderToString(<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 100">
  {rows.map((r, i) => <rect x={10 + i * 100} y={100 - 100 * r[1] / max} width={80} height={100 * r[1] / max} fill="#3b82f6" />)}
</svg>);
fs.writeFileSync(path.join("media", "bars.svg"), svg);
fs.appendFileSync("presentation.md", "\n## Bars\n\n![Sales](media/bars.svg)\n");
fs.mkdirSync("/tmp/work", { recursive: true });
fs.writeFileSync("/tmp/work/n.txt", "scratch");
fs.writeFileSync("notes.txt", "where?");
try { fs.readFileSync("data/none.csv"); } catch (e) { console.log(e.code); }
`
	r := call(t, s, "run_script", map[string]any{"deck_id": id, "code": prog})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	txt := textOf(r)
	match(t, txt, `ran to the end`)
	match(t, txt, `apps code data media presentation\.md tmp /`)
	match(t, txt, `ENOENT`)
	match(t, txt, `Kept in the presentation: media/bars\.svg \(\d+ bytes\), presentation\.md`)
	match(t, txt, `notes\.txt: only media/, data/, apps/, code/ and presentation\.md are kept`)
	match(t, txt, `Scratch, gone after the run: tmp/work/n\.txt`)
	files := textOf(call(t, s, "list_files", map[string]any{"deck_id": id}))
	match(t, files, `media/bars\.svg`)
	md := textOf(call(t, s, "get_presentation", map[string]any{"deck_id": id}))
	match(t, md, `## Bars`)
	svg := textOf(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "media/bars.svg"}))
	if !strings.Contains(svg, "<rect") && !strings.Contains(svg, "picture") {
		t.Fatal("no SVG kept:", svg)
	}

	// a program the deck keeps, with words for process.argv
	k := call(t, s, "run_script", map[string]any{"deck_id": id, "path": "code/scripts/top.js", "args": []any{"60"}})
	if k.IsError {
		t.Fatal(textOf(k))
	}
	match(t, textOf(k), `code/scripts/top\.js ran to the end`)
	match(t, textOf(k), `It printed:\n2 months`)
	top := textOf(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "data/top.csv"}))
	match(t, top, `Feb`)
	if strings.Contains(top, "Jan") {
		t.Fatal("Jan is under 60:", top)
	}

	// save: false keeps nothing
	dry := call(t, s, "run_script", map[string]any{"deck_id": id, "save": false, "code": `require("fs").writeFileSync("data/dry.csv", "a\n1\n")`})
	match(t, textOf(dry), `would keep \(save: false, so nothing is kept\): data/dry\.csv`)
	match(t, textOf(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "data/dry.csv"})), `No file data/dry\.csv`)

	// an error keeps nothing and says what the program printed
	bad := call(t, s, "run_script", map[string]any{"deck_id": id, "code": "const fs = require(\"fs\");\nfs.writeFileSync(\"data/half.csv\", \"x\\n\");\nconsole.log(\"before\");\nnull.x;\n"})
	if !bad.IsError {
		t.Fatal("an error was not said:", textOf(bad))
	}
	match(t, textOf(bad), `stopped: Uncaught TypeError`)
	match(t, textOf(bad), `It printed:\nbefore`)
	match(t, textOf(bad), `Nothing is kept`)
	exit := call(t, s, "run_script", map[string]any{"deck_id": id, "code": "process.exit(2)"})
	match(t, textOf(exit), `stopped: process\.exit\(2\)`)
	ok := call(t, s, "run_script", map[string]any{"deck_id": id, "code": "console.log('a'); process.exit(0); console.log('b')"})
	if ok.IsError {
		t.Fatal(textOf(ok))
	}
	eq(t, fmt.Sprint(sc(ok)["output"]), "a")

	// an endless loop is stopped
	loop := call(t, s, "run_script", map[string]any{"deck_id": id, "code": "while (true) {}"})
	match(t, textOf(loop), `longer than 10 s`)

	// unknown modules and a deck's file it may not remove
	mod := call(t, s, "run_script", map[string]any{"deck_id": id, "code": "try { require(\"lodash\") } catch (e) { console.log(e.message) }\nrequire(\"fs\").unlinkSync(\"data/sales.csv\")"})
	match(t, textOf(mod), `Cannot find module 'lodash': there are no npm packages here`)
	match(t, textOf(mod), `data/sales\.csv: not removed`)

	// a workbook's sheets are there as read-only CSVs
	wb := call(t, s, "write_workbook", map[string]any{"deck_id": id, "path": "data/book.xlsx", "sheets": []any{map[string]any{"name": "Costs", "rows": []any{[]any{"item", "eur"}, []any{"Rent", 900}}}}})
	if wb.IsError {
		t.Fatal(textOf(wb))
	}
	sheet := call(t, s, "run_script", map[string]any{"deck_id": id, "code": "const fs = require(\"fs\");\nconsole.log(fs.readFileSync(\"data/book-Costs.csv\", \"utf8\"));\ntry { fs.writeFileSync(\"data/book-Costs.csv\", \"x\") } catch (e) { console.log(e.code) }\ntry { fs.readFileSync(\"data/book.xlsx\") } catch (e) { console.log(e.code, fs.statSync(\"data/book.xlsx\").size > 0) }"})
	match(t, textOf(sheet), `item,eur\nRent,900`)
	match(t, textOf(sheet), `EACCES`)
	match(t, textOf(sheet), `ENOTSUP true`)

	none := call(t, s, "run_script", map[string]any{"deck_id": id, "path": "code/none.js"})
	match(t, textOf(none), `No program code/none\.js`)
}
