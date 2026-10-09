// node --test: shared rooms' copies in this browser (web/rooms.js syncShared, adoptRoom)
import test from "node:test";
import assert from "node:assert/strict";
import { emptyRooms, createRoom, moveDeck, createFolder, touchRoom, syncShared, adoptRoom, isShared, roomOf, listRooms, foldersOf, folderOf, changeKept, readKept, GENERAL } from "../rooms.js";

let n = 0;
const idOf = () => "id" + ++n;

function memStore() {
  const m = new Map();
  return { get length() { return m.size; }, key: (i) => [...m.keys()][i] ?? null, getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k) };
}

test("a room shared from here keeps its presentations, folders and use under the server's id", () => {
  let { state, id } = createRoom(emptyRooms(), "Launch", idOf);
  state = moveDeck(state, "d1", id);
  const f = createFolder(state, id, "Drafts", idOf);
  state = moveDeck(f.state, "d2", id, f.id);
  state = touchRoom(state, id, 5);
  state = { ...state, order: [id] };
  state = adoptRoom(state, id, "S1");
  assert.equal(state.rooms.some((r) => r.id === id), false);
  assert.ok(isShared(state, "S1"));
  assert.deepEqual([roomOf(state, "d1"), roomOf(state, "d2"), folderOf(state, "d2")], ["S1", "S1", f.id]);
  assert.deepEqual(foldersOf(state, "S1").map((x) => x.name), ["Drafts"]);
  assert.deepEqual([state.touched.S1, state.order], [5, ["S1"]]);
  assert.equal(adoptRoom(state, GENERAL, "S2"), state, "General is no one's to share");
});

test("the server's list: new shared rooms are kept, names follow, gone ones leave their decks in General", () => {
  let state = createRoom(emptyRooms(), "Mine", idOf).state;
  state = syncShared(state, [{ room_id: "S1", title: "Launch", description: "Q4", created: 10, role: "editor" }, { room_id: "general", title: "x" }]);
  assert.ok(isShared(state, "S1"));
  assert.equal(state.rooms.filter((r) => r.id === "general").length, 0, "a built-in id is not a shared room");
  state = moveDeck(state, "d1", "S1");
  assert.equal(roomOf(state, "d1"), "S1", "a deck goes into a shared room like any other");
  assert.deepEqual(listRooms(state, [{ id: "d1" }], []).find((r) => r.room_id === "S1").presentations, 1);
  state = syncShared(state, [{ room_id: "S1", title: "Launch 2", created: 10, role: "editor", archived: true }]);
  const s1 = state.rooms.find((r) => r.id === "S1");
  assert.deepEqual([s1.title, s1.description, s1.archived, s1.role], ["Launch 2", undefined, true, "editor"]);
  assert.equal(state.rooms.filter((r) => r.title === "Mine").length, 1, "rooms of this browser stay");
  state = syncShared(state, []);
  assert.equal(isShared(state, "S1"), false, "taken out of it");
  assert.equal(roomOf(state, "d1"), GENERAL);
});

test("shared rooms are kept as facts like the others", () => {
  const store = memStore();
  changeKept(store, "rooms", (s) => syncShared(s, [{ room_id: "S1", title: "Launch", created: 3, role: "owner" }]));
  const back = readKept(store, "rooms");
  assert.deepEqual(back.rooms.map((r) => [r.id, r.shared, r.role]), [["S1", true, "owner"]]);
});
