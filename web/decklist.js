// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The presentations window's list (File → Presentations…): the decks kept in
// this browser and, signed in, the user's own cloud shares, one row per deck.
// No DOM: main.js reads the stores, this makes the rows, PresPanels draws them.

// local: vfs docs { id, name, created, updated, cloud? (its share id) }
// cloud: listMine rows { id, name, created, updated }
// → [{ id, name, created, updated, where: "browser" | "cloud" | "both", current }]
// A deck kept here and in the cloud is one row, opened from here; its times
// are the later of the two, since an assistant may have changed the share.
// byCloud: a deck kept here that is in the cloud goes by "cloud:<id>" (the
// editor on sliqtly.com, whose store lasts as long as the page)
export function deckRows(local, cloud, currentId, { byCloud = false } = {}) {
  const shares = new Map((cloud || []).map((r) => [r.id, r]));
  const rows = (local || []).map((d) => {
    const share = d.cloud ? shares.get(d.cloud) : null;
    if (share) shares.delete(d.cloud);
    return {
      id: byCloud && d.cloud ? "cloud:" + d.cloud : d.id,
      name: d.name || share?.name || "",
      created: d.created || share?.created || d.updated || 0,
      updated: Math.max(d.updated || 0, share?.updated || 0) || d.created || 0,
      where: share ? "both" : "browser",
      current: d.id === currentId,
    };
  });
  for (const r of shares.values()) {
    rows.push({
      id: "cloud:" + r.id,
      name: r.name || "",
      created: r.created || r.updated || 0,
      updated: r.updated || r.created || 0,
      where: "cloud",
      current: false,
    });
  }
  return rows;
}

// A room's presentations on a server of one's own (get_room's
// [{ deck_id, name, updated }]) as rows of the window: one kept in this
// browser too is that deck's row (its id, its times), the others the
// server's ("cloud:<deck_id>"), as the room's list in the rail opens them.
export function roomShareRows(presentations, local, currentId) {
  const kept = new Map((local || []).filter((d) => d.cloud).map((d) => [d.cloud, d]));
  return (presentations || []).map((p) => {
    const d = kept.get(p.deck_id);
    if (d) {
      return {
        id: d.id,
        name: d.name || p.name || "",
        created: d.created || p.updated || 0,
        updated: Math.max(d.updated || 0, p.updated || 0),
        where: "both",
        current: d.id === currentId,
      };
    }
    return { id: "cloud:" + p.deck_id, name: p.name || "", created: p.updated || 0, updated: p.updated || 0, where: "cloud", current: false };
  });
}

export const SORTS = ["updated", "created", "name"];

// The way a column sorts first: times newest first ("desc"), names A–Z ("asc").
export function firstDir(by) {
  return by === "name" ? "asc" : "desc";
}

// A column's head pressed while the list is sorted by cur ({ by, dir }): the
// same column again turns the order round, another starts in its first way.
export function nextSort(cur, by) {
  if (!SORTS.includes(by)) return cur;
  if (cur && cur.by === by) return { by, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { by, dir: firstDir(by) };
}

// by: "updated" (the default: last changed first), "created" (newest first)
// or "name" (A–Z); dir "asc" or "desc" turns that column's order (the
// column's first way when left out). Ties fall back to the last changed first.
export function sortRows(rows, by = "updated", dir = firstDir(by)) {
  const name = (r) => String(r.name || "").toLocaleLowerCase();
  const recent = (a, b) => (b.updated - a.updated) || (b.created - a.created);
  const sign = dir === "asc" ? -1 : 1;
  const cmp = by === "name"
    ? (a, b) => -sign * name(a).localeCompare(name(b)) || recent(a, b)
    : by === "created"
      ? (a, b) => sign * (b.created - a.created) || recent(a, b)
      : (a, b) => sign * ((b.updated - a.updated) || (b.created - a.created));
  return rows.slice().sort(cmp);
}

// "4.10.2026 11:39"; "" for no time
export function whenText(ms) {
  if (!ms) return "";
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "";
  const two = (n) => String(n).padStart(2, "0");
  return `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()} ${two(d.getHours())}:${two(d.getMinutes())}`;
}

// What the window is sent: {"sort", "dir", "rows": [{"id", "name", "added",
// "modified", "where", "current"}], "note"}. t translates.
export function deckListJson(rows, by, t, note = "", dir) {
  const key = SORTS.includes(by) ? by : "updated";
  const way = dir === "asc" || dir === "desc" ? dir : firstDir(key);
  const sorted = sortRows(rows, key, way);
  return JSON.stringify({
    sort: key,
    dir: way,
    rows: sorted.map((r) => ({
      id: r.id,
      name: r.name || t("presentation"),
      added: whenText(r.created),
      modified: whenText(r.updated),
      where: r.where === "cloud" ? t("In the cloud") : r.where === "both" ? t("This browser and the cloud") : t("This browser"),
      cloudOnly: r.where === "cloud",
      current: !!r.current,
    })),
    note,
  });
}
