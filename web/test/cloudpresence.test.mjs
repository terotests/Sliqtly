// node --test: who is working on the Stories, from Firestore (web/cloudpresence.js)
import test from "node:test";
import assert from "node:assert/strict";
import { agentRowOf, presenceAnswer, listenPresence, AGENT_WORKING_MS, AGENT_GONE_MS } from "../cloudpresence.js";
import { presenceByDeck, presenceSummary } from "../presence.js";

const now = 10_000_000;
const doc = (id, ago, more = {}) => ({ id, client: "Claude Code", tool: "get_presentation", calls: 3, first: now - 120_000, last: now - ago, slides: [], whole: false, note: "", ...more });

test("working, idle or gone from its last call", () => {
  assert.equal(agentRowOf(doc("a", 1000), now).state, "working");
  assert.equal(agentRowOf(doc("a", AGENT_WORKING_MS), now).state, "idle");
  assert.equal(agentRowOf(doc("a", AGENT_GONE_MS), now), null);
  // holding slides it works however long since its last call
  assert.equal(agentRowOf(doc("a", AGENT_WORKING_MS * 2, { slides: [2, 3] }), now).state, "working");
  assert.equal(agentRowOf(doc("a", AGENT_WORKING_MS * 2, { whole: true }), now).state, "working");
  assert.equal(agentRowOf({ name: "x" }, now), null);
  assert.equal(agentRowOf(null, now), null);
});

test("a row as the server's", () => {
  const r = agentRowOf(doc("a", 5000, { name: "Writer", slides: [3, 2], note: "charts" }), now);
  assert.deepEqual(r, { id: "a", name: "Writer", client: "Claude Code", state: "working", busy: false, tool: "get_presentation", calls: 3, since_s: 120, seen_s: 5, slides: [3, 2], note: "charts" });
  assert.equal(agentRowOf(doc("b", 0), now).name, "Claude Code", "no name of its own: its client's");
  assert.equal(agentRowOf(doc("c", 0, { client: "" }), now).name, "Assistant");
});

test("the answer feeds the Stories' lights", () => {
  const docs = new Map([["d1", [doc("a", 1000), doc("b", 60_000, { name: "Aino's bot" })]], ["d2", [doc("c", AGENT_GONE_MS + 1)]], ["d3", []]]);
  const ans = presenceAnswer(docs, now);
  assert.deepEqual(ans.decks.map((d) => d.deck_id), ["d1"]);
  assert.deepEqual(ans.decks[0].agents.map((a) => a.id), ["a", "b"]);
  assert.deepEqual(presenceSummary(presenceByDeck(ans).get("d1")), { agents: 2, working: 1, people: 0, light: "working" });
});

test("one listener a Story, each change handed on, another's Story quiet", () => {
  const subs = new Map();
  const db = {
    collection: (c) => ({ doc: (deck) => ({ collection: (sub) => ({
      onSnapshot: (next, fail) => {
        assert.equal(c + "/" + sub, "presence/agents");
        subs.set(deck, { next, fail });
        return () => subs.delete(deck);
      },
    }) }) }),
  };
  const seen = [];
  const errors = [];
  const stop = listenPresence(db, ["d1", "d2", "d1"], (docs) => seen.push(JSON.stringify([...docs])), (e) => errors.push(e));
  assert.equal(subs.size, 2);
  subs.get("d1").next({ docs: [{ data: () => doc("a", 0) }] });
  subs.get("d2").fail({ code: "permission-denied" });
  subs.get("d2").fail({ code: "unavailable" });
  assert.equal(seen.length, 1);
  assert.equal(errors.length, 1);
  stop();
  assert.equal(subs.size, 0);
});
