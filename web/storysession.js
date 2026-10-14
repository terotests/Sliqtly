// SPDX-License-Identifier: AGPL-3.0-or-later

// A Story's session on the page (docs/story-workspace.md): between the
// Session panel drawn in the app (src/PresPanels.rgr `session`, an EVGUI
// ChatCtl) and the server's operations (mcp-go/storysession.go,
// POST /api/rooms/<op>). No UI here.
//
// The panel shows the Story's timeline (its parents' entries up to its fork
// point, then its own) as messages, a line under the title (where it was
// forked from, how many forks) and the actions: Fork with context, each
// fork to open, the original to open. What is typed and sent is a note; a
// fork is named by what is typed in the field when Fork with context is
// pressed.
//
//   const s = new StorySession({ app, call, deck, me, t, lang, toast, openDeck, deckName })
//
// deck() is the open presentation's share id ("" none); me() who writes
// ({ id, name, avatar, color }, web/roomchat.js chatMe); call(op, args) the
// server's operation; deckName(id) a Story's name.
//   s.toggle()        the rail's Session pressed
//   s.request(r)      "session-send", "session-act\t<value>", "session-closed"
//   s.event(v)        a `chat` event from the stream ({t: "session", deck,
//                     entries}, or "reopen")
//   s.deckChanged()   another presentation opened

// the words a kind is shown with
export function kindLabel(kind, t = same) {
  switch (kind) {
    case "prompt": return t("Prompt");
    case "reply": return t("Reply");
    case "tool": return t("Tool call");
    case "search": return t("Search");
    case "file": return t("File");
    case "artifact": return t("Artifact");
    case "decision": return t("Decision");
    case "finding": return t("Finding");
    case "note": return t("Note");
    case "fork": return t("Fork");
  }
  return kind;
}

// an assistant's colour when the server gave none, and a person's
const BOT_COLOR = "#7c3aed";
const PERSON_COLOR = "#2563eb";

// how many entries the panel reads at a time, and keeps
export const PAGE = 500;

import { chatTime } from "./collab.js";

// the words as they are, where no translation is given
const same = (s) => s;

// One entry as the panel's message: an id that is the same in every
// timeline it shows in (the Story it was written in and its number there),
// who wrote it, and its text: the kind in bold, the tool as code, the
// words, what it used, and for an inherited entry the Story it came from.
export function entryRow(e, deck, names, t = same, now = Date.now(), lang = "en") {
  const by = e.by || {};
  const bot = by.kind === "bot";
  const head = ["*" + kindLabel(e.kind, t) + "*"];
  if (e.tool) head.push("`" + e.tool + "`");
  const lines = [head.join(" ")];
  if (e.text) lines.push(e.text);
  if (e.refs?.length) lines.push(e.refs.map((r) => "`" + r + "`").join(" "));
  if (e.origin && e.origin !== deck) lines.push("_" + t("from") + " " + (names?.[e.origin] || e.origin) + " #" + e.seq + "_");
  return {
    id: (e.origin || deck) + ":" + e.seq,
    who: by.id || (bot ? "bot" : "person"),
    name: (by.name || (bot ? t("Assistant") : t("Someone"))) + (bot ? " 🤖" : ""),
    color: by.color || (bot ? BOT_COLOR : PERSON_COLOR),
    text: lines.join("\n"),
    time: chatTime(Number(e.at) || 0, now, lang),
  };
}

// the line under the title: where the Story was forked from, its forks
export function subtitleOf(s, names, t = same) {
  const parts = [];
  const f = s.forked_from;
  if (f) parts.push(t("Forked from") + " " + (names?.[f.deck_id] || f.deck_id) + " " + t("at entry") + " " + f.at);
  const n = (s.forks || []).length;
  if (n) parts.push(n === 1 ? t("1 fork") : n + " " + t("forks"));
  parts.push((s.total || 0) === 1 ? t("1 entry") : (s.total || 0) + " " + t("entries"));
  return parts.join(" · ");
}

// the panel's buttons: [value, label, variant]
export function actionsOf(s, names, t = same) {
  const out = [["fork", "⑂ " + t("Fork with context"), "primary"]];
  if (s.forked_from) out.push(["open:" + s.forked_from.deck_id, "↰ " + (names?.[s.forked_from.deck_id] || t("Original")), ""]);
  for (const f of s.forks || []) out.push(["open:" + f.deck_id, "⑂ " + f.name + " (" + f.entries_since + ")", ""]);
  return out;
}

// a fork's name when none was typed: "fork 2" after "fork", …
export function forkName(typed, forks) {
  const t = String(typed || "").replace(/\s+/g, " ").trim();
  if (t) return t.slice(0, 80);
  const taken = new Set((forks || []).map((f) => f.name));
  for (let i = (forks || []).length + 1; ; i++) {
    const n = "fork " + i;
    if (!taken.has(n)) return n;
  }
}

