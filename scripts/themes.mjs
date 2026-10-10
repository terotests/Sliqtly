// The built-in themes: themes/base.css holds the rules every theme shares,
// themes/<name>.css only what that theme sets otherwise (its colours, its
// fonts, and the sizes or art it differs in). A theme is used whole: the
// build, the server (mcp-go/gen.mjs) and the desktop editor take
// themeCss(name), base and theme put together rule by rule, so the editor's
// CSS tab and every export see one full sheet as before.
//
// Theme CSS is a plain list of rules, one selector each, no @-rules (MdCss
// in RangerMarkdown); a comment just before a rule belongs to it.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const themesDir = path.join(root, "themes");

/** { head, rules: [{ comment, selector, props: [[name, value]] }] } */
export function parseTheme(css) {
  const rules = [];
  let i = 0;
  let head = "";
  let comment = "";
  const n = css.length;
  while (i < n) {
    while (i < n && /\s/.test(css[i])) i++;
    if (i >= n) break;
    if (css.startsWith("/*", i)) {
      const e = css.indexOf("*/", i + 2);
      if (e < 0) throw new Error("theme: an unclosed comment");
      const c = css.slice(i, e + 2);
      // the file's own head: the /* ---- banner it opens with
      if (rules.length === 0 && !head && !comment && c.startsWith("/* ---")) head = c;
      else comment = comment ? comment + "\n" + c : c;
      i = e + 2;
      continue;
    }
    const open = css.indexOf("{", i);
    const close = css.indexOf("}", open);
    if (open < 0 || close < 0) throw new Error("theme: a rule without { }");
    const selector = css.slice(i, open).trim();
    const props = [];
    for (const decl of css.slice(open + 1, close).split(";")) {
      const c = decl.indexOf(":");
      if (c < 0) {
        if (decl.trim()) throw new Error(`theme: "${decl.trim()}" in ${selector}`);
        continue;
      }
      props.push([decl.slice(0, c).trim(), decl.slice(c + 1).trim()]);
    }
    rules.push({ comment, selector, props });
    comment = "";
    i = close + 1;
  }
  return { head, rules };
}

export function writeTheme({ head, rules }) {
  const out = [];
  if (head) out.push(head, "");
  for (const r of rules) {
    if (r.comment) out.push(r.comment);
    out.push(`${r.selector} {`, ...r.props.map(([k, v]) => `  ${k}: ${v};`), "}", "");
  }
  return out.join("\n");
}

/**
 * base + theme: a property the theme sets wins; a rule only the theme has
 * comes where the theme puts it (after the rule it follows there).
 */
export function composeTheme(baseCss, themeCss) {
  const base = parseTheme(baseCss);
  const theme = parseTheme(themeCss);
  const rules = base.rules.map((r) => ({ ...r, props: r.props.map((p) => [...p]) }));
  let after = -1;
  for (const t of theme.rules) {
    const at = rules.findIndex((r) => r.selector === t.selector);
    if (at >= 0) {
      const r = rules[at];
      for (const [k, v] of t.props) {
        const p = r.props.find((q) => q[0] === k);
        if (p) p[1] = v;
        else r.props.push([k, v]);
      }
      if (t.comment) r.comment = t.comment;
      after = at;
    } else {
      rules.splice(after + 1, 0, { ...t, props: t.props.map((p) => [...p]) });
      after += 1;
    }
  }
  // base.css lists a rule only the themes fill in (colours) empty, to keep
  // the order; a theme that has no such rule leaves it out
  return writeTheme({ head: theme.head, rules: rules.filter((r) => r.props.length) });
}

export function themeNames() {
  return fs.readdirSync(themesDir).filter((f) => f.endsWith(".css") && f !== "base.css").map((f) => f.slice(0, -4)).sort();
}

/** a built-in theme as one full sheet */
export function themeCss(name) {
  return composeTheme(fs.readFileSync(path.join(themesDir, "base.css"), "utf8"), fs.readFileSync(path.join(themesDir, `${name}.css`), "utf8"));
}
