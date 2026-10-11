// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The light player's rules that need no browser (web/play.js), kept apart
// so they can be tested under Node (web/test/playlink.test.mjs).
//
// A presentation exported to the cloud as a light HTML page (the editor's
// Export ▸ Cloud HTML) is at /r/{id}/{hash}: {id} the deck's viewing link,
// {hash} the recording's (web/recstream.js streamHash), so a new recording
// is a new address and an old one can be cached for good.

const ID = /^[A-Za-z0-9]{6,32}$/;
const HASH = /^[0-9a-f]{24}$/;

/** { id, hash } of a /r/{id}/{hash} address, or null. */
export function playLinkOf(pathname) {
  const m = /^\/r\/([^/]+)\/([^/]+)\/?$/.exec(pathname || "");
  if (!m || !ID.test(m[1]) || !HASH.test(m[2])) return null;
  return { id: m[1], hash: m[2] };
}

/** The slide (0-based) a #slide=N hash names, 0 without one. */
export function slideOfHash(hash) {
  const n = parseInt(new URLSearchParams(String(hash || "").replace(/^#/, "")).get("slide") || "", 10);
  return n > 0 ? n - 1 : 0;
}

/** The page's address on `origin`. */
export function playUrl(origin, id, hash) {
  return origin + "/r/" + id + "/" + hash;
}

/** Where the page reads the recording (GET, View.rgr ViewPictures.recording). */
export function recUrl(link) {
  return "/api/view/" + link.id + "/rec/" + link.hash;
}

/**
 * A key (web/recstream.js RecNav): Space, Enter and N go on to the next
 * step ("next"); the arrows and the page keys from slide to slide; Home and
 * End the first and last slide; A plays it all ("auto"). "" for any other.
 */
export function recActionForKey(key) {
  switch (key) {
    case " ": case "Enter": case "n": case "N":
      return "next";
    case "ArrowRight": case "ArrowDown": case "PageDown":
      return "nextSlide";
    case "ArrowLeft": case "ArrowUp": case "PageUp": case "Backspace": case "p": case "P":
      return "prevSlide";
    case "Home":
      return "first";
    case "End":
      return "last";
    case "a": case "A":
      return "auto";
    default:
      return "";
  }
}
