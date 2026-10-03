// Text that does not stand out from a slide's background picture.
//
// The Go server (mcp-go/rgr/Contrast.rgr) lays the deck out with the
// editor's own model and judges every run of text against the pixels under
// it, as the editor's painter does. This server has no layout, so it
// ESTIMATES: the theme's heading colour against the top fifth of the picture
// and its body colour against the rest, after the slide's bg-dim, by the same
// rule (WCAG 4.5:1, 3:1 for large text; a quarter of the points may fall
// short) and with the same fixes (the least bg-dim that reads, or the text's
// colour lightened or darkened until it reads).

import jpeg from "jpeg-js";
import { PNG } from "pngjs";

const GRID = 48;
const SHORT_SHARE = 0.25;

const channel = (v) => {
  v /= 255;
  return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
};
export const luminance = (r, g, b) => 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
const ratioOf = (a, b) => (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);

// The picture as { w, h, grid } (GRID×GRID RGBA, each cell the mean of the
// pixels it covers), or null when it does not decode (SVG, WebP, GIF).
export function pictureGrid(data) {
  let img;
  try {
    if (data[0] === 0x89 && data[1] === 0x50) img = PNG.sync.read(data);
    else if (data[0] === 0xff && data[1] === 0xd8) img = jpeg.decode(data, { useTArray: true, maxMemoryUsageInMB: 512 });
    else return null;
  } catch {
    return null;
  }
  const { width: w, height: h, data: px } = img;
  if (!w || !h) return null;
  const grid = new Array(GRID * GRID * 4).fill(0);
  for (let gy = 0; gy < GRID; gy++) {
    const y0 = Math.floor((gy * h) / GRID), y1 = Math.max(y0 + 1, Math.floor(((gy + 1) * h) / GRID));
    for (let gx = 0; gx < GRID; gx++) {
      const x0 = Math.floor((gx * w) / GRID), x1 = Math.max(x0 + 1, Math.floor(((gx + 1) * w) / GRID));
      const sx = Math.max(1, Math.floor((x1 - x0) / 8)), sy = Math.max(1, Math.floor((y1 - y0) / 8));
      let r = 0, g = 0, b = 0, a = 0, n = 0;
      for (let y = y0; y < y1 && y < h; y += sy) {
        for (let x = x0; x < x1 && x < w; x += sx) {
          const i = (y * w + x) * 4, pa = px[i + 3];
          r += px[i] * pa; g += px[i + 1] * pa; b += px[i + 2] * pa; a += pa; n++;
        }
      }
      const o = (gy * GRID + gx) * 4;
      if (a > 0) {
        grid[o] = r / a; grid[o + 1] = g / a; grid[o + 2] = b / a; grid[o + 3] = a / n;
      }
    }
  }
  return { w, h, grid };
}

// --- the stylesheet's colours

function parseColor(v) {
  if (!v) return null;
  v = v.trim().toLowerCase();
  let m = /^#([0-9a-f]{3})$/.exec(v);
  if (m) return [...m[1]].map((c) => parseInt(c + c, 16));
  m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/.exec(v);
  if (m) return [0, 2, 4].map((k) => parseInt(m[1].slice(k, k + 2), 16));
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/.exec(v);
  if (m) return [m[1], m[2], m[3]].map(Number);
  if (v === "white") return [255, 255, 255];
  if (v === "black") return [0, 0, 0];
  return null;
}

