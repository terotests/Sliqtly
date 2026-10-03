// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Versions of a presentation, and edits made in two places at once.
//
// History. A deck's versions are commits of RangerDiff (terotests/RangerDiff,
// web/dist/rangerdiff.mjs): a tree of its files (deck.md, the theme's edited
// CSS, pictures, data) with parents, a time, the device and a message. The
// newest version is kept whole and older ones as reverse deltas, so history
// costs about what was changed. A picture edited in the image editor is
// kept as its original and the edits ("recipe"); the edited picture is made
// again from them when a version is restored. Objects live in this browser
// (vfs objects); a PRO deck's go to Storage beside its share
// (shares/{id}/.versions/…, as forward deltas: an uploaded object never
// changes), and the share keeps the head and a short log.
//
// Two places. The deck in the editor is a working copy over the version it
// was loaded or last saved as (its base). The other place's copy is read on
// an interval, when the window gets the focus and, for tabs of this browser,
// at once when one of them saves. Changed there only: this one is updated.
// Changed on both sides: the two are merged against the base, line by line
// for the Markdown and the CSS; what both changed differently is shown for
// the user to choose. Nothing is written over unseen.

import { RdRepo, RdTree, RdEntry, RdText, RdStored, RdSmart, RdDelta, RdPack } from "./rangerdiff.mjs";

// --- bytes ---------------------------------------------------------------------------
// A RangerDiff buffer is an ArrayBuffer with a DataView in `_view`.
export function rb(bytes) {
  const u8 = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  const ab = u8.byteOffset === 0 && u8.byteLength === u8.buffer.byteLength ? u8.buffer : u8.slice().buffer;
  if (!ab._view) ab._view = new DataView(ab);
  return ab;
}
const enc = new TextEncoder();
const dec = new TextDecoder();
const textBuf = (s) => rb(enc.encode(s));
const bufText = (ab) => dec.decode(new Uint8Array(ab));

async function bytesOf(data) {
  if (data == null) return new Uint8Array(0);
  if (typeof data === "string") return enc.encode(data);
  if (data instanceof Blob) return new Uint8Array(await data.arrayBuffer());
  return new Uint8Array(data);
}

