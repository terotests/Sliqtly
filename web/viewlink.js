// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The public viewer's rules that need no browser (web/view.js), kept
// apart so they can be tested under Node (web/test/viewlink.test.mjs).

const ID = /^[A-Za-z0-9]{6,32}$/;

/**
 * Which presentation a page address shows: { id, slides, slide } or
 * { md: true } for a link that carries the Markdown itself (#md=…), which
 * this page cannot lay out, or null for the front page.
 *   /s/{id}[?slides=a,b][#slide=3]
 *   /s/{id}?slide=3  one slide's link (Share slide): the server reads it too
 *                (its link card, mcp-go/linkcard.go); #slide= wins
 *   #share={id}  the same where the page has no address of its own: the
 *                assistant's preview (mcp-go/assets/preview.html) gives it in
 *                <meta name="sliqtly-link">, passed here as `given`
 */
export function linkOf(pathname, search, hash, given = "") {
  const h = new URLSearchParams(String(given || hash || "").replace(/^#/, ""));
  const slide = parseInt(h.get("slide") || "", 10);
  const at = slide > 0 ? slide - 1 : 0;
  const m = /^\/s\/([A-Za-z0-9]{6,32})\/?$/.exec(pathname || "");
  if (m) {
    const q = new URLSearchParams(search || "");
    const named = parseInt(q.get("slide") || "", 10);
    return { id: m[1], slides: q.get("slides") || "", slide: slide > 0 || !(named > 0) ? at : named - 1 };
  }
  const share = h.get("share");
  if (share && ID.test(share)) return { id: share, slides: "", slide: at };
  if (h.has("md")) return { md: true };
  return null;
}

/**
 * The link to slide `at` (0-based) of `link` on `origin`: /s/{id}?slide=n,
 * with the picked slides' keys. The slide is in the query, not the hash, so
 * a chat app's preview (which never sees a hash) shows that slide.
 */
export function slideLink(origin, link, at) {
  const q = new URLSearchParams();
  if (link.slides) q.set("slides", link.slides);
  q.set("slide", String(at + 1));
  return origin + "/s/" + link.id + "?" + q.toString();
}

/** The address of the slides of `link` (GET, answered by View.rgr). */
export function viewUrl(link) {
  return "/api/view/" + link.id + (link.slides ? "?slides=" + encodeURIComponent(link.slides) : "");
}

/** The address of `link`'s download as format (pdf, pptx or md; View.rgr ViewExport). */
export function exportUrl(link, format) {
  return "/api/export/" + link.id + "/" + format + (link.slides ? "?slides=" + encodeURIComponent(link.slides) : "");
}

/** A file name for a download of the deck named `name`. */
export function exportName(name, format) {
  const base = String(name || "").replace(/[\u0000-\u001f\/\\:*?"<>|#]/g, "-").trim().slice(0, 80) || "presentation";
  return base + "." + format;
}

/**
 * The pictures to fetch: each stored file a slide draws, under the name its
 * commands use ("/media/x.png"), with the address to fetch it from.
 */
export function picturesOf(deck) {
  return (deck.files || []).filter((f) => f.path && f.url).map((f) => ({ src: "/" + f.path, url: f.url, type: f.type || "", path: f.path }));
}

// The faces a diagram look draws with ({style=cartoon}, …), fetched only for
// a deck that draws with one (web/main.js LOOK_FACES).
export const LOOK_FACES = {
  "Gloria Hallelujah": "GloriaHallelujah.ttf",
  "Fjalla One": "FjallaOne-Regular.ttf",
  "Josefin Sans-Bold": "JosefinSans-Bold.ttf",
  "Droid Serif-BoldItalic": "DroidSerif-BoldItalic.ttf",
  // the faces a deck's CSS can name besides Open Sans and Noto Sans
  // (src/PresFonts.rgr), fetched when a deck uses them
  "Droid Serif": "DroidSerif.ttf",
  "Droid Serif-Bold": "DroidSerif-Bold.ttf",
  "Droid Serif-Italic": "DroidSerif-Italic.ttf",
  "Lato": "Lato-Regular.ttf",
  "Lato-Bold": "Lato-Bold.ttf",
  "Lato-Italic": "Lato-Italic.ttf",
  "Lato-BoldItalic": "Lato-BoldItalic.ttf",
};

/** The LOOK_FACES names the lists' text is set in. */
export function lookFacesOf(lists) {
  const families = new Set();
  for (const l of lists || []) {
    for (const c of l.cmds || []) if (c.font) families.add(c.font);
  }
  return Object.keys(LOOK_FACES).filter((name) => families.has(name.split("-")[0]));
}

/** The slide a key press goes to, or -1 when the key is not one of ours. */
export function slideForKey(key, at, n) {
  switch (key) {
    case "ArrowRight": case "ArrowDown": case "PageDown": case " ": case "Enter": case "n": case "N":
      return Math.min(n - 1, at + 1);
    case "ArrowLeft": case "ArrowUp": case "PageUp": case "Backspace": case "p": case "P":
      return Math.max(0, at - 1);
    case "Home":
      return 0;
    case "End":
      return n - 1;
    default:
      return -1;
  }
}

/**
 * Where the slide goes in a w × h window: centred, as large as it fits,
 * { x, y, scale } in CSS pixels.
 */
export function fitSlide(w, h, pw, ph) {
  const scale = Math.max(0.01, Math.min(w / pw, h / ph));
  return { x: (w - pw * scale) / 2, y: (h - ph * scale) / 2, scale };
}

/**
 * A view of the slide zoomed in by a pinch: `view` ({ x, y, scale }, as
 * fitSlide gives) scaled by f about the fingers' midpoint (px, py), which
 * moved (dx, dy) since `view`, so the point under the fingers stays under
 * them. No smaller than the fitted slide and no more than six times it.
 */
export function pinchView(view, w, h, pw, ph, f, px, py, dx, dy) {
  const fit = fitSlide(w, h, pw, ph).scale;
  const scale = Math.max(fit, Math.min(fit * 6, view.scale * f));
  const u = (px - dx - view.x) / view.scale;
  const v = (py - dy - view.y) / view.scale;
  return keepInView({ x: px - u * scale, y: py - v * scale, scale }, w, h, pw, ph);
}

/** `view` moved by (dx, dy): a zoomed slide dragged with one finger. */
export function panView(view, w, h, pw, ph, dx, dy) {
  return keepInView({ x: view.x + dx, y: view.y + dy, scale: view.scale }, w, h, pw, ph);
}

/** Whether `view` shows the slide larger than it fits the window. */
export function isZoomed(view, w, h, pw, ph) {
  return !!view && view.scale > fitSlide(w, h, pw, ph).scale * 1.0001;
}

// the slide covers the window where it is larger than it, and is centred
// where it is not
function keepInView(view, w, h, pw, ph) {
  const along = (at, size, room) => (size <= room ? (room - size) / 2 : Math.min(0, Math.max(room - size, at)));
  return { x: along(view.x, pw * view.scale, w), y: along(view.y, ph * view.scale, h), scale: view.scale };
}

/**
 * The link under slide point (x, y) on a slide's list (its `links`, laid out
 * by the server: mcp-go/rgr/View.rgr), null for none. A line's height
 * around the words counts, as in the editor (src/PresStageClick.rgr).
 */
export function linkAt(list, x, y) {
  for (const a of (list && list.links) || []) {
    if (x >= a.x - 1 && x <= a.x + a.w + 1 && y >= a.y - 2 && y <= a.y + a.h + 2) return a;
  }
  return null;
}

/**
 * What a click on link area `a` does in the viewer on `origin`, whose
 * public site is `site`: { slide } a slide of this deck, { open } another
 * Sliqtly presentation on this site (this tab, so Back returns), { tab } any
 * other web address (a new tab), or null (a script, mail, nothing).
 */
export function linkGo(a, origin, site) {
  if (!a) return null;
  if (a.slide >= 0) return { slide: a.slide };
  const href = String(a.href || "").trim();
  if (!href || href.startsWith("#")) return null;
  let u;
  try {
    u = new URL(href, origin);
  } catch (_) {
    return null;
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return null;
  let siteOrigin = "";
  try {
    siteOrigin = new URL(site).origin;
  } catch (_) { /* no site */ }
  if (u.origin === origin || (siteOrigin && u.origin === siteOrigin)) {
    // the editor's address of a deck is shown here as the viewer's
    const path = u.pathname.replace(/^\/editor\/[sd](?=\/)/, "/s");
    if (/^\/s\/[A-Za-z0-9]{6,32}\/?$/.test(path)) {
      // a deck on the public site opens there; on this page's own origin
      // (a server of one's own) at this origin
      return { open: (u.origin === origin ? origin : u.origin) + path + u.search + u.hash };
    }
  }
  return { tab: u.href };
}

/**
 * The slide a link's `#name` names (0-based), -1 when none does: a
 * heading's anchor (or its words) or a slide's id; deck.names has each
 * slide's heading words and id.
 */
export function slideOfName(names, hash) {
  const h = String(hash || "").replace(/^#/, "");
  if (!h || /^slide=/.test(h) || h.includes("=")) return -1;
  let name = h;
  try {
    name = decodeURIComponent(h);
  } catch (_) { /* as it is */ }
  const want = anchorOf(name);
  const list = names || [];
  for (let i = 0; i < list.length; i++) {
    for (const n of list[i] || []) if (n === name || (want && anchorOf(n) === want)) return i;
  }
  return -1;
}

// a heading's words as an anchor, as MdAnchor.slug makes it: lower case,
// letters, digits and _ kept, a space or a dash between words one dash,
// other signs left out
function anchorOf(t) {
  let out = "";
  let dash = false;
  for (const ch of String(t || "").trim().toLowerCase()) {
    if (/[\p{L}\p{N}_]/u.test(ch)) {
      if (dash && out) out += "-";
      dash = false;
      out += ch;
    } else if (ch === " " || ch === "-" || ch === "\t") dash = true;
  }
  return out;
}
