// Rooms in this browser (ADR 0001), for a page with no rooms server: the
// rooms made here and which room each presentation is in. Everything starts
// in General; Playground is for trying things; Onboarding holds the sample
// decks. A server of one's own keeps its rooms itself (POST /api/rooms/<op>,
// mcp-go/roomsapi.go); this is the same shape for the decks kept here.
//
// state: { rooms: [{ id, title, created, description, archived }],
//          placed: { deckId: roomId },
//          touched: { roomId: ms }  (when one last opened or used a room:
//          the panel lists the rooms one is active in, the rest by search),
//          order: [roomId],  (the made rooms as one dragged them; a room
//          not in it is newer than the order and comes first, newest first:
//          rooms are a running process, the latest work on top)
//          folders: { roomId: [{ id, name }] },  (one level of folders in a
//          room, e.g. its test decks out of the way; Onboarding has none)
//          filed: { deckId: folderId } }  (the folder a deck is in, within
//          its room; none, or a folder gone, is the room's top)
//
// The order is this browser's view of the rooms, the server's rooms too.

export const GENERAL = "general";
export const PLAYGROUND = "playground";
export const ONBOARDING = "onboarding";
const BUILT_IN = [GENERAL, PLAYGROUND, ONBOARDING];
// a room's presentations shown under it before "… Show all"
export const SHOWN = 15;
// the rooms one is active in: used in the last ACTIVE_DAYS, at most
// ACTIVE_MAX of them besides the built-in three
export const ACTIVE_DAYS = 30;
export const ACTIVE_MAX = 8;
const DAY = 86400000;

export function emptyRooms() {
  return { rooms: [], placed: {}, touched: {}, order: [], folders: {}, filed: {} };
}

export const isBuiltIn = (id) => BUILT_IN.includes(id);

// What localStorage held, or an empty state when it held nothing usable.
export function parseRooms(text) {
  // nothing kept is the usual first visit, not an error to throw and catch
  if (!text) return emptyRooms();
  try {
    const s = JSON.parse(text);
    const rooms = Array.isArray(s?.rooms) ? s.rooms.filter((r) => r && typeof r.id === "string" && typeof r.title === "string") : [];
    const placed = s?.placed && typeof s.placed === "object" ? { ...s.placed } : {};
    const touched = s?.touched && typeof s.touched === "object" ? { ...s.touched } : {};
    const order = Array.isArray(s?.order) ? s.order.filter((x) => typeof x === "string") : [];
    const folders = {};
    if (s?.folders && typeof s.folders === "object") for (const [r, list] of Object.entries(s.folders)) folders[r] = folderList(list);
    const filed = s?.filed && typeof s.filed === "object" ? { ...s.filed } : {};
    return { rooms, placed, touched, order, folders, filed };
  } catch (_) {
    return emptyRooms();
  }
}

// Kept in `store` (localStorage) one fact per key under `key` + "/": each
// room ("room/<id>", its JSON), each placed deck ("deck/<deckId>", its room),
// each room's last use ("touched/<id>"), the order ("order"), each room's
// folders ("folders/<roomId>") and each filed deck ("filed/<deckId>"). Every tab of
// the browser shares them, and a tab hears another's writes only a while
// later (later still when it is busy), so a tab writing all of the rooms as
// one value would put back what it last heard over moves made in another
// tab since. A change writes only the facts it changed: opening a room in one
// tab writes that room's use, and a deck moved in another tab stays moved.
// The one value of before (`key` itself) is read, split up on the first
// change, and removed.
function factsOf(state) {
  const out = new Map();
  for (const r of state.rooms) out.set("room/" + r.id, JSON.stringify(r));
  for (const [deck, room] of Object.entries(state.placed)) out.set("deck/" + deck, String(room));
  for (const [room, ms] of Object.entries(state.touched)) out.set("touched/" + room, String(ms));
  if (state.order?.length) out.set("order", JSON.stringify(state.order));
  for (const [room, list] of Object.entries(state.folders || {})) if (list.length) out.set("folders/" + room, JSON.stringify(list));
  for (const [deck, folder] of Object.entries(state.filed || {})) out.set("filed/" + deck, String(folder));
  return out;
}

