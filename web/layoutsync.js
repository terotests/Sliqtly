// SPDX-License-Identifier: AGPL-3.0-or-later

// The rooms (topics), folders and stories of this browser (web/rooms.js),
// kept on sliqtly.com for the signed-in account too (POST
// /editor/api/layout, mcp-go/layout.go): an assistant connected to Sliqtly
// sees and changes them there, and the account's other browsers get them.
//
// The facts are web/rooms.js's ("room/<id>", "deck/<deck>", …). A change
// made here is written to localStorage as before and noted as pending
// (kept, so a reload or a lost connection does not lose it); a sync sends
// the pending ones and gets every fact changed since the number it last
// had. The first sync of an account in this browser sends what was kept
// before as "fill": it goes only where the server has nothing, so the
// first browser's rooms become the account's and another browser's join
// them without overwriting. No DOM: main.js calls sync and repaints.
//
//   const ls = layoutSync({ store, key: "sliqtly.rooms", call, onChange })
//   changeKept(store, key, fn, ls.mark); ls.soon()

import { factsOf, readKept } from "./rooms.js";

export const LAYOUT_SEQ = "sliqtly.layout.seq";
export const LAYOUT_PENDING = "sliqtly.layout.pending";
// a sync after a change waits this long for more changes (ms)
export const LAYOUT_SOON = 800;
// and is made at least this often while the page is open (ms)
export const LAYOUT_EVERY = 20000;

function readJSON(store, k, fallback) {
  try {
    const v = JSON.parse(store.getItem(k) || "null");
    return v ?? fallback;
  } catch (_) {
    return fallback;
  }
}

// The facts the server sent written into `store` under `key` + "/": the
// ones pending here are left as they are (this browser's are newer; the
// next sync sends them). full: the server's are all of them, so a fact
// here it does not have goes. → whether anything changed
export function applyFacts(store, key, facts, { full = false, pending = {} } = {}) {
  const pre = key + "/";
  let changed = false;
  const seen = new Set();
  for (const f of facts || []) {
    if (!f || typeof f.k !== "string") continue;
    seen.add(f.k);
    if (Object.hasOwn(pending, f.k)) continue;
    const had = store.getItem(pre + f.k);
    if (typeof f.v === "string") {
      if (had !== f.v) {
        store.setItem(pre + f.k, f.v);
        changed = true;
      }
    } else if (had !== null) {
      store.removeItem(pre + f.k);
      changed = true;
    }
  }
  if (full) {
    const gone = [];
    for (let i = 0; i < store.length; i++) {
      const k = store.key(i);
      if (k && k.startsWith(pre) && !seen.has(k.slice(pre.length)) && !Object.hasOwn(pending, k.slice(pre.length))) gone.push(k);
    }
    for (const k of gone) store.removeItem(k);
    if (gone.length) changed = true;
  }
  return changed;
}

// The sync of one account's layout. store: that account's localStorage
// (web/account.js); call(args) → the server's answer; onChange() after the
// server's facts changed what is kept here.
export function layoutSync({ store, key, call, onChange = () => {}, setTimer = setTimeout, clearTimer = clearTimeout }) {
  let running = null;
  let again = false;
  let timer = null;
  const pending = () => readJSON(store, LAYOUT_PENDING, {});

  async function once() {
    const sent = pending();
    const seq = Number(store.getItem(LAYOUT_SEQ));
    const first = !Number.isFinite(seq) || seq <= 0 || store.getItem(LAYOUT_SEQ) === null;
    const put = Object.entries(sent).map(([k, v]) => ({ k, v }));
    if (first) {
      // what this browser kept before it synced: only where the server has nothing
      for (const [k, v] of factsOf(readKept(store, key))) if (!Object.hasOwn(sent, k)) put.push({ k, v, fill: true });
    }
    const out = await call({ since: first ? 0 : seq, put });
    // the ones sent are no longer pending, unless changed again meanwhile
    const now = pending();
    for (const [k, v] of Object.entries(sent)) if (Object.hasOwn(now, k) && now[k] === v) delete now[k];
    store.setItem(LAYOUT_PENDING, JSON.stringify(now));
    if (Number.isFinite(Number(out?.seq))) store.setItem(LAYOUT_SEQ, String(out.seq));
    // the one value of before (web/rooms.js changeKept) went as facts
    if (first && store.getItem(key) !== null) store.removeItem(key);
    if (applyFacts(store, key, out?.facts, { full: !!out?.full, pending: now })) onChange();
  }

  // one sync at a time; asked again meanwhile, one more after it
  function sync() {
    if (running) {
      again = true;
      return running;
    }
    running = once().finally(() => {
      running = null;
      if (again) {
        again = false;
        sync().catch(() => {});
      }
    });
    return running;
  }

  return {
    // a fact written here (v null: removed), to send
    mark(k, v) {
      const p = pending();
      p[k] = v;
      try { store.setItem(LAYOUT_PENDING, JSON.stringify(p)); } catch (_) { /* sent with the next full fill */ }
    },
    // a sync shortly, after the changes made together
    soon() {
      if (timer) clearTimer(timer);
      timer = setTimer(() => {
        timer = null;
        sync().catch((e) => console.warn("topics not synced", e));
      }, LAYOUT_SOON);
    },
    sync,
  };
}
