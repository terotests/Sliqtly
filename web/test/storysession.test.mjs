// node --test: a Story's session on the page (web/storysession.js), against
// a fake app and a fake server
import test from "node:test";
import assert from "node:assert/strict";
import { StorySession, entryRow, subtitleOf, actionsOf, forkName } from "../storysession.js";
import { chatTime } from "../collab.js";

const tool = { n: 1, seq: 1, origin: "main", at: Date.UTC(2026, 9, 14, 9, 5), kind: "tool", tool: "read_file", refs: ["file:data/req.csv"], by: { id: "bot-claude", name: "Claude", kind: "bot", color: "#0ea5e9" } };
const said = { n: 2, seq: 1, origin: "sec", at: Date.UTC(2026, 9, 14, 9, 6), kind: "finding", text: "No recovery path", by: { id: "p-anna", name: "Anna", kind: "person" } };

test("an entry as a message: kind, tool, words, refs, where it came from", () => {
  const now = Date.UTC(2026, 9, 14, 12);
  const r = entryRow(tool, "sec", { main: "Passkey support" }, undefined, now, "en");
  assert.equal(r.id, "main:1");
  assert.equal(r.name, "Claude 🤖");
  assert.equal(r.color, "#0ea5e9");
  assert.equal(r.text, "*Tool call* `read_file`\n`file:data/req.csv`\n_from Passkey support #1_");
  assert.equal(r.time, chatTime(tool.at, now, "en"));
  const own = entryRow(said, "sec", {}, undefined, now, "en");
  assert.equal(own.text, "*Finding*\nNo recovery path");
  assert.equal(own.color, "#2563eb");
});

test("the line under the title and the buttons", () => {
  const s = { total: 5, forked_from: { deck_id: "main", at: 4 }, forks: [{ deck_id: "ux", name: "ux", entries_since: 2 }] };
  const names = { main: "Passkey support" };
  assert.equal(subtitleOf(s, names), "Forked from Passkey support at entry 4 · 1 fork · 5 entries");
  assert.equal(subtitleOf({ total: 1 }, {}), "1 entry");
  assert.deepEqual(actionsOf(s, names), [
    ["fork", "⑂ Fork with context", "primary"],
    ["open:main", "↰ Passkey support", ""],
    ["open:ux", "⑂ ux (2)", ""],
  ]);
});

test("a fork's name: what was typed, else the next free fork n", () => {
  assert.equal(forkName("  security   review ", []), "security review");
  assert.equal(forkName("", [{ name: "fork 2" }]), "fork 3");
  assert.equal(forkName("", [{ name: "x" }, { name: "fork 3" }]), "fork 4");
  assert.equal(forkName("", []), "fork 1");
});

function world() {
  const w = { open: false, rows: [], acts: [], calls: [], opened: [], toasts: [], draft: "", sent: "" };
  w.app = {
    sessionIsOpen: () => w.open,
    sessionOpen: (on) => { w.open = on; },
    sessionClear: (sub) => { w.rows = []; w.acts = []; w.sub = sub; },
    sessionAdd: (id, who, name, color, text) => { if (w.rows.some((r) => r.id === id)) return false; w.rows.push({ id, name, text }); return true; },
    sessionAction: (v, label) => w.acts.push(v),
    sessionSubtitle: (sub) => { w.sub = sub; },
    sessionTakeSent: () => { const s = w.sent; w.sent = ""; return s; },
    sessionTakeDraft: () => { const s = w.draft; w.draft = ""; return s; },
  };
  w.answers = {
    get_session: (a) => (a.after ? { entries: [said], total: 2 } : { deck_id: "sec", name: "Passkey · security", total: 2, entries: [tool], next_after: 1, forked_from: { deck_id: "main", at: 1 }, forks: [] }),
    log_session: (a) => ({ entries: [{ ...said, seq: 2, n: 3, kind: "note", text: a.entries[0].text }] }),
    fork_story: (a) => ({ deck_id: "ux1", name: "Passkey · " + a.name }),
  };
  w.s = new StorySession({
    app: w.app,
    deck: () => w.deck ?? "sec",
    me: () => ({ id: "anna1", name: "Anna" }),
    call: async (op, a) => { w.calls.push([op, a]); return w.answers[op](a); },
    deckName: async (id) => (id === "main" ? "Passkey support" : ""),
    openDeck: async (id) => w.opened.push(id),
    toast: (m) => w.toasts.push(m),
  });
  return w;
}

test("the rail opens it with the whole timeline, pages read on", async () => {
  const w = world();
  await w.s.toggle();
  assert.equal(w.open, true);
  assert.deepEqual(w.calls.map(([op, a]) => [op, a.after]), [["get_session", undefined], ["get_session", 1]]);
  assert.deepEqual(w.rows.map((r) => r.id), ["main:1", "sec:1"]);
  assert.match(w.rows[0].text, /from Passkey support #1/);
  assert.deepEqual(w.acts, ["fork", "open:main"]);
  assert.equal(w.sub, "Forked from Passkey support at entry 1 · 2 entries");
  await w.s.toggle();
  assert.equal(w.open, false);
});

test("a note is written as the page's person and shown once", async () => {
  const w = world();
  await w.s.toggle();
  w.sent = "Looks right";
  await w.s.request("session-send");
  const [op, a] = w.calls.at(-1);
  assert.equal(op, "log_session");
  assert.deepEqual(a, { deck_id: "sec", entries: [{ kind: "note", text: "Looks right" }], as: { id: "anna1", name: "Anna" } });
  assert.equal(w.rows.length, 3);
  // the stream tells the same entry: not twice
  await w.s.event({ t: "session", deck: "sec", entries: [{ ...said, seq: 2, n: 3, kind: "note", text: "Looks right" }] });
  assert.equal(w.rows.length, 3);
  assert.equal(w.sub, "Forked from Passkey support at entry 1 · 3 entries");
  // another Story's entries are not this one's
  await w.s.event({ t: "session", deck: "other", entries: [{ ...said, seq: 9 }] });
  assert.equal(w.rows.length, 3);
});

test("Fork with context names the fork by what was typed and opens it", async () => {
  const w = world();
  await w.s.toggle();
  w.draft = "security review";
  await w.s.request("session-act\tfork");
  assert.deepEqual(w.calls.at(-1), ["fork_story", { deck_id: "sec", name: "security review", as: { id: "anna1", name: "Anna" } }]);
  assert.deepEqual(w.opened, ["ux1"]);
  assert.equal(w.open, false);
  assert.match(w.toasts[0], /Passkey · security review/);
  await w.s.request("session-act\topen:main");
  assert.deepEqual(w.opened, ["ux1", "main"]);
});

test("no saved presentation: a note, nothing read", async () => {
  const w = world();
  w.deck = "";
  await w.s.toggle();
  assert.equal(w.open, false);
  assert.equal(w.calls.length, 0);
  assert.match(w.toasts[0], /Save the presentation first/);
});

test("another presentation opened: the open panel follows it", async () => {
  const w = world();
  await w.s.toggle();
  w.deck = "main";
  w.answers.get_session = () => ({ deck_id: "main", name: "Passkey support", total: 0, entries: [], forks: [{ deck_id: "sec", name: "security", entries_since: 1 }] });
  await w.s.deckChanged();
  assert.deepEqual(w.rows, []);
  assert.deepEqual(w.acts, ["fork", "open:sec"]);
  assert.equal(w.sub, "1 fork · 0 entries");
});