// The rooms as `store` keeps them now.
export function readKept(store, key) {
  const s = parseRooms(store.getItem(key));
  const rooms = new Map(s.rooms.map((r) => [r.id, r]));
  const pre = key + "/";
  for (let i = 0; i < store.length; i++) {
    const k = store.key(i);
    if (!k || !k.startsWith(pre)) continue;
    const name = k.slice(pre.length);
    const v = store.getItem(k);
    if (v === null) continue;
    const slash = name.indexOf("/");
    const kind = slash < 0 ? name : name.slice(0, slash);
    const id = slash < 0 ? "" : name.slice(slash + 1);
    try {
      if (kind === "room") {
        const r = JSON.parse(v);
        if (r && r.id === id && typeof r.title === "string") rooms.set(id, r);
      } else if (kind === "deck" && id) s.placed[id] = v;
      else if (kind === "touched" && id && Number.isFinite(Number(v))) s.touched[id] = Number(v);
      else if (kind === "folders" && id) s.folders[id] = folderList(JSON.parse(v));
      else if (kind === "filed" && id) s.filed[id] = v;
      else if (kind === "order") {
        const o = JSON.parse(v);
        if (Array.isArray(o)) s.order = o.filter((x) => typeof x === "string");
      }
    } catch (_) { /* a fact that does not read is left out */ }
  }
  // as made: the order of the list (orderRooms) does not hang on it
  s.rooms = [...rooms.values()].sort((a, b) => (a.created || 0) - (b.created || 0));
  return s;
}

// A change to the rooms `store` keeps: fn gets them as kept now, and only
// the facts it changed are written (a room deleted takes its facts with it).
// → the state now
export function changeKept(store, key, fn) {
  const before = readKept(store, key);
  const next = fn(before);
  const legacy = store.getItem(key) !== null;
  const had = legacy ? new Map() : factsOf(before);
  const now = factsOf(next);
  for (const [k, v] of now) if (had.get(k) !== v) store.setItem(key + "/" + k, v);
  for (const k of factsOf(before).keys()) if (!now.has(k)) store.removeItem(key + "/" + k);
  if (legacy) store.removeItem(key);
  return next;
}

// The room a deck is in: the one it was moved to, if that room still
// exists, else General.
export function roomOf(state, deckId) {
  const r = state.placed[deckId];
  return r && (r === PLAYGROUND || state.rooms.some((x) => x.id === r)) ? r : GENERAL;
}

// Rows in the panel's order: the made rooms first, those not in `order`
// newest first, then those in `order` as dragged; the built-in rooms last,
// as they came, a group of their own (the work is in the made ones).
// rows: [{ room_id, created }] (listRooms's or the server's).
export function orderRooms(rows, order = []) {
  const at = new Map(order.map((id, i) => [id, i]));
  const fixed = rows.filter((r) => BUILT_IN.includes(r.room_id));
  const made = rows.filter((r) => !BUILT_IN.includes(r.room_id));
  const fresh = made.filter((r) => !at.has(r.room_id)).sort((a, b) => (b.created || 0) - (a.created || 0));
  const placed = made.filter((r) => at.has(r.room_id)).sort((a, b) => at.get(a.room_id) - at.get(b.room_id));
  return [...fresh, ...placed, ...fixed];
}

// The rooms in order: the ones made here, then General, Playground and
// Onboarding (orderRooms). decks: the deck rows (web/decklist.js); samples:
// [{ key, name }]. The three built-in rooms keep their names in every
// language, as a channel's name does. Archived rooms only when asked.
export function listRooms(state, decks, samples, { archived = false } = {}) {
  const count = {};
  for (const d of decks) {
    const r = roomOf(state, d.id);
    count[r] = (count[r] || 0) + 1;
  }
  const made = state.rooms.filter((r) => archived || !r.archived);
  return orderRooms([
    { room_id: GENERAL, title: "General", presentations: count[GENERAL] || 0 },
    { room_id: PLAYGROUND, title: "Playground", presentations: count[PLAYGROUND] || 0 },
    { room_id: ONBOARDING, title: "Onboarding", presentations: samples.length },
    ...made.map((r) => ({ room_id: r.id, title: r.title, description: r.description || "", created: r.created || 0, archived: !!r.archived, presentations: count[r.id] || 0 })),
  ], state.order);
}

// A room's presentations, last changed first: { id, name, current, folder }
// (folder "" at the room's top). Those of Onboarding are the samples,
// "sample:<key>".
export function roomDecks(state, roomId, decks, samples) {
  if (roomId === ONBOARDING) return samples.map((s) => ({ id: "sample:" + s.key, name: s.name, current: !!s.current }));
  return decks
    .filter((d) => roomOf(state, d.id) === roomId)
    .sort((a, b) => (b.updated || 0) - (a.updated || 0))
    .map((d) => ({ id: d.id, name: d.name, current: !!d.current, folder: folderOf(state, d.id) }));
}

