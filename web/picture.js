// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A picture file made ready for the slides and the exports.
//
// A PNG, JPEG, GIF or WebP is used as it is. An SVG is a drawing, and the
// rest of the pipeline knows only pixels: createImageBitmap refuses SVG, an
// SVG with only a viewBox has no natural size of its own (Chrome gives it
// 300 × 150 shrunk to the viewBox's shape), and the PDF and PPTX writers
// decode PNG and JPEG. So an SVG is drawn here once, at the size of a full
// slide, and the slides, the PDF and the PPTX all get that PNG. The deck
// keeps the SVG file; it is drawn again whenever the deck is opened.

import { asPicture } from "./image-adjust.js";

// The longer side, in pixels, an SVG is drawn at: a full-slide background on
// a large screen stays sharp.
export const SVG_RASTER = 2560;

export function isSvg(type, path) {
  return /^image\/svg/i.test(type || "") || /\.svgz?$/i.test(path || "");
}

const UNITS = { "": 1, px: 1, pt: 96 / 72, pc: 16, in: 96, cm: 96 / 2.54, mm: 96 / 25.4, q: 96 / 101.6, em: 16, rem: 16, ex: 8, ch: 8 };

// A length attribute in CSS pixels; 0 when it has none (absent, a
// percentage, or nonsense).
export function svgLength(v) {
  const m = /^\s*([+-]?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?)\s*([a-z]*)\s*$/i.exec(v || "");
  if (!m) return 0;
  const k = UNITS[m[2].toLowerCase()];
  const n = Number(m[1]) * (k || 0);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

// The outermost <svg …> start tag: where it is and its attributes.
function rootTag(text) {
  const re = /<svg\b((?:[^>"']|"[^"]*"|'[^']*')*)>/i;
  // skip what may come before it: an XML declaration, comments, a doctype
  const body = String(text || "").replace(/<!--[\s\S]*?-->/g, (c) => " ".repeat(c.length));
  const m = re.exec(body);
  if (!m) return null;
  const attrs = {};
  for (const a of m[1].matchAll(/([^\s=/]+)\s*=\s*("([^"]*)"|'([^']*)')/g)) attrs[a[1]] = a[3] ?? a[4];
  return { start: m.index, end: m.index + m[0].length, inner: m[1], attrs };
}

function viewBoxOf(attrs) {
  const n = String(attrs.viewBox || "").trim().split(/[\s,]+/).map(Number);
  return n.length === 4 && n.every(Number.isFinite) && n[2] > 0 && n[3] > 0 ? n : null;
}

// The SVG's own size in CSS pixels: its width and height, one of them with
// the viewBox's shape, the viewBox alone, or CSS's 300 × 150 when it says
// nothing. Null when the text is not an SVG.
export function svgSize(text) {
  const tag = rootTag(text);
  if (!tag) return null;
  let w = svgLength(tag.attrs.width);
  let h = svgLength(tag.attrs.height);
  const vb = viewBoxOf(tag.attrs);
  if (vb) {
    if (w && !h) h = (w * vb[3]) / vb[2];
    else if (h && !w) w = (h * vb[2]) / vb[3];
    else if (!w && !h) [w, h] = [vb[2], vb[3]];
  }
  return [w || 300, h || 150];
}

// The SVG with its root sized `w` × `h` pixels, so a browser draws it at
// that size (an SVG without a viewBox gets one of its own size, so it is
// scaled, not cut). Null when the text is not an SVG.
export function svgSizedTo(text, w, h) {
  const src = String(text || "");
  const tag = rootTag(src);
  if (!tag) return null;
  const size = svgSize(src);
  let inner = tag.inner.replace(/\s(width|height)\s*=\s*("[^"]*"|'[^']*')/gi, "");
  if (!viewBoxOf(tag.attrs)) inner += ` viewBox="0 0 ${size[0]} ${size[1]}"`;
  inner += ` width="${w}" height="${h}"`;
  return src.slice(0, tag.start) + "<svg" + inner + ">" + src.slice(tag.end);
}

// The pixel size an SVG of size `w` × `h` is drawn at.
export function rasterSize(w, h, most = SVG_RASTER) {
  const k = most / Math.max(w, h, 1);
  return [Math.max(1, Math.round(w * k)), Math.max(1, Math.round(h * k))];
}

function loadImage(blob) {
  const url = URL.createObjectURL(blob);
  const img = new Image();
  img.src = url;
  return img.decode().then(() => img, () => null).finally(() => URL.revokeObjectURL(url));
}

// The picture of `bytes`: `img` to draw (an <img> or a canvas, null when it
// does not decode), its natural size `w` × `h`, and the `bytes` and `type`
// the slides' store and the exports get (an SVG's PNG).
export async function decodePicture(bytes, type, path = "") {
  if (isSvg(type, path)) {
    const text = new TextDecoder().decode(bytes);
    const size = svgSize(text);
    if (!size) return { img: null, w: 0, h: 0, bytes, type };
    const [pw, ph] = rasterSize(size[0], size[1]);
    const img = await loadImage(new Blob([svgSizedTo(text, pw, ph)], { type: "image/svg+xml" }));
    if (!img) return { img: null, w: 0, h: 0, bytes, type };
    const c = document.createElement("canvas");
    c.width = pw;
    c.height = ph;
    c.getContext("2d").drawImage(img, 0, 0, pw, ph);
    const png = await new Promise((r) => c.toBlob(r, "image/png"));
    if (!png) return { img: null, w: 0, h: 0, bytes, type };
    return { img: asPicture(c), w: Math.round(size[0]), h: Math.round(size[1]), bytes: await png.arrayBuffer(), type: "image/png" };
  }
  const img = await loadImage(new Blob([bytes], { type }));
  return img ? { img, w: img.naturalWidth, h: img.naturalHeight, bytes, type } : { img: null, w: 0, h: 0, bytes, type };
}
