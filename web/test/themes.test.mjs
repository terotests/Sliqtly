// The built-in themes: themes/base.css is the shape they share, each theme
// its colours and fonts (scripts/themes.mjs puts them together).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { parseTheme, composeTheme, themeCss, themeNames, themesDir } from "../../scripts/themes.mjs";

const colourOrFont = (k) => /color|^colors$|^font-family$/.test(k);

test("base.css sets no colour and no font", () => {
  const base = parseTheme(fs.readFileSync(path.join(themesDir, "base.css"), "utf8"));
  for (const r of base.rules) for (const [k] of r.props) assert.ok(!colourOrFont(k), `${r.selector} { ${k} }`);
});

test("every theme names its own colours and fonts, and comes out whole", () => {
  for (const name of themeNames()) {
    const own = parseTheme(fs.readFileSync(path.join(themesDir, `${name}.css`), "utf8"));
    const set = new Set(own.rules.flatMap((r) => r.props.map(([k]) => `${r.selector} ${k}`)));
    for (const need of ["page background-color", "document color", "document font-family", "heading font-family", "chart color"]) {
      assert.ok(set.has(need), `${name}: ${need}`);
    }
    const whole = parseTheme(themeCss(name));
    const page = whole.rules.find((r) => r.selector === "page");
    assert.deepEqual(page.props.map(([k]) => k).filter((k) => k === "width" || k === "height"), ["width", "height"], name);
    assert.ok(whole.head.includes(`${name}.css`), `${name}: its own head comment`);
  }
});

test("a theme's own value wins, its own rule keeps its place, empty base rules drop out", () => {
  const base = "a {\n}\n\npage {\n  width: 10in;\n  padding: 1in;\n}\n\nh1 {\n  font-size: 40pt;\n}\n";
  const theme = "/* --- t.css */\n\npage {\n  padding: 2in;\n  background-color: #fff;\n}\n\n.lead {\n  font-size: 20pt;\n}\n";
  assert.equal(composeTheme(base, theme),
    "/* --- t.css */\n\npage {\n  width: 10in;\n  padding: 2in;\n  background-color: #fff;\n}\n\n.lead {\n  font-size: 20pt;\n}\n\nh1 {\n  font-size: 40pt;\n}\n");
});
