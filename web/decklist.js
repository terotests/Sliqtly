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
export function deckRows(local, cloud, currentId) {
  const shares = new Map((cloud || []).map((r) => [r.id, r]));
  const rows = (local || []).map((d) => {
    const share = d.cloud ? shares.get(d.cloud) : null;
    if (share) shares.delete(d.cloud);
    return {
      id: d.id,
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

export const SORTS = ["updated", "created", "name"];

// by: "updated" (the default: last changed first), "created" (newest first)
// or "name" (A–Z); ties fall back to the last changed first.
export function sortRows(rows, by = "updated") {
  const name = (r) => String(r.name || "").toLocaleLowerCase();
  const recent = (a, b) => (b.updated - a.updated) || (b.created - a.created);
  const cmp = by === "name"
    ? (a, b) => name(a).localeCompare(name(b)) || recent(a, b)
    : by === "created"
      ? (a, b) => (b.created - a.created) || recent(a, b)
      : recent;
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

// What the window is sent: {"sort", "rows": [{"id", "name", "added",
// "modified", "where", "current"}], "note"}. t translates.
export function deckListJson(rows, by, t, note = "") {
  const sorted = sortRows(rows, SORTS.includes(by) ? by : "updated");
  return JSON.stringify({
    sort: SORTS.includes(by) ? by : "updated",
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
