// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The documents' own files, kept in this browser (IndexedDB).
//
//   docs   one record per presentation: its name, its markdown, the theme it
//          was set in and that theme as edited in the CSS tab
//   files  the presentation's other files by path: media/… (pictures pasted
//          or added), data/… (what its charts read), charts/… (Vega-Lite
//          specs kept as files) — text as a string, anything else as a Blob
//   objects  the presentation's version history (versions.js): RangerDiff
//          objects by id, each a Uint8Array
//
// Nothing here leaves the browser: a share link still carries only the text.
// Where IndexedDB is not to be had (a private window that refuses it) the
// same calls work on a store in memory that lasts as long as the page.

const DB_NAME = "evg-presentation";
const DB_VERSION = 2;

function promised(req) {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

// Another tab may hold the database at an older version (a page loaded
// before an update): opening at this version waits until that tab lets go.
// Meanwhile `waiting` is told, once: when the open is blocked, or when it has
// not finished in a moment (an open queued behind another tab's blocked one
// gets no event of its own). The open is never given up for a store in
// memory: what this browser keeps would then be missing from this page.
// Every tab lets go of its own copy when a newer page asks (`closed`), so a
// tab of this version never holds the next update up.
function openDb({ waiting, closed } = {}) {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    let told = false;
    const wait = () => {
      if (told) return;
      told = true;
      waiting?.();
    };
    const slow = setTimeout(wait, 1500);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains("docs")) db.createObjectStore("docs", { keyPath: "id" });
      if (!db.objectStoreNames.contains("files")) {
        const files = db.createObjectStore("files", { keyPath: ["doc", "path"] });
        files.createIndex("doc", "doc", { unique: false });
      }
      if (!db.objectStoreNames.contains("objects")) {
        const objects = db.createObjectStore("objects", { keyPath: ["doc", "id"] });
        objects.createIndex("doc", "doc", { unique: false });
      }
    };
    req.onsuccess = () => {
      clearTimeout(slow);
      const db = req.result;
      db.onversionchange = () => {
        db.close();
        closed?.();
      };
      resolve(db);
    };
    req.onerror = () => {
      clearTimeout(slow);
      reject(req.error);
    };
    req.onblocked = wait;
  });
}

// How long a transaction may go without finishing before it is taken as
// stuck: it waits for a lock another tab of this browser holds (a tab frozen
// in the background, or one stuck itself), or the browser's store does not
// answer. Every call here is a few keys or one deck's files; none takes this
// long when the store answers.
export const STALL_MS = 5000;

export class StallError extends Error {
  constructor(store, mode) {
    super("browser storage did not answer (" + store + ", " + mode + ")");
    this.name = "StallError";
  }
}

// The store over one connection at a time. `open()` gives a connection
// (openDb). A transaction that does not finish in `stallMs` is aborted,
// which lets go of what it holds; the connection is then closed and the
// call made once more over a new one (every call is a get, put or delete by
// key, so a second go does no harm). A connection the browser closed (an
// update in another tab, the database deleted) is replaced the same way.
// `events.stalled(store, mode)` hears of each stuck transaction, so the page
// can say so instead of waiting without a word.
export function idbStore(open, events = {}, { stallMs = STALL_MS } = {}) {
  let db = null;
  let opening = null;
  // the transactions not finished yet (letGo aborts them)
  const live = new Set();
  const leftGo = new WeakSet();
  const connect = () => {
    if (db) return Promise.resolve(db);
    if (!opening) {
      opening = open().then((d) => {
        opening = null;
        db = d;
        d.addEventListener?.("close", () => { if (db === d) db = null; });
        return d;
      }, (e) => {
        opening = null;
        throw e;
      });
    }
    return opening;
  };
  const drop = (d) => {
    if (!d || db !== d) return;
    db = null;
    try { d.close(); } catch (_) { /* closed already */ }
  };
  const once = (d, store, mode, fn) => new Promise((resolve, reject) => {
    let tx;
    try {
      tx = d.transaction(store, mode);
    } catch (e) {
      reject(e); // "the connection is closing": a new one is opened
      return;
    }
    let result;
    try {
      result = fn(tx.objectStore(store));
    } catch (e) {
      try { tx.abort(); } catch (_) { /* finished */ }
      reject(e);
      return;
    }
    Promise.resolve(result).catch(() => {}); // the transaction's own error says it
    live.add(tx);
    const timer = setTimeout(() => {
      try { tx.abort(); } catch (_) { /* finished meanwhile */ }
      reject(new StallError(store, mode));
    }, stallMs);
    const done = () => { clearTimeout(timer); live.delete(tx); };
    tx.oncomplete = async () => {
      done();
      try { resolve(await result); } catch (e) { reject(e); }
    };
    tx.onerror = () => { done(); reject(tx.error); };
    tx.onabort = () => {
      done();
      if (leftGo.has(tx)) {
        const e = new Error("let go while frozen");
        e.name = "InvalidStateError"; // made again over a new connection, without a word
        reject(e);
      } else reject(tx.error || new StallError(store, mode));
    };
  });
  const again = (e) => e instanceof StallError || e?.name === "InvalidStateError";
  const run = async (store, mode, fn) => {
    const d = await connect();
    try {
      return await once(d, store, mode, fn);
    } catch (e) {
      if (!again(e)) throw e;
      if (e instanceof StallError) {
        console.warn("IndexedDB:", e.message, "— trying again over a new connection");
        events.stalled?.(store, mode);
      }
      drop(d);
      return once(await connect(), store, mode, fn);
    }
  };
  // A page frozen in the background (Chrome freezes a busy tab there), or
  // kept in the back/forward cache, runs no code: a transaction of its left
  // open would hold the store up for every other tab until it is woken. It
  // aborts what it has open (the call fails, and a save is made again when
  // the page is back) and lets go of its connection; the next call opens a
  // new one.
  const letGo = () => {
    for (const tx of [...live]) {
      leftGo.add(tx);
      try { tx.abort(); } catch (_) { /* finished */ }
    }
    live.clear();
    drop(db);
  };
  if (typeof document !== "undefined") document.addEventListener("freeze", letGo);
  if (typeof window !== "undefined") window.addEventListener("pagehide", (e) => { if (e.persisted) letGo(); });
  return {
    persistent: true,
    // ready: the first connection made (openVfs waits for it)
    ready: () => connect(),
    letGo,
    listDocs: () => run("docs", "readonly", (s) => promised(s.getAll())),
    getDoc: (id) => run("docs", "readonly", (s) => promised(s.get(id))),
    putDoc: (doc) => run("docs", "readwrite", (s) => promised(s.put(doc))),
    // keepObjects: the deck's version history stays (its files are replaced)
    deleteDoc: async (id, keepObjects = false) => {
      const files = await run("files", "readonly", (s) => promised(s.index("doc").getAllKeys(id)));
      await run("files", "readwrite", (s) => Promise.all(files.map((k) => promised(s.delete(k)))));
      const objects = keepObjects ? [] : await run("objects", "readonly", (s) => promised(s.index("doc").getAllKeys(id)));
      await run("objects", "readwrite", (s) => Promise.all(objects.map((k) => promised(s.delete(k)))));
      await run("docs", "readwrite", (s) => promised(s.delete(id)));
    },
    listFiles: (doc) => run("files", "readonly", (s) => promised(s.index("doc").getAll(doc))),
    getFile: (doc, path) => run("files", "readonly", (s) => promised(s.get([doc, path]))),
    putFile: (file) => run("files", "readwrite", (s) => promised(s.put(file))),
    deleteFile: (doc, path) => run("files", "readwrite", (s) => promised(s.delete([doc, path]))),
    listObjects: (doc) => run("objects", "readonly", (s) => promised(s.index("doc").getAll(doc))),
    getObject: (doc, id) => run("objects", "readonly", (s) => promised(s.get([doc, id]))),
    putObject: (doc, id, data) => run("objects", "readwrite", (s) => promised(s.put({ doc, id, data }))),
  };
}