export async function sha256(u8) {
  const h = new Uint8Array(await crypto.subtle.digest("SHA-256", u8));
  return [...h].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// this device and this tab, for a commit's "device" line and the tabs' talk
export const TAB = Math.random().toString(36).slice(2, 10);
export function deviceName() {
  let id = "";
  try { id = localStorage.getItem("sliqtly.device") || ""; } catch (_) { /* this page only */ }
  if (!id) {
    const ua = navigator.userAgent || "";
    const os = /iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Mac/.test(ua) ? "Mac"
      : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
    id = (os ? os + " " : "") + Math.random().toString(36).slice(2, 6);
    try { localStorage.setItem("sliqtly.device", id); } catch (_) { /* this page only */ }
  }
  return id;
}

// --- snapshots -----------------------------------------------------------------------
// A deck as versions see it: { name, md, theme, css (null: the theme's own),
// files: [{ path, type, data (string | Blob), orig? }] }. `orig` is a picture
// edited in the image editor: { data: Blob of the original, type, plans: [] }.
const MD = "deck.md";
const META = ".sliqtly/deck.json";
const CSS = ".sliqtly/theme.css";
const STATE = "@state";

export function isTextPath(path) {
  return /\.(md|markdown|txt|css|json|csv|tsv|svg|html|xml|ya?ml)$/i.test(path);
}

// Lines added and removed between two texts (for the log).
export function lineStats(a, b) {
  if (a === b) return { added: 0, removed: 0 };
  const d = RdText.diff(a ?? "", b ?? "");
  return { added: d.added, removed: d.removed };
}

// --- three-way merge of two working copies -------------------------------------------
// base, mine, theirs: { md, css, theme, name }. files: [{ path, base, mine,
// theirs }] where each side is null (no such file) or { sig, rec }: the same
// sig is the same content. → { snap: { md, css, theme, name }, files:
// [{ path, rec|null }], conflicts: [...], auto: n }. A conflict is
// { kind: "text", field: "md"|"css", merge: RdMerge } or
// { kind: "file", path, mine, theirs }.
export function mergeCopies(base, mine, theirs, files = []) {
  const out = { snap: {}, files: [], conflicts: [], auto: 0 };
  const pick = (b, m, t) => (m === b ? t : t === b ? m : m === t ? m : undefined);
  for (const field of ["md", "css"]) {
    const b = base[field] ?? "";
    const m = mine[field] ?? "";
    const th = theirs[field] ?? "";
    const simple = pick(b, m, th);
    if (simple !== undefined) {
      out.snap[field] = field === "css" && simple === "" && mine.css == null && theirs.css == null ? null : simple;
      if (m !== b && th !== b) out.auto += 1;
      else if (th !== b) out.auto += 1;
      continue;
    }
    const merge = RdText.merge3(b, m, th);
    if (merge.clean()) {
      out.snap[field] = merge.text("markers");
      out.auto += merge.fromMine + merge.fromTheirs;
    } else {
      out.snap[field] = null;
      out.conflicts.push({ kind: "text", field, merge });
      out.auto += merge.fromMine + merge.fromTheirs;
    }
  }
  for (const field of ["theme", "name"]) {
    const v = pick(base[field] ?? "", mine[field] ?? "", theirs[field] ?? "");
    // both renamed / both changed the theme: this window's choice stays
    out.snap[field] = v === undefined ? mine[field] : v;
  }
  for (const f of files) {
    const sig = (x) => (x ? x.sig : null);
    const b = sig(f.base);
    const m = sig(f.mine);
    const th = sig(f.theirs);
    if (m === th || th === b) out.files.push({ path: f.path, rec: f.mine ? f.mine.rec : null });
    else if (m === b) {
      out.files.push({ path: f.path, rec: f.theirs ? f.theirs.rec : null });
      out.auto += 1;
    } else out.conflicts.push({ kind: "file", path: f.path, mine: f.mine, theirs: f.theirs });
  }
  return out;
}

// The merge with the user's picks: per text conflict an array of picks per
// region ("mine" | "theirs" | "both"), per file conflict "mine" | "theirs".
export function resolveMerge(result, picks) {
  const snap = { ...result.snap };
  const files = [...result.files];
  result.conflicts.forEach((c, i) => {
    const p = picks[i];
    if (c.kind === "text") {
      const regions = Array.isArray(p) ? p : new Array(c.merge.conflicts).fill(p || "mine");
      snap[c.field] = c.merge.resolve(regions);
    } else {
      const side = p === "theirs" ? c.theirs : c.mine;
      files.push({ path: c.path, rec: side ? side.rec : null });
    }
  });
  return { snap, files };
}

// --- a deck's history ----------------------------------------------------------------
export class DeckHistory {
  // store: vfs (objects of this deck kept by docId); cloud: () => share id or
  // null; pro: () => window.sliqtly (signed in) or null
  constructor({ vfs, docId, cloud, pro, author }) {
    this.vfs = vfs;
    this.docId = docId;
    this.cloud = cloud || (() => null);
    this.pro = pro || (() => null);
    this.author = author || (() => "");
    this.repo = new RdRepo();
    this.head = "";
    this.cloudHead = null;
    this.cloudObjs = {};
    this.ids = new Map(); // path + stamp → blob id, so an unchanged file is not hashed again
    this.ready = this.load();
  }

  // The objects of this deck, and "@state": { head, cloudHead, cloudObjs }
  // (cloudObjs: id → delta depth of the objects the cloud has).
  async load() {
    if (!this.vfs?.listObjects) return;
    for (const o of await this.vfs.listObjects(this.docId)) {
      if (o.id === STATE) this.useState(o.data);
      else this.repo.load(rb(o.data));
    }
    this.repo.takeDirty();
  }

  useState(st) {
    this.head = st?.head || "";
    this.cloudHead = st?.cloudHead ?? null;
    this.cloudObjs = st?.cloudObjs || {};
  }

  async persist() {
    const ids = this.repo.takeDirty();
    for (const id of ids) await this.vfs.putObject(this.docId, id, new Uint8Array(this.repo.stored(id)));
    await this.vfs.putObject(this.docId, STATE, { head: this.head, cloudHead: this.cloudHead, cloudObjs: this.cloudObjs });
  }

  // Another tab of this browser may have made versions since: their objects
  // are read in, and its head becomes this one's when it comes after it. →
  // the stored head when it is on another line than this one (a parent for
  // the next version, which has its content: the tabs save through one
  // record), else "".
  async refresh() {
    await this.ready;
    if (!this.vfs?.getObject) return "";
    const st = (await this.vfs.getObject(this.docId, STATE))?.data;
    const other = st?.head || "";
    if (!other || other === this.head) return "";
    if (!this.repo.has(other)) {
      for (const o of await this.vfs.listObjects(this.docId)) if (o.id !== STATE && !this.repo.has(o.id)) this.repo.load(rb(o.data));
      this.repo.takeDirty();
    }
    for (const [id, depth] of Object.entries(st.cloudObjs || {})) if (this.cloudObjs[id] == null) this.cloudObjs[id] = depth;
    if (!this.repo.has(other)) return "";
    if (!this.head || this.repo.isAncestor(this.head, other)) {
      this.head = other;
      if (st.cloudHead && (!this.cloudHead || this.repo.has(st.cloudHead))) this.cloudHead = st.cloudHead;
      return "";
    }
    return this.repo.isAncestor(other, this.head) ? "" : other;
  }

  // a tree entry for a file of the snapshot
  async entry(path, data, stamp, recipe = "") {
    const key = path + "\u0000" + (stamp ?? "") + "\u0000" + recipe;
    let known = stamp != null ? this.ids.get(key) : null;
    const e = new RdEntry();
    e.path = path;
    e.recipe = recipe;
    if (known && this.repo.has(known.id)) {
      e.blob = known.id;
      e.size = known.size;
      return e;
    }
    const u8 = await bytesOf(data);
    e.blob = this.repo.putBlobId(await sha256(u8), rb(u8));
    e.size = u8.byteLength;
    if (stamp != null) this.ids.set(key, { id: e.blob, size: e.size });
    return e;
  }

  async treeOf(snap) {
    const t = new RdTree();
    const types = {};
    t.entries.push(await this.entry(MD, snap.md ?? ""));
    if (snap.css != null) t.entries.push(await this.entry(CSS, snap.css));
    for (const f of snap.files || []) {
      types[f.path] = f.type || "";
      if (f.orig && f.orig.plans?.length) {
        const recipe = JSON.stringify({ type: f.orig.type || "", plans: f.orig.plans });
        t.entries.push(await this.entry(f.path, f.orig.data, f.orig.stamp ?? null, recipe));
      } else {
        t.entries.push(await this.entry(f.path, f.data, f.stamp ?? null));
      }
    }
    t.entries.push(await this.entry(META, JSON.stringify({ name: snap.name || "", theme: snap.theme || "", types })));
    return t;
  }

  // a version of `snap`; null when it is the same as the head's, unless
  // `force` (a version saved by hand). Another tab's newer head is taken
  // first (refresh); `alone`: not as a parent.
  async commit(snap, message, extraParents = [], alone = false, force = false) {
    await this.ready;
    const other = await this.refresh();
    if (other && !alone) extraParents = extraParents.concat([other]);
    const tree = await this.treeOf(snap);
    const parents = (this.head ? [this.head] : []).concat(extraParents.filter((p) => p && p !== this.head));
    if (this.head && parents.length === 1 && !force) {
      const sorted = [...tree.entries].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
      const same = new RdTree();
      for (const e of sorted) same.entries.push(e);
      const headTree = this.repo.commit(this.head)?.tree;
      if (headTree && this.repo.readText(headTree) === same.text()) return null;
    }
    const id = this.repo.commitTree(tree, parents, this.author(), deviceName(), new Date().toISOString(), message || "");
    this.head = id;
    await this.persist();
    return id;
  }

  // the versions, newest first: { id, time, device, author, message, parents }
  // from this browser's and, for a PRO deck, the share's log
  async log(remoteLog = []) {
    await this.ready;
    const out = new Map();
    for (const e of remoteLog || []) out.set(e.id, { ...e, remote: !this.repo.has(e.id) });
    if (this.head) {
      let n = 0;
      for (const c of this.repo.log(this.head, 300)) {
        const e = n++ < 100 ? this.logEntry(c) : { id: c.id, time: c.time, device: c.device, author: c.author, message: c.message, parents: [...c.parents] };
        out.set(c.id, { ...out.get(c.id), ...e, remote: false });
      }
    }
    return [...out.values()].sort((a, b) => (a.time < b.time ? 1 : a.time > b.time ? -1 : 0));
  }

  // --- objects from the cloud, as they are needed
  async ensure(id, depth = 0) {
    if (!id || this.repo.has(id) || depth > 64) return;
    const p = this.pro();
    const share = this.cloud();
    if (!p || !share) return;
    const bytes = await p.getObject(share, id);
    if (!bytes) return;
    this.repo.load(rb(bytes));
    await this.vfs.putObject(this.docId, id, new Uint8Array(bytes));
    const s = RdStored.decode(rb(bytes));
    if (s) {
      this.cloudObjs[id] = s.depth;
      if (s.kind === 1) await this.ensure(s.base, depth + 1);
    }
  }

  // the commits from `id` back to ones this browser has (not their files)
  async ensureLine(id, limit = 200) {
    await this.ready;
    const todo = [id];
    for (let n = 0; todo.length && n < limit; n++) {
      const c0 = todo.pop();
      if (!c0 || this.repo.has(c0)) continue;
      await this.ensure(c0);
      const c = this.repo.commit(c0);
      if (c) todo.push(...c.parents);
    }
  }

  async ensureCommit(id) {
    await this.ready;
    await this.ensure(id);
    const c = this.repo.commit(id);
    if (!c) return null;
    await this.ensure(c.tree);
    for (const e of this.repo.tree(c.tree).entries) await this.ensure(e.blob);
    return c;
  }

  // what version `id` changed against its first parent: [{ path, kind,
  // added, removed, detail }]
  async changes(id) {
    const c = await this.ensureCommit(id);
    if (!c) return [];
    const parent = c.parents[0];
    if (parent) await this.ensureCommit(parent);
    const out = [];
    const before = parent ? this.repo.commit(parent)?.tree : null;
    const list = before ? this.repo.diffTrees(before, c.tree) : this.repo.diffTrees(this.emptyTree(), c.tree);
    for (const ch of list) {
      if (ch.path === META) continue;
      const row = { path: ch.path === MD ? MD : ch.path === CSS ? "theme.css" : ch.path, kind: ch.kind, added: ch.added, removed: ch.removed, detail: "", text: isTextPath(ch.path) };
      if (ch.kind === "changed" && /\.(xlsx|docx|pptx|ods|zip)$/i.test(ch.path)) {
        const info = RdPack.info(this.repo.read(ch.oldBlob), RdSmart.diff(this.repo.read(ch.oldBlob), this.repo.read(ch.newBlob)));
        if (info.ok) row.detail = info.changed().map((p) => p.name.replace(/^xl\//, "") + " (" + p.kindName() + ")").join(", ");
      }
      if (ch.kind === "recipe" || ch.newRecipe) row.detail = recipeText(ch.newRecipe);
      out.push(row);
    }
    return out;
  }

  emptyTree() {
    return this.repo.putText("");
  }

  // the unified diff of a text file in version `id` against its parent
  async diffText(id, path) {
    const c = await this.ensureCommit(id);
    if (!c) return "";
    const parent = c.parents[0];
    if (parent) await this.ensureCommit(parent);
    const real = path === "theme.css" ? CSS : path;
    const oldText = parent ? this.repo.fileText(parent, real) : "";
    return RdText.unified(RdText.diff(oldText, this.repo.fileText(id, real)), 2);
  }

  // version `id` as a snapshot; `render(origBlob, plans, type)` makes an
  // edited picture again from its original
  async checkout(id, render) {
    const c = await this.ensureCommit(id);
    if (!c) return null;
    const tree = this.repo.tree(c.tree);
    let meta = { name: "", theme: "", types: {} };
    try { meta = JSON.parse(bufText(this.repo.read(tree.find(META)?.blob || ""))); } catch (_) { /* old or none */ }
    const snap = { name: meta.name, theme: meta.theme, md: "", css: null, files: [] };
    for (const e of tree.entries) {
      const bytes = new Uint8Array(this.repo.read(e.blob));
      if (e.path === META) continue;
      if (e.path === MD) { snap.md = dec.decode(bytes); continue; }
      if (e.path === CSS) { snap.css = dec.decode(bytes); continue; }
      const type = meta.types?.[e.path] || "";
      if (e.recipe) {
        let r = null;
        try { r = JSON.parse(e.recipe); } catch (_) { r = null; }
        const orig = new Blob([bytes], { type: r?.type || type });
        const made = r && render ? await render(orig, r.plans || [], type).catch(() => null) : null;
        snap.files.push({ path: e.path, blob: e.blob, recipe: e.recipe, type: made?.type || type, data: made?.blob || orig, orig: { data: orig, type: r?.type || type, plans: r?.plans || [] } });
      } else {
        snap.files.push({ path: e.path, blob: e.blob, recipe: "", type, data: isTextPath(e.path) ? dec.decode(bytes) : new Blob([bytes], { type }) });
      }
    }
    return snap;
  }

  // --- the cloud copy of the history (PRO)
  // New commits up as immutable objects: trees and commits whole, a file
  // whole or as a forward delta against its version in the parent (already
  // up), then the share's head moved from what this page last saw. When
  // another device moved it meanwhile: with `snapForMerge` (→ the merged
  // working copy) a version with both heads as parents goes up instead;
  // without, false, and the editor merges first.
  async pushCloud(snapForMerge) {
    await this.ready;
    const p = this.pro();
    const share = this.cloud();
    if (!p || !share || !this.head || !p.pushHead) return false;
    if (this.cloudHead === this.head) return true;
    for (let round = 0; round < 3; round++) {
      const fresh = [];
      for (const c of this.repo.log(this.head, 300)) {
        if (this.cloudObjs[c.id] != null) break;
        fresh.push(c);
      }
      for (const c of fresh.reverse()) await this.upload(c);
      const entries = fresh.map((c) => this.logEntry(c));
      const r = await p.pushHead(share, this.cloudHead, this.head, entries);
      if (r.ok) {
        this.cloudHead = this.head;
        await this.persist();
        return true;
      }
      // moved on elsewhere: both heads become parents of one version
      const theirs = r.head;
      if (!theirs || (this.repo.has(theirs) && this.repo.isAncestor(theirs, this.head))) {
        this.cloudHead = theirs;
        continue;
      }
      if (!snapForMerge) return false;
      await this.ensureLine(theirs).catch(() => {});
      this.cloudHead = theirs;
      if (snapForMerge) await this.commit(await snapForMerge(), "Merged", [theirs]);
    }
    return false;
  }

  logEntry(c) {
    const e = { id: c.id, parents: [...c.parents], time: c.time, device: c.device, author: c.author, message: c.message };
    const parent = c.parents[0];
    const pc = parent && this.repo.has(parent) ? this.repo.commit(parent) : null;
    if (pc && this.repo.has(pc.tree) && this.repo.has(c.tree)) {
      try {
        const s = lineStats(this.repo.fileText(parent, MD), this.repo.fileText(c.id, MD));
        e.added = s.added;
        e.removed = s.removed;
        e.files = this.repo.diffCommits(parent, c.id).filter((x) => x.path !== META && x.path !== MD).length;
      } catch (_) { /* a file of it not here yet */ }
    }
    return e;
  }

  async upload(c) {
    const p = this.pro();
    const share = this.cloud();
    const send = async (stored) => {
      await p.putObject(share, stored.id, new Uint8Array(stored.encode()));
      this.cloudObjs[stored.id] = stored.depth;
    };
    const whole = (id) => {
      const s = new RdStored();
      s.id = id;
      s.data = this.repo.read(id);
      return s;
    };
    const tree = this.repo.tree(c.tree);
    const parentTree = c.parents[0] && this.repo.has(c.parents[0]) ? this.repo.tree(this.repo.commit(c.parents[0]).tree) : null;
    for (const e of tree.entries) {
      if (this.cloudObjs[e.blob] != null) continue;
      const prev = parentTree?.find(e.path);
      const depth = prev ? this.cloudObjs[prev.blob] : undefined;
      let stored = whole(e.blob);
      if (prev && depth != null && depth < 16 && this.repo.has(prev.blob)) {
        const from = this.repo.read(prev.blob);
        const to = stored.data;
        let d = RdSmart.diff(from, to);
        if (!RdSmart.exact(d)) d = RdDelta.diff(from, to);
        if (d.byteLength * 5 < to.byteLength * 4) {
          stored = new RdStored();
          stored.id = e.blob;
          stored.kind = 1;
          stored.base = prev.blob;
          stored.depth = depth + 1;
          stored.data = d;
        }
      }
      await send(stored);
    }
    if (this.cloudObjs[c.tree] == null) await send(whole(c.tree));
    await send(whole(c.id));
  }
}

export function recipeText(recipe) {
  if (!recipe) return "";
  try {
    const r = JSON.parse(recipe);
    const last = (r.plans || [])[r.plans.length - 1] || {};
    const parts = [];
    for (const k of ["bright", "contrast", "sat", "temp", "tint"]) if (last[k]) parts.push(k + " " + (last[k] > 0 ? "+" : "") + last[k]);
    if (last.crop && !last.whole) parts.push("crop");
    return (r.plans.length > 1 ? r.plans.length + " edits: " : "") + parts.join(", ");
  } catch (_) {
    return "";
  }
}
