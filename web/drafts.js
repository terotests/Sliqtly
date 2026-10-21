// SPDX-License-Identifier: AGPL-3.0-or-later

// What the editor keeps of a deck's text until the place it lives has it,
// and which text a deck opens with (web/main.js openOwnCloud).
//
// A deck living in the cloud or on a server of one's own is written there a
// moment after each change (or by its room, web/collab.js). Until that has
// gone through, the change is only on this page. It is kept:
//   - on a server of one's own, in this browser's deck record (web/vfs.js):
//     `md` the text here, `cloudMd` the text the share was last known to
//     have;
//   - on sliqtly.com's editor, which keeps nothing in IndexedDB (it hung
//     pages, 2026-10-09), as a draft in localStorage: no connection, no
//     transaction and no version to wait for, so it cannot block a page.
//     A deck's text is small; drafts past `maxBytes` drop the oldest.
//
// Opening the deck again (another deck in between, a reload) puts the two
// together instead of taking the share's text over this page's.

import { rebaseText } from "./collab.js";

// Which text a deck opens with. `kept` = { md, cloudMd } (this browser's
// record or draft, or null); `shared` = the share's text now.
// → { md, from }: "shared" (nothing of this page's waits), "kept" (the
// share has not moved since: this page's text is the newer), "merged" (both
// changed: this page's changes on top of the share's).
export function openPlan(ot, kept, shared) {
  const theirs = shared ?? "";
  if (!kept || typeof kept.md !== "string" || kept.md === theirs) return { md: theirs, from: "shared" };
  // not known what the share had: as before, the share's
  if (typeof kept.cloudMd !== "string" || kept.md === kept.cloudMd) return { md: theirs, from: "shared" };
  if (theirs === kept.cloudMd) return { md: kept.md, from: "kept" };
  return { md: rebaseText(ot, kept.cloudMd, kept.md, theirs), from: "merged" };
}

const PREFIX = "sliqtly.draft.";

// Drafts in a Storage (localStorage): one key a deck, { md, cloudMd, at }.
// Every call is synchronous and never throws (a full or refused storage
// keeps nothing).
export function draftStore(storage, { maxBytes = 2_000_000, now = () => Date.now() } = {}) {
  const read = (key) => {
    try {
      const v = JSON.parse(storage?.getItem(key) || "null");
      return v && typeof v.md === "string" ? v : null;
    } catch (_) {
      return null;
    }
  };
  const keys = () => {
    const out = [];
    try {
      for (let i = 0; i < (storage?.length || 0); i += 1) {
        const k = storage.key(i);
        if (k && k.startsWith(PREFIX)) out.push(k);
      }
    } catch (_) { /* none */ }
    return out;
  };
  const drop = (id) => {
    try { storage?.removeItem(PREFIX + id); } catch (_) { /* gone */ }
  };
  return {
    get: (id) => (id ? read(PREFIX + id) : null),
    drop,
    // this page's text of deck `id`, and the share's as last known; nothing
    // kept when the two are the same (the share has it all)
    keep(id, md, cloudMd) {
      if (!id || typeof md !== "string") return;
      if (md === cloudMd) {
        drop(id);
        return;
      }
      const rec = JSON.stringify({ md, cloudMd: typeof cloudMd === "string" ? cloudMd : null, at: now() });
      if (rec.length > maxBytes) return;
      // the oldest others go while all of them are over the limit
      const others = keys().filter((k) => k !== PREFIX + id)
        .map((k) => {
          let size = 0;
          try { size = (storage.getItem(k) || "").length; } catch (_) { /* none */ }
          return { k, at: read(k)?.at || 0, size };
        })
        .sort((a, b) => a.at - b.at);
      let total = rec.length + others.reduce((n, o) => n + o.size, 0);
      for (const o of others) {
        if (total <= maxBytes) break;
        try { storage.removeItem(o.k); } catch (_) { /* fine */ }
        total -= o.size;
      }
      try { storage?.setItem(PREFIX + id, rec); } catch (_) { /* full: not kept */ }
    },
  };
}

const EDITING_KEY = "sliqtly.editing";

// The decks this browser tab has in Edit (sessionStorage): a deck left in
// Edit is in Edit when it is opened again in the tab, after a reload too.
// Keys are the decks' own (a share's id, or this browser's deck id).
export function editKeeper(storage, max = 50) {
  const read = () => {
    try {
      const v = JSON.parse(storage?.getItem(EDITING_KEY) || "[]");
      return Array.isArray(v) ? v.filter((k) => typeof k === "string") : [];
    } catch (_) {
      return [];
    }
  };
  const write = (list) => {
    try { storage?.setItem(EDITING_KEY, JSON.stringify(list.slice(-max))); } catch (_) { /* this page only */ }
  };
  return {
    has: (key) => !!key && read().includes(key),
    set(key, on) {
      if (!key) return;
      const list = read().filter((k) => k !== key);
      if (on) list.push(key);
      write(list);
    },
  };
}
