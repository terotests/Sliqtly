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
  const w = { calls: [], log: [], open: false, last: 0, toasts: [], links: [], decks: [], rooms: [] };
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
    roomChatDeck: (id, name) => w.log.push(["deck", id, name]),
    roomChatLastSeq: () => w.last,
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
  await w.chat.request("send\t\thello\tthere");
  assert.deepEqual(w.calls[0], ["post_room_message", { room_id: "r1", text: "hello\tthere", thread_id: undefined, as: w.chat.as() }]);
  await w.chat.request("send\tm1\tin a thread");
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
  await w.chat.event({ t: "reopen" });
  assert.deepEqual(w.calls.at(-1), ["read_room_chat", { room_id: "r1", after_seq: 3, limit: 500 }]);
  w.chat.close();
  assert.equal(JSON.parse(w.store.getItem(READ_KEY)).r1, 3);
});
