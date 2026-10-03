// Checks on what a model sends, before anything is stored.

import dns from "node:dns/promises";

export const THEMES = ["aurora", "nebula", "carbon", "ember", "midnight", "corporate", "editorial"];
export const MAX_MD = 300 * 1024;
export const MAX_CSS = 100 * 1024;
export const MAX_IMAGE = 5 * 1024 * 1024;
export const MAX_IMAGES = 20;
export const MAX_DATA = 10 * 1024 * 1024;
export const MAX_DATA_FILES = 10;
export const XLSX_MIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
const DATA_TYPES = { xlsx: XLSX_MIME, csv: "text/csv", tsv: "text/tab-separated-values", json: "application/json", txt: "text/plain" };

const TYPES = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp", svg: "image/svg+xml" };

export class InputError extends Error {}

// "team photo.JPG" → "team-photo.jpg"; null when nothing usable is left
export function cleanName(name) {
  const s = String(name || "").trim().replace(/^media\//, "").replace(/\s+/g, "-").replace(/[^A-Za-z0-9._-]/g, "");
  if (!s || s.startsWith(".") || s.length > 80) return null;
  return s.replace(/\.([A-Za-z0-9]+)$/, (_, e) => "." + e.toLowerCase());
}

export function typeOf(name, given) {
  if (given && /^image\/(png|jpeg|gif|webp|svg\+xml)$/.test(given)) return given;
  const ext = (/\.([a-z0-9]+)$/.exec(name) || [])[1];
  return TYPES[ext] || null;
}

// What the slides are: titles of `#` / `##` headings outside fences, and the
// media/… the text points at.
export function outline(md) {
  const titles = [];
  const media = new Set();
  let fence = null;
  for (const line of md.split(/\r?\n/)) {
    const f = /^\s*(```+|~~~+)/.exec(line);
    if (f) {
      if (!fence) fence = f[1];
      else if (f[1].startsWith(fence[0]) && f[1].length >= fence.length) fence = null;
      continue;
    }
    if (fence) continue;
    const h = /^(#{1,2})\s+(.*)$/.exec(line);
    if (h) titles.push(h[2].replace(/\s*\{[^}]*\}\s*$/, "").trim());
    for (const m of line.matchAll(/media\/([A-Za-z0-9._-]+)/g)) media.add(m[1]);
  }
  return { titles, media: [...media] };
}

// Notes for the model: pictures the text names but nobody sent, and the
// other way round.
export function warnings(md, imageNames, storedNames = []) {
  const { media } = outline(md);
  const have = new Set([...imageNames, ...storedNames]);
  const out = [];
  for (const m of media) if (!have.has(m)) out.push(`media/${m} is used in the Markdown but no image by that name was sent.`);
  for (const n of imageNames) if (!media.includes(n)) out.push(`Image ${n} was sent but the Markdown does not use media/${n}.`);
  out.push(...typeWarnings(md));
  return out;
}

export function privateHost(host) {
  const h = host.toLowerCase().replace(/^\[|\]$/g, "").replace(/\.$/, "");
  return h === "localhost" || h.endsWith(".localhost") || h.endsWith(".internal") || h.endsWith(".local")
    || /^(127\.|10\.|0\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/.test(h)
    || h === "::1" || h === "::" || h.startsWith("::ffff:") || h.startsWith("fc") || h.startsWith("fd") || h.startsWith("fe80");
}

// The name's addresses, when the real network is used: a public name that
// points at a private address (127.0.0.1.nip.io, a cloud metadata server)
// is refused like the address itself. A test's fetchImpl skips this.
async function resolvesPrivate(host, fetchImpl) {
  if (fetchImpl !== globalThis.fetch) return false;
  let all;
  try { all = await dns.lookup(host.replace(/^\[|\]$/g, ""), { all: true }); } catch { return false; }
  return all.some((a) => privateHost(a.address));
}

// GET a public https URL: every redirect is checked like the first address,
// and at most `max` bytes are read whatever the server claims.
export async function publicFetch(raw, fetchImpl, { max, timeout = 15000, headers = {} }) {
  let u = raw instanceof URL ? raw : new URL(raw);
  const signal = AbortSignal.timeout(timeout);
  for (let hop = 0; hop <= 5; hop++) {
    if (u.protocol !== "https:" || privateHost(u.hostname) || await resolvesPrivate(u.hostname, fetchImpl)) throw new InputError("only public https URLs are fetched");
    const res = await fetchImpl(u, { signal, headers, redirect: "manual" });
    const to = res.status >= 300 && res.status < 400 ? res.headers.get("location") : null;
    if (!to) return { res, bytes: () => readCapped(res, max) };
    try { await res.body?.cancel(); } catch { /* nothing to drop */ }
    u = new URL(to, u);
  }
  throw new InputError("too many redirects");
}

async function readCapped(res, max) {
  if (Number(res.headers.get("content-length") || 0) > max) return null;
  if (!res.body) return Buffer.alloc(0);
  const parts = [];
  let size = 0;
  for await (const chunk of res.body) {
    size += chunk.length;
    if (size > max) { try { await res.body.cancel(); } catch { /* already closed */ } return null; }
    parts.push(chunk);
  }
  return Buffer.concat(parts.map((p) => Buffer.from(p)));
}

async function fetchBytes(raw, label, fetchImpl, max, accept) {
  let u;
  try { u = new URL(raw); } catch { throw new InputError(`${label}: "${raw}" is not a URL.`); }
  let got;
  try {
    got = await publicFetch(u, fetchImpl, { max, headers: { "user-agent": "Sliqtly-MCP/1.0" } });
  } catch (e) {
    if (e instanceof InputError) throw new InputError(`${label}: ${e.message}.`);
    throw e;
  }
  const { res } = got;
  if (!res.ok) throw new InputError(`${label}: ${u.hostname} answered ${res.status}.`);
  const ct = (res.headers.get("content-type") || "").split(";")[0].trim();
  const why = accept(ct);
  if (why) throw new InputError(`${label}: the URL gave ${ct}, ${why}.`);
  const data = await got.bytes();
  if (!data) throw new InputError(`${label} is larger than ${max / 1024 / 1024} MB.`);
  return { data, ct };
}

// Data files (.xlsx, .csv, .tsv, .json, .txt) as { name, path: "data/<name>",
// type, data: Buffer }, kept where the editor keeps a dropped file
// (web/vfs.js placeFor). A workbook must open.
export async function loadDataFiles(list, fetchImpl = fetch, checkWorkbook = null) {
  if (!list || !list.length) return [];
  if (list.length > MAX_DATA_FILES) throw new InputError(`At most ${MAX_DATA_FILES} data files per call.`);
  const out = [];
  for (const f of list) {
    const name = cleanName(String(f.name || "").replace(/^data\//, ""));
    if (!name) throw new InputError(`File name "${f.name}" is not usable: use letters, digits, ".", "-" and "_".`);
    const ext = (/\.([a-z0-9]+)$/.exec(name) || [])[1];
    const type = DATA_TYPES[ext];
    if (!type) throw new InputError(`File ${name}: data files are .xlsx, .csv, .tsv, .json or .txt.`);
    const given = [f.text != null, !!f.data_base64, !!f.url].filter(Boolean).length;
    if (given !== 1) throw new InputError(`File ${name}: give one of text, data_base64 or url.`);
    let data;
    if (f.text != null) {
      if (ext === "xlsx") throw new InputError(`File ${name}: a workbook is sent as data_base64 or url, not text.`);
      data = Buffer.from(String(f.text), "utf8");
    } else if (f.data_base64) {
      data = Buffer.from(String(f.data_base64).replace(/^data:[^;,]*;base64,/, ""), "base64");
    } else {
      ({ data } = await fetchBytes(f.url, `File ${name}`, fetchImpl, MAX_DATA, (ct) => (/^(image|video|audio)\//.test(ct) || ct === "text/html" ? "not data" : null)));
    }
    if (!data.length) throw new InputError(`File ${name} is empty.`);
    if (data.length > MAX_DATA) throw new InputError(`File ${name} is larger than 10 MB.`);
    if (ext === "xlsx" && checkWorkbook) {
      try { checkWorkbook(data); } catch (e) { throw new InputError(`File ${name} is not a workbook Sliqtly can read: ${e.message}`); }
    }
    out.push({ name, path: "data/" + name, type, data });
  }
  return out;
}

// The pictures as { name, type, data: Buffer }, fetched or decoded.
export async function loadImages(list, fetchImpl = fetch) {
  if (!list || !list.length) return [];
  if (list.length > MAX_IMAGES) throw new InputError(`At most ${MAX_IMAGES} images per call.`);
  const out = [];
  for (const img of list) {
    const name = cleanName(img.name);
    if (!name) throw new InputError(`Image name "${img.name}" is not usable: use letters, digits, ".", "-" and "_".`);
    let data;
    let type = typeOf(name, img.mime_type);
    if (img.data_base64) {
      const b64 = String(img.data_base64).replace(/^data:([^;,]+);base64,/, (_, t) => { type = type || typeOf(name, t); return ""; });
      data = Buffer.from(b64, "base64");
    } else if (img.url) {
      const got = await fetchBytes(img.url, `Image ${name}`, fetchImpl, MAX_IMAGE, (ct) => (ct && !ct.startsWith("image/") ? "not a picture" : null));
      data = got.data;
      type = type || typeOf(name, got.ct);
    } else {
      throw new InputError(`Image ${name}: give either url or data_base64.`);
    }
    if (!data.length) throw new InputError(`Image ${name} is empty.`);
    if (data.length > MAX_IMAGE) throw new InputError(`Image ${name} is larger than 5 MB.`);
    if (!type) throw new InputError(`Image ${name}: unknown picture type; name it .png, .jpg, .gif, .webp or .svg.`);
    out.push({ name, type, data });
  }
  return out;
}

// The ```vega-lite fences: { open, close } line numbers, the spec's text,
// and the title of the slide they are on.
export function charts(md) {
  const lines = md.split(/\r?\n/);
  const out = [];
  let fence = null;
  let title = "";
  for (let i = 0; i < lines.length; i++) {
    const f = /^\s*(```+|~~~+)\s*([^\s`]*)/.exec(lines[i]);
    if (f) {
      if (!fence) fence = { mark: f[1], lang: f[2].toLowerCase(), open: i };
      else if (f[1].startsWith(fence.mark[0]) && f[1].length >= fence.mark.length && !f[2]) {
        if (fence.lang === "vega-lite" || fence.lang === "vegalite") {
          out.push({ open: fence.open, close: i, text: lines.slice(fence.open + 1, i).join("\n"), title });
        }
        fence = null;
      }
      continue;
    }
    if (fence) continue;
    const h = /^(#{1,2})\s+(.*)$/.exec(lines[i]);
    if (h) title = h[2].replace(/\s*\{[^}]*\}\s*$/, "").trim();
  }
  return out;
}

// A chart's data as the deck writes it: a CSV/JSON URL, a Google Sheets
// link or sheet://…, or { google_sheets, sheet?, range? }
export function dataSource(source) {
  if (typeof source === "string") {
    const s = source.trim();
    let u;
    try { u = new URL(s); } catch { throw new InputError(`source "${s}" is not a URL.`); }
    if (u.protocol !== "https:" && u.protocol !== "sheet:") throw new InputError("source: an https URL (CSV, JSON or a Google Sheets link) or sheet://<id>/<range>.");
    return { url: s };
  }
  const id = String(source?.google_sheets || "").trim();
  if (!id) throw new InputError("source: a URL, or { google_sheets: <sheet id or link>, sheet?, range? }.");
  const out = { source: "google-sheets", id };
  if (source.sheet) out.sheet = String(source.sheet);
  if (source.range) out.range = String(source.range);
  return out;
}

// md with one chart's `data` replaced; chart is its 1-based number among
// the deck's charts, or the title of the slide it is on.
// → { md, spec, index, title }
export function bindChartData(md, chart, source) {
  const all = charts(md);
  if (!all.length) throw new InputError("The presentation has no ```vega-lite chart.");
  let hit;
  if (typeof chart === "number") {
    hit = all[chart - 1];
    if (!hit) throw new InputError(`chart ${chart}: the presentation has ${all.length} chart${all.length > 1 ? "s" : ""}.`);
  } else {
    const want = String(chart).trim().toLowerCase();
    const on = all.filter((c) => c.title.toLowerCase() === want);
    if (!on.length) throw new InputError(`No chart on a slide titled "${chart}". Charts are on: ${all.map((c, i) => `${i + 1}. ${c.title || "(untitled)"}`).join("; ")}.`);
    if (on.length > 1) throw new InputError(`The slide "${chart}" has ${on.length} charts: give chart as its number (${on.map((c) => all.indexOf(c) + 1).join(" or ")}).`);
    hit = on[0];
  }
  let spec;
  try { spec = JSON.parse(hit.text); } catch (e) { throw new InputError(`Chart ${all.indexOf(hit) + 1} is not valid JSON: ${e.message}`); }
  if (!spec || typeof spec !== "object" || Array.isArray(spec)) throw new InputError(`Chart ${all.indexOf(hit) + 1} is not a Vega-Lite spec object.`);
  spec.data = dataSource(source);
  // a layer's own data would win over the new one
  if (Array.isArray(spec.layer)) for (const l of spec.layer) if (l && typeof l === "object") delete l.data;
  const lines = md.split(/\r?\n/);
  lines.splice(hit.open + 1, hit.close - hit.open - 1, ...JSON.stringify(spec, null, 2).split("\n"));
  return { md: lines.join("\n"), spec, index: all.indexOf(hit) + 1, title: hit.title };
}

// Encoding types Vega-Lite knows; any other (a model's "point") puts every
// mark at 0 on that axis. Looked for in layers, concats and facet specs too.
const VL_TYPES = ["quantitative", "ordinal", "nominal", "temporal", "geojson"];
function badTypes(v, out) {
  if (Array.isArray(v)) { for (const x of v) badTypes(x, out); return; }
  if (!v || typeof v !== "object") return;
  if (v.encoding && typeof v.encoding === "object") {
    for (const [ch, def] of Object.entries(v.encoding)) {
      if (def && typeof def === "object" && typeof def.type === "string" && !VL_TYPES.includes(def.type)) out.push(`encoding ${ch} has type "${def.type}"`);
    }
  }
  for (const k of ["layer", "concat", "hconcat", "vconcat", "spec"]) badTypes(v[k], out);
}

// a warning per chart whose spec has an unknown encoding type
export function typeWarnings(md) {
  const out = [];
  charts(md).forEach((c, i) => {
    let spec;
    try { spec = JSON.parse(c.text); } catch { return; }
    const bad = [];
    badTypes(spec, bad);
    if (bad.length) out.push(`Chart ${i + 1}${c.title ? ` on "${c.title}"` : ""}: ${bad.join(", ")}; Vega-Lite types are quantitative, ordinal, nominal and temporal. Points on an axis of an unknown type all fall at 0.`);
  });
  return out;
}
