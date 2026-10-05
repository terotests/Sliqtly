// Rooms in this browser (ADR 0001), for a page with no rooms server: the
// rooms made here and which room each presentation is in. Everything starts
// in General; Playground is for trying things; Onboarding holds the sample
// decks. A server of one's own keeps its rooms itself (POST /api/rooms/<op>,
// mcp-go/roomsapi.go); this is the same shape for the decks kept here.
//
// state: { rooms: [{ id, title, created }], placed: { deckId: roomId } }

export const GENERAL = "general";
export const PLAYGROUND = "playground";
export const ONBOARDING = "onboarding";
const BUILT_IN = [GENERAL, PLAYGROUND, ONBOARDING];
// a room's presentations shown under it before "… Show all"
export const SHOWN = 5;

export function emptyRooms() {
  return { rooms: [], placed: {} };
}

// What localStorage held, or an empty state when it held nothing usable.
export function parseRooms(text) {
  try {
    const s = JSON.parse(text || "");
    const rooms = Array.isArray(s?.rooms) ? s.rooms.filter((r) => r && typeof r.id === "string" && typeof r.title === "string") : [];
    const placed = s?.placed && typeof s.placed === "object" ? { ...s.placed } : {};
    return { rooms, placed };
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

// The rooms in order: General, Playground, Onboarding, then the ones made
// here by name. decks: the deck rows (web/decklist.js); samples: [{ key, name }].
// The three built-in rooms keep their names in every language, as a
// channel's name does.
export function listRooms(state, decks, samples) {
  const count = {};
  for (const d of decks) {
    const r = roomOf(state, d.id);
    count[r] = (count[r] || 0) + 1;
  }
  const made = [...state.rooms].sort((a, b) => a.title.localeCompare(b.title));
  return [
    { room_id: GENERAL, title: "General", presentations: count[GENERAL] || 0 },
    { room_id: PLAYGROUND, title: "Playground", presentations: count[PLAYGROUND] || 0 },
    { room_id: ONBOARDING, title: "Onboarding", presentations: samples.length },
    ...made.map((r) => ({ room_id: r.id, title: r.title, presentations: count[r.id] || 0 })),
  ];
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

// A new room; its id from `idOf` (a fresh one each call). A title already
// used is still a room of its own, as the server's are.
export function createRoom(state, title, idOf) {
  const name = String(title || "").replace(/\s+/g, " ").trim().slice(0, 200);
  if (!name) return { state, id: "" };
  let id = "r-" + idOf();
  while (BUILT_IN.includes(id) || state.rooms.some((r) => r.id === id)) id = "r-" + idOf();
  return { state: { ...state, rooms: [...state.rooms, { id, title: name, created: Date.now() }] }, id };
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
// then "… Show all" when there are more, then the row that moves the open
// presentation here when it is elsewhere. "id TAB name TAB 1 if open TAB kind".
export function deckLines(rows, { showAll = "", moveHere = "", currentId = "" } = {}) {
  const clean = (s) => String(s || "").replace(/[\t\n\r]+/g, " ");
  const lines = rows.slice(0, SHOWN).map((r) => [r.id, clean(r.name), r.current ? "1" : "", ""].join("\t"));
  if (rows.length > SHOWN && showAll) lines.push(["all", showAll, "", "a"].join("\t"));
  if (moveHere && currentId && !rows.some((r) => r.id === currentId)) lines.push(["move", moveHere, "", "m"].join("\t"));
  return lines.join("\n");
}