export const roomTitle = (title) => String(title || "").replace(/\s+/g, " ").trim().slice(0, 200);
export const roomText = (text) => String(text || "").replace(/\r/g, "").trim().slice(0, 2000);

// A new room, first in the panel; its id from `idOf` (a fresh one each
// call). A title already used is still a room of its own, as the server's are.
// It stays out of `order`: a room not dragged yet comes before the dragged
// ones and the newest of those first (orderRooms), so a room made here or on
// the server is on top whatever rooms were made before there was an order.
export function createRoom(state, title, idOf, { description = "" } = {}) {
  const name = roomTitle(title);
  if (!name) return { state, id: "" };
  let id = "r-" + idOf();
  while (BUILT_IN.includes(id) || state.rooms.some((r) => r.id === id)) id = "r-" + idOf();
  const now = Date.now();
  // newer than every room here, also one made within the same millisecond
  const created = Math.max(now, ...state.rooms.map((r) => (r.created || 0) + 1));
  const room = { id, title: name, created };
  if (roomText(description)) room.description = roomText(description);
  return {
    state: { ...state, rooms: [...state.rooms, room], touched: { ...state.touched, [id]: now } },
    id,
  };
}

// A made room's name and description changed (an empty name keeps the old).
export function updateRoom(state, id, { title, description } = {}) {
  if (!state.rooms.some((r) => r.id === id)) return state;
  return {
    ...state,
    rooms: state.rooms.map((r) => {
      if (r.id !== id) return r;
      const next = { ...r };
      if (title !== undefined && roomTitle(title)) next.title = roomTitle(title);
      if (description !== undefined) {
        if (roomText(description)) next.description = roomText(description);
        else delete next.description;
      }
      return next;
    }),
  };
}

// Into the archive (on) or out of it: an archived room is out of the panel,
// found by search; its presentations stay in it.
export function archiveRoom(state, id, on = true) {
  if (!state.rooms.some((r) => r.id === id)) return state;
  return { ...state, rooms: state.rooms.map((r) => (r.id !== id ? r : on ? { ...r, archived: true } : (({ archived, ...rest }) => rest)(r))) };
}

// A made room removed; its presentations are in General again (none is
// deleted with it).
export function deleteRoom(state, id) {
  if (!state.rooms.some((r) => r.id === id)) return state;
  const placed = Object.fromEntries(Object.entries(state.placed).filter(([, r]) => r !== id));
  const touched = { ...state.touched };
  delete touched[id];
  // its folders go with it, and its decks are at General's top
  const folders = { ...(state.folders || {}) };
  delete folders[id];
  const filed = Object.fromEntries(Object.entries(state.filed || {}).filter(([deck]) => state.placed[deck] !== id));
  return { ...state, rooms: state.rooms.filter((r) => r.id !== id), placed, touched, order: (state.order || []).filter((x) => x !== id), folders, filed };
}

// A made room dragged before `beforeId`: "" or a built-in room (the group
// under the made ones) is after the last one. rows: the rooms as the panel orders them (listRooms's, the server's
// through orderRooms); the order kept is then all of the made ones.
export function moveRoom(state, rows, id, beforeId) {
  if (BUILT_IN.includes(id) || id === beforeId) return state;
  const ids = rows.map((r) => r.room_id).filter((x) => !BUILT_IN.includes(x));
  if (!ids.includes(id)) return state;
  const rest = ids.filter((x) => x !== id);
  let at = beforeId ? rest.indexOf(beforeId) : rest.length;
  // let go on a built-in room (the group under the made ones): the last
  if (BUILT_IN.includes(beforeId)) at = rest.length;
  if (at < 0) at = rest.length;
  rest.splice(at, 0, id);
  return { ...state, order: rest };
}

// A deck into a room (Onboarding holds only the samples), at its top or
// into one of its folders (`folderId`; one not in that room is the top).
export function moveDeck(state, deckId, roomId, folderId = "") {
  if (!deckId || !hasRoom(state, roomId)) return state;
  const placed = { ...state.placed };
  if (roomId === GENERAL) delete placed[deckId];
  else placed[deckId] = roomId;
  const filed = { ...(state.filed || {}) };
  if (folderId && foldersOf(state, roomId).some((f) => f.id === folderId)) filed[deckId] = folderId;
  else delete filed[deckId];
  return { ...state, placed, filed };
}

// a room decks can be in: General, Playground or one made here
const hasRoom = (state, roomId) => roomId === GENERAL || roomId === PLAYGROUND || state.rooms.some((r) => r.id === roomId);

