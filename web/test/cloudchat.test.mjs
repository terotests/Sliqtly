// node --test: a shared room's chat from the cloud (web/cloudchat.js),
// against a fake fetch and a fake Firestore
import test from "node:test";
import assert from "node:assert/strict";
import { cloudRoomsCall, chatEventOf, listenRoomChat, roomChatKey } from "../cloudchat.js";

test("calls go to /editor/api/rooms as JSON, errors carry the server's words", async () => {
  const sent = [];
  const fetchFn = async (url, init) => {
    sent.push([url, init.method, JSON.parse(init.body), init.credentials]);
    if (url.endsWith("read_room_chat")) return { ok: false, status: 401, json: async () => ({ error: "Sign in again.", code: "signed-out" }) };
    return { ok: true, status: 200, json: async () => ({ message_id: "m1", seq: 3 }) };
  };
  const call = cloudRoomsCall(fetchFn);
  assert.deepEqual(await call("post_room_message", { room_id: "R1", text: "hi" }), { message_id: "m1", seq: 3 });
  assert.deepEqual(sent[0], ["/editor/api/rooms/post_room_message", "POST", { room_id: "R1", text: "hi" }, "same-origin"]);
  await assert.rejects(call("read_room_chat", { room_id: "R1" }), (e) => e.message === "Sign in again." && e.code === "signed-out" && e.status === 401);
});

test("a message document is the stream's chat event", () => {
  const msg = { room: "R1", id: "m1", seq: 2, at: Date.UTC(2026, 9, 9), from: { id: "p-u1", name: "Tero" }, text: "hi" };
  const v = chatEventOf("R1", { seq: 2, touched: 5, msg: JSON.stringify(msg) });
  assert.equal(v.t, "msg");
  assert.equal(v.room, "R1");
  assert.equal(v.msg.text, "hi");
  assert.equal(v.msg.time, "2026-10-09T00:00:00.000Z");
  assert.equal(chatEventOf("R1", { msg: "{" }), null);
  assert.equal(chatEventOf("R1", { msg: JSON.stringify({ text: "no id" }) }), null);
  assert.equal(chatEventOf("R1", {}), null);
});

test("listens to the room's messages touched since it opened", () => {
  const asked = [];
  let handler = null;
  const q = {
    where: (f, op, v) => (asked.push(["where", f, op, v]), q),
    orderBy: (f) => (asked.push(["orderBy", f]), q),
    onSnapshot: (ok) => ((handler = ok), () => asked.push(["stop"])),
  };
  const db = {
    collection: (c) => (asked.push(["collection", c]), { doc: (k) => (asked.push(["doc", k]), { collection: (s) => (asked.push(["sub", s]), q) }) }),
  };
  const got = [];
  const stop = listenRoomChat(db, "R1", 1000, (v) => got.push(v));
  assert.deepEqual(asked, [["collection", "room_chat"], ["doc", roomChatKey("R1")], ["sub", "msgs"], ["where", "touched", ">", 1000], ["orderBy", "touched"]]);
  const docOf = (m) => ({ data: () => ({ msg: JSON.stringify(m) }) });
  handler({ docChanges: () => [{ type: "added", doc: docOf({ id: "a", seq: 1 }) }, { type: "modified", doc: docOf({ id: "b", seq: 2 }) }, { type: "removed", doc: docOf({ id: "c", seq: 3 }) }] });
  assert.deepEqual(got.map((v) => v.msg.id), ["a", "b"]);
  stop();
  assert.deepEqual(asked.at(-1), ["stop"]);
});
