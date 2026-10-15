// SPDX-License-Identifier: AGPL-3.0-or-later

// Who is working on the Stories, on sliqtly.com: what a server of one's own
// answers to POST /api/rooms/presence (web/presence.js), read from
// Firestore instead, as it changes. The MCP server writes one document an
// assistant on a Story (presence/{deck}/agents/{agent},
// mcp-go/presencecloud.go) when it shows up, names itself or takes slides,
// and again every 30 s while it keeps calling; the Story's owner and the
// editors it invited read them (firestore.rules). Whether it is working,
// idle or gone is told here from its last call, with the times the server
// uses (mcp-go/presence.go agentWorking, agentGone). Nobody's open pages
// are told in the cloud: the answer has assistants only.
//
//   const stop = listenPresence(db, deckIds, (docs) => { … presenceAnswer(docs, Date.now()) … })

export const AGENT_WORKING_MS = 45_000;
export const AGENT_GONE_MS = 5 * 60_000;
// at most this many Stories are listened to at once
export const PRESENCE_LISTEN_MAX = 100;

// One document as the server's agent row; null when it is gone or not one.
export function agentRowOf(data, now) {
  if (!data || typeof data.id !== "string" || !data.id) return null;
  const last = Number(data.last);
  const first = Number(data.first);
  if (!Number.isFinite(last) || now - last >= AGENT_GONE_MS) return null;
  const slides = Array.isArray(data.slides) ? data.slides.map(Number).filter((n) => Number.isInteger(n) && n > 0) : [];
  const whole = data.whole === true;
  const client = typeof data.client === "string" ? data.client : "";
  const name = (typeof data.name === "string" && data.name) || client || "Assistant";
  const working = now - last < AGENT_WORKING_MS || slides.length > 0 || whole;
  const row = {
    id: data.id, name, client, state: working ? "working" : "idle", busy: false,
    tool: typeof data.tool === "string" ? data.tool : "", calls: Number(data.calls) || 0,
    since_s: Number.isFinite(first) ? Math.max(0, Math.floor((now - first) / 1000)) : 0,
    seen_s: Math.max(0, Math.floor((now - last) / 1000)),
  };
  if (slides.length) row.slides = slides;
  if (whole) row.whole = true;
  if (typeof data.note === "string" && data.note) row.note = data.note;
  return row;
}

// docs: deck id → [document data] → the server's answer ({ decks }), a
// Story nobody is on left out; working ones first, then by name, as the
// server sorts them
export function presenceAnswer(docs, now) {
  const decks = [];
  for (const [deck, list] of docs || []) {
    const agents = (list || []).map((d) => agentRowOf(d, now)).filter(Boolean);
    if (!agents.length) continue;
    agents.sort((a, b) => (a.state === b.state ? 0 : a.state === "working" ? -1 : 1) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0) || (a.id < b.id ? -1 : 1));
    decks.push({ deck_id: deck, agents, people: [] });
  }
  return { decks };
}

// Listens to the assistants on each of deckIds (Firestore's compat API, db
// = firebase.firestore()); onChange gets every Story's documents (deck id →
// [data]) each time one of them changes. A Story the reader may not read
// (another's) is left out quietly. → stop()
export function listenPresence(db, deckIds, onChange, onError = () => {}) {
  const docs = new Map();
  const stops = [];
  for (const deck of [...new Set(deckIds)].slice(0, PRESENCE_LISTEN_MAX)) {
    stops.push(db.collection("presence").doc(deck).collection("agents").onSnapshot(
      (snap) => {
        docs.set(deck, snap.docs.map((d) => d.data()));
        onChange(docs);
      },
      (e) => {
        docs.delete(deck);
        if (e?.code !== "permission-denied") onError(e);
      },
    ));
  }
  return () => {
    for (const s of stops) s();
  };
}
