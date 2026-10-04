// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Files copied from one presentation to paste into another (the Files tab's
// Copy and Paste), and the order the Files tab lists a deck's files in.
//
// The copies are kept in this browser's file store (vfs.js) under a document
// of their own, CLIP_DOC, which is never a presentation: what was copied
// stays there until the next Copy, whatever happens to the deck it came from,
// and every tab of this browser reads the same copies. A note in
// localStorage (CLIP_KEY: how many, from which deck) tells the other tabs
// that the copies changed (its `storage` event), so their Paste appears.

export const CLIP_DOC = "~clipboard";
export const CLIP_KEY = "sliqtly.fileClip";

const LIVE = "data/live/";

/** The folder of a path with its slash ("data/"), "" at the top. */
export function folderOf(path) {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i + 1);
}

/**
 * The Files tab's order: files at the top first, then by folder, then by
 * name, so each folder's files come together under one heading. (A sort by
 * the whole path put data/live/x between data/cars and data/movies, and the
 * data/ heading showed twice.)
 */
export function sortFiles(rows) {
  return [...rows].sort((a, b) => {
    const fa = folderOf(a.path);
    const fb = folderOf(b.path);
    if (fa !== fb) {
      if (!fa) return -1;
      if (!fb) return 1;
      return fa < fb ? -1 : 1;
    }
    return a.path.localeCompare(b.path);
  });
}

/**
 * Where a pasted file goes in a deck that already has the files `taken`
 * (a Set of paths): its own path when that is free, else the same name with
 * -2, -3… before the extension. A copy kept of a linked source
 * (data/live/<hash>.csv) is named after the source it stands for, so it
 * replaces the deck's copy of the same source instead.
 */
export function pasteTarget(path, taken) {
  if (!taken.has(path) || path.startsWith(LIVE)) return path;
  const dir = folderOf(path);
  const name = path.slice(dir.length);
  const dot = name.lastIndexOf(".");
  const stem = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : "";
  for (let n = 2; ; n += 1) {
    const p = dir + stem + "-" + n + ext;
    if (!taken.has(p)) return p;
  }
}

/**
 * The plan of a paste: for each copied path, where it goes. Two copies never
 * land on one path. `same`: the deck already has this very file (same path,
 * same contents), so it is left as it is.
 */
export async function pastePlan(files, existing) {
  const taken = new Set(existing.map((f) => f.path));
  const byPath = new Map(existing.map((f) => [f.path, f]));
  const out = [];
  for (const f of files) {
    // the deck has this file already: under its own name, or under a name
    // an earlier paste gave it (cars-2.json)
    let have = null;
    for (const p of [f.path, ...existing.map((x) => x.path).filter((p) => isRenamed(f.path, p))]) {
      const x = byPath.get(p);
      if (x && (await sameData(x.data, f.data))) { have = p; break; }
    }
    if (have) {
      out.push({ from: f.path, to: have, same: true });
      continue;
    }
    const to = pasteTarget(f.path, taken);
    taken.add(to);
    out.push({ from: f.path, to, same: false });
  }
  return out;
}

// `p` is `path` with a -N a paste gave it: data/cars-2.json for data/cars.json
function isRenamed(path, p) {
  const dir = folderOf(path);
  if (folderOf(p) !== dir) return false;
  const name = path.slice(dir.length);
  const dot = name.lastIndexOf(".");
  const stem = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : "";
  const rest = p.slice(dir.length);
  if (!rest.startsWith(stem + "-") || !rest.endsWith(ext)) return false;
  return /^\d+$/.test(rest.slice(stem.length + 1, rest.length - ext.length));
}

async function sameData(a, b) {
  if (typeof a === "string" || typeof b === "string") return a === b;
  if (!a || !b || a.size !== b.size) return false;
  // two Blobs of one size (a picture pasted again): their bytes decide
  const [x, y] = await Promise.all([a.arrayBuffer(), b.arrayBuffer()]);
  const u = new Uint8Array(x);
  const v = new Uint8Array(y);
  for (let i = 0; i < u.length; i++) if (u[i] !== v[i]) return false;
  return true;
}

/** What the clipboard note says: { count, from, at } or null. */
export function readClipNote(storage) {
  try {
    const n = JSON.parse(storage?.getItem(CLIP_KEY) || "null");
    return n && n.count > 0 ? { count: n.count | 0, from: String(n.from || ""), at: n.at || 0 } : null;
  } catch (_) {
    return null;
  }
}

/**
 * The clipboard over a file store. `storage`: localStorage, or null where
 * the browser refuses it (the copies then last as long as this page).
 *   copy(files, from)  the files as they are (records of vfs.listFiles)
 *   files()            the copies, to paste
 *   note()             { count, from } or null
 */
export function fileClipboard(vfs, storage) {
  let mine = null;
  return {
    async copy(files, from) {
      for (const old of await vfs.listFiles(CLIP_DOC)) await vfs.deleteFile(CLIP_DOC, old.path);
      for (const f of files) {
        const { doc: _d, updated: _u, ...rest } = f;
        await vfs.putFile({ ...rest, doc: CLIP_DOC, updated: Date.now() });
      }
      mine = { count: files.length, from: String(from || ""), at: Date.now() };
      try { storage?.setItem(CLIP_KEY, JSON.stringify(mine)); } catch (_) { /* this page only */ }
      return mine;
    },
    files: () => vfs.listFiles(CLIP_DOC),
    note() {
      return readClipNote(storage) || mine;
    },
  };
}
