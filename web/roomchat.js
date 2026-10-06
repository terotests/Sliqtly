// SPDX-License-Identifier: AGPL-3.0-or-later

// A room's chat on the page: between the channel drawn in the app
// (src/PresRoomView.rgr, EVGUI ChannelCtl) and the folder server's chat
// (mcp-go/roomchat.go, POST /api/rooms/<op>). No UI here.
//
// The app asks for things as requests ("send\t<thread>\t<text>", "react…",
// see ChannelCtl); this posts them and hands back what the server says.
// Nothing is shown before the server has it: a message posted comes back on
// the page's stream (web/eventline.js, `chat` events) like everyone else's,
// and a stream that dropped asks for what came after the last one seen.
//
// Who one is in the chat — a made-up id for this browser, a name, a retro
// character and its colour — is kept in localStorage, as is the last
// message read in each room (the "New" line).
//
//   const chat = new RoomChat({ app, call, store, now, zone, t, toast,
//                               openLink, openDeck, openRoom, copy, rooms })
//   chat.open(room)         a room pressed: its channel
//   chat.close()           the room's chat steps aside (a presentation opened)
//   chat.request(r)         one of the app's "roomchat:" requests
//   chat.event(v)           a `chat` event from the stream

export const ME_KEY = "sliqtly.chatMe";
export const READ_KEY = "sliqtly.chatRead";
export const HERE_MS = 30000;
export const AVATARS = ["knight", "ghost", "cat", "wizard", "alien", "slime", "bunny", "owl", "ninja", "mushroom"];
export const COLORS = ["#ef4444", "#f97316", "#eab308", "#84cc16", "#22c55e", "#14b8a6", "#0ea5e9", "#2563eb", "#7c3aed", "#c026d3", "#db2777", "#64748b"];

function read(store, key, dflt) {
  try {
    const v = JSON.parse(store.getItem(key) || "null");
    return v ?? dflt;
  } catch (_) {
    return dflt;
  }
}
function write(store, key, v) {
  try { store.setItem(key, JSON.stringify(v)); } catch (_) { /* this page only */ }
}

// This browser's chat identity: made once, a character and colour picked by
// chance until the person picks their own.
export function chatMe(store, name, rand = Math.random) {
  const had = read(store, ME_KEY, null);
  if (had && /^[A-Za-z0-9_-]{4,40}$/.test(had.id || "")) return had;
  const id = Array.from({ length: 16 }, () => "abcdefghijkmnpqrstuvwxyz23456789"[Math.floor(rand() * 32)]).join("");
  const me = {
    id,
    name: String(name || "").trim() || "Guest",
    avatar: AVATARS[Math.floor(rand() * AVATARS.length)],
    color: COLORS[Math.floor(rand() * COLORS.length)],
  };
  write(store, ME_KEY, me);
  return me;
}

export class RoomChat {
  constructor(deps) {
    this.d = deps;
    this.room = "";
    this.me = chatMe(deps.store, deps.name);
    this.timer = 0;
    // which open() is the latest: a close() while one still waits for the
    // rooms keeps the chat closed when they arrive
    this.opening = 0;
  }

  // the person as the server takes it ("as"): their id becomes p-<id>
  as() {
    return { id: this.me.id, name: this.me.name, avatar: this.me.avatar, color: this.me.color };
  }

  fromId() {
    return "p-" + this.me.id;
  }

  async open(room) {
    const { app } = this.d;
    if (!room) return;
    const same = room === this.room && app.roomChatOpen();
    const turn = ++this.opening;
    this.room = room;
    app.roomChatMe(this.fromId(), this.me.name, this.me.avatar, this.me.color);
    app.roomChatClock(this.d.now(), this.d.zone());
    const info = (await this.d.rooms()).find((r) => r.room_id === room) || { title: room };
    if (turn !== this.opening) return;
    if (!this.d.ownServer()) {
      app.roomChatShow(room, info.title || "", info.description || "", true, this.d.t("The chat works on a Sliqtly server of your own (sliqtly serve) for now."));
      return;
    }
    app.roomChatShow(room, info.title || "", info.description || "", !!info.archived, "");
    if (same) return;
    app.roomChatReadUpTo(read(this.d.store, READ_KEY, {})[room] || 0);
    app.roomChatChannels((await this.d.rooms()).filter((r) => r.room_id !== room).map((r) => String(r.title || "").replace(/\s+/g, "_")).join("\n"));
    const got = await this.d.call("read_room_chat", { room_id: room });
    if (turn !== this.opening) return;
    app.roomChatLoad(JSON.stringify(got), false);
    this.markRead();
    this.here();
    this.decks(room).catch(() => {});
  }

  close() {
    const { app } = this.d;
    this.opening++;
    if (!app.roomChatOpen()) return;
    this.markRead();
    app.roomChatHide();
    clearTimeout(this.timer);
    this.timer = 0;
    if (this.room && this.d.ownServer()) this.d.call("chat_here", { room_id: this.room, as: this.as(), away: true }).catch(() => {});
  }

