// SPDX-License-Identifier: AGPL-3.0-or-later

// The shared files of a server of one's own (mcp-go/sharedfiles.go): design
// files everyone on the server sees, kept apart from any room or deck. No
// page in it: main.js hands it `fetch` and draws what it returns.
//
// A Figma file put here is indexed once on the server; a screen, or a part
// of one, is then a picture the server draws from that index:
//
//   /files/shared/<file>/png/<layer>.png        (layer: an id or a screen's name)
//   /files/shared/<file>/index.json             (pages, screens, parts, words)

// The address of layer `node` of shared file `file` drawn as a PNG, at most
// `max` pixels on its long side. A layer inside an instance is "12:34#1:2;3:4",
// a name may hold anything: the whole layer is one escaped path segment.
export function sharedPngUrl(file, node, max = 2048) {
  return `/files/shared/${encodeURIComponent(file)}/png/${encodeURIComponent(node)}.png${max ? `?max=${max}` : ""}`;
}

export function sharedIndexUrl(file) {
  return `/files/shared/${encodeURIComponent(file)}/index.json`;
}

async function answer(res) {
  const text = await res.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch { /* a plain message */ }
  if (!res.ok) throw new Error((body && (body.error || body.message)) || text || `HTTP ${res.status}`);
  return body;
}

// { files: [{ id, name, kind, bytes, status, error, pages, screens, added, updated }], max }
export async function listShared(fetchFn = fetch) {
  return answer(await fetchFn("/api/shared"));
}

// A file from the page (a File or Blob with a name) put in the shared
// files; resolves to its card, status "indexing" until the server is done.
export async function putShared(file, fetchFn = fetch) {
  return answer(await fetchFn("/api/shared/" + encodeURIComponent(file.name), { method: "PUT", body: file }));
}

export async function renameShared(id, name, fetchFn = fetch) {
  return answer(await fetchFn(`/api/shared/${encodeURIComponent(id)}/rename`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }),
  }));
}

export async function removeShared(id, fetchFn = fetch) {
  return answer(await fetchFn(`/api/shared/${encodeURIComponent(id)}`, { method: "DELETE" }));
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
  if (f.screens) parts.push(f.screens === 1 ? t("1 screen") : t("{n} screens").replace("{n}", String(f.screens)));
  if (f.bytes) parts.push(sizeText(f.bytes));
  return parts.join(" · ");
}

export function sizeText(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return Math.round(n / (1 << 10)) + " KB";
  return n + " B";
}