// --- folders: one level of them in a room ---------------------------------
export const folderName = (name) => String(name || "").replace(/\s+/g, " ").trim().slice(0, 100);
const fold = (s) => String(s || "").normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();

// what a kept list of folders holds that reads as folders
function folderList(list) {
  return Array.isArray(list) ? list.filter((f) => f && typeof f.id === "string" && f.id && typeof f.name === "string").map((f) => ({ id: f.id, name: f.name })) : [];
}

// A room's folders by name (numbers in names as numbers: "Sprint 2"
// before "Sprint 10"). folders: [{ id, name }], this browser's or the
// server's (get_room's, folder_id as id).
export function sortFolders(folders) {
  return [...folders].sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" }) || a.id.localeCompare(b.id));
}

export function foldersOf(state, roomId) {
  return sortFolders(state.folders?.[roomId] || []);
}

// The folder a deck is in: "" at its room's top, also when the folder is
// gone or the deck moved to another room since.
export function folderOf(state, deckId) {
  const f = state.filed?.[deckId];
  return f && foldersOf(state, roomOf(state, deckId)).some((x) => x.id === f) ? f : "";
}

// A new folder in a room; a name the room has already (any case or
// accent) is that folder: { state, id }, id "" when nothing was made.
export function createFolder(state, roomId, name, idOf) {
  const n = folderName(name);
  if (!n || !hasRoom(state, roomId)) return { state, id: "" };
  const list = state.folders?.[roomId] || [];
  const same = list.find((f) => fold(f.name) === fold(n));
  if (same) return { state, id: same.id };
  let id = "f-" + idOf();
  while (list.some((f) => f.id === id)) id = "f-" + idOf();
  return { state: { ...state, folders: { ...(state.folders || {}), [roomId]: [...list, { id, name: n }] } }, id };
}

// A folder renamed (an empty name, or one another folder there has, keeps the old).
export function renameFolder(state, roomId, id, name) {
  const n = folderName(name);
  const list = state.folders?.[roomId] || [];
  if (!n || !list.some((f) => f.id === id) || list.some((f) => f.id !== id && fold(f.name) === fold(n))) return state;
  return { ...state, folders: { ...state.folders, [roomId]: list.map((f) => (f.id === id ? { ...f, name: n } : f)) } };
}

// A folder removed: its decks are at the room's top again (none is deleted).
export function deleteFolder(state, roomId, id) {
  const list = state.folders?.[roomId] || [];
  if (!list.some((f) => f.id === id)) return state;
  const folders = { ...state.folders };
  const rest = list.filter((f) => f.id !== id);
  if (rest.length) folders[roomId] = rest;
  else delete folders[roomId];
  const filed = Object.fromEntries(Object.entries(state.filed || {}).filter(([deck, f]) => !(f === id && roomOf(state, deck) === roomId)));
  return { ...state, folders, filed };
}

// The rows the panel draws under the open room: its folders by name, each
// open one followed by its presentations; then at most SHOWN presentations
// of the room's top, then "… Show all" when there are more, then the rows
// making a new presentation and a new folder in the room.
// "id TAB name TAB 1 if open TAB kind TAB count": kind "" a presentation at
// the top, "i" one in a folder, "f" a folder (id "f:<folder id>", count its
// presentations), "a" Show all, "n" new presentation, "nf" new folder.
// rows: roomDecks's (or the server's, with folder); folders: [{ id, name }];
// open: the ids of the folders shown open.
export function deckLines(rows, { folders = [], open = [], showAll = "", addNew = "", newFolder = "" } = {}) {
  const clean = (s) => String(s || "").replace(/[\t\n\r]+/g, " ");
  const known = new Set(folders.map((f) => f.id));
  const isOpen = new Set(open);
  const lines = [];
  for (const f of sortFolders(folders)) {
    const inside = rows.filter((r) => r.folder === f.id);
    lines.push(["f:" + f.id, clean(f.name), isOpen.has(f.id) ? "1" : "", "f", inside.length ? String(inside.length) : ""].join("\t"));
    if (isOpen.has(f.id)) for (const r of inside.slice(0, SHOWN)) lines.push([r.id, clean(r.name), r.current ? "1" : "", "i"].join("\t"));
  }
  const top = rows.filter((r) => !r.folder || !known.has(r.folder));
  for (const r of top.slice(0, SHOWN)) lines.push([r.id, clean(r.name), r.current ? "1" : "", ""].join("\t"));
  if (top.length > SHOWN && showAll) lines.push(["all", showAll, "", "a"].join("\t"));
  if (addNew) lines.push(["new", addNew, "", "n"].join("\t"));
  if (newFolder) lines.push(["newfolder", newFolder, "", "nf"].join("\t"));
  return lines.join("\n");
}

