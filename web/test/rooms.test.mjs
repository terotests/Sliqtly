// node --test: rooms in this browser (web/rooms.js)
import test from "node:test";
import assert from "node:assert/strict";
import { emptyRooms, parseRooms, roomOf, listRooms, roomDecks, createRoom, moveDeck, deckLines, touchRoom, activeRooms, searchRooms, GENERAL, PLAYGROUND, ONBOARDING, SHOWN, ACTIVE_DAYS, ACTIVE_MAX } from "../rooms.js";

const decks = [
  { id: "a", name: "Budget", updated: 300 },
  { id: "b", name: "Kickoff", updated: 100, current: true },
  { id: "cloud:M1", name: "Made by Claude", updated: 200 },
];
const samples = [{ key: "welcome", name: "Welcome" }, { key: "talous", name: "Finance" }];
let n = 0;
const idOf = () => "id" + ++n;

test("every presentation starts in General, newest first; Onboarding holds the samples", () => {
  const s = emptyRooms();
  const rooms = listRooms(s, decks, samples);
  assert.deepEqual(rooms.map((r) => [r.room_id, r.presentations]), [[GENERAL, 3], [PLAYGROUND, 0], [ONBOARDING, 2]]);
  assert.deepEqual(roomDecks(s, GENERAL, decks, samples).map((d) => d.id), ["a", "cloud:M1", "b"]);
  assert.deepEqual(roomDecks(s, ONBOARDING, decks, samples).map((d) => d.id), ["sample:welcome", "sample:talous"]);
});

test("a new room is listed after the built-in ones, by name, and a deck moves into it", () => {
  let { state, id } = createRoom(emptyRooms(), "  Zeta   team ", idOf);
  assert.ok(id.startsWith("r-"));
  ({ state } = createRoom(state, "Alpha", idOf));
  assert.deepEqual(listRooms(state, decks, samples).map((r) => r.title), ["General", "Playground", "Onboarding", "Alpha", "Zeta team"]);
  state = moveDeck(state, "a", id);
  assert.equal(roomOf(state, "a"), id);
  assert.deepEqual(roomDecks(state, id, decks, samples).map((d) => d.name), ["Budget"]);
  assert.equal(listRooms(state, decks, samples)[0].presentations, 2);
  // back to General is no placing at all
  state = moveDeck(state, "a", GENERAL);
  assert.deepEqual(state.placed, {});
});

test("an empty name makes no room; Onboarding and unknown rooms take no decks", () => {
  const s = emptyRooms();
  assert.equal(createRoom(s, "   ", idOf).id, "");
  assert.equal(moveDeck(s, "a", ONBOARDING), s);
  assert.equal(moveDeck(s, "a", "r-nope"), s);
});

test("a deck in a room that is gone is in General", () => {
  const s = { rooms: [], placed: { a: "r-gone" } };
  assert.equal(roomOf(s, "a"), GENERAL);
});

test("what localStorage held is read back, and junk is an empty state", () => {
  const { state } = createRoom(emptyRooms(), "Kept", idOf);
  assert.deepEqual(parseRooms(JSON.stringify(state)), state);
  assert.deepEqual(parseRooms("{not json"), emptyRooms());
  assert.deepEqual(parseRooms(null), emptyRooms());
  assert.deepEqual(parseRooms(JSON.stringify({ rooms: [{ id: 3 }], placed: 5 })), emptyRooms());
});

test("the panel shows at most SHOWN, then Show all, then the row moving the open deck here", () => {
  const many = Array.from({ length: SHOWN + 2 }, (_, i) => ({ id: "d" + i, name: "Deck\t" + i }));
  const lines = deckLines(many, { showAll: "… Show all (7)", moveHere: "+ Move here", currentId: "x" }).split("\n");
  assert.equal(lines.length, SHOWN + 2);
  assert.equal(lines[0], "d0\tDeck 0\t\t");
  assert.equal(lines[SHOWN], "all\t… Show all (7)\t\ta");
  assert.equal(lines[SHOWN + 1], "move\t+ Move here\t\tm");
  // the open one already here: no move row; few decks: no Show all
  assert.equal(deckLines(many.slice(0, 2), { showAll: "all", moveHere: "+", currentId: "d1" }).split("\n").length, 2);
});

test("the panel lists the rooms one is active in: built-in three, then the recently used, newest first", () => {
  const now = 1_000_000_000_000;
  const day = 86400000;
  let s = emptyRooms();
  const rows = [
    { room_id: GENERAL, title: "General" }, { room_id: PLAYGROUND, title: "Playground" }, { room_id: ONBOARDING, title: "Onboarding" },
    ...Array.from({ length: ACTIVE_MAX + 3 }, (_, i) => ({ room_id: "r" + i, title: "Room " + i })),
  ];
  for (let i = 0; i < ACTIVE_MAX + 2; i++) s = touchRoom(s, "r" + i, now - i * 1000);
  s = touchRoom(s, "r0", now - (ACTIVE_DAYS + 1) * day); // gone quiet
  const { shown, hidden } = activeRooms(rows, s, now);
  assert.deepEqual(shown.slice(0, 3).map((r) => r.room_id), [GENERAL, PLAYGROUND, ONBOARDING]);
  assert.equal(shown.length, 3 + ACTIVE_MAX);
  assert.equal(shown[3].room_id, "r1");
  assert.ok(!shown.some((r) => r.room_id === "r0"));
  assert.equal(hidden, rows.length - shown.length);
});

test("search finds rooms by every word of the query, any case and accent", () => {
  const rows = [{ room_id: "a", title: "Q1 Päivitys" }, { room_id: "b", title: "Q1 budget" }, { room_id: "c", title: "Team" }];
  assert.deepEqual(searchRooms(rows, "q1 paivitys").map((r) => r.room_id), ["a"]);
  assert.deepEqual(searchRooms(rows, "Q1").map((r) => r.room_id), ["a", "b"]);
  assert.deepEqual(searchRooms(rows, "  "), []);
  assert.equal(searchRooms(Array.from({ length: 50 }, (_, i) => ({ room_id: "x" + i, title: "x" })), "x").length, 20);
});

test("a room made here is touched, so it is listed at once", () => {
  const { state, id } = createRoom(emptyRooms(), "Fresh", () => "z");
  assert.ok(state.touched[id] > 0);
});
