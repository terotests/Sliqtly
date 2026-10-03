/**
 * npm run i18n: the interface's strings against the language tables.
 *
 * Collects every English string the interface translates — PresI18n.t("…")
 * in src/*.rgr, t("…") in web/*.js, the data-i18n texts and titles in
 * web/index.html — and for each table in web/i18n/ lists the strings it has
 * no entry for and the entries no string uses any more.
 *
 *   node scripts/i18n.mjs            report
 *   node scripts/i18n.mjs --merge    first fold web/i18n/parts/<lang>.*.json
 *                                    into web/i18n/<lang>.json, then report
 *   node scripts/i18n.mjs --strict   exit 1 when anything is missing
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const dir = path.join(root, "web/i18n");
const args = new Set(process.argv.slice(2));

// a JS/Ranger string literal's value
const unquote = (s) => JSON.parse('"' + s.replace(/\\'/g, "'") + '"');
const LIT = String.raw`"((?:[^"\\]|\\.)*)"`;

function collect() {
  const keys = new Set();
  for (const f of fs.readdirSync(path.join(root, "src")).filter((f) => f.endsWith(".rgr"))) {
    const s = fs.readFileSync(path.join(root, "src", f), "utf8");
    for (const m of s.matchAll(new RegExp(String.raw`PresI18n\.t\(\s*` + LIT, "g"))) keys.add(unquote(m[1]));
  }
  for (const f of ["main.js", "sliqtly.js", "sheets-live.js", "versions-ui.js"]) {
    const s = fs.readFileSync(path.join(root, "web", f), "utf8");
    for (const m of s.matchAll(new RegExp(String.raw`\bt\(\s*` + LIT, "g"))) keys.add(unquote(m[1]));
  }
  // strings the code translates through a variable, named in a comment:
  // `; i18n: "Text" "Speech"` (Ranger) or `// i18n: "Dark" "Light"` (JS)
  for (const f of [...fs.readdirSync(path.join(root, "src")).map((f) => "src/" + f), "web/main.js", "web/sliqtly.js", "web/sheets-live.js", "web/versions-ui.js"]) {
    for (const line of fs.readFileSync(path.join(root, f), "utf8").split("\n")) {
      const at = line.search(/(;|\/\/)\s*i18n:/);
      if (at >= 0) for (const m of line.slice(at).matchAll(new RegExp(LIT, "g"))) keys.add(unquote(m[1]));
    }
  }
  const html = fs.readFileSync(path.join(root, "web/index.html"), "utf8");
  for (const m of html.matchAll(/<(\w+)([^>]*\bdata-i18n(?![-\w])[^>]*)>([^<]*)</g)) if (m[3].trim()) keys.add(m[3].trim());
  for (const m of html.matchAll(/<\w+[^>]*\btitle="([^"]*)"[^>]*\bdata-i18n-title\b|<\w+[^>]*\bdata-i18n-title\b[^>]*\btitle="([^"]*)"/g)) keys.add(m[1] ?? m[2]);
  for (const m of html.matchAll(/<\w+[^>]*\baria-label="([^"]*)"[^>]*\bdata-i18n-aria\b|<\w+[^>]*\bdata-i18n-aria\b[^>]*\baria-label="([^"]*)"/g)) keys.add(m[1] ?? m[2]);
  return keys;
}

if (args.has("--merge")) {
  const parts = path.join(dir, "parts");
  const byLang = {};
  for (const f of fs.existsSync(parts) ? fs.readdirSync(parts) : []) {
    const lang = f.split(".")[0];
    byLang[lang] = { ...(byLang[lang] || {}), ...JSON.parse(fs.readFileSync(path.join(parts, f), "utf8")) };
  }
  for (const [lang, add] of Object.entries(byLang)) {
    const file = path.join(dir, lang + ".json");
    const have = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : {};
    const all = { ...have, ...add };
    const sorted = Object.fromEntries(Object.keys(all).sort((a, b) => a.localeCompare(b)).map((k) => [k, all[k]]));
    fs.writeFileSync(file, JSON.stringify(sorted, null, 2) + "\n");
    console.log(`merged ${Object.keys(add).length} into ${lang}.json`);
  }
}

const keys = collect();
let bad = false;
console.log(`${keys.size} strings`);
for (const f of fs.readdirSync(dir).filter((f) => f.endsWith(".json"))) {
  const table = JSON.parse(fs.readFileSync(path.join(dir, f), "utf8"));
  const missing = [...keys].filter((k) => !(k in table));
  const unused = Object.keys(table).filter((k) => !keys.has(k));
  console.log(`${f}: ${Object.keys(table).length} entries, ${missing.length} missing, ${unused.length} unused`);
  for (const k of missing) console.log("  missing: " + JSON.stringify(k));
  for (const k of unused) console.log("  unused:  " + JSON.stringify(k));
  if (missing.length) bad = true;
}
if (bad && args.has("--strict")) process.exit(1);
