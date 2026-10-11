// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The published window's list (File → Published…): what the signed-in user
// has put out on sliqtly.com, one row each. No DOM: main.js reads the cloud
// (web/sliqtly.js listPublished), this makes the rows, PresPanels draws them
// in the presentations window.
//
// What there is to list:
//   link   a presentation's viewing link, /s/{linkId} (Share; links/ in
//          firestore.rules)
//   open   a presentation anyone reads at its own address, /s/{shareId}:
//          any visibility but "private", none at all too (firestore.rules
//          visibility(), as shares were before viewing links)
//   page   its Cloud HTML, the light player at /r/{shown}/{hash}
//   video  its Cloud video, at the same kind of address
// A recording is shown through the presentation's viewing link (or its own
// address when that is open, mcp-go/rgr/View.rgr recording); without either
// nobody reaches it, yet it is still kept in the cloud.

import { playUrl } from "./playlink.js";
import { whenText } from "./decklist.js";

const REC = /^recordings\/web-([0-9a-f]{24})\.(sliqrec|webm)$/;

// shares: [{ id, name, visibility, files: [{ path, size, at }] }] (at: when
//   the file went up, ms, 0 when not known)
// links: [{ id, of, created }] (created in ms)
// → [{ id, kind, deck, name, path, created, size, url }]
// id names what a delete removes: "link:<linkId>", "open:<shareId>" or
// "file:<shareId>:<path>". url is "" for a recording nobody reaches.
export function publishedRows(shares, links, origin) {
  const decks = new Map((shares || []).map((s) => [s.id, s]));
  // the presentation's viewing link its recordings are shown through: the
  // first one made (Share makes one and reuses it)
  const shownBy = new Map();
  for (const l of (links || []).slice().sort((a, b) => (a.created || 0) - (b.created || 0))) {
    if (!shownBy.has(l.of)) shownBy.set(l.of, l.id);
  }
  const rows = [];
  for (const l of links || []) {
    rows.push({
      id: "link:" + l.id, kind: "link", deck: l.of, name: decks.get(l.of)?.name || "",
      path: "", created: l.created || 0, size: 0, url: origin + "/s/" + l.id,
    });
  }
  for (const s of shares || []) {
    const open = s.visibility !== "private";
    if (open) {
      rows.push({
        id: "open:" + s.id, kind: "open", deck: s.id, name: s.name || "",
        path: "", created: 0, size: 0, url: origin + "/s/" + s.id,
      });
    }
    const shown = shownBy.get(s.id) || (open ? s.id : "");
    for (const f of s.files || []) {
      const m = REC.exec(f.path || "");
      if (!m) continue;
      rows.push({
        id: "file:" + s.id + ":" + f.path, kind: m[2] === "webm" ? "video" : "page", deck: s.id,
        name: s.name || "", path: f.path, created: f.at || 0, size: f.size || 0,
        url: shown ? playUrl(origin, shown, m[1]) : "",
      });
    }
  }
  return rows;
}

// What a row's delete does: { what: "link", link } (the viewing link goes:
// /s/{link} and the recordings shown through it show nothing), { what:
// "private", deck } (the presentation's own address is read by its owner
// only), { what: "file", deck, path } (the recording leaves the cloud), or
// null for an id that is none of these.
export function removal(id) {
  const s = String(id || "");
  if (s.startsWith("link:") && s.length > 5) return { what: "link", link: s.slice(5) };
  if (s.startsWith("open:") && s.length > 5) return { what: "private", deck: s.slice(5) };
  const m = /^file:([^:]+):(.+)$/.exec(s);
  if (m && REC.test(m[2])) return { what: "file", deck: m[1], path: m[2] };
  return null;
}

// The rows a removal leaves: a viewing link gone takes the address of the
// recordings shown through it (another link of the presentation, or its
// own address when open, still shows them).
export function afterRemoval(rows, id, origin) {
  const r = removal(id);
  const left = rows.filter((x) => x.id !== id);
  if (!r || r.what === "file") return left;
  const gone = rows.find((x) => x.id === id);
  if (!gone) return left;
  const deck = gone.deck;
  const other = left.filter((x) => x.deck === deck && x.kind === "link").sort((a, b) => a.created - b.created)[0];
  const open = left.some((x) => x.deck === deck && x.kind === "open");
  const shown = other ? other.id.slice(5) : open ? deck : "";
  return left.map((x) => {
    if (x.deck !== deck || (x.kind !== "page" && x.kind !== "video")) return x;
    const hash = REC.exec(x.path)[1];
    return { ...x, url: shown ? playUrl(origin, shown, hash) : "" };
  });
}

export const SORTS = ["created", "name", "size"];

// times and sizes largest first, names A–Z
export function firstDir(by) {
  return by === "name" ? "asc" : "desc";
}

// As the presentations window's heads: the same column again turns the
// order round, another starts in its first way.
export function nextSort(cur, by) {
  if (!SORTS.includes(by)) return cur;
  if (cur && cur.by === by) return { by, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { by, dir: firstDir(by) };
}

// Ties fall back to the newest first, then the name.
export function sortPublished(rows, by = "created", dir = firstDir(by)) {
  const name = (r) => String(r.name || "").toLocaleLowerCase();
  const sign = dir === "asc" ? -1 : 1;
  const tie = (a, b) => (b.created - a.created) || name(a).localeCompare(name(b)) || a.id.localeCompare(b.id);
  const cmp = by === "name"
    ? (a, b) => -sign * name(a).localeCompare(name(b)) || tie(a, b)
    : by === "size"
      ? (a, b) => sign * (b.size - a.size) || tie(a, b)
      : (a, b) => sign * (b.created - a.created) || tie(a, b);
  return rows.slice().sort(cmp);
}

// "12.3 MB", "640 kB"; "" for none
export function sizeText(bytes) {
  if (!bytes) return "";
  if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + " MB";
  return Math.max(1, Math.round(bytes / 1024)) + " kB";
}

// "sliqtly.com/r/Ab12Cd/…": the address without its scheme, its hash cut short
export function shortUrl(url) {
  return String(url || "").replace(/^https?:\/\//, "").replace(/\/([0-9a-f]{6})[0-9a-f]{18}$/, "/$1…");
}

// i18n: "Viewing link" "Open to anyone with its address" "Cloud HTML (light player)" "Cloud video (.webm)"
const KIND = { link: "Viewing link", open: "Open to anyone with its address", page: "Cloud HTML (light player)", video: "Cloud video (.webm)" };

// What the presentations window is sent for this list (PresPanels
// decksDialog): the deck list's fields, with the window's own title, column
// heads, request prefix and empty text. A row's "where" is its kind and
// address; "added" when it was published, "modified" its size. t translates.
export function publishedListJson(rows, by, t, note = "", dir) {
  const key = SORTS.includes(by) ? by : "created";
  const way = dir === "asc" || dir === "desc" ? dir : firstDir(key);
  return JSON.stringify({
    list: "pub",
    title: t("Published in the cloud"),
    head1: "created",
    head1Label: t("Published"),
    head2: "size",
    head2Label: t("Size"),
    empty: t("Nothing published. Share, Export ▸ Cloud HTML and Export ▸ Cloud video publish a presentation on sliqtly.com."),
    sort: key,
    dir: way,
    rows: sortPublished(rows, key, way).map((r) => ({
      id: r.id,
      name: r.name || t("presentation"),
      where: t(KIND[r.kind]) + " · " + (r.url ? shortUrl(r.url) : t("not shown: no viewing link")),
      added: whenText(r.created),
      modified: sizeText(r.size),
      canDelete: true,
      current: false,
    })),
    note,
  });
}
