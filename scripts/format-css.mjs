// One declaration per line: the theme CSS as a person edits it.
//
//   chart { color: #ffa546; accent-color: #5ce1ff }
//
// becomes
//
//   chart {
//     color: #ffa546;
//     accent-color: #5ce1ff;
//   }
//
// Comments are kept as they were written. `node scripts/format-css.mjs <file…>`
// rewrites the files in place; `formatCss(text)` is what the build uses.
import fs from "node:fs";
import url from "node:url";

export function formatCss(src) {
  const out = [];
  let i = 0;
  const n = src.length;
  let pending = "";
  const flushText = () => {
    const t = pending.trim();
    pending = "";
    return t;
  };
  while (i < n) {
    if (src.startsWith("/*", i)) {
      const end = src.indexOf("*/", i + 2);
      const stop = end < 0 ? n : end + 2;
      const before = flushText();
      if (before) out.push({ kind: "text", text: before });
      out.push({ kind: "comment", text: src.slice(i, stop) });
      i = stop;
      continue;
    }
    const c = src[i];
    if (c === "{") {
      const sel = flushText().replace(/\s+/g, " ");
      let j = i + 1;
      let depth = 0;
      let quote = "";
      while (j < n) {
        const d = src[j];
        if (quote) {
          if (d === quote) quote = "";
        } else if (d === "\"" || d === "'") quote = d;
        else if (d === "(") depth += 1;
        else if (d === ")") depth -= 1;
        else if (d === "}" && depth <= 0) break;
        j += 1;
      }
      const body = src.slice(i + 1, j);
      out.push({ kind: "rule", sel, decls: splitDecls(body) });
      i = j + 1;
      continue;
    }
    pending += c;
    i += 1;
  }
  const tail = flushText();
  if (tail) out.push({ kind: "text", text: tail });

  const lines = [];
  let prev = "";
  for (const item of out) {
    if (item.kind === "comment") {
      if (lines.length) lines.push("");
      lines.push(item.text.trim());
    } else if (item.kind === "rule") {
      if (lines.length && prev !== "comment") lines.push("");
      lines.push(item.sel + " {");
      for (const d of item.decls) lines.push("  " + d + ";");
      lines.push("}");
    } else {
      lines.push(item.text);
    }
    prev = item.kind;
  }
  return lines.join("\n").replace(/\n{3,}/g, "\n\n") + "\n";
}

function splitDecls(body) {
  const out = [];
  let cur = "";
  let depth = 0;
  let quote = "";
  for (const c of body) {
    if (quote) {
      if (c === quote) quote = "";
    } else if (c === "\"" || c === "'") quote = c;
    else if (c === "(") depth += 1;
    else if (c === ")") depth -= 1;
    else if (c === ";" && depth <= 0) {
      if (cur.trim()) out.push(tidy(cur));
      cur = "";
      continue;
    }
    cur += c;
  }
  if (cur.trim()) out.push(tidy(cur));
  return out;
}

function tidy(decl) {
  const t = decl.replace(/\s+/g, " ").trim();
  const k = t.indexOf(":");
  if (k < 0) return t;
  return t.slice(0, k).trim() + ": " + t.slice(k + 1).trim();
}

if (process.argv[1] && url.fileURLToPath(import.meta.url) === process.argv[1]) {
  for (const f of process.argv.slice(2)) fs.writeFileSync(f, formatCss(fs.readFileSync(f, "utf8")));
}
