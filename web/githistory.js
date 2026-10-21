// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A deck's versions in the Git repository of a server of one's own
// (mcp-go/githistory.go, /api/history/…): listed in Version history beside
// the browser's own, looked at, restored, and opened by a link that names
// one (/s/{id}@{version}). Kept apart from main.js so it can be tested under
// Node (web/test/githistory.test.mjs); `get` is fetch.

// A version's id in the history panel: "git:" and the commit.
export const GIT_PREFIX = "git:";

export function isGitVersion(id) {
  return typeof id === "string" && id.startsWith(GIT_PREFIX);
}

const REV = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

/**
 * /s/{id}@{version}: the deck and the version, or null. The version is a
 * commit (7–40 hex) or a branch. The editor's own addresses (/editor/s/…)
 * take one too.
 */
export function versionAddress(pathname) {
  const m = /^((?:\/editor\/[sd]|\/s)\/([A-Za-z0-9]{6,32}))@([^/]+)\/?$/.exec(String(pathname || ""));
  if (!m) return null;
  let rev;
  try {
    rev = decodeURIComponent(m[3]);
  } catch (_) {
    return null;
  }
  if (!REV.test(rev) || rev.includes("..")) return null;
  return { path: m[1], deck: m[2], rev };
}

async function json(get, url) {
  const res = await get(url, { cache: "no-store" });
  const out = await res.json().catch(() => ({}));
  if (!res.ok) {
    const e = new Error(out.error || res.statusText || String(res.status));
    e.status = res.status;
    throw e;
  }
  return out;
}

const base = (deck) => "/api/history/" + encodeURIComponent(deck);

/**
 * The deck's commits as the history panel lists its versions, newest first;
 * [] when the server keeps no Git (or this is no such server).
 */
export async function gitEntries(get, deck) {
  let out;
  try {
    out = await json(get, base(deck) + "?n=100");
  } catch (e) {
    if (e.status === 404) return [];
    throw e;
  }
  return (out.commits || []).map((c) => ({
    id: GIT_PREFIX + c.id,
    commit: c.id,
    parent: c.parent || "",
    time: new Date(c.when).toISOString(),
    device: "Git " + c.id.slice(0, 7) + (c.author ? " · " + c.author : ""),
    message: c.subject || "",
    git: true,
  }));
}

const TEXT = /\.(md|markdown|txt|css|json|csv|tsv|svg|html|xml|ya?ml)$/i;

async function fileText(get, deck, rev, path) {
  const res = await get(base(deck) + "/" + rev + "/file/" + path.split("/").map(encodeURIComponent).join("/"));
  if (res.status === 404) return "";
  if (!res.ok) throw new Error(res.statusText || String(res.status));
  return res.text();
}

/**
 * The deck as version `rev` has it, as Version history's checkout gives a
 * version: { name, md, css, theme, files: [{ path, type, data }] }, pictures
 * as Blobs and data files as text.
 */
export async function gitCheckout(get, deck, rev) {
  const v = await json(get, base(deck) + "/" + encodeURIComponent(rev));
  const files = [];
  for (const f of v.files || []) {
    const url = base(deck) + "/" + v.id + "/file/" + f.path.split("/").map(encodeURIComponent).join("/");
    const res = await get(url);
    if (!res.ok) continue;
    const data = TEXT.test(f.path) ? await res.text() : await res.blob();
    files.push({ path: f.path, type: f.type || "", data });
  }
  return { id: v.id, time: new Date(v.when).toISOString(), message: v.subject || "", name: v.name || "", md: v.md || "", css: v.css ?? null, theme: v.theme || "", files };
}

/**
 * What version `commit` changed, as the history panel lists a version's
 * files: [{ path, kind, added, removed, text }]. stats(a, b) counts the
 * lines (versions.js lineStats).
 */
export async function gitChanges(get, deck, commit, parent, stats) {
  const out = await json(get, base(deck) + "/" + commit + "/changes");
  const rows = [];
  for (const c of out.changes || []) {
    const row = { path: c.path, kind: c.kind, added: 0, removed: 0, text: !!c.text };
    if (c.text) {
      const now = c.kind === "removed" ? "" : await fileText(get, deck, commit, c.path);
      const was = c.kind === "added" || !parent ? "" : await fileText(get, deck, parent, c.path);
      Object.assign(row, stats(was, now));
    }
    rows.push(row);
  }
  return rows;
}

/** A text file of version `commit` and of `parent` (the one before; "" none). */
export async function gitTexts(get, deck, commit, parent, path) {
  return {
    was: parent ? await fileText(get, deck, parent, path) : "",
    now: await fileText(get, deck, commit, path),
  };
}

/** The browser's versions and the server's, newest first. */
export function mergeEntries(local, git) {
  return [...local, ...git].sort((a, b) => String(b.time).localeCompare(String(a.time)));
}
