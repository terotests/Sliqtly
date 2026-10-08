// node --test: rooms in this browser (web/rooms.js)
import test from "node:test";
import assert from "node:assert/strict";
import { emptyRooms, parseRooms, changeKept, readKept, roomOf, listRooms, roomDecks, createRoom, moveDeck, deckLines, touchRoom, activeRooms, searchRooms, orderRooms, updateRoom, archiveRoom, deleteRoom, moveRoom, createFolder, renameFolder, deleteFolder, foldersOf, folderOf, GENERAL, PLAYGROUND, ONBOARDING, SHOWN, ACTIVE_DAYS, ACTIVE_MAX } from "../rooms.js";

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

test("a new room is listed first, newest first, the built-in ones under them, and a deck moves into it", () => {
  let { state, id } = createRoom(emptyRooms(), "  Zeta   team ", idOf);
  assert.ok(id.startsWith("r-"));
  ({ state } = createRoom(state, "Alpha", idOf));
  assert.deepEqual(listRooms(state, decks, samples).map((r) => r.title), ["Alpha", "Zeta team", "General", "Playground", "Onboarding"]);
  ({ state } = createRoom(state, "Beta", idOf, { description: "  PROJ-12 \n" }));
  assert.deepEqual(listRooms(state, decks, samples).slice(0, -3).map((r) => [r.title, r.description]), [["Beta", "PROJ-12"], ["Alpha", ""], ["Zeta team", ""]]);
  state = moveDeck(state, "a", id);
  assert.equal(roomOf(state, "a"), id);
  assert.deepEqual(roomDecks(state, id, decks, samples).map((d) => d.name), ["Budget"]);
  assert.equal(listRooms(state, decks, samples).find((r) => r.room_id === GENERAL).presentations, 2);
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
  // kept before rooms had an order
  assert.deepEqual(parseRooms(JSON.stringify({ rooms: [], placed: {}, touched: {} })).order, []);
});

test("the panel shows at most SHOWN, then Show all, then the row making a new presentation there", () => {
  const many = Array.from({ length: SHOWN + 2 }, (_, i) => ({ id: "d" + i, name: "Deck\t" + i }));
  const lines = deckLines(many, { showAll: "… Show all (7)", addNew: "+ Add new presentation" }).split("\n");
  assert.equal(lines.length, SHOWN + 2);
  assert.equal(lines[0], "d0\tDeck 0\t\t");
  assert.equal(lines[SHOWN], "all\t… Show all (7)\t\ta");
  assert.equal(lines[SHOWN + 1], "new\t+ Add new presentation\t\tn");
  // few decks: no Show all; no add row asked (Onboarding): none
  assert.equal(deckLines(many.slice(0, 2), { showAll: "all", addNew: "+" }).split("\n").length, 3);
  assert.equal(deckLines(many.slice(0, 2), { showAll: "all" }).split("\n").length, 2);
});

test("the panel lists the rooms one is active in: built-in three, then the recently used in the panel's order", () => {
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
  // in the rows' order, not by when they were used
  assert.equal(shown[3].room_id, "r1");
  assert.equal(shown[4].room_id, "r2");
  assert.ok(!shown.some((r) => r.room_id === "r" + (ACTIVE_MAX + 1)));
  assert.ok(!shown.some((r) => r.room_id === "r0"));
  assert.equal(hidden, rows.length - shown.length);
});

