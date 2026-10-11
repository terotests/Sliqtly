// SPDX-License-Identifier: AGPL-3.0-or-later
// The AI assistant's calls (the ✨ AI drawer, src/PresAi.rgr): on
// sliqtly.com/editor the server answers (mcp-go/aiassist.go); a page without
// the editor's sign-in (a server of one's own, the checks) has no assistant.
//
//   GET  /editor/api/ai/credits   {"available", "credits"}
//   POST /editor/api/ai/ask       {"reply", "credits"} or {"error", "code", "credits"}
//
// The app reads the answers as they come (JSON text), refusals included: a
// refusal with a reason is said in the conversation.

export const AI_BASE = "/editor/api/ai/";

// whether this page has the assistant: the editor's page on the server
export function aiOnPage(doc = globalThis.document) {
  return !!doc?.querySelector?.('meta[name="sliqtly-editor"]');
}

// The server's answer as JSON text. A refusal with a reason ({"error"}) is an
// answer too; only no answer at all (no connection, a page that is not JSON)
// throws.
export async function aiCall(op, body, fetchFn = globalThis.fetch, base = AI_BASE) {
  const res = await fetchFn(base + op, body === undefined
    ? { cache: "no-store" }
    : { method: "POST", headers: { "content-type": "application/json" }, body });
  const text = await res.text();
  let out = null;
  try { out = JSON.parse(text); } catch (_) { /* not JSON */ }
  if (!out || typeof out !== "object") throw new Error(`HTTP ${res.status}`);
  return text;
}
