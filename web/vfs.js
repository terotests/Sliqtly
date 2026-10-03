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

function idbStore(db) {
  const run = (store, mode, fn) => {
    const tx = db.transaction(store, mode);
    const result = fn(tx.objectStore(store));
    return new Promise((resolve, reject) => {
      tx.oncomplete = async () => resolve(await result);
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error);
    });
  };
  return {
    persistent: true,
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

// events: { waiting, closed } (openDb)
export async function openVfs(events) {
  try {
    if (typeof indexedDB === "undefined") throw new Error("no IndexedDB");
    return idbStore(await openDb(events));
  } catch (e) {
    console.warn("files kept in memory only:", e);
    return memoryStore();
  }
}

// What a file is, from its path and type: how the files tab shows it and
// what it can be used for.
export function kindOf(path, type) {
  const p = path.toLowerCase();
  if (/^image\//.test(type || "") || /\.(png|jpe?g|gif|webp|svg)$/.test(p)) return "image";
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