// A room opened or used now: it is one of the rooms one is active in.
export function touchRoom(state, roomId, now = Date.now()) {
  if (!roomId) return state;
  return { ...state, touched: { ...state.touched, [roomId]: now } };
}

// The rooms the panel lists: the built-in three, then those used in the last
// ACTIVE_DAYS, at most ACTIVE_MAX (the most recently used), in the order
// rows has them. rows: listRooms's (or the server's, through orderRooms).
// { shown, hidden }: hidden counts the rest, found by search.
export function activeRooms(rows, state, now = Date.now()) {
  // a new room counts as used when it was made: one someone else just made
  // shows up for everyone, not only for the one who made it
  const when = (r) => Math.max(state.touched?.[r.room_id] || 0, r.created || 0);
  const recent = new Set(rows
    .filter((r) => !BUILT_IN.includes(r.room_id) && now - when(r) <= ACTIVE_DAYS * DAY)
    .sort((a, b) => when(b) - when(a))
    .slice(0, ACTIVE_MAX)
    .map((r) => r.room_id));
  const shown = rows.filter((r) => BUILT_IN.includes(r.room_id) || recent.has(r.room_id));
  return { shown, hidden: rows.length - shown.length };
}

// Rooms whose name has every word of the query in it, any case and accent,
// at most `max`.
export function searchRooms(rows, query, max = 20) {
  const fold = (s) => String(s || "").normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (!words.length) return [];
  return rows.filter((r) => words.every((w) => fold(r.title).includes(w))).slice(0, max);
}

// --- shared rooms (sliqtly.com/editor) -----------------------------------------
// A room shared with other people is kept by the server (POST
// /editor/api/rooms, mcp-go/editorrooms.go) with its members and its chat;
// this browser keeps a copy of it among its rooms ({ shared: true, role }),
// so its presentations, folders and order are this browser's as for any
// other room (a presentation's own sharing stays its own).

// The rooms as the server lists them now: each one this browser keeps a copy
// of, its name, description and archive as the server has them; a shared room
// the server no longer lists (one was taken out of it, or it was deleted) is
// gone, its presentations in General again. rows: list_rooms' rooms.
export function syncShared(state, rows) {
  const listed = new Map((rows || []).filter((r) => r && typeof r.room_id === "string" && r.room_id && !BUILT_IN.includes(r.room_id)).map((r) => [r.room_id, r]));
  let next = state;
  for (const r of state.rooms) if (r.shared && !listed.has(r.id)) next = deleteRoom(next, r.id);
  const rooms = next.rooms.filter((r) => !listed.has(r.id));
  const had = new Map(next.rooms.map((r) => [r.id, r]));
  for (const [id, row] of listed) {
    const room = { ...(had.get(id) || {}), id, title: roomTitle(row.title) || "Room", created: Number(row.created) || had.get(id)?.created || 0, shared: true, role: String(row.role || "") };
    if (roomText(row.description)) room.description = roomText(row.description);
    else delete room.description;
    if (row.archived) room.archived = true;
    else delete room.archived;
    rooms.push(room);
  }
  rooms.sort((a, b) => (a.created || 0) - (b.created || 0));
  return { ...next, rooms };
}

// A room of this browser shared: the server made it under `sharedId`, and
// everything this browser had under its old id (presentations, folders, use,
// place in the order) is under the new one.
export function adoptRoom(state, localId, sharedId, role = "owner") {
  const old = state.rooms.find((r) => r.id === localId);
  if (!old || !sharedId || BUILT_IN.includes(localId)) return state;
  const swap = (id) => (id === localId ? sharedId : id);
  const placed = Object.fromEntries(Object.entries(state.placed).map(([d, r]) => [d, swap(r)]));
  const touched = Object.fromEntries(Object.entries(state.touched).map(([r, ms]) => [swap(r), ms]));
  const folders = Object.fromEntries(Object.entries(state.folders || {}).map(([r, l]) => [swap(r), l]));
  const rooms = state.rooms.filter((r) => r.id !== sharedId).map((r) => (r.id === localId ? { ...r, id: sharedId, shared: true, role } : r));
  return { ...state, rooms, placed, touched, folders, order: (state.order || []).map(swap) };
}

// The room's copy here is a shared one
export const isShared = (state, id) => state.rooms.some((r) => r.id === id && r.shared);
