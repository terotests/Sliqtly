/**
 * Build stamps on module URLs.
 *
 * index.html is served no-cache and names main.js?v=<build>. Every module
 * that main.js (or any module after it) imports by a relative path must
 * carry the same stamp, or a browser keeps the old copy of that one module
 * from its cache and the new main.js asks it for an export it does not have
 * ("does not provide an export named …"). So the build rewrites every
 * relative import in every module it ships, not a hand-kept list.
 */

// `from "./x.js"`, `import "./x.js"`, `import("./x.js")`, `export … from "./x.js"`
const RELATIVE = /(\bfrom\s*|\bimport\s*\(?\s*)(["'])(\.{1,2}\/[^"'?#]+\.m?js)\2/g;

/** The module text with ?v=<stamp> on every relative import. */
export function stampImports(code, stamp) {
  return code.replace(RELATIVE, (_, lead, q, url) => `${lead}${q}${url}?v=${stamp}${q}`);
}

/** The relative imports in `code` that carry no ?v= (empty once stamped). */
export function unstampedImports(code) {
  const out = [];
  for (const m of code.matchAll(/(?:\bfrom\s*|\bimport\s*\(?\s*)["'](\.{1,2}\/[^"']+)["']/g)) {
    if (!/[?&]v=/.test(m[1])) out.push(m[1]);
  }
  return out;
}
