// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The published window's list (File → Published…): what the signed-in user
// has put out on sliqtly.com, one row each. No DOM: main.js reads the cloud
// (web/sliqtly.js listPublished), this makes the rows, PresPanels draws them
// in the presentations window. A server of one's own lists its own links
// the same way (File → Shared links…, mcp-go/localplay.go published):
// recordings and their links only, at its own address.
//
// What there is to list:
//   link   a presentation's viewing link, /s/{linkId} (Share; links/ in
//          firestore.rules)
//   open   a presentation anyone reads at its own address, /s/{shareId}:
//          any visibility but "private", none at all too (firestore.rules
//          visibility(), as shares were before viewing links)
//   page   its Cloud HTML, the light player at /r/{shown}/{hash}
//   video  its Cloud video, at the same kind of address
// A recording is shown through a link of its own (Export makes one, `file`
// on links/, working the days asked: web/linkdays.js), else through the
// presentation's viewing link (recordings published before links had a
// term), or its own address when that is open (mcp-go/rgr/View.rgr
// recording); without any nobody reaches it, yet it is still kept in the
// cloud. A link past its term shows nothing: its row says so.

import { playUrl } from "./playlink.js";
import { whenText } from "./decklist.js";
import { linkEnds } from "./linkdays.js";

const REC = /^recordings\/web-([0-9a-f]{24})\.(sliqrec|webm)$/;