test("a room made lately is one of those listed, also for those who have not opened it", () => {
  const now = 1_000_000_000_000;
  const day = 86400000;
  const rows = [
    { room_id: GENERAL, title: "General" },
    { room_id: "new", title: "Made by Bob", created: now - day },
    { room_id: "old", title: "Made long ago", created: now - (ACTIVE_DAYS + 1) * day },
  ];
  const { shown, hidden } = activeRooms(rows, emptyRooms(), now);
  assert.deepEqual(shown.map((r) => r.room_id), [GENERAL, "new"]);
  assert.equal(hidden, 1);
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

test("rooms not dragged yet come first, newest first; dragged ones keep their place; the built-in ones last", () => {
  const rows = [
    { room_id: GENERAL }, { room_id: "a", created: 1 }, { room_id: "b", created: 3 }, { room_id: "c", created: 2 }, { room_id: "d", created: 9 },
  ];
  assert.deepEqual(orderRooms(rows, ["c", "a"]).map((r) => r.room_id), ["d", "b", "c", "a", GENERAL]);
  assert.deepEqual(orderRooms(rows).map((r) => r.room_id), ["d", "b", "c", "a", GENERAL]);
});

test("a room dragged before another, to the end, or onto a built-in (the end too)", () => {
  let s = emptyRooms();
  for (const t of ["A", "B", "C"]) ({ state: s } = createRoom(s, t, idOf));
  const titles = (st) => listRooms(st, [], []).slice(0, -3).map((r) => r.title);
  assert.deepEqual(titles(s), ["C", "B", "A"]);
  const id = (t) => s.rooms.find((r) => r.title === t).id;
  const rows = () => listRooms(s, [], []);
  s = moveRoom(s, rows(), id("A"), id("C"));
  assert.deepEqual(titles(s), ["A", "C", "B"]);
  s = moveRoom(s, rows(), id("A"), "");
  assert.deepEqual(titles(s), ["C", "B", "A"]);
  s = moveRoom(s, rows(), id("C"), GENERAL);
  assert.deepEqual(titles(s), ["B", "A", "C"]);
  // a built-in room stays where it is
  assert.equal(moveRoom(s, rows(), GENERAL, id("A")), s);
  // a room made after the drag is first
  ({ state: s } = createRoom(s, "D", idOf));
  assert.deepEqual(titles(s), ["D", "B", "A", "C"]);
});

test("a new room is first also next to rooms made before there was an order", () => {
  // kept before rooms had an order, then one room dragged, then a new room
  let s = parseRooms(JSON.stringify({ rooms: [{ id: "r-old", title: "Testi", created: 1 }, { id: "r-two", title: "Two", created: 2 }], placed: {}, touched: {} }));
  s = moveRoom(s, listRooms(s, [], []), "r-old", "");
  ({ state: s } = createRoom(s, "N3D-6531", idOf));
  assert.deepEqual(listRooms(s, [], []).slice(0, -3).map((r) => r.title), ["N3D-6531", "Two", "Testi"]);
  s = parseRooms(JSON.stringify({ rooms: [{ id: "r-old", title: "Testi", created: 1 }], placed: {}, touched: {} }));
  ({ state: s } = createRoom(s, "N3D-6531", idOf));
  assert.deepEqual(listRooms(s, [], []).slice(0, -3).map((r) => r.title), ["N3D-6531", "Testi"]);
});

test("a room's settings: renamed, described, archived and back, deleted with its decks back in General", () => {
  let { state: s, id } = createRoom(emptyRooms(), "Ticket", idOf);
  s = updateRoom(s, id, { title: "  PROJ-7  login ", description: "Users sign in" });
  assert.deepEqual(listRooms(s, [], [])[0], { room_id: id, title: "PROJ-7 login", description: "Users sign in", created: s.rooms[0].created, archived: false, presentations: 0 });
  assert.equal(updateRoom(s, id, { title: " " }).rooms[0].title, "PROJ-7 login");
  assert.equal(updateRoom(s, id, { description: "" }).rooms[0].description, undefined);
  s = moveDeck(s, "a", id);
  s = archiveRoom(s, id);
  assert.equal(listRooms(s, decks, samples).length, 3);
  assert.equal(listRooms(s, decks, samples, { archived: true })[0].archived, true);
  // its presentations stay in it
  assert.equal(roomOf(s, "a"), id);
  s = archiveRoom(s, id, false);
  assert.equal(s.rooms[0].archived, undefined);
  s = deleteRoom(s, id);
  assert.deepEqual([s.rooms, s.placed, s.order, s.touched[id]], [[], {}, [], undefined]);
  assert.equal(roomOf(s, "a"), GENERAL);
  // built-in and unknown rooms are not changed
  assert.equal(deleteRoom(s, GENERAL), s);
  assert.equal(archiveRoom(s, PLAYGROUND), s);
});

// localStorage as a tab sees it: `seen` is what the tab has heard of the
// shared values so far (another tab's writes reach it a while later)
function tabStore(box, seen = box) {
  return {
    get length() { return seen.size; },
    key: (i) => [...seen.keys()][i] ?? null,
    getItem: (k) => seen.get(k) ?? null,
    setItem: (k, v) => { box.set(k, String(v)); if (seen !== box) seen.set(k, String(v)); },
    removeItem: (k) => { box.delete(k); if (seen !== box) seen.delete(k); },
  };
}
const KEY = "sliqtly.rooms";

test("two tabs: opening a room in a tab that has not heard of the moves yet keeps them", () => {
  const box = new Map();
  const a = tabStore(box);
  let id = "";
  changeKept(a, KEY, (s) => {
    const made = createRoom(s, "Sliqtly", idOf);
    id = made.id;
    return made.state;
  });
  // tab B heard this much; then tab A moves two decks into the room
  const b = tabStore(box, new Map(box));
  changeKept(a, KEY, (s) => moveDeck(s, "a", id));
  changeKept(a, KEY, (s) => moveDeck(s, "b", id));
  // B opens the room: only that room's use is written
  const inB = changeKept(b, KEY, (s) => touchRoom(s, id, 5));
  assert.equal(inB.touched[id], 5);
  // a reload reads both
  const now = readKept(tabStore(box), KEY);
  assert.deepEqual(roomDecks(now, id, decks, samples).map((d) => d.id), ["a", "b"]);
  assert.equal(now.touched[id], 5);
  // a deck moved back in B, a room deleted in A: each only its own facts
  changeKept(b, KEY, (s) => moveDeck(s, "a", PLAYGROUND));
  assert.equal(roomOf(readKept(tabStore(box), KEY), "a"), PLAYGROUND);
  assert.equal(roomOf(readKept(tabStore(box), KEY), "b"), id);
  changeKept(a, KEY, (s) => deleteRoom(s, id));
  assert.deepEqual([...box.keys()], [KEY + "/deck/a"]);
});

test("the one value kept before is split into facts on the first change", () => {
  const box = new Map([[KEY, JSON.stringify({ rooms: [{ id: "r-x", title: "X", created: 1 }], placed: { a: "r-x" }, touched: { "r-x": 9 }, order: ["r-x"] })]]);
  const st = tabStore(box);
  assert.equal(roomOf(readKept(st, KEY), "a"), "r-x");
  changeKept(st, KEY, (s) => touchRoom(s, "r-x", 10));
  assert.equal(box.has(KEY), false);
  const now = readKept(st, KEY);
  assert.deepEqual(now.rooms.map((r) => r.id), ["r-x"]);
  assert.equal(roomOf(now, "a"), "r-x");
  assert.equal(now.touched["r-x"], 10);
  assert.deepEqual(now.order, ["r-x"]);
});

test("folders: one level in a room, by name; a deck filed there and back to the top", () => {
  let { state, id: room } = createRoom(emptyRooms(), "Sprint", idOf);
  let made = createFolder(state, room, "  Testing  ", idOf);
  assert.ok(made.id.startsWith("f-"));
  const testing = made.id;
  state = made.state;
  // a name the room has already, any case or accent, is that folder
  assert.equal(createFolder(state, room, "TESTING", idOf).id, testing);
  ({ state } = createFolder(state, room, "Sprint 10", idOf));
  ({ state } = createFolder(state, room, "Sprint 2", idOf));
  assert.deepEqual(foldersOf(state, room).map((f) => f.name), ["Sprint 2", "Sprint 10", "Testing"]);
  // General and Playground take folders too; Onboarding and unknown rooms do not
  assert.ok(createFolder(state, GENERAL, "Old", idOf).id);
  assert.equal(createFolder(state, ONBOARDING, "X", idOf).id, "");
  assert.equal(createFolder(state, "r-nope", "X", idOf).id, "");
  assert.equal(createFolder(state, room, "   ", idOf).id, "");

  state = moveDeck(state, "a", room, testing);
  assert.equal(folderOf(state, "a"), testing);
  assert.deepEqual(roomDecks(state, room, decks, samples).map((d) => [d.id, d.folder]), [["a", testing]]);
  // a folder of another room is the top
  const other = createFolder(state, GENERAL, "Elsewhere", idOf);
  state = moveDeck(other.state, "b", room, other.id);
  assert.equal(folderOf(state, "b"), "");
  // moved to another room, the deck leaves its folder
  state = moveDeck(state, "a", GENERAL);
  assert.equal(folderOf(state, "a"), "");
  assert.equal(state.filed.a, undefined);
});

test("a folder renamed (not to another's name) and deleted: its decks are at the room's top", () => {
  let { state, id: room } = createRoom(emptyRooms(), "Work", idOf);
  const a = createFolder(state, room, "Testing", idOf);
  const b = createFolder(a.state, room, "Drafts", idOf);
  state = moveDeck(b.state, "a", room, a.id);
  assert.equal(renameFolder(state, room, a.id, "drafts"), state);
  assert.equal(renameFolder(state, room, a.id, " "), state);
  state = renameFolder(state, room, a.id, "Tests");
  assert.deepEqual(foldersOf(state, room).map((f) => f.name), ["Drafts", "Tests"]);
  state = deleteFolder(state, room, a.id);
  assert.deepEqual(foldersOf(state, room).map((f) => f.name), ["Drafts"]);
  assert.equal(folderOf(state, "a"), "");
  assert.equal(roomOf(state, "a"), room);
  // a room deleted takes its folders, and its decks are at General's top
  state = moveDeck(state, "a", room, b.id);
  state = deleteRoom(state, room);
  assert.equal(state.folders[room], undefined);
  assert.equal(folderOf(state, "a"), "");
  assert.deepEqual(state.filed, {});
});

test("the panel: folders first by name with their counts, an open one's decks under it, then the top", () => {
  const rows = [
    { id: "a", name: "Budget", folder: "f-t" },
    { id: "b", name: "Kickoff", current: true, folder: "" },
    { id: "c", name: "Load test", folder: "f-t" },
    { id: "d", name: "Lost", folder: "f-gone" },
  ];
  const folders = [{ id: "f-t", name: "Testing" }, { id: "f-e", name: "Empty" }];
  const shut = deckLines(rows, { folders, addNew: "+ New", newFolder: "+ Folder" }).split("\n").map((l) => l.split("\t"));
  assert.deepEqual(shut, [
    ["f:f-e", "Empty", "", "f", ""],
    ["f:f-t", "Testing", "", "f", "2"],
    ["b", "Kickoff", "1", ""],
    ["d", "Lost", "", ""],
    ["new", "+ New", "", "n"],
    ["newfolder", "+ Folder", "", "nf"],
  ]);
  const open = deckLines(rows, { folders, open: ["f-t"] }).split("\n").map((l) => l.split("\t").slice(0, 4));
  assert.deepEqual(open.map((l) => [l[0], l[3]]), [["f:f-e", "f"], ["f:f-t", "f"], ["a", "i"], ["c", "i"], ["b", ""], ["d", ""]]);
  // Show all counts only the room's top
  const many = Array.from({ length: SHOWN }, (_, i) => ({ id: "x" + i, name: "X", folder: "f-t" })).concat([{ id: "y", name: "Y", folder: "" }]);
  assert.ok(!deckLines(many, { folders, showAll: "… all" }).includes("… all"));
});

test("folders and filed decks are kept as facts of their own, read back by another tab", () => {
  const store = tabStore(new Map());
  let room = "";
  let folder = "";
  changeKept(store, "k", (s) => {
    const r = createRoom(s, "Kept", idOf);
    room = r.id;
    const f = createFolder(r.state, room, "Testing", idOf);
    folder = f.id;
    return moveDeck(f.state, "a", room, folder);
  });
  assert.equal(store.getItem("k/filed/a"), folder);
  assert.deepEqual(JSON.parse(store.getItem("k/folders/" + room)), [{ id: folder, name: "Testing" }]);
  const back = readKept(store, "k");
  assert.equal(folderOf(back, "a"), folder);
  // deleting the folder removes both facts
  changeKept(store, "k", (s) => deleteFolder(s, room, folder));
  assert.equal(store.getItem("k/folders/" + room), null);
  assert.equal(store.getItem("k/filed/a"), null);
});