function memoryStore() {
  const docs = new Map();
  const files = new Map();
  const objects = new Map();
  const key = (doc, path) => doc + "\u0000" + path;
  return {
    persistent: false,
    listDocs: async () => [...docs.values()],
    getDoc: async (id) => docs.get(id),
    putDoc: async (doc) => { docs.set(doc.id, doc); },
    deleteDoc: async (id, keepObjects = false) => {
      docs.delete(id);
      for (const k of [...files.keys()]) if (k.startsWith(id + "\u0000")) files.delete(k);
      if (!keepObjects) for (const k of [...objects.keys()]) if (k.startsWith(id + "\u0000")) objects.delete(k);
    },
    listFiles: async (doc) => [...files.values()].filter((f) => f.doc === doc),
    getFile: async (doc, path) => files.get(key(doc, path)),
    putFile: async (file) => { files.set(key(file.doc, file.path), file); },
    deleteFile: async (doc, path) => { files.delete(key(doc, path)); },
    listObjects: async (doc) => [...objects.values()].filter((o) => o.doc === doc),
    getObject: async (doc, id) => objects.get(key(doc, id)),
    putObject: async (doc, id, data) => { objects.set(key(doc, id), { doc, id, data }); },
  };
}

// events: { waiting, closed } (openDb), { stalled } (idbStore)
export async function openVfs(events) {
  try {
    if (typeof indexedDB === "undefined") throw new Error("no IndexedDB");
    const store = idbStore(() => openDb(events), events);
    await store.ready();
    return store;
  } catch (e) {
    console.warn("files kept in memory only:", e);
    return memoryStore();
  }
}

// What a file is, from its path and type: how the files tab shows it and
// what it can be used for.
export function kindOf(path, type) {
  const p = path.toLowerCase();
  // a SmartArt diagram (media/steps.xml) is kept and referenced like a picture
  if (/^image\//.test(type || "") || /\.(png|jpe?g|gif|webp|svg|xml)$/.test(p) || /drawingml\.diagramData/.test(type || "")) return "image";
  if (/\.vl\.json$|\.vg\.json$/.test(p) || p.startsWith("charts/")) return "chart";
  if (/\.(csv|tsv|json|topojson|geojson|txt|xlsx)$/.test(p)) return "data";
  if (/\.css$/.test(p)) return "css";
  if (/\.(md|markdown)$/.test(p)) return "md";
  return "text";
}

export function isText(path, type) {
  if (/^text\//.test(type || "") || /json/.test(type || "")) return true;
  return /\.(csv|tsv|json|topojson|geojson|txt|css|md|markdown)$/i.test(path);
}

// Where a file added from the computer goes: pictures under media/, a chart
// spec under charts/, other data under data/.
export function placeFor(name, type) {
  const clean = name.replace(/[\\/:*?"<>|]+/g, "-");
  const k = kindOf(clean, type);
  if (k === "image") return "media/" + clean;
  if (k === "chart") return "charts/" + clean;
  if (/\.json$/i.test(clean)) {
    return "data/" + clean;
  }
  return "data/" + clean;
}

export function newId() {
  return Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 8);
}