export class StorySession {
  constructor(deps) {
    this.d = { t: same, ...deps };
    // the session shown: its deck and what get_session said
    this.deck = "";
    this.state = null;
    // the Stories' names, for "from …" and the buttons
    this.names = {};
    this.busy = false;
  }

  async toggle() {
    const { app } = this.d;
    if (app.sessionIsOpen()) {
      app.sessionOpen(false);
      return;
    }
    const deck = this.d.deck();
    if (!deck) {
      this.d.toast?.(this.d.t("Save the presentation first: its session is kept with it on the server."));
      return;
    }
    await this.load(deck);
    app.sessionOpen(true);
  }

  // read the whole timeline (a page at a time) and show it
  async load(deck) {
    const { app, call, t } = this.d;
    const first = await call("get_session", { deck_id: deck, limit: PAGE });
    const entries = [...(first.entries || [])];
    let after = first.next_after;
    while (after) {
      const more = await call("get_session", { deck_id: deck, after, limit: PAGE });
      entries.push(...(more.entries || []));
      after = more.next_after;
    }
    this.deck = deck;
    this.state = { ...first, entries };
    this.names[deck] = first.name || deck;
    await this.nameStories(entries, first);
    app.sessionClear(subtitleOf(this.state, this.names, t));
    for (const [v, label, variant] of actionsOf(this.state, this.names, t)) app.sessionAction(v, label, variant);
    this.put(entries);
  }

  // the names of the Stories it shows entries of, its parent's and forks'
  async nameStories(entries, s) {
    for (const f of s.forks || []) this.names[f.deck_id] = f.name;
    const ids = new Set(entries.map((e) => e.origin).filter(Boolean));
    if (s.forked_from) ids.add(s.forked_from.deck_id);
    for (const id of ids) {
      if (this.names[id]) continue;
      this.names[id] = (await this.d.deckName?.(id).catch(() => "")) || id;
    }
  }

  put(entries) {
    const { app, t, lang } = this.d;
    const now = (this.d.now || Date.now)();
    for (const e of entries) {
      const r = entryRow(e, this.deck, this.names, t, now, lang);
      app.sessionAdd(r.id, r.who, r.name, r.color, r.text, r.time);
    }
  }

  async request(r) {
    const { app, call, toast, t } = this.d;
    if (r === "session-closed") return;
    if (!this.deck) return;
    if (r === "session-send") {
      const text = app.sessionTakeSent().trim();
      if (!text) return;
      try {
        const out = await call("log_session", { deck_id: this.deck, entries: [{ kind: "note", text }], as: this.d.me() });
        this.take(out.entries || []);
      } catch (e) {
        toast?.(t("The note was not kept: ") + (e?.message || e));
      }
      return;
    }
    if (!r.startsWith("session-act\t")) return;
    const v = r.slice("session-act\t".length);
    if (v.startsWith("open:")) {
      app.sessionOpen(false);
      await this.d.openDeck(v.slice(5));
      return;
    }
    if (v === "fork" && !this.busy) {
      this.busy = true;
      try {
        const name = forkName(app.sessionTakeDraft(), this.state?.forks);
        const out = await call("fork_story", { deck_id: this.deck, name, as: this.d.me() });
        toast?.(t("Forked with its context: ") + out.name);
        app.sessionOpen(false);
        await this.d.openDeck(out.deck_id);
      } catch (e) {
        toast?.(t("Could not fork: ") + (e?.message || e));
      } finally {
        this.busy = false;
      }
    }
  }

  // entries the server kept: shown once (the stream tells them too)
  take(entries) {
    if (!this.state) return;
    const known = new Set(this.state.entries.map((e) => e.origin + ":" + e.seq));
    const fresh = entries.filter((e) => !known.has(e.origin + ":" + e.seq));
    if (!fresh.length) return;
    this.state.entries.push(...fresh);
    this.state.total = (this.state.total || 0) + fresh.length;
    this.d.app.sessionSubtitle(subtitleOf(this.state, this.names, this.d.t));
    this.put(fresh);
  }

  async event(v) {
    const { app } = this.d;
    if (!v || !this.deck || !app.sessionIsOpen()) return;
    if (v.t === "reopen") {
      await this.load(this.deck);
      return;
    }
    if (v.t !== "session" || v.deck !== this.deck) return;
    // a fork made shows as a button too: read it again
    if ((v.entries || []).some((e) => e.kind === "fork")) {
      await this.load(this.deck);
      return;
    }
    this.take(v.entries || []);
  }

  // another presentation opened: an open panel shows its session
  async deckChanged() {
    const { app } = this.d;
    if (!app.sessionIsOpen()) return;
    const deck = this.d.deck();
    if (deck === this.deck) return;
    if (!deck) {
      app.sessionOpen(false);
      return;
    }
    await this.load(deck);
  }
}
