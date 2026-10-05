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
//          order: [roomId] }  (the made rooms as one dragged them; a room
//          not in it is newer than the order and comes first, newest first:
//          rooms are a running process, the latest work on top)
//
// The order is this browser's view of the rooms, the server's rooms too.

export const GENERAL = "general";
export const PLAYGROUND = "playground";
export const ONBOARDING = "onboarding";
const BUILT_IN = [GENERAL, PLAYGROUND, ONBOARDING];
// a room's presentations shown under it before "… Show all"
export const SHOWN = 5;
// the rooms one is active in: used in the last ACTIVE_DAYS, at most
// ACTIVE_MAX of them besides the built-in three
export const ACTIVE_DAYS = 30;
export const ACTIVE_MAX = 8;
const DAY = 86400000;

export function emptyRooms() {
  return { rooms: [], placed: {}, touched: {}, order: [] };
}

export const isBuiltIn = (id) => BUILT_IN.includes(id);

// What localStorage held, or an empty state when it held nothing usable.
export function parseRooms(text) {
  try {
    const s = JSON.parse(text || "");
    const rooms = Array.isArray(s?.rooms) ? s.rooms.filter((r) => r && typeof r.id === "string" && typeof r.title === "string") : [];
    const placed = s?.placed && typeof s.placed === "object" ? { ...s.placed } : {};
    const touched = s?.touched && typeof s.touched === "object" ? { ...s.touched } : {};
    const order = Array.isArray(s?.order) ? s.order.filter((x) => typeof x === "string") : [];
    return { rooms, placed, touched, order };
  } catch (_) {
    return emptyRooms();
  }
}

// The room a deck is in: the one it was moved to, if that room still
// exists, else General.
export function roomOf(state, deckId) {
  const r = state.placed[deckId];
  return r && (r === PLAYGROUND || state.rooms.some((x) => x.id === r)) ? r : GENERAL;
}

// Rows in the panel's order: the built-in rooms first as they came, then
// the made ones not in `order` newest first, then those in `order` as
// dragged. rows: [{ room_id, created }] (listRooms's or the server's).
export function orderRooms(rows, order = []) {
  const at = new Map(order.map((id, i) => [id, i]));
  const fixed = rows.filter((r) => BUILT_IN.includes(r.room_id));
  const made = rows.filter((r) => !BUILT_IN.includes(r.room_id));
  const fresh = made.filter((r) => !at.has(r.room_id)).sort((a, b) => (b.created || 0) - (a.created || 0));
  const placed = made.filter((r) => at.has(r.room_id)).sort((a, b) => at.get(a.room_id) - at.get(b.room_id));
  return [...fixed, ...fresh, ...placed];
}

// The rooms in order: General, Playground, Onboarding, then the ones made
// here (orderRooms). decks: the deck rows (web/decklist.js); samples:
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

// A room's presentations, last changed first: { id, name, current }. Those
// of Onboarding are the samples, "sample:<key>".
export function roomDecks(state, roomId, decks, samples) {
  if (roomId === ONBOARDING) return samples.map((s) => ({ id: "sample:" + s.key, name: s.name, current: !!s.current }));
  return decks
    .filter((d) => roomOf(state, d.id) === roomId)
    .sort((a, b) => (b.updated || 0) - (a.updated || 0))
    .map((d) => ({ id: d.id, name: d.name, current: !!d.current }));
}

export const roomTitle = (title) => String(title || "").replace(/\s+/g, " ").trim().slice(0, 200);
export const roomText = (text) => String(text || "").replace(/\r/g, "").trim().slice(0, 2000);

// A new room, first in the panel; its id from `idOf` (a fresh one each
// call). A title already used is still a room of its own, as the server's are.
export function createRoom(state, title, idOf, { description = "" } = {}) {
  const name = roomTitle(title);
  if (!name) return { state, id: "" };
  let id = "r-" + idOf();
  while (BUILT_IN.includes(id) || state.rooms.some((r) => r.id === id)) id = "r-" + idOf();
  const now = Date.now();
  const room = { id, title: name, created: now };
  if (roomText(description)) room.description = roomText(description);
  return {
    state: { ...state, rooms: [...state.rooms, room], touched: { ...state.touched, [id]: now }, order: [id, ...(state.order || []).filter((x) => x !== id)] },
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
  return { ...state, rooms: state.rooms.filter((r) => r.id !== id), placed, touched, order: (state.order || []).filter((x) => x !== id) };
}

// A made room dragged before `beforeId`: "" is after the last one, a
// built-in room (always on top) the first place. rows: the rooms as the panel orders them (listRooms's, the server's
// through orderRooms); the order kept is then all of the made ones.
export function moveRoom(state, rows, id, beforeId) {
  if (BUILT_IN.includes(id) || id === beforeId) return state;
  const ids = rows.map((r) => r.room_id).filter((x) => !BUILT_IN.includes(x));
  if (!ids.includes(id)) return state;
  const rest = ids.filter((x) => x !== id);
  let at = beforeId ? rest.indexOf(beforeId) : rest.length;
  if (BUILT_IN.includes(beforeId)) at = 0;
  if (at < 0) at = rest.length;
  rest.splice(at, 0, id);
  return { ...state, order: rest };
}

// A deck into a room (Onboarding holds only the samples).
export function moveDeck(state, deckId, roomId) {
  if (!deckId || roomId === ONBOARDING) return state;
  if (roomId !== GENERAL && roomId !== PLAYGROUND && !state.rooms.some((r) => r.id === roomId)) return state;
  const placed = { ...state.placed };
  if (roomId === GENERAL) delete placed[deckId];
  else placed[deckId] = roomId;
  return { ...state, placed };
}

// The rows the panel draws under the open room: at most SHOWN presentations,
// then "… Show all" when there are more, then the row making a new
// presentation in the room. "id TAB name TAB 1 if open TAB kind".
export function deckLines(rows, { showAll = "", addNew = "" } = {}) {
  const clean = (s) => String(s || "").replace(/[\t\n\r]+/g, " ");
  const lines = rows.slice(0, SHOWN).map((r) => [r.id, clean(r.name), r.current ? "1" : "", ""].join("\t"));
  if (rows.length > SHOWN && showAll) lines.push(["all", showAll, "", "a"].join("\t"));
  if (addNew) lines.push(["new", addNew, "", "n"].join("\t"));
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
  const when = (r) => state.touched?.[r.room_id] || 0;
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
