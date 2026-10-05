// SPDX-License-Identifier: AGPL-3.0-or-later

// Editing a deck together on a server of one's own (mcp-go/collab.go), with
// no page in it: main.js hands it the editor and the server's transport.
//
// The Markdown is one text everyone edits; an edit is a delta (RangerDiff's
// RdOt, the Quill Delta form) sent on the revision this page has. RdOtClient
// keeps what was sent and not yet acknowledged, and turns the others' edits
// into what to apply here. The server sends every edit back in one order,
// this page's own as its acknowledgement.
//
// Who is here: each person starts as an "Anonymous Zebra" in a colour of
// their own (kept in this browser) and can rename themselves; their caret
// and selection show in the editor, the chat beside the deck.

export const ANIMALS = [
  "Zebra", "Otter", "Panda", "Koala", "Lynx", "Falcon", "Heron", "Badger", "Beaver", "Bison",
  "Dolphin", "Ferret", "Gecko", "Hedgehog", "Ibis", "Jaguar", "Kiwi", "Lemur", "Moose", "Narwhal",
  "Ocelot", "Puffin", "Quokka", "Raven", "Seal", "Tapir", "Walrus", "Yak", "Fox", "Owl",
];

// readable on white, far enough apart
export const COLORS = [
  "#ea580c", "#0d9488", "#7c3aed", "#db2777", "#2563eb", "#16a34a", "#ca8a04", "#dc2626",
  "#0891b2", "#9333ea", "#65a30d", "#c2410c",
];

export function anonymousName(rand = Math.random) {
  return "Anonymous" + ANIMALS[Math.floor(rand() * ANIMALS.length) % ANIMALS.length];
}

export function pickColor(rand = Math.random) {
  return COLORS[Math.floor(rand() * COLORS.length) % COLORS.length];
}

function randomId(rand = Math.random) {
  const abc = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let s = "";
  for (let i = 0; i < 16; i += 1) s += abc[Math.floor(rand() * abc.length) % abc.length];
  return s;
}

const ME_KEY = "sliqtly.collab.me";

// This person (kept in `storage`) and this page (new each time).
export function loadMe(storage, rand = Math.random) {
  let me = null;
  try { me = JSON.parse(storage?.getItem(ME_KEY) || "null"); } catch (_) { /* a new one */ }
  if (!me || typeof me.who !== "string" || !/^[A-Za-z0-9_-]{4,64}$/.test(me.who)) {
    me = { who: randomId(rand), name: anonymousName(rand), color: pickColor(rand) };
    saveMe(storage, me);
  }
  if (typeof me.name !== "string" || !me.name.trim()) me.name = anonymousName(rand);
  if (!/^#[0-9a-fA-F]{6}$/.test(me.color || "")) me.color = pickColor(rand);
  return { who: me.who, name: me.name, color: me.color, client: randomId(rand) };
}

export function saveMe(storage, me) {
  try { storage?.setItem(ME_KEY, JSON.stringify({ who: me.who, name: me.name, color: me.color })); } catch (_) { /* this page only */ }
}

export function cleanName(s) {
  // eslint-disable-next-line no-control-regex
  return [...String(s || "").replace(/[\u0000-\u001f\u007f]/g, "").trim()].slice(0, 40).join("");
}

export function rgbOf(hex) {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || "");
  return m ? [parseInt(m[1], 16), parseInt(m[2], 16), parseInt(m[3], 16)] : [100, 116, 139];
}

// "14:05" today, "3.10. 14:05" another day (`lang` "fi") or "Oct 3 14:05"
export function chatTime(at, now = Date.now(), lang = "en") {
  const d = new Date(at);
  const n = new Date(now);
  const hm = String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  if (d.toDateString() === n.toDateString()) return hm;
  if (lang === "fi") return `${d.getDate()}.${d.getMonth() + 1}. ${hm}`;
  return d.toLocaleString("en", { month: "short" }) + " " + d.getDate() + " " + hm;
}

// A delta as editor replaces, each at an offset of the text as the ones
// before it left it: [{ offset, removed, text }]
export function editsOf(delta) {
  const out = [];
  let pos = 0;
  for (const o of delta.ops) {
    if (o.kind === 1) {
      pos += o.n;
    } else if (o.kind === 2) {
      out.push({ offset: pos, removed: 0, text: o.text });
      pos += o.text.length;
    } else {
      const last = out[out.length - 1];
      // an insert and the delete after it: one replace
      if (last && last.offset + last.text.length === pos && last.removed === 0) last.removed = o.n;
      else out.push({ offset: pos, removed: o.n, text: "" });
    }
  }
  return out;
}

// The text, the revision and the others' carets, as this page sees them.
export class CollabDoc {
  constructor(ot, { text, rev, client }) {
    this.ot = ot;
    this.text = text;
    this.client = client;
    this.state = new ot.RdOtClient(rev);
    // client → { who, name, color, caret, anchor } (offsets in `text`)
    this.peers = new Map();
  }