// what the sheet sets, later rules winning: { "page": { "background-color": … }, … }
function rules(css) {
  const out = {};
  const text = String(css || "").replace(/\/\*[\s\S]*?\*\//g, "");
  for (const m of text.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const decls = {};
    for (const d of m[2].split(";")) {
      const at = d.indexOf(":");
      if (at > 0) decls[d.slice(0, at).trim().toLowerCase()] = d.slice(at + 1).trim();
    }
    for (const sel of m[1].split(",").map((s) => s.trim().toLowerCase())) out[sel] = { ...out[sel], ...decls };
  }
  return out;
}

const pt = (v) => {
  const m = /^([\d.]+)\s*(pt|px)?$/.exec(String(v || "").trim());
  if (!m) return null;
  return m[2] === "px" ? Number(m[1]) * 0.75 : Number(m[1]);
};

// paper, heading and body colours and whether the body is large text
export function sheetColours(css) {
  const r = rules(css);
  const get = (sels, prop) => {
    for (const s of sels) if (r[s] && r[s][prop]) return r[s][prop];
    return null;
  };
  const paper = parseColor(get(["page"], "background-color") || get(["page"], "background")) || [255, 255, 255];
  const body = parseColor(get(["document", "p"], "color")) || [0, 0, 0];
  const heading = parseColor(get(["h2", "heading", "h1"], "color")) || body;
  // WCAG large text: 18pt, or 14pt bold
  const size = pt(get(["p", "document"], "font-size")) || 20;
  return { paper, heading, body, bodyNeed: size >= 18 ? 3 : 4.5 };
}

// --- the judging

const mixTowards = (c, up, t) => c.map((v) => Math.round(up ? v + (255 - v) * t : v * (1 - t)));

// the colour nearest c that reads over backdrop luminances lbs (sorted), as
// the editor's painter's readableColour
function readableColour(c, lbs, need) {
  const n = lbs.length;
  const lo = lbs[Math.floor((n - 1) * SHORT_SHARE)];
  const hi = lbs[Math.ceil((n - 1) * (1 - SHORT_SHARE))];
  const goal = Math.max(need, 4.5) * 1.05;
  const upL = goal * (hi + 0.05) - 0.05;
  const downL = (lo + 0.05) / goal - 0.05;
  const lt = luminance(...c);
  const canUp = upL <= 1, canDown = downL >= 0;
  let up;
  if (canUp && canDown) up = upL - lt <= lt - downL;
  else if (canUp || canDown) up = canUp;
  else return ratioOf(1, hi) >= ratioOf(0, lo) ? [255, 255, 255] : [0, 0, 0];
  const ok = (t) => {
    const l = luminance(...mixTowards(c, up, t));
    return up ? l >= upL : l <= downL;
  };
  if (!ok(1)) return up ? [255, 255, 255] : [0, 0, 0];
  let a = 0, b = 1;
  for (let i = 0; i < 14; i++) {
    const m = (a + b) / 2;
    if (ok(m)) b = m; else a = m;
  }
  return mixTowards(c, up, b);
}

// the luminances of the picture's rows [r0, r1) as the slide shows them:
// cover-cropped to 16:9, composited over white, then dimmed towards paper
function band(pic, r0, r1, paper, dim) {
  const src = pic.w / pic.h, box = 16 / 9;
  let u0 = 0, u1 = 1, v0 = 0, v1 = 1;
  if (src > box) { const f = box / src; u0 = (1 - f) / 2; u1 = 1 - u0; }
  else { const f = src / box; v0 = (1 - f) / 2; v1 = 1 - v0; }
  const out = [];
  for (let j = 0; j < 12; j++) {
    const ty = r0 + ((r1 - r0) * (j + 0.5)) / 12;
    const gy = Math.min(GRID - 1, Math.floor((v0 + (v1 - v0) * ty) * GRID));
    for (let i = 0; i < 24; i++) {
      const gx = Math.min(GRID - 1, Math.floor((u0 + (u1 - u0) * ((i + 0.5) / 24)) * GRID));
      const o = (gy * GRID + gx) * 4, a = pic.grid[o + 3] / 255;
      const c = [0, 1, 2].map((k) => 255 * (1 - a) + pic.grid[o + k] * a).map((v, k) => v * (1 - dim) + paper[k] * dim);
      out.push(luminance(...c));
    }
  }
  return out.sort((a, b) => a - b);
}

function judge(colour, lbs, need) {
  const lt = luminance(...colour);
  const ratios = lbs.map((lb) => ratioOf(lt, lb)).sort((a, b) => a - b);
  const at = ratios[Math.floor(ratios.length * SHORT_SHARE)];
  return at >= need ? null : { ratio: at, need, fix: readableColour(colour, lbs, need) };
}

function parts(pic, colours, dim) {
  const out = [];
  const h = judge(colours.heading, band(pic, 0.03, 0.2, colours.paper, dim), 3);
  if (h) out.push({ what: "the heading", ...h });
  const b = judge(colours.body, band(pic, 0.25, 0.9, colours.paper, dim), colours.bodyNeed);
  if (b) out.push({ what: "the body text", ...b });
  return out;
}

const one = (v) => (Math.round(v * 10) / 10).toFixed(1);
const hex = (c) => "#" + c.map((v) => Math.max(0, Math.min(255, v)).toString(16).padStart(2, "0")).join("");

// The slides with a background picture, and their bg-dim, from the
// Markdown: [{ title, page, picture, dim }].
export function backgrounds(md) {
  const out = [];
  let fence = null, page = -1;
  for (const line of md.split(/\r?\n/)) {
    const f = /^\s*(```+|~~~+)/.exec(line);
    if (f) {
      if (!fence) fence = f[1];
      else if (f[1].startsWith(fence[0]) && f[1].length >= fence.length) fence = null;
      continue;
    }
    if (fence) continue;
    const h = /^(#{1,2})\s+(.*)$/.exec(line);
    if (!h) continue;
    page += 1;
    const attrs = /\{([^}]*)\}\s*$/.exec(h[2]);
    const bg = attrs && /(?:^|\s)bg=media\/([A-Za-z0-9._-]+)/.exec(attrs[1]);
    if (!bg) continue;
    const dim = /(?:^|\s)bg-dim=([\d.]+)/.exec(attrs[1]);
    out.push({ title: h[2].replace(/\s*\{[^}]*\}\s*$/, "").trim(), page, picture: bg[1], dim: dim ? Math.min(1, Number(dim[1]) || 0) : 0 });
  }
  return out;
}

// Notes for the slides whose text likely does not stand out from their
// background picture. pictures: name → Buffer; css: the deck's whole sheet.
export function contrastWarnings(md, css, pictures) {
  const colours = sheetColours(css);
  const out = [];
  const grids = new Map();
  for (const s of backgrounds(md)) {
    const data = pictures.get(s.picture);
    if (!data) continue;
    if (!grids.has(s.picture)) grids.set(s.picture, pictureGrid(data));
    const pic = grids.get(s.picture);
    if (!pic) continue;
    const low = parts(pic, colours, s.dim);
    if (!low.length) continue;
    let dimFix = 0;
    for (let step = Math.floor(s.dim * 20) + 1; step <= 18 && !dimFix; step++) {
      if (!parts(pic, colours, step / 20).length) dimFix = step / 20;
    }
    const where = s.title ? `Slide "${s.title}"` : `Slide ${s.page + 1}`;
    const what = low.map((l) => `${l.what} about ${one(l.ratio)}:1 (needs ${one(l.need)}:1)`).join(", ");
    const colour = hex(low[0].fix);
    const fix = dimFix
      ? `a stronger dim, bg-dim=${dimFix} in the slide's heading attributes, or a text colour such as ${colour} in css`
      : `a text colour such as ${colour} in css, or a calmer picture (bg-dim does not help: it fades towards the slide colour)`;
    out.push(`${where}: text is likely hard to read over the background picture (estimated from the picture; this server does not lay the text out): ${what}. Fix: ${fix}.`);
  }
  return out;
}
