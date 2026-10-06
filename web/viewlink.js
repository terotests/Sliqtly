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
    const slides = new URLSearchParams(search || "").get("slides");
    return { id: m[1], slides: slides || "", slide: at };
  }
  const share = h.get("share");
  if (share && ID.test(share)) return { id: share, slides: "", slide: at };
  if (h.has("md")) return { md: true };
  return null;
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
