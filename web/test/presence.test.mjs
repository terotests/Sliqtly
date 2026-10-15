// node --test: who is working on the Stories (web/presence.js)
import test from "node:test";
import assert from "node:assert/strict";
import {
  presenceByDeck, presenceSummary, countsText, cardPresence, headPresence, slidesText, doingText, spanText,
  whoRows, sortWho, nextWhoSort, whoListJson, whoNote, whoTitle, samePresence,
} from "../presence.js";

const t = (s) => s;
const agent = (id, state, more = {}) => ({ id, name: "Agent " + id, client: "Claude Code", state, busy: false, tool: "get_presentation", calls: 1, since_s: 30, seen_s: 5, ...more });
const answer = {
  decks: [
    { deck_id: "d1", agents: [agent("a", "working", { busy: true, tool: "update_presentation", slides: [2, 3, 4, 7] }), agent("b", "idle", { seen_s: 90 })], people: [{ id: "p1", name: "Tero", color: "#ea580c", pages: 2 }] },
    { deck_id: "d2", agents: [agent("a", "idle"), agent("c", "working", { whole: true, name: "Reviewer", client: "Reviewer" })], people: [] },
    { agents: [] },
  ],
};

test("the answer by Story; one without a deck id is left out", () => {
  const m = presenceByDeck(answer);
  assert.deepEqual([...m.keys()], ["d1", "d2"]);
  assert.equal(presenceByDeck(null).size, 0);
  assert.deepEqual(presenceByDeck({ decks: [{ deck_id: "x" }] }).get("x"), { agents: [], people: [] });
});

test("a Story's light and counts", () => {
  const m = presenceByDeck(answer);
  assert.deepEqual(presenceSummary(m.get("d1")), { agents: 2, working: 1, people: 1, light: "working" });
  assert.equal(countsText(presenceSummary(m.get("d1")), t), "2 agents, 1 working · 1 person");
  assert.deepEqual(cardPresence(m.get("d2"), t), { light: "working", text: "2 agents, 1 working" });
  assert.equal(cardPresence({ agents: [], people: [] }, t), null);
  assert.deepEqual(cardPresence({ agents: [agent("x", "idle")], people: [] }, t), { light: "here", text: "1 agent idle" });
  assert.deepEqual(cardPresence({ agents: [], people: [{ id: "p" }, { id: "q" }] }, t), { light: "here", text: "2 people" });
  assert.equal(countsText({ agents: 3, working: 3, people: 0 }, t), "3 agents working");
  assert.equal(countsText({ agents: 1, working: 1, people: 1 }, t), "1 agent working · 1 person");
  assert.equal(countsText({ agents: 4, working: 0, people: 0 }, t), "4 agents idle");
});

test("a room counts each one once, working where it works anywhere", () => {
  const m = presenceByDeck(answer);
  assert.deepEqual(presenceSummary(...m.values()), { agents: 3, working: 2, people: 1, light: "working" });
});

test("slides, doings and spans in words", () => {
  assert.equal(slidesText([7, 2, 3, 4, 4]), "2–4, 7");
  assert.equal(slidesText([]), "");
  assert.equal(doingText("update_presentation", t), "editing slides");
  assert.equal(doingText("search_figma", t), "search_figma");
  assert.equal(spanText(42), "42 s");
  assert.equal(spanText(125), "2 min");
  assert.equal(spanText(7300), "2 h");
});

test("the window's rows: one Story, or the room with each Story named", () => {
  const m = presenceByDeck(answer);
  const names = new Map([["d1", "Budget"], ["d2", "Plan"]]);
  assert.equal(whoRows(m, names, "d1").length, 3);
  const all = whoRows(m, names);
  assert.equal(all.length, 5);
  const a = all.find((r) => r.id === "agent:a:d1");
  assert.equal(a.story, "Budget");
  assert.equal(a.seen, 0, "a call runs: no time since the last one");
  // a client of the same name as the agent is not said twice
  assert.equal(all.find((r) => r.id === "agent:c:d2").client, "");
  // people first, then working, then idle
  assert.deepEqual(sortWho(all).map((r) => r.id), ["person:p1:d1", "agent:a:d1", "agent:c:d2", "agent:a:d2", "agent:b:d1"]);
  assert.deepEqual(nextWhoSort({ by: "name", dir: "asc" }, "name"), { by: "name", dir: "desc" });
  assert.deepEqual(nextWhoSort({ by: "name", dir: "asc" }, "since"), { by: "since", dir: "desc" });
  assert.deepEqual(nextWhoSort({ by: "name", dir: "asc" }, "bogus"), { by: "name", dir: "asc" });
});

