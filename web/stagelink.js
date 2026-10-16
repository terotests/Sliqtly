// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A link clicked on a slide in the editor (src/PresStageClick.rgr), the
// page's part: where it goes, kept apart from the page so it can be tested
// under Node (web/test/stagelink.test.mjs). A link to this deck's own
// slides (#heading, #slide=3) never comes here: the app follows those.

import { linkOf } from "./viewlink.js";

// How long the page waits before following a link, so that a double click
// on it (which picks the block) does not leave the slide first. Longer
// than the page's double-click time (400 ms in main.js).
export const FOLLOW_MS = 420;

/**
 * Where `href` goes, seen from a page on `origin` whose public site is
 * `site`: { deck, slide, name, url } for a Sliqtly presentation (/s/{id},
 * /editor/s/{id}, /editor/d/{id}, on this origin or the site), { url } for any other web
 * address (a new tab), or null for one that is not followed (a script
 * address, mail, a path of no use here).
 */
export function linkTarget(href, origin, site) {
  if (!String(href || "").trim()) return null;
  let u;
  try {
    u = new URL(String(href || "").trim(), origin);
  } catch (_) {
    return null;
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return null;
  const ours = u.origin === origin || u.origin === safeOrigin(site);
  if (ours) {
    // the editor's addresses of a deck: /editor/s/{id}, /editor/d/{id}
    const path = u.pathname.replace(/^\/editor\/[sd](?=\/)/, "/s");
    const link = linkOf(path, u.search, u.hash);
    if (link && link.id) return { deck: link.id, slide: link.slide, name: nameOf(u.hash), url: u.href };
  }
  return { url: u.href };
}

function safeOrigin(s) {
  try {
    return new URL(s).origin;
  } catch (_) {
    return "";
  }
}

// "#pdf-export" of /s/{id}#pdf-export: a heading of that deck, its slide
// found once the deck is open (app.followLink); "" for #slide=3 or none
function nameOf(hash) {
  const h = String(hash || "").replace(/^#/, "");
  return h && !h.includes("=") ? "#" + h : "";
}
