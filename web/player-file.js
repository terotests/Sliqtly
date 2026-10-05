// A presentation as one .html file that plays itself (File → Export ▸
// Presentation player).
//
// The file is the page itself, built once with the site (scripts/player.mjs
// writes web/dist/player.html): its stylesheets, fonts, translations and the
// compiled engine sit in the page as one table of gzipped, base64 assets
// (`<script type="application/json" id="sliqtlyAssets">`), and the exported
// deck goes in beside them (`id="sliqtlyDeck"`). Opened from disk, with no
// network, the page reads what it would have fetched from that table, finds
// the deck and shows it as a shared presentation does.
//
// Kept apart from main.js so the rules can be tested under Node
// (web/test/player-file.test.mjs).

export const ASSETS_ID = "sliqtlyAssets";
export const DECK_ID = "sliqtlyDeck";
// where scripts/player.mjs leaves room for the deck in the built page
export const DECK_SLOT = "<!--sliqtly-deck-->";

function jsonScript(doc, id) {
  const el = doc && doc.getElementById ? doc.getElementById(id) : null;
  if (!el) return null;
  try {
    return JSON.parse(el.textContent);
  } catch (_) {
    return null;
  }
}

let table;
function assets() {
  if (table === undefined) table = jsonScript(globalThis.document, ASSETS_ID);
  return table;
}

/** "./fonts/A.ttf?v=1" → "fonts/A.ttf": the name an asset is kept under. */
export function assetKey(url) {
  let k = String(url).split("#")[0].split("?")[0];
  while (k.startsWith("./")) k = k.slice(2);
  return k;
}

function bytesOf(b64) {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** The bytes of one table entry ({ type, gz, b64 }), unzipped. */
export async function entryBytes(e) {
  const raw = bytesOf(e.b64);
  if (!e.gz) return raw;
  const stream = new Blob([raw]).stream().pipeThrough(new DecompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

/**
 * What a fetch of the page's own `url` answers in a player file, or null
 * when the page is not one (or has no such asset): then it is fetched.
 * `from`: the table to read, the page's own when not given.
 */
export async function embeddedAsset(url, from = assets()) {
  if (!from || !String(url).startsWith("./")) return null;
  const e = from[assetKey(url)];
  if (!e) return null;
  return new Response(await entryBytes(e), { headers: { "content-type": e.type || "application/octet-stream" } });
}

/** A classic script of the page's own as a blob: URL, or null (fetch it). */
export async function embeddedScriptUrl(url, from = assets()) {
  const res = await embeddedAsset(url, from);
  if (!res) return null;
  return URL.createObjectURL(new Blob([await res.arrayBuffer()], { type: "text/javascript" }));
}

/**
 * The deck a player file carries, or null on any other page:
 * { name, md, theme, css, files: [{ path, type, text } | { path, type, b64 }] }.
 */
export function embeddedDeck(doc = globalThis.document) {
  const d = jsonScript(doc, DECK_ID);
  if (!d || typeof d.md !== "string") return null;
  return { name: d.name || "presentation", md: d.md, theme: d.theme || "", css: d.css == null ? null : String(d.css), files: Array.isArray(d.files) ? d.files : [] };
}

/** One file of the deck as the page keeps it: text as a string, else a Blob. */
export function fileData(f) {
  if (typeof f.text === "string") return f.text;
  return new Blob([bytesOf(f.b64 || "")], { type: f.type || "application/octet-stream" });
}

/**
 * The built player page (`template`) with `deck` in its slot. The JSON is
 * kept inside its script element: every "<" is written as <, so no
 * "</script>" (or "<!--") in a deck can end it early.
 */
export function playerHtml(template, deck) {
  const at = template.indexOf(DECK_SLOT);
  if (at < 0) throw new Error("player.html has no place for the deck");
  const json = JSON.stringify(deck).replace(/</g, "\\u003c").replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
  const tag = `<script type="application/json" id="${DECK_ID}">${json}</script>`;
  return template.slice(0, at) + tag + template.slice(at + DECK_SLOT.length);
}

/** Bytes as base64, in pieces (a long apply() overflows the stack). */
export function base64(bytes) {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(s);
}
