// SPDX-License-Identifier: AGPL-3.0-or-later

// A page's one stream to a server of one's own (mcp-go/localevents.go): the
// server's state, decks changed, and the room of the deck being edited
// together (web/collab.js), all on one connection. No UI here; the page
// (mcp-go/assets/sliqtly-local.js) hands it what to call.
//
// A WebSocket, because a browser opens at most six HTTP/1.1 connections to
// one server for all its tabs together, and an EventSource holds one of them
// for as long as the page is open: three tabs with two streams each took
// all six, and opening or saving a deck in any tab waited until one closed.
// WebSockets are counted apart. A server behind a proxy that does not pass
// WebSockets on is still heard, through Server-Sent Events (/api/events).
//
// One room at a time: joining one opens the stream again with the room in
// its address; leaving opens it again without, a moment later, so leaving a
// deck and joining the next one opens it once. A stream that drops comes
// back by itself, naming the last rev it had, and the server sends the
// edits it missed.
//
//   new EventLine({ WebSocket, EventSource, location, setTimeout, clearTimeout }, {
//     status(st), changed(id), open(), lost(),
//   })
//   line.start()
//   line.join(id, query, onEvent, onOpen) → leave()

export const RETRY_MS = [500, 1000, 2000, 4000, 5000];
export const LEAVE_MS = 300;

export class EventLine {
  constructor(env, on) {
    this.env = env;
    this.on = on;
    this.room = null; // { id, q, rev, onEvent, onOpen }
    this.conn = null; // the WebSocket or EventSource open now
    this.sse = typeof env.WebSocket !== "function";
    this.everOpen = false; // a WebSocket has opened here: they get through
    this.fails = 0; // WebSockets that closed without opening, in a row
    this.tries = 0;
    this.timer = 0;
  }

  start() {
    this.connect();
  }

  // the room's address part; rev: the last edit this page has
  query() {
    const r = this.room;
    if (!r) return "";
    const q = r.q;
    const p = { room: r.id, client: q.client, who: q.who, name: q.name, color: q.color, rev: String(r.rev) };
    // the room's run this rev counts in: another run's revs are other edits
    if (q.epoch) p.epoch = q.epoch;
    return "?" + new URLSearchParams(p);
  }

  join(id, q, onEvent, onOpen) {
    const room = { id, q, rev: Number(q.rev) || 0, onEvent, onOpen };
    this.room = room;
    this.connect();
    return () => {
      if (this.room !== room) return;
      this.room = null;
      this.later(LEAVE_MS);
    };
  }

  later(ms) {
    this.env.clearTimeout(this.timer);
    this.timer = this.env.setTimeout(() => this.connect(), ms);
  }

  close() {
    const c = this.conn;
    this.conn = null;
    if (c) {
      c.onopen = c.onmessage = c.onerror = c.onclose = null;
      c.close();
    }
  }

  connect() {
    this.env.clearTimeout(this.timer);
    this.timer = 0;
    this.close();
    if (this.sse) this.openSse();
    else this.openSocket();
  }

  opened(c) {
    if (c !== this.conn) return;
    this.tries = 0;
    this.on.open?.();
    this.room?.onOpen?.();
  }

  // a room event: the rev of the last edit kept, for coming back
  roomEvent(m) {
    const r = this.room;
    if (!r) return;
    if (m.t === "op" && m.rev > r.rev) r.rev = m.rev;
    r.onEvent(m);
  }

  openSocket() {
    const loc = this.env.location;
    const url = (loc.protocol === "https:" ? "wss://" : "ws://") + loc.host + "/api/socket" + this.query();
    const ws = new this.env.WebSocket(url);
    this.conn = ws;
    let open = false;
    ws.onopen = () => {
      open = true;
      this.everOpen = true;
      this.fails = 0;
      this.opened(ws);
    };
    ws.onmessage = (ev) => {
      let m = null;
      try { m = JSON.parse(ev.data); } catch (_) { return; }
      if (!m || ws !== this.conn) return;
      if (m.k === "status") this.on.status?.(m.v || {});
      else if (m.k === "changed") this.on.changed?.(m.id || "");
      else if (m.k === "room" && m.v?.t) this.roomEvent(m.v);
    };
    ws.onerror = () => {};
    ws.onclose = () => {
      if (ws !== this.conn) return;
      this.conn = null;
      // never through, twice, where none ever was: something between
      // does not pass WebSockets on; events then
      if (!open && !this.everOpen && ++this.fails >= 2) {
        this.sse = true;
        this.connect();
        return;
      }
      this.on.lost?.();
      this.later(RETRY_MS[Math.min(this.tries++, RETRY_MS.length - 1)]);
    };
  }

  // Server-Sent Events: the browser comes back by itself, with the
  // Last-Event-ID of the room's last edit
  openSse() {
    const es = new this.env.EventSource("/api/events" + this.query());
    this.conn = es;
    es.addEventListener("status", (ev) => {
      if (es !== this.conn) return;
      try { this.on.status?.(JSON.parse(ev.data)); } catch (_) { /* not one */ }
    });
    es.onopen = () => this.opened(es);
    es.onerror = () => { if (es === this.conn) this.on.lost?.(); };
    es.onmessage = (ev) => {
      let m = null;
      try { m = JSON.parse(ev.data); } catch (_) { return; }
      if (!m || es !== this.conn) return;
      if (m.t) this.roomEvent(m);
      else if (m.id) this.on.changed?.(m.id);
    };
  }
}
