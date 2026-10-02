// Checks on what a model sends, before anything is stored.

export const THEMES = ["aurora", "nebula", "carbon", "ember", "midnight", "corporate", "editorial"];
export const MAX_MD = 300 * 1024;
export const MAX_CSS = 100 * 1024;
export const MAX_IMAGE = 5 * 1024 * 1024;
export const MAX_IMAGES = 20;

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
  return out;
}

function privateHost(host) {
  const h = host.toLowerCase().replace(/^\[|\]$/g, "");
  return h === "localhost" || h.endsWith(".localhost") || h.endsWith(".internal") || h.endsWith(".local")
    || /^(127\.|10\.|0\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(h)
    || h === "::1" || h.startsWith("fc") || h.startsWith("fd") || h.startsWith("fe80");
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
      let u;
      try { u = new URL(img.url); } catch { throw new InputError(`Image ${name}: "${img.url}" is not a URL.`); }
      if (u.protocol !== "https:" || privateHost(u.hostname)) throw new InputError(`Image ${name}: only public https URLs are fetched.`);
      const res = await fetchImpl(u, { signal: AbortSignal.timeout(15000), headers: { "user-agent": "Sliqtly-MCP/1.0" } });
      if (!res.ok) throw new InputError(`Image ${name}: ${u.hostname} answered ${res.status}.`);
      const ct = (res.headers.get("content-type") || "").split(";")[0].trim();
      if (ct && !ct.startsWith("image/")) throw new InputError(`Image ${name}: the URL gave ${ct}, not a picture.`);
      if (Number(res.headers.get("content-length") || 0) > MAX_IMAGE) throw new InputError(`Image ${name} is larger than 5 MB.`);
      data = Buffer.from(await res.arrayBuffer());
      type = type || typeOf(name, ct);
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
