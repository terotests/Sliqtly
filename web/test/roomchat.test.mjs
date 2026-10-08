// node --test: a room's chat on the page (web/roomchat.js), against a fake
// app and a fake server
import test from "node:test";
import assert from "node:assert/strict";
import { RoomChat, chatMe, ME_KEY, READ_KEY, AVATARS } from "../roomchat.js";

function memStore(init = {}) {
  const m = new Map(Object.entries(init));
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), m };
}

function world({ own = true } = {}) {
  const w = { calls: [], log: [], open: false, last: 0, toasts: [], links: [], decks: [], rooms: [], puts: [], picked: [], loads: [] };
  w.app = {
    roomChatOpen: () => w.open,
    roomChatShow: (id, title, about, ro, notice) => { w.open = true; w.log.push(["show", id, title, about, ro, notice]); },
    roomChatHide: () => { w.open = false; w.log.push(["hide"]); },
    roomChatMe: (id, name, av, col) => w.log.push(["me", id, name, av, col]),
    roomChatClock: () => {},
    roomChatReadUpTo: (seq) => w.log.push(["read", seq]),
    roomChatChannels: (names) => w.log.push(["channels", names]),
    roomChatLoad: (json, older) => { w.log.push(["load", JSON.parse(json), older]); },
    roomChatPut: (json) => { const m = JSON.parse(json); w.log.push(["put", m.id]); const gap = m.seq > w.last + 1 ? w.last : -1; w.last = Math.max(w.last, m.seq); return gap; },
    roomChatHere: (json) => w.log.push(["here", JSON.parse(json).here]),
    roomChatAbout: (ht, title, ha, about) => w.log.push(["about", ht, title, ha, about]),
    roomChatDeck: (id, name) => w.log.push(["deck", id, name]),
    roomChatLastSeq: () => w.last,
    roomChatPictures: (pattern, linkPattern, own) => w.log.push(["pictures", pattern, linkPattern, own]),
    roomChatUploading: (th, n) => w.log.push(["uploading", th, n]),
    roomChatPending: (th, name, url, type, size, pw, ph) => w.log.push(["pending", th, name, url, type, size, pw, ph]),
  };
  w.answers = {
    read_room_chat: () => ({ room: { room_id: "r1" }, messages: [], last_seq: 4 }),
    chat_here: () => ({ here: 1, people: [] }),
    get_room: () => ({ presentations: [{ deck_id: "d1", name: "Plan" }] }),
  };
  w.store = memStore();
  w.rooms = [{ room_id: "r1", title: "PAY 817", description: "Retry" }, { room_id: "r2", title: "general" }];
  w.chat = new RoomChat({
    app: w.app,
    store: w.store,
    name: "Ada",
    t: (s) => s,
    toast: (s) => w.toasts.push(s),
    ownServer: () => own,
    call: async (op, args) => { w.calls.push([op, args]); return (w.answers[op] || (() => ({ ok: true })))(args); },
    now: () => 0,
    zone: () => 0,
    rooms: async () => w.rooms,
    roomsChanged: async () => {},
    openLink: (u) => w.links.push(u),
    openDeck: async (d, s) => w.decks.push([d, s]),
    openRoom: async (id) => w.log.push(["room", id]),
    copy: async (s) => w.log.push(["copy", s]),
    paint: () => {},
    put: async (path, file, type) => {
      w.puts.push([path, file.name, type]);
      if (file.name === "bad.bin") throw new Error("too big");
      return { name: file.name === "dup.png" ? "dup (2).png" : file.name, url: "http://h/files/" + file.name, type, size: file.size };
    },
    pickFiles: async () => w.picked,
    deckName: async (id) => (id === "abcdef1" ? "Budget" : ""),
    sizeOf: async (f) => ({ w: 640, h: 480 }),
    load: async (src) => { w.loads.push(src); if (src.includes("broken")) throw new Error("no"); return { src }; },
  });
  return w;
}

test("a browser's chat identity is made once and kept", () => {
  const store = memStore();
  const me = chatMe(store, "Ada", () => 0.5);
  assert.match(me.id, /^[a-z2-9]{16}$/);
  assert.equal(me.name, "Ada");
  assert.ok(AVATARS.includes(me.avatar));
  assert.deepEqual(chatMe(store, "Bob"), me);
  assert.equal(chatMe(memStore(), "  ").name, "Guest");
  assert.equal(JSON.parse(store.getItem(ME_KEY)).id, me.id);
});

