// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Looking at an older version before restoring it (Version history → View
// version). The version's slides play in a frame of their own: the page
// again, opened as `?version-view`, in the viewer (slides only, no editor,
// no store), so nothing the open deck has is touched until Restore.
//
// The editor hands the frame the version (DeckHistory.checkout) by
// postMessage once the frame says it is ready. What crosses is checked here,
// kept apart from main.js so it can be tested under Node
// (web/test/version-view.test.mjs).

export const VIEW_PARAM = "version-view";
const READY = "sliqtly:version-view:ready";
const PACKET = "sliqtly:version-view";

// The frame's address, relative to the page's base.
export function viewSrc() {
  return "./?" + VIEW_PARAM;
}

// Whether this page is such a frame: asked for in the address and inside
// another page (opened by itself, it is the editor as usual).
export function isViewFrame(search, framed) {
  return !!framed && new URLSearchParams(search || "").has(VIEW_PARAM);
}

export function readyMessage() {
  return { kind: READY };
}
export function isReady(data) {
  return !!data && data.kind === READY;
}

// A version as checked out → what the frame is sent: the text, the theme and
// the files (pictures as Blobs, data files as text). `name` and `time` say
// which version it is.
export function viewPacket(snap, { name = "", time = "" } = {}) {
  const files = [];
  for (const f of snap?.files || []) {
    if (!f || typeof f.path !== "string") continue;
    files.push({ path: f.path, type: f.type || "", data: f.data });
  }
  return {
    kind: PACKET,
    name: String(snap?.name || name || ""),
    time: String(time || ""),
    md: String(snap?.md ?? ""),
    css: snap?.css == null ? null : String(snap.css),
    theme: String(snap?.theme ?? ""),
    files,
  };
}

// What the frame was sent → the version, or null when it is not one: only
// text and Blobs are taken, a path that climbs out ("..", "/x") is left out.
export function readPacket(data) {
  if (!data || data.kind !== PACKET || typeof data.md !== "string") return null;
  const files = [];
  for (const f of Array.isArray(data.files) ? data.files : []) {
    if (!f || typeof f.path !== "string" || !safePath(f.path)) continue;
    const isBlob = typeof Blob !== "undefined" && f.data instanceof Blob;
    if (typeof f.data !== "string" && !isBlob) continue;
    files.push({ path: f.path, type: typeof f.type === "string" ? f.type : "", data: f.data });
  }
  return {
    name: typeof data.name === "string" ? data.name : "",
    time: typeof data.time === "string" ? data.time : "",
    md: data.md,
    css: typeof data.css === "string" ? data.css : null,
    theme: typeof data.theme === "string" ? data.theme : "",
    files,
  };
}

function safePath(p) {
  return !!p && !p.startsWith("/") && !p.split("/").some((s) => s === ".." || s === "");
}