// shares: [{ id, name, visibility, files: [{ path, size, at }] }] (at: when
//   the file went up, ms, 0 when not known)
// links: [{ id, of, created, since, days, file }] (times in ms; file "" for
//   a presentation's own link)
// → [{ id, kind, deck, name, path, created, size, url, via, ends, hasDeck }]
// id names what a delete removes: "link:<linkId>", "open:<shareId>" or
// "file:<shareId>:<path>". url is "" for a recording nobody reaches; via
// the link it is shown through ("" for none, or the open address); ends
// when that link stops working (ms, 0: it does not); hasDeck whether the
// presentation is still there to open.
export function publishedRows(shares, links, origin, now = Date.now()) {
  const decks = new Map((shares || []).map((s) => [s.id, s]));
  const works = (l) => linkEnds(l) > now;
  const byAge = (links || []).slice().sort((a, b) => (a.created || 0) - (b.created || 0));
  // the presentation's viewing link the older recordings are shown through:
  // the first one made that still works (Share makes one and reuses it)
  const shownBy = new Map();
  // a recording's own link: the newest that still works
  const ownLink = new Map();
  for (const l of byAge) {
    if (!works(l)) continue;
    if (l.file) ownLink.set(l.of + ":" + l.file, l);
    else if (!shownBy.has(l.of)) shownBy.set(l.of, l);
  }
  const rows = [];
  for (const l of links || []) {
    if (l.file) continue;
    rows.push({
      id: "link:" + l.id, kind: "link", deck: l.of, name: decks.get(l.of)?.name || "",
      path: "", created: l.created || 0, size: 0, url: origin + "/s/" + l.id, via: l.id, ends: linkEnds(l), hasDeck: decks.has(l.of),
    });
  }
  for (const s of shares || []) {
    const open = s.visibility !== "private";
    if (open) {
      rows.push({
        id: "open:" + s.id, kind: "open", deck: s.id, name: s.name || "",
        path: "", created: 0, size: 0, url: origin + "/s/" + s.id, via: "", ends: 0, hasDeck: true,
      });
    }
    for (const f of s.files || []) {
      const m = REC.exec(f.path || "");
      if (!m) continue;
      const own = ownLink.get(s.id + ":" + f.path) || shownBy.get(s.id) || null;
      const shown = own ? own.id : open ? s.id : "";
      rows.push({
        id: "file:" + s.id + ":" + f.path, kind: m[2] === "webm" ? "video" : "page", deck: s.id,
        name: s.name || "", path: f.path, created: f.at || 0, size: f.size || 0,
        url: shown ? playUrl(origin, shown, m[1]) : "", via: own ? own.id : "", ends: own ? linkEnds(own) : 0, hasDeck: true,
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
// recordings shown through it (another working link of the presentation,
// or its own address when open, still shows them); a recording's own link
// is not the presentation's, so those keep theirs.
export function afterRemoval(rows, id, origin, now = Date.now()) {
  const r = removal(id);
  const left = rows.filter((x) => x.id !== id);
  if (!r || r.what === "file") return left;
  const gone = rows.find((x) => x.id === id);
  if (!gone) return left;
  const deck = gone.deck;
  const other = left.filter((x) => x.deck === deck && x.kind === "link" && !(x.ends > 0 && x.ends <= now)).sort((a, b) => a.created - b.created)[0];
  const open = left.some((x) => x.deck === deck && x.kind === "open");
  return left.map((x) => {
    if (x.deck !== deck || (x.kind !== "page" && x.kind !== "video")) return x;
    // shown through a link that stays: as it was
    if (x.via && x.via !== gone.via) return x;
    const hash = REC.exec(x.path)[1];
    if (other) return { ...x, url: playUrl(origin, other.via, hash), via: other.via, ends: other.ends };
    return { ...x, url: open ? playUrl(origin, deck, hash) : "", via: "", ends: 0 };
  });
}

// "Works until 14.10.2026" or "Expired" for a row shown through a link with
// a term; "" for one without. t translates.
export function termText(row, t, now = Date.now()) {
  if (!row.ends) return "";
  if (row.ends <= now) return t("Expired");
  const d = new Date(row.ends);
  return t("Works until {date}").replace("{date}", `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()}`);
}

// "14.10.2026" when the row's link stops working, "Expired" past it, ""
// for a link without a term. t translates.
export function endsText(row, t, now = Date.now()) {
  if (!row.ends) return "";
  if (row.ends <= now) return t("Expired");
  const d = new Date(row.ends);
  return `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()}`;
}

export const SORTS = ["created", "name", "size", "ends"];

// times and sizes largest first, names A–Z, the soonest to expire first
export function firstDir(by) {
  return by === "name" || by === "ends" ? "asc" : "desc";
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
      : by === "ends"
        // a link without a term ends last
        ? (a, b) => sign * ((b.ends || Infinity) - (a.ends || Infinity) || 0) || tie(a, b)
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

// i18n: "Viewing link" "Open to anyone with its address" "Cloud HTML (light player)" "Cloud video (.webm)" "Light player (HTML)" "Video (.webm)"
const KIND = { link: "Viewing link", open: "Open to anyone with its address", page: "Cloud HTML (light player)", video: "Cloud video (.webm)" };
// a server of one's own (mcp-go/localplay.go): its links at its own
// address, nothing in the cloud
const OWN_KIND = { ...KIND, page: "Light player (HTML)", video: "Video (.webm)" };

// The window's title: what is out on sliqtly.com, or a server of one's
// own's links. t translates.
export function publishedTitle(t, own = false) {
  return own ? t("Shared links") : t("Published in the cloud");
}

// What the presentations window is sent for this list (PresPanels
// decksDialog): the deck list's fields, with the window's own title, column
// heads, request prefix and empty text. A row's "where" is its kind,
// address and size; "added" when it was published, "modified" when its link
// expires; "acts" its buttons: copy the link, open the presentation in the
// editor (requests "pub:copy:<id>", "pub:deck:<id>"). t translates.
// own: a server of one's own's links (File → Shared links…).
export function publishedListJson(rows, by, t, note = "", dir, now = Date.now(), own = false) {
  const key = SORTS.includes(by) ? by : "created";
  const way = dir === "asc" || dir === "desc" ? dir : firstDir(key);
  const kinds = own ? OWN_KIND : KIND;
  return JSON.stringify({
    list: "pub",
    title: publishedTitle(t, own),
    head1: "created",
    head1Label: t("Published"),
    head2: "ends",
    head2Label: t("Expires"),
    empty: own
      ? t("No links yet. Export ▸ Link: light player (HTML) and Export ▸ Link: video (.webm) make one.")
      : t("Nothing published. Share, Export ▸ Cloud HTML and Export ▸ Cloud video publish a presentation on sliqtly.com."),
    sort: key,
    dir: way,
    rows: sortPublished(rows, key, way).map((r) => ({
      id: r.id,
      name: r.name || t("presentation"),
      where: [t(kinds[r.kind]), r.url ? shortUrl(r.url) : t("not shown: no viewing link"), sizeText(r.size)].filter(Boolean).join(" · "),
      added: whenText(r.created),
      modified: r.url ? endsText(r, t, now) : "",
      acts: [r.url ? { id: "copy", label: t("Copy link") } : null, r.hasDeck ? { id: "deck", label: t("Presentation") } : null].filter(Boolean),
      canDelete: true,
      current: false,
    })),
    note,
  });
}