test("opening a room shows its channel, loads it and says one is here", async () => {
  const w = world();
  w.store.setItem(READ_KEY, JSON.stringify({ r1: 2 }));
  await w.chat.open("r1");
  const shown = w.log.find((l) => l[0] === "show");
  assert.deepEqual(shown, ["show", "r1", "PAY 817", "Retry", false, ""]);
  assert.deepEqual(w.log.find((l) => l[0] === "read"), ["read", 2]);
  assert.deepEqual(w.log.find((l) => l[0] === "channels"), ["channels", "general"]);
  // a deck of this server by its own address; one shared on another Sliqtly
  // (sliqtly.com) by that one's link card picture
  assert.deepEqual(w.log.find((l) => l[0] === "pictures"), ["pictures", "/s/{deck}/{slide}.jpg", "{origin}/api/card/{deck}.jpg?slide={slide}", ""]);
  assert.deepEqual(w.calls.map((c) => c[0]).slice(0, 2), ["read_room_chat", "chat_here"]);
  assert.equal(w.calls[1][1].as.name, "Ada");
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(w.log.some((l) => l[0] === "deck" && l[1] === "d1"));
  w.chat.close();
  assert.deepEqual(w.calls.at(-1), ["chat_here", { room_id: "r1", as: w.chat.as(), away: true }]);
  assert.equal(w.open, false);
});

test("without a server of one's own the channel says so and is read only", async () => {
  const w = world({ own: false });
  await w.chat.open("r1");
  const shown = w.log.find((l) => l[0] === "show");
  assert.equal(shown[4], true);
  assert.match(shown[5], /server of your own/);
  assert.equal(w.calls.length, 0);
});

test("a presentation opened while the room is still being opened keeps the chat closed", async () => {
  for (const own of [true, false]) {
    const w = world({ own });
    let rooms;
    const wait = new Promise((r) => { rooms = r; });
    w.chat.d.rooms = () => wait;
    const opening = w.chat.open("r1");
    // the page shows a presentation before the rooms arrive (web/main.js shownDoc)
    w.chat.close();
    rooms(w.rooms);
    await opening;
    assert.equal(w.open, false, "own server: " + own);
    assert.ok(!w.log.some((l) => l[0] === "show"));
    assert.ok(!w.calls.some((c) => c[0] === "chat_here"));
    // pressing the room again opens it
    w.chat.d.rooms = async () => w.rooms;
    await w.chat.open("r1");
    assert.equal(w.open, true);
    w.chat.close();
  }
});

test("a presentation opened while the room's messages load: nothing more is drawn or marked", async () => {
  const w = world();
  let answer;
  w.answers.read_room_chat = () => new Promise((r) => { answer = r; });
  const opening = w.chat.open("r1");
  while (!answer) await new Promise((r) => setTimeout(r, 0));
  w.chat.close();
  answer({ room: { room_id: "r1" }, messages: [], last_seq: 4 });
  await opening;
  assert.equal(w.open, false);
  assert.ok(!w.log.some((l) => l[0] === "load"));
  assert.ok(!w.calls.some((c) => c[0] === "chat_here" && !c[1].away));
});

test("requests go to the server as the person", async () => {
  const w = world();
  await w.chat.open("r1");
  w.calls.length = 0;
  await w.chat.request("send\t\t\thello\tthere");
  assert.deepEqual(w.calls[0], ["post_room_message", { room_id: "r1", text: "hello\tthere", thread_id: undefined, as: w.chat.as() }]);
  await w.chat.request("send\tm1\t\tin a thread");
  assert.equal(w.calls[1][1].thread_id, "m1");
  await w.chat.request("react\tm1\t👍");
  assert.deepEqual(w.calls[2], ["chat_react", { room_id: "r1", message_id: "m1", emoji: "👍", as: w.chat.as() }]);
  await w.chat.request("delete\tm1");
  assert.equal(w.calls[3][0], "chat_delete");
  await w.chat.request("thread\tm1");
  assert.deepEqual(w.calls[4], ["read_room_chat", { room_id: "r1", thread_id: "m1" }]);
  await w.chat.request("older\t\t10");
  assert.deepEqual(w.calls[5], ["read_room_chat", { room_id: "r1", thread_id: undefined, before_seq: 10 }]);
  assert.equal(w.log.at(-1)[2], true);
  await w.chat.request("describe\tNew words");
  assert.deepEqual(w.calls[6], ["update_room", { room_id: "r1", description: "New words" }]);
  await w.chat.request("link\thttps://example.com");
  assert.deepEqual(w.links, ["https://example.com"]);
  // the message menu's Edit: one's own message gets its new text
  await w.chat.request("edit\tm1\tfixed\ttext");
  assert.deepEqual(w.calls.at(-1), ["post_room_message", { room_id: "r1", message_id: "m1", text: "fixed\ttext", as: w.chat.as() }]);
  await w.chat.request("channel\tgeneral");
  assert.deepEqual(w.log.at(-1), ["room", "r2"]);
  await w.chat.request("channel\tnowhere");
  assert.deepEqual(w.toasts, ["No room called #nowhere"]);
  await w.chat.request("copy\tsome text");
  assert.deepEqual(w.log.at(-1), ["copy", "some text"]);
  await w.chat.request("deck\td1\t3");
  assert.deepEqual(w.decks, [["d1", 3]]);
  assert.equal(w.open, false);
});