  get rev() { return this.state.rev; }

  waiting() { return this.state.waiting(); }

  // The editor's text is `next` now (the caret after the edit helps tell
  // which "a" of "aa" was typed): the delta, or null when nothing changed.
  local(next, caret = -1) {
    if (next === this.text) return null;
    const d = this.ot.RdOtDelta.diff(this.text, next, caret);
    this.text = next;
    this.state.local(d);
    for (const p of this.peers.values()) {
      p.caret = d.transformIndex(p.caret, false);
      p.anchor = d.transformIndex(p.anchor, false);
    }
    return d;
  }

  // what to send now: { rev, ops } once, or null
  takeSend() {
    const d = this.state.takeSend();
    return d ? { rev: this.state.rev, ops: JSON.parse(d.toJson()) } : null;
  }

  // An edit from the stream, in order. Its own: acknowledged ([]). Anyone
  // else's: applied here, and returned as the editor's replaces.
  op(m) {
    if (m.rev !== this.state.rev + 1) return null; // seen already, or a gap (the caller resets)
    if (m.client === this.client && this.state.waiting()) {
      this.state.ack();
      return [];
    }
    const d = this.state.receive(this.ot.RdOtDelta.fromJson(JSON.stringify(m.ops)));
    this.text = d.apply(this.text);
    for (const p of this.peers.values()) {
      const own = p.client === m.client;
      p.caret = d.transformIndex(p.caret, own);
      p.anchor = d.transformIndex(p.anchor, own);
    }
    return editsOf(d);
  }

  // The server's list of who is here (carets at its revision `rev`, which
  // is this page's when it arrives in order)
  setPeers(list) {
    const next = new Map();
    for (const p of list || []) {
      if (!p || p.client === this.client) continue;
      next.set(p.client, { client: p.client, who: p.who, name: p.name, color: p.color, caret: this.local0(p.caret), anchor: this.local0(p.anchor) });
    }
    this.peers = next;
  }

  cursor(m) {
    const p = this.peers.get(m.client);
    if (!p) return false;
    p.caret = this.local0(m.caret);
    p.anchor = this.local0(m.anchor);
    return true;
  }

  // a position in the server's text at this page's revision → here
  local0(at) {
    const n = Math.max(0, Math.min(Number(at) || 0, Number.MAX_SAFE_INTEGER));
    return Math.min(this.state.toLocal(n), this.text.length);
  }

  // the rows ScriptEditor draws: "name\tr\tg\tb\tcaret\tanchor"
  peerRows() {
    return [...this.peers.values()].map((p) => [p.name, ...rgbOf(p.color), p.caret, p.anchor].join("\t")).join("\n");
  }
}

// One page in one deck's room: the editor watched and kept in step, the
// others' carets, the chat.
//
//   editor:    { version(), text(), caret(), anchor(), apply(offset, removed, text), synced(), setPeers(rows) }
//   transport: { snapshot(id), send(id, body), presence(id, body), chat(id, body),
//                stream(id, query, onEvent, onOpen) → close }
//   on:        { peers(list), chat(msg), state(text), me(name, color) }
export class CollabSession {
  constructor(ot, transport, editor, me, on = {}) {
    this.ot = ot;
    this.t = transport;
    this.ed = editor;
    this.me = me;
    this.on = on;
    this.id = "";
    this.doc = null;
    this.close = null;
    this.seq = 0;
    this.sending = null;
    this.version = -1;
    this.caretSent = "";
    this.presenceDue = 0;
    this.people = [];
    this.stopped = false;
    this.resetting = null;
    this.joined = false;
    this.epoch = "";
  }

  active() { return !!(this.doc && !this.stopped); }

  // Joins the deck's room with the editor holding `text` (what this page
  // has): the room's text replaces it where they differ.
  async start(id) {
    this.id = id;
    this.stopped = false;
    await this.resync();
  }

  stop() {
    this.stopped = true;
    if (this.close) this.close();
    this.close = null;
    this.doc = null;
    this.ed.setPeers("");
  }

