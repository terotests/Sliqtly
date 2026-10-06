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
// Files put into the chat go into the room's own files first (PUT
// /api/files/rooms/<room>/<name>, mcp-go/roomfiles.go) and wait in the
// composer; the message names them when it is sent. The pictures the
// channel draws (attached ones, embedded slides) are loaded here, by the
// address the display list names (`want`).
//
//   const chat = new RoomChat({ app, call, store, now, zone, t, toast,
//                               openLink, openDeck, openRoom, copy, rooms,
//                               put, pickFiles, sizeOf, load })
//   chat.open(room)         a room pressed: its channel
//   chat.close()           the room's chat steps aside (a presentation opened)
//   chat.request(r)         one of the app's "roomchat:" requests
//   chat.event(v)           a `chat` event from the stream
//   chat.attach(files, inThread)   files picked, pasted or dropped
//   chat.want(srcs)         pictures the channel draws; loaded once
//   chat.pictures           src → picture, for the painter

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
    // a picture's size by its name in the room's files, for the message
    this.sizes = new Map();
    this.pictures = new Map();
    this.loading = new Set();
    this.up = { main: 0, thread: 0 };
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
    // a presentation embedded in the chat shows its slide, drawn by the
    // server of one's own (/s/<deck>/<n>.jpg)
    app.roomChatPictures(this.d.ownServer() ? "/s/{deck}/{slide}.jpg" : "");
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
    this.names(got);
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

  // the names of presentations embedded in messages ([[slides:<id>]], a
  // link to one), each asked for once, whatever room they live in
  names(got) {
    if (!this.d.deckName) return;
    this.named = this.named || new Set();
    const ms = [...(got?.messages || []), ...(got?.root ? [got.root] : [])];
    for (const m of ms) {
      for (const [, id] of String(m?.text || "").matchAll(/(?:\[\[slides:|\/s\/)([A-Za-z0-9]{6,32})/g)) {
        if (this.named.has(id)) continue;
        this.named.add(id);
        this.d
          .deckName(id)
          .then((name) => {
            if (name) {
              this.d.app.roomChatDeck(id, name);
              this.d.paint();
            }
          })
          .catch(() => {});
      }
    }
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
      this.names({ messages: [v.msg] });
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
    this.names(got);
    this.d.paint();
  }

  // Files picked, pasted or dropped: into the room's files, then waiting
  // in the composer (the thread's when inThread) until the message is sent
  async attach(files, inThread = false) {
    const { app } = this.d;
    const room = this.room;
    if (!room || !app.roomChatOpen() || !this.d.ownServer()) return;
    const key = inThread ? "thread" : "main";
    const list = [...files];
    this.up[key] += list.length;
    app.roomChatUploading(inThread, this.up[key]);
    this.d.paint();
    for (const file of list) {
      try {
        const type = file.type || "application/octet-stream";
        const size = /^image\//.test(type) ? await this.d.sizeOf(file).catch(() => null) : null;
        const got = await this.d.put("/api/files/rooms/" + encodeURIComponent(room) + "/" + encodeURIComponent(file.name || "file") + "?unique=1", file, type);
        if (size && size.w > 0 && size.h > 0) this.sizes.set(got.name, { w: size.w, h: size.h });
        if (room === this.room) app.roomChatPending(inThread, got.name, got.url || "", got.type || type, got.size || 0, size?.w || 0, size?.h || 0);
      } catch (e) {
        this.d.toast(this.d.t("Could not attach ") + (file.name || "") + ": " + (e.message || e));
      }
      this.up[key] -= 1;
      app.roomChatUploading(inThread, this.up[key]);
      this.d.paint();
    }
  }

  // The pictures the channel's display list draws: each loaded once (a
  // picture that will not load stays a gap)
  want(srcs) {
    for (const src of srcs) {
      if (!src || this.pictures.has(src) || this.loading.has(src)) continue;
      this.loading.add(src);
      this.d
        .load(src)
        .then((img) => this.pictures.set(src, img))
        .catch(() => this.pictures.set(src, null))
        .finally(() => {
          this.loading.delete(src);
          this.d.paint();
        });
    }
  }

  // One of the app's requests ("roomchat:" taken off).
  async request(r) {
    const [what, ...f] = r.split("\t");
    const room = this.room;
    const { app, call } = this.d;
    if (what === "close") {
      this.close();
    } else if (what === "send") {
      const [thread, names, ...text] = f;
      const files = String(names || "").split("\n").filter(Boolean).map((n) => (this.sizes.has(n) ? { name: n, ...this.sizes.get(n) } : n));
      const args = { room_id: room, text: text.join("\t"), thread_id: thread || undefined, as: this.as() };
      if (files.length) args.files = files;
      await call("post_room_message", args);
    } else if (what === "attach") {
      const files = await this.d.pickFiles();
      if (files && files.length) await this.attach(files, f[0] === "thread");
    } else if (what === "file") {
      this.d.openLink(f[0]);
    } else if (what === "react") {
      await call("chat_react", { room_id: room, message_id: f[0], emoji: f[1], as: this.as() });
    } else if (what === "delete") {
      await call("chat_delete", { room_id: room, message_id: f[0], as: this.as() });
    } else if (what === "thread") {
      const got = await call("read_room_chat", { room_id: room, thread_id: f[0] });
      if (room === this.room) app.roomChatLoad(JSON.stringify(got), false);
      this.names(got);
    } else if (what === "older") {
      const [thread, before] = f;
      const got = await call("read_room_chat", { room_id: room, thread_id: thread || undefined, before_seq: Number(before) || 0 });
      if (room === this.room) app.roomChatLoad(JSON.stringify(got), true);
      this.names(got);
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
