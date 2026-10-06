// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Where a vectorized picture came from, and where its SVG goes.
//
// An SVG the vectorizer saved carries the picture it was traced from and the
// settings it was traced with, in a <metadata> element right after its <svg>
// tag:
//
//   <metadata id="sliqtly-trace" data-source="media/kuva.png" data-settings="preset=photo;colorCount=24"/>
//
// so Edit on the SVG opens the vectorizer on that picture again with those
// settings, and Save writes the same SVG. An SVG without it (traced before
// this, or made elsewhere) is traced again from a picture of the same name.
// Saving a picture's trace when its SVG already exists asks whether to
// replace that one or keep both (base-2.svg, base-3.svg…).

const MARK = 'id="sliqtly-trace"';
const PICTURE_EXTS = [".png", ".jpg", ".jpeg", ".webp", ".gif"];

function attrEsc(s) {
  return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function attrUnesc(s) {
  return String(s).replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
}

// The settings as one line: `name=value` pairs with `;` between them
// (PresTraceSettings.parse reads both forms).
export function settingsLine(text) {
  return String(text || "").split(/[\r\n;]+/).map((s) => s.trim()).filter(Boolean).join(";");
}

// `svg` with the stamp (an earlier one replaced).
export function stampSvg(svg, source, settings) {
  const clean = unstampSvg(svg);
  const open = clean.search(/<svg[\s>]/);
  if (open < 0) return clean;
  const end = clean.indexOf(">", open);
  if (end < 0) return clean;
  const tag = `<metadata ${MARK} data-source="${attrEsc(source)}" data-settings="${attrEsc(settingsLine(settings))}"/>`;
  return clean.slice(0, end + 1) + tag + clean.slice(end + 1);
}

export function unstampSvg(svg) {
  return String(svg).replace(/<metadata id="sliqtly-trace"[^>]*\/>/g, "");
}

// {source, settings} from an SVG's text (its first few KB do), or null.
export function readStamp(svgText) {
  const m = /<metadata id="sliqtly-trace"([^>]*)\/>/.exec(String(svgText || ""));
  if (!m) return null;
  const attr = (name) => {
    const a = new RegExp(`\\s${name}="([^"]*)"`).exec(m[1]);
    return a ? attrUnesc(a[1]) : "";
  };
  const source = attr("data-source");
  if (!source) return null;
  return { source, settings: attr("data-settings") };
}

function baseOf(path) {
  const slash = path.lastIndexOf("/");
  const dot = path.lastIndexOf(".");
  return dot > slash ? path.slice(0, dot) : path;
}

// The picture an SVG is traced again from: the stamped one while it is in
// the files, else a picture of the same name ("" when there is none).
export function retraceSource(svgPath, stamp, paths) {
  const have = new Set(paths);
  if (stamp && stamp.source && have.has(stamp.source)) return stamp.source;
  const base = baseOf(svgPath);
  const lower = new Map(paths.map((p) => [p.toLowerCase(), p]));
  for (const ext of PICTURE_EXTS) {
    const hit = lower.get((base + ext).toLowerCase());
    if (hit) return hit;
  }
  return "";
}

// Where the trace of `picture` is saved: `existing` its SVG already in the
// files ("" none), `fresh` the next free name beside it.
export function svgTarget(picture, paths) {
  const have = new Set(paths);
  const base = baseOf(picture);
  const first = base + ".svg";
  let fresh = first;
  for (let i = 2; have.has(fresh); i += 1) fresh = `${base}-${i}.svg`;
  return { existing: have.has(first) ? first : "", fresh };
}

// Whether a picture's pixels (RGBA) look flat — a drawing, a logo, a chart,
// a screenshot — rather than a photo: most of it in a few colours. Such a
// picture is vectorized by default when added (an SVG stays sharp at any
// size); a photo is not, since tracing turns it into a poster.
// Colours are counted at 4 bits a channel over at most ~40 000 pixels, the
// fully transparent ones left out; flat when the 24 commonest cover 85 %.
export function looksFlat(rgba, w, h) {
  const n = w * h;
  if (!n || !rgba || rgba.length < n * 4) return false;
  const step = Math.max(1, Math.floor(Math.sqrt(n / 40000)));
  const count = new Map();
  let seen = 0;
  for (let y = 0; y < h; y += step) {
    for (let x = 0; x < w; x += step) {
      const i = (y * w + x) * 4;
      if (rgba[i + 3] < 16) continue;
      const key = ((rgba[i] >> 4) << 8) | ((rgba[i + 1] >> 4) << 4) | (rgba[i + 2] >> 4);
      count.set(key, (count.get(key) || 0) + 1);
      seen += 1;
    }
  }
  if (!seen) return false;
  const top = [...count.values()].sort((a, b) => b - a).slice(0, 24).reduce((a, b) => a + b, 0);
  return top / seen >= 0.85;
}