test("what the window is sent", () => {
  const m = presenceByDeck(answer);
  const names = new Map([["d1", "Budget"], ["d2", "Plan"]]);
  const j = JSON.parse(whoListJson(whoRows(m, names), { t, room: true, note: whoNote(m, t) }));
  assert.equal(j.list, "who");
  assert.equal(j.title, "Agents and people");
  assert.equal(j.rows[0].name, "Tero");
  assert.equal(j.rows[0].where, "Person · editing in the editor · 2 pages · Budget");
  assert.equal(j.rows[0].light, "person");
  assert.equal(j.rows[1].name, "Agent a (Claude Code)");
  assert.equal(j.rows[1].where, "Agent working · editing slides · slides 2–4, 7 · Budget");
  assert.equal(j.rows[1].modified, "now");
  assert.equal(j.rows[2].where, "Agent working · reading the presentation · the whole presentation · Plan");
  assert.equal(j.rows.at(-1).where, "Agent idle · Budget");
  assert.equal(j.rows.at(-1).modified, "1 min ago");
  assert.ok(j.rows.every((r) => r.canDelete === false));
  assert.match(j.note, /^3 agents, 2 working · 1 person on 2 presentations\. Assistants show up/);
  // one Story: its name in the title, not on the rows
  const one = JSON.parse(whoListJson(whoRows(m, names, "d2"), { t, story: "Plan" }));
  assert.equal(one.title, whoTitle(t, "Plan"));
  assert.equal(one.title, "Who is on Plan");
  assert.ok(!one.rows.some((r) => r.where.endsWith("Plan")));
});

test("the cards change only when a light or a count does", () => {
  const a = presenceByDeck(answer);
  const later = presenceByDeck(JSON.parse(JSON.stringify(answer).replaceAll('"seen_s":5', '"seen_s":10')));
  assert.ok(samePresence(a, later));
  const gone = presenceByDeck({ decks: answer.decks.slice(0, 1) });
  assert.ok(!samePresence(a, gone));
  assert.ok(samePresence(new Map(), null));
});

test("the room's head always says whether an assistant works", () => {
  assert.deepEqual(headPresence({ agents: 0, working: 0, people: 0, light: "" }, t), { light: "none", text: "No agents working" });
  assert.deepEqual(headPresence({ agents: 0, working: 0, people: 1, light: "here" }, t), { light: "here", text: "No agents working · 1 person" });
  assert.deepEqual(headPresence({ agents: 2, working: 2, people: 0, light: "working" }, t), { light: "working", text: "2 agents working" });
});

test("people editing come first, people only reading after the working assistants", () => {
  const m = presenceByDeck({
    decks: [{
      deck_id: "d1",
      agents: [agent("w", "working"), agent("i", "idle")],
      people: [
        { id: "r", name: "Aino", pages: 1, editing: false, edited_s: -1 },
        { id: "e", name: "Ville", pages: 1, editing: true, edited_s: 4 },
        { id: "o", name: "Old server", pages: 1 },
      ],
    }],
  });
  const rows = whoRows(m, new Map([["d1", "Budget"]]));
  assert.deepEqual(sortWho(rows).map((r) => r.id), ["person:o:d1", "person:e:d1", "agent:w:d1", "person:r:d1", "agent:i:d1"]);
  const j = JSON.parse(whoListJson(rows, { t }));
  const by = (id) => j.rows.find((r) => r.id === id);
  assert.equal(by("person:e:d1").where, "Person · editing in the editor");
  assert.equal(by("person:e:d1").modified, "now", "a change within seconds");
  assert.equal(by("person:r:d1").where, "Person · reading, not editing");
  assert.equal(by("person:r:d1").modified, "");
  assert.equal(by("person:o:d1").where, "Person · editing in the editor", "a server that does not say: as before");
});

test("where nobody's open pages are told, the empty window says assistants only", () => {
  const j = JSON.parse(whoListJson([], { t, people: false }));
  assert.equal(j.empty, "Nobody is working here now. An assistant connected to Sliqtly's MCP shows up here as soon as it works on a presentation.");
  assert.match(JSON.parse(whoListJson([], { t })).empty, /and so does a person/);
});
