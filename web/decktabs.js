// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The presentations open in this tab of the browser, a tab each in the row
// under the top bar (PresApp's deck tabs, EVGUI's DocTabsCtl): what a tab's
// key names and how the deck behind it is opened again.
//
// A deck this browser keeps is its id. One shown but not kept yet has no
// record to open, so its key says where it came from: "sample:<key>" (a
// sample, opened again from the sample) or "cloud:<share id>" (the owner's
// deck in the cloud). When such a deck is kept on its first change, its tab
// takes the deck's id. A deck with neither (a shared one opened as a copy,
// a pasted link) cannot come back once left, so its tab goes when it is left.
//
// The row lasts over a reload of the tab: sessionStorage, DECK_TABS_KEY, in
// PresApp.deckTabsState's form (the key in front, then "<key>\t<label>").

export const DECK_TABS_KEY = "sliqtly.deckTabs";

/** The tab key of the deck shown: { persisted, id, src }. */
export function deckKey(doc) {
  if (doc.persisted) return doc.id;
  return doc.src || doc.id;
}

/** Whether a deck that is left can be opened again from its tab. */
export function canReturn(doc) {
  return !!(doc.persisted || doc.src);
}

/** How a tab's deck is opened: { kind: "sample" | "cloud" | "doc", arg }. */
export function reopenPlan(key) {
  const k = String(key || "");
  if (k.startsWith("sample:")) return { kind: "sample", arg: k.slice(7) };
  if (k.startsWith("cloud:")) return { kind: "cloud", arg: k.slice(6) };
  return { kind: "doc", arg: k };
}

/** A label as a tab can carry it: one line, no tab character. */
export function tabLabel(name) {
  return String(name || "").replace(/[\t\r\n]+/g, " ").trim() || "presentation";
}

export function readDeckTabs(storage) {
  try {
    return storage?.getItem(DECK_TABS_KEY) || "";
  } catch (_) {
    return "";
  }
}

export function keepDeckTabs(storage, state) {
  try {
    if (state && state.includes("\n")) storage?.setItem(DECK_TABS_KEY, state);
    else storage?.removeItem(DECK_TABS_KEY);
  } catch (_) { /* this page only */ }
}
