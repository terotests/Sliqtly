// SPDX-License-Identifier: AGPL-3.0-or-later

// A shared room's chat on sliqtly.com: what web/roomchat.js asks of a
// server of one's own, from the cloud instead. Writes go to the server
// (POST /editor/api/rooms/<op>, mcp-go/editorrooms.go), which numbers each
// message; the new ones are read from Firestore as they come
// (room_chat/cloud~<room>/msgs, mcp-go/fschat.go), which only the room's
// members may read (firestore.rules). Each one arrives as the `chat` event
// the folder server's stream sends, so RoomChat draws it the same way.
//
//   const call = cloudRoomsCall(fetch)
//   await call("read_room_chat", { room_id })
//   const stop = listenRoomChat(db, room, since, (v) => chat.event(v), onError)

export const CLOUD_TENANT = "cloud";

// POST /editor/api/rooms/<op>: the answer, or an Error with the server's
// words (and its code, e.g. "signed-out")
export function cloudRoomsCall(fetchFn = globalThis.fetch, base = "/editor/api/rooms/") {
  return async (op, args = {}) => {
    const res = await fetchFn(base + encodeURIComponent(op), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args || {}),
    });
    let out = {};
    try {
      out = await res.json();
    } catch (_) { /* no JSON: said below */ }
    if (!res.ok) {
      const e = new Error(out?.error || "The room could not be reached (" + res.status + ")");
      e.code = out?.code || "";
      e.status = res.status;
      throw e;
    }
    return out;
  };
}

// the room's chat document, by the same key as the server's
export const roomChatKey = (room) => CLOUD_TENANT + "~" + room;

// A message document as the event the stream would send; null when it is
// not one (a document half written, another shape).
export function chatEventOf(room, data) {
  if (!data || typeof data.msg !== "string") return null;
  let msg;
  try {
    msg = JSON.parse(data.msg);
  } catch (_) {
    return null;
  }
  if (!msg || typeof msg.id !== "string" || !Number.isFinite(Number(msg.seq))) return null;
  if (!msg.time && msg.at) msg.time = new Date(msg.at).toISOString();
  return { t: "msg", room, msg };
}

// Listens to the messages of `room` posted or changed after `since` (ms),
// with Firestore's compat API (db = firebase.firestore()); each is handed
// to onEvent as a `chat` event, the oldest change first. → stop()
export function listenRoomChat(db, room, since, onEvent, onError = () => {}) {
  const q = db
    .collection("room_chat")
    .doc(roomChatKey(room))
    .collection("msgs")
    .where("touched", ">", since)
    .orderBy("touched");
  return q.onSnapshot(
    (snap) => {
      for (const ch of snap.docChanges()) {
        if (ch.type === "removed") continue;
        const v = chatEventOf(room, ch.doc.data());
        if (v) onEvent(v);
      }
    },
    (e) => onError(e),
  );
}