test("a new profile is kept and told to the room", async () => {
  const w = world();
  await w.chat.open("r1");
  w.calls.length = 0;
  await w.chat.request("profile\tAda L\towl\t#22C55E");
  const me = JSON.parse(w.store.getItem(ME_KEY));
  assert.deepEqual([me.name, me.avatar, me.color], ["Ada L", "owl", "#22c55e"]);
  assert.deepEqual(w.log.at(-2).slice(2), ["Ada L", "owl", "#22c55e"]);
  assert.equal(w.calls[0][0], "chat_here");
  assert.equal(w.calls[0][1].as.avatar, "owl");
  await w.chat.request("profile\tAda L\tdragon\tred");
  const again = JSON.parse(w.store.getItem(ME_KEY));
  assert.deepEqual([again.avatar, again.color], ["owl", "#22c55e"]);
  w.chat.close();
});

test("events: the open room's are drawn, a gap asks for what was missed", async () => {
  const w = world();
  await w.chat.open("r1");
  w.calls.length = 0;
  await w.chat.event({ t: "msg", room: "r2", msg: { id: "x", seq: 1 } });
  assert.ok(!w.log.some((l) => l[0] === "put"));
  await w.chat.event({ t: "msg", room: "r1", msg: { id: "a", seq: 1 } });
  await w.chat.event({ t: "msg", room: "r1", msg: { id: "c", seq: 3 } });
  assert.deepEqual(w.log.filter((l) => l[0] === "put").map((l) => l[1]), ["a", "c"]);
  assert.deepEqual(w.calls[0], ["read_room_chat", { room_id: "r1", after_seq: 1, limit: 500 }]);
  await w.chat.event({ t: "here", room: "r1", here: 3, people: [] });
  assert.deepEqual(w.log.at(-1), ["here", 3]);
  // described again by someone else: the head shows it, the name stays
  await w.chat.event({ t: "room", room: "r1", description: "Retry, then a person" });
  assert.deepEqual(w.log.at(-1), ["about", false, "", true, "Retry, then a person"]);
  await w.chat.event({ t: "reopen" });
  assert.deepEqual(w.calls.at(-1), ["read_room_chat", { room_id: "r1", after_seq: 3, limit: 500 }]);
  w.chat.close();
  assert.equal(JSON.parse(w.store.getItem(READ_KEY)).r1, 3);
});

test("files go into the room's files, wait in the composer and go with the message", async () => {
  const w = world();
  await w.chat.open("r1");
  w.calls.length = 0;
  w.picked = [{ name: "dup.png", type: "image/png", size: 10 }, { name: "notes.txt", type: "text/plain", size: 3 }, { name: "bad.bin", type: "", size: 1 }];
  await w.chat.request("attach\tthread");
  assert.deepEqual(w.puts.map((p) => p[0]), ["/api/files/rooms/r1/dup.png?unique=1", "/api/files/rooms/r1/notes.txt?unique=1", "/api/files/rooms/r1/bad.bin?unique=1"]);
  assert.deepEqual(w.log.filter((l) => l[0] === "pending"), [
    ["pending", true, "dup (2).png", "http://h/files/dup.png", "image/png", 10, 640, 480],
    ["pending", true, "notes.txt", "http://h/files/notes.txt", "text/plain", 3, 0, 0],
  ]);
  assert.deepEqual(w.log.filter((l) => l[0] === "uploading").map((l) => l[2]), [3, 2, 1, 0]);
  assert.equal(w.toasts.length, 1);
  assert.match(w.toasts[0], /bad\.bin: too big/);
  await w.chat.request("send\tm1\tdup (2).png\nnotes.txt\tsee these");
  assert.deepEqual(w.calls.at(-1), ["post_room_message", { room_id: "r1", text: "see these", thread_id: "m1", as: w.chat.as(), files: [{ name: "dup (2).png", w: 640, h: 480 }, "notes.txt"] }]);
  await w.chat.request("file\thttp://h/files/notes.txt\tnotes.txt");
  assert.deepEqual(w.links, ["http://h/files/notes.txt"]);
  w.chat.close();
});

test("pictures the channel draws are loaded once", async () => {
  const w = world();
  w.chat.want(["/a.png", "/broken.png", "/a.png", ""]);
  w.chat.want(["/a.png"]);
  await new Promise((r) => setTimeout(r, 0));
  w.chat.want(["/a.png", "/broken.png"]);
  assert.deepEqual(w.loads, ["/a.png", "/broken.png"]);
  assert.deepEqual(w.chat.pictures.get("/a.png"), { src: "/a.png" });
  assert.equal(w.chat.pictures.get("/broken.png"), null);
});

test("embedded presentations are named, each asked for once", async () => {
  const w = world();
  w.answers.read_room_chat = () => ({ room: { room_id: "r1" }, messages: [{ id: "a", seq: 1, text: "[[slides:abcdef1#2]] and https://h/s/zzzzzz9" }], last_seq: 1 });
  await w.chat.open("r1");
  await w.chat.event({ t: "msg", room: "r1", msg: { id: "b", seq: 2, text: "again [[slides:abcdef1]]" } });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(w.log.filter((l) => l[0] === "deck" && l[1] !== "d1"), [["deck", "abcdef1", "Budget"]]);
  assert.deepEqual([...w.chat.named], ["abcdef1", "zzzzzz9"]);
  w.chat.close();
});
