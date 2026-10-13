// SPDX-License-Identifier: AGPL-3.0-or-later

// The shared files of a server of one's own (mcp-go/sharedfiles.go): design
// files everyone on the server sees, kept apart from any room or deck. No
// page in it: main.js hands it `fetch` and draws what it returns.
//
// A Figma file (or a PowerPoint deck) put here is indexed once on the
// server; a screen (a slide), or a part of one, is then a picture the
// server draws from that index:
//
//   /files/shared/<file>/png/<layer>.png        (layer: an id or a screen's name)
//   /files/shared/<file>/index.json             (pages, screens, parts, words)
//   /files/shared/<file>/source                 (the file itself)
//
// On sliqtly.com/editor each account has its own files, under
// /editor/api/shared (mcp-go/editorshared.go): a file goes to Storage first
// (the page's `upload`), then the server moves it in and reads it.

const roots = { api: "/api/shared", files: "/files/shared" };

// sliqtly.com/editor: every call under /editor/api/shared
export function useEditorRoots() {
  roots.api = "/editor/api/shared";
  roots.files = "/editor/api/shared";
}

// The address of layer `node` of shared file `file` drawn as a PNG, at most
// `max` pixels on its long side. A layer inside an instance is "12:34#1:2;3:4",
// a name may hold anything: the whole layer is one escaped path segment.
// On sliqtly.com `deck` (its cloud id) draws it from the deck owner's files.
export function sharedPngUrl(file, node, max = 2048, deck = "") {
  const q = new URLSearchParams();
  if (max) q.set("max", String(max));
  if (deck) q.set("deck", deck);
  const qs = q.toString();
  return `${roots.files}/${encodeURIComponent(file)}/png/${encodeURIComponent(node)}.png${qs ? "?" + qs : ""}`;
}

// the file itself, as it is now (a deck an assistant changed in place)
export function sharedSourceUrl(file) {
  return `${roots.files}/${encodeURIComponent(file)}/source`;
}

export function sharedIndexUrl(file) {
  return `${roots.files}/${encodeURIComponent(file)}/index.json`;
}

async function answer(res) {
  const text = await res.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch { /* a plain message */ }
  if (!res.ok) throw new Error((body && (body.error || body.message)) || text || `HTTP ${res.status}`);
  return body;
}

// { files: [{ id, name, kind, bytes, status, error, pages, screens, added, updated, base, changes }], max }
// (sliqtly.com adds `upload`: the Storage folder the page puts a file in)
export async function listShared(fetchFn = fetch) {
  return answer(await fetchFn(roots.api));
}

// A file from the page (a File or Blob with a name) put in the shared
// files; resolves to its card, status "indexing" until the server is done.
// `upload` (sliqtly.com) puts the file in Storage and resolves to where;
// the server then moves it in.
export async function putShared(file, fetchFn = fetch, upload = null) {
  if (upload) {
    const path = await upload(file);
    return answer(await fetchFn(`${roots.api}/add`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: file.name, upload: path }),
    }));
  }
  return answer(await fetchFn(`${roots.api}/` + encodeURIComponent(file.name), { method: "PUT", body: file }));
}

export async function renameShared(id, name, fetchFn = fetch) {
  return answer(await fetchFn(`${roots.api}/${encodeURIComponent(id)}/rename`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }),
  }));
}

export async function removeShared(id, fetchFn = fetch) {
  return answer(await fetchFn(`${roots.api}/${encodeURIComponent(id)}`, { method: "DELETE" }));
}

export async function sharedIndex(id, fetchFn = fetch) {
  return answer(await fetchFn(sharedIndexUrl(id)));
}

// Whether any file is still being indexed: the list is asked again until
// none is.
export function anyIndexing(files) {
  return (files || []).some((f) => f.status === "indexing");
}

// What a file's row says under its name: its size and screens, the
// indexing, or why it failed. `t` translates.
export function sharedNote(f, t = (s) => s) {
  if (!f) return "";
  if (f.status === "indexing") return t("Indexing…");
  if (f.status === "failed") return t("Could not read it") + (f.error ? ": " + f.error : "");
  const parts = [];
  // a variation keeps only its change against the original (base)
  if (f.kind === "figma-variation") parts.push(f.base ? t("Variation of {name}").replace("{name}", f.base) : t("Made here"));
  if (f.screens && f.kind === "pptx") parts.push(f.screens === 1 ? t("1 slide") : t("{n} slides").replace("{n}", String(f.screens)));
  else if (f.screens) parts.push(f.screens === 1 ? t("1 screen") : t("{n} screens").replace("{n}", String(f.screens)));
  if (f.bytes) parts.push(sizeText(f.bytes));
  return parts.join(" · ");
}

export function sizeText(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return Math.round(n / (1 << 10)) + " KB";
  return n + " B";
}