  // the presentations of the room, for the embeds' titles
  async decks(room) {
    const g = await this.d.call("get_room", { room_id: room });
    for (const p of g.presentations || []) this.d.app.roomChatDeck(p.deck_id, p.name || "");
  }

  // "I'm here", again before the server forgets (mcp-go chatPresenceTTL)
  async here() {
    clearTimeout(this.timer);
    const room = this.room;
    if (!room || !this.d.app.roomChatOpen()) return;
    try {
      const out = await this.d.call("chat_here", { room_id: room, as: this.as() });
      if (room === this.room) this.d.app.roomChatHere(JSON.stringify(out));
    } catch (_) { /* the next beat tries again */ }
    this.timer = setTimeout(() => this.here(), HERE_MS);
  }

  markRead() {
    if (!this.room) return;
    const seq = this.d.app.roomChatLastSeq();
    if (!seq) return;
    const all = read(this.d.store, READ_KEY, {});
    if ((all[this.room] || 0) >= seq) return;
    all[this.room] = seq;
    write(this.d.store, READ_KEY, all);
  }

  // A `chat` event from the stream: only the open room's is drawn.
  async event(v) {
    const { app } = this.d;
    if (!v || !this.room || !app.roomChatOpen()) return;
    if (v.t === "reopen") {
      await this.catchUp();
      return;
    }
    if (v.room !== this.room) return;
    if (v.t === "here") {
      app.roomChatHere(JSON.stringify(v));
    } else if (v.t === "msg" && v.msg) {
      const missed = app.roomChatPut(JSON.stringify(v.msg));
      if (missed >= 0) await this.catchUp(missed);
    }
    this.d.paint();
  }

  // what came after `after` (the last one seen), for a page that missed some
  async catchUp(after) {
    const room = this.room;
    const from = after ?? this.d.app.roomChatLastSeq();
    const got = await this.d.call("read_room_chat", { room_id: room, after_seq: from, limit: 500 });
    if (room !== this.room) return;
    this.d.app.roomChatLoad(JSON.stringify(got), false);
    this.d.paint();
  }

  // One of the app's requests ("roomchat:" taken off).
  async request(r) {
    const [what, ...f] = r.split("\t");
    const room = this.room;
    const { app, call } = this.d;
    if (what === "close") {
      this.close();
    } else if (what === "send") {
      const [thread, ...text] = f;
      await call("post_room_message", { room_id: room, text: text.join("\t"), thread_id: thread || undefined, as: this.as() });
    } else if (what === "react") {
      await call("chat_react", { room_id: room, message_id: f[0], emoji: f[1], as: this.as() });
    } else if (what === "delete") {
      await call("chat_delete", { room_id: room, message_id: f[0], as: this.as() });
    } else if (what === "thread") {
      const got = await call("read_room_chat", { room_id: room, thread_id: f[0] });
      if (room === this.room) app.roomChatLoad(JSON.stringify(got), false);
    } else if (what === "older") {
      const [thread, before] = f;
      const got = await call("read_room_chat", { room_id: room, thread_id: thread || undefined, before_seq: Number(before) || 0 });
      if (room === this.room) app.roomChatLoad(JSON.stringify(got), true);
    } else if (what === "link") {
      this.d.openLink(f[0]);
    } else if (what === "deck") {
      this.close();
      await this.d.openDeck(f[0], Number(f[1]) || 0);
    } else if (what === "channel") {
      const want = String(f[0] || "").toLowerCase();
      const hit = (await this.d.rooms()).find((x) => String(x.title || "").replace(/\s+/g, "_").toLowerCase() === want);
      if (hit) await this.d.openRoom(hit.room_id);
      else this.d.toast(this.d.t("No room called #") + f[0]);
    } else if (what === "describe") {
      await call("update_room", { room_id: room, description: f.join("\t") });
      await this.d.roomsChanged();
      if (room === this.room) {
        const again = (await this.d.rooms()).find((x) => x.room_id === room);
        app.roomChatShow(room, again?.title || "", again?.description || "", !!again?.archived, "");
      }
    } else if (what === "profile") {
      const [name, avatar, color] = f;
      this.me = { ...this.me, name: String(name || "").trim() || this.me.name, avatar: AVATARS.includes(avatar) ? avatar : this.me.avatar, color: /^#[0-9a-f]{6}$/i.test(color) ? color.toLowerCase() : this.me.color };
      write(this.d.store, ME_KEY, this.me);
      app.roomChatMe(this.fromId(), this.me.name, this.me.avatar, this.me.color);
      await this.here();
    } else if (what === "copy") {
      await this.d.copy(f.join("\t"));
    }
    this.d.paint();
  }
}
