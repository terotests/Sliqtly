// SPDX-License-Identifier: AGPL-3.0-or-later
// Slides and elements on the clipboard (src/PresClip.rgr writes and reads
// the text): their files go in as base64. The bytes of a deck's file, and
// base64 both ways, without the page.

const CHUNK = 0x8000;

export function toBase64(bytes) {
  const u8 = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let bin = "";
  for (let i = 0; i < u8.length; i += CHUNK) bin += String.fromCharCode.apply(null, u8.subarray(i, i + CHUNK));
  return btoa(bin);
}

export function fromBase64(text) {
  const bin = atob(String(text).replace(/\s+/g, ""));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

// A file record of the deck (vfs, or one not yet saved): its bytes.
export async function fileBytes(rec) {
  const data = rec?.data;
  if (data == null) return new ArrayBuffer(0);
  if (typeof data === "string") return new TextEncoder().encode(data).buffer;
  if (data instanceof ArrayBuffer) return data;
  if (ArrayBuffer.isView(data)) return data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);
  return data.arrayBuffer();
}

// A file record's size in bytes, without reading it.
export function fileSize(rec) {
  const data = rec?.data;
  if (data == null) return Number(rec?.size) || 0;
  if (typeof data === "string") return new TextEncoder().encode(data).byteLength;
  if (data instanceof ArrayBuffer || ArrayBuffer.isView(data)) return data.byteLength;
  if (typeof data.size === "number") return data.size;
  return Number(rec.size) || 0;
}

// A pasted file against the deck's one of the same name: "new", "same"
// or "differs" (src/PresClip.rgr fileSteps).
export async function fileState(rec, b64) {
  if (!rec) return "new";
  return toBase64(await fileBytes(rec)) === String(b64).replace(/\s+/g, "") ? "same" : "differs";
}

// Ctrl/⌘+Shift+V: the paste event that follows is Paste without formatting.
export function plainChord(ev) {
  return !!(ev.ctrlKey || ev.metaKey) && !!ev.shiftKey && !ev.altKey && (ev.key === "v" || ev.key === "V");
}

// The copy's picture as HTML, for apps that paste HTML (mail, Word, Docs):
// the PNG inline.
export function clipImgHtml(b64) {
  return '<img src="data:image/png;base64,' + b64 + '" alt="">';
}