  async resync() {
    if (this.resetting) return this.resetting;
    this.resetting = (async () => {
      if (this.close) this.close();
      this.close = null;
      const s = await this.t.snapshot(this.id);
      if (!s || this.stopped) return;
      // the editor takes the room's text, its caret kept where it can be
      const here = this.ed.text();
      const d = this.ot.RdOtDelta.diff(here, s.md, -1);
      for (const e of editsOf(d)) this.ed.apply(e.offset, e.removed, e.text);
      this.ed.synced();
      this.doc = new CollabDoc(this.ot, { text: s.md, rev: s.rev, client: this.me.client });
      this.version = this.ed.version();
      this.sending = null;
      this.doc.setPeers(s.peers);
      for (const m of s.chat || []) this.on.chat?.(m);
      // the run of the room these revs belong to: a room opened again (the
      // server restarted, or it was left empty) counts from 0 once more
      this.epoch = s.epoch || "";
      const q = { client: this.me.client, who: this.me.who, name: this.me.name, color: this.me.color, rev: s.rev, epoch: this.epoch };
      // the caret is told once the stream has joined; a stream that came
      // back (the browser reconnects it) tells this page's name as it is now
      this.joined = false;
      this.close = this.t.stream(this.id, q, (m) => this.event(m), () => {
        this.caretSent = "";
        if (this.joined) this.t.presence(this.id, { client: this.me.client, name: this.me.name, color: this.me.color }).catch(() => {});
        this.joined = true;
      });
      this.on.state?.(s.md);
    })();
    try { await this.resetting; } finally { this.resetting = null; }
  }

  // every frame: the editor's change sent, the caret told now and then
  tick(now = Date.now()) {
    if (!this.active() || this.resetting) return;
    this.takeLocal();
    this.pump();
    if (this.joined && now >= this.presenceDue && !this.doc.waiting()) {
      const at = this.ed.caret() + ":" + this.ed.anchor() + ":" + this.doc.rev;
      if (at !== this.caretSent) {
        this.caretSent = at;
        this.presenceDue = now + 120;
        this.t.presence(this.id, { client: this.me.client, rev: this.doc.rev, caret: this.ed.caret(), anchor: this.ed.anchor() })
          .catch(() => { this.caretSent = ""; });
      }
    }
  }

  // what was typed since the last look, as an edit of this page's
  takeLocal() {
    if (!this.active() || this.resetting) return;
    const v = this.ed.version();
    if (v === this.version) return;
    this.version = v;
    if (this.doc.local(this.ed.text(), this.ed.caret())) this.paintPeers();
  }

  // what waits goes, one edit at a time; one that got no answer goes again
  // (the server takes a seq once)
  pump() {
    if (this.sending || !this.doc) return;
    const out = this.doc.takeSend();
    if (!out) return;
    this.seq += 1;
    const body = { client: this.me.client, rev: out.rev, ops: out.ops, seq: this.seq, epoch: this.epoch };
    const go = (tries) => {
      this.sending = this.t.send(this.id, body).then(() => { this.sending = null; }, (e) => {
        if (this.stopped) return;
        if (e?.code && e.code !== "timeout") {
          // the room cannot take it (too far behind, gone): start again from it
          this.sending = null;
          this.resync().catch(() => {});
          return;
        }
        setTimeout(() => go(tries + 1), Math.min(4000, 250 * 2 ** tries));
      });
    };
    go(0);
  }

  event(m) {
    if (!this.doc || this.stopped) return;
    if (m.t === "op") {
      // typed before this arrived: an edit made on the text before it
      if (!this.resetting) this.takeLocal();
      const edits = this.doc.op(m);
      if (edits === null) {
        if (m.rev > this.doc.rev + 1) this.resync().catch(() => {});
        return;
      }
      if (edits.length) {
        for (const e of edits) this.ed.apply(e.offset, e.removed, e.text);
        this.version = this.ed.version();
        this.ed.synced();
        this.paintPeers();
      }
      this.pump();
    } else if (m.t === "cursor") {
      if (m.rev === this.doc.rev && this.doc.cursor(m)) this.paintPeers();
    } else if (m.t === "peers") {
      if (m.rev === this.doc.rev) this.doc.setPeers(m.peers);
      else this.doc.setPeers((m.peers || []).map((p) => ({ ...p, caret: 0, anchor: 0 })));
      this.people = m.peers || [];
      // the room gives a name or colour someone else here has to no one:
      // this page goes by the one it was given (sent again on a reconnect)
      const mine = this.people.find((p) => p.client === this.me.client);
      if (mine && (mine.name !== this.me.name || mine.color !== this.me.color)) {
        this.me.name = mine.name;
        this.me.color = mine.color;
        this.on.me?.(mine.name, mine.color);
      }
      this.paintPeers();
      this.on.peers?.(this.people);
    } else if (m.t === "chat") {
      this.on.chat?.(m.msg);
    } else if (m.t === "reset") {
      this.resync().catch(() => {});
    }
  }

  paintPeers() {
    this.ed.setPeers(this.doc ? this.doc.peerRows() : "");
  }

  async rename(name, color) {
    const n = cleanName(name);
    if (n) this.me.name = n;
    if (color && /^#[0-9a-fA-F]{6}$/.test(color)) this.me.color = color;
    if (this.active()) await this.t.presence(this.id, { client: this.me.client, name: this.me.name, color: this.me.color });
  }

  async say(text) {
    if (!this.active() || !String(text || "").trim()) return null;
    return this.t.chat(this.id, { client: this.me.client, text });
  }
}
