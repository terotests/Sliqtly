// node --test: a room's presentations as stories (web/stories.js, web/rooms.js)
import test from "node:test";
import assert from "node:assert/strict";
import { STORY_TYPES, STORY_PHASES, STORY_TYPE_STYLE, TYPE_GROUP_ORDER, storyOf, changeStory, cleanTags, storyColor, sortStories, groupStories, agoOf, phaseCounts, groupByOf, storySortOf, NEW_STORY } from "../stories.js";
import { emptyRooms, readKept, changeKept, roomDecks, setStory, storyFor, forgetStory, GENERAL } from "../rooms.js";

test("a presentation with no story is the default: a story, active", () => {
  assert.deepEqual(storyOf(undefined), { type: "story", phase: "active", tags: [], color: "" });
  assert.deepEqual(storyOf({ type: "nope", phase: 3, tags: "x", color: "red" }), { type: "story", phase: "active", tags: [], color: "" });
  assert.deepEqual(storyOf({ type: "review", phase: "draft", tags: ["PDF"], color: "#AABBCC" }), { type: "review", phase: "draft", tags: ["PDF"], color: "#aabbcc" });
  assert.equal(NEW_STORY.phase, "draft");
});

test("every type has its colour in the one mapping, and its place in the group order", () => {
  for (const t of STORY_TYPES) assert.match(STORY_TYPE_STYLE[t].color, /^#[0-9a-f]{6}$/);
  assert.deepEqual([...TYPE_GROUP_ORDER].sort(), [...STORY_TYPES].sort());
  assert.equal(storyColor({ type: "task" }), STORY_TYPE_STYLE.task.color);
  assert.equal(storyColor({ type: "task", color: "#112233" }), "#112233");
});

test("a change keeps what is not said, and ignores what is not a type, phase or colour", () => {
  let s = changeStory(undefined, { type: "review" });
  assert.deepEqual(s, { type: "review", phase: "active", tags: [], color: "" });
  s = changeStory(s, { phase: "done", tags: "PDF, renderer ,pdf,, Q4" });
  assert.deepEqual(s, { type: "review", phase: "done", tags: ["PDF", "renderer", "Q4"], color: "" });
  assert.deepEqual(changeStory(s, { type: "folder", phase: "later", color: "blue" }), s);
  s = changeStory(s, { color: "#FF0000" });
  assert.equal(s.color, "#ff0000");
  assert.equal(changeStory(s, { color: "" }).color, "");
});

test("tags: one space between words, no repeats in any case or accent, at most 20 of 40 characters", () => {
  assert.deepEqual(cleanTags(["  Äö  x ", "ao X", "", 5, "b"]), ["Äö x", "b"]);
  assert.equal(cleanTags(Array.from({ length: 30 }, (_, i) => "t" + i)).length, 20);
  assert.equal(cleanTags(["x".repeat(60)])[0].length, 40);
});

const rows = [
  { id: "a", name: "PDF/X transparency review", type: "review", phase: "draft", tags: ["PDF", "Renderer"], created: 10, updated: 500 },
  { id: "b", name: "Black background in exported PDF", type: "problem", phase: "review", tags: ["pdf"], created: 20, updated: 300 },
  { id: "c", name: "CMYK renderer review", type: "review", phase: "done", tags: [], created: 30, updated: 900 },
  { id: "d", name: "Old deck", created: 5, updated: 100 },
];

test("grouped by type: the types that have stories, problems first, each in its colour, last changed first", () => {
  const g = groupStories(rows);
  assert.deepEqual(g.map((x) => [x.key, x.label, x.rows.map((r) => r.id)]), [["problem", "Problems", ["b"]], ["review", "Reviews", ["c", "a"]], ["story", "Stories", ["d"]]]);
  assert.equal(g[1].color, STORY_TYPE_STYLE.review.color);
  // a deck from before stories reads as the default story
  assert.equal(g[2].rows[0].phase, "active");
});

test("grouped by phase, by tag, or not at all; filtered by phase; sorted by title or newest", () => {
  assert.deepEqual(groupStories(rows, { by: "phase" }).map((x) => [x.label, x.rows.map((r) => r.id)]), [["Draft", ["a"]], ["Active", ["d"]], ["In review", ["b"]], ["Done", ["c"]]]);
  // a story under each of its tags; tags the same in any case are one
  assert.deepEqual(groupStories(rows, { by: "tag" }).map((x) => [x.label, x.rows.map((r) => r.id)]), [["PDF", ["a", "b"]], ["Renderer", ["a"]], ["No tags", ["c", "d"]]]);
  assert.deepEqual(groupStories(rows, { by: "none", sort: "title" }).map((x) => x.rows.map((r) => r.id)), [["b", "c", "d", "a"]]);
  assert.deepEqual(groupStories(rows, { by: "none", phases: ["draft", "review"] })[0].rows.map((r) => r.id), ["a", "b"]);
  assert.deepEqual(groupStories([], { by: "none" }), []);
  assert.deepEqual(sortStories(rows, "created").map((r) => r.id), ["c", "b", "a", "d"]);
  assert.equal(groupByOf("folder"), "type");
  assert.equal(storySortOf("x"), "updated");
});

test("how long ago, and how many in each phase", () => {
  const now = 1_000_000_000;
  assert.deepEqual(agoOf(now - 30_000, now), { n: 0, unit: "now" });
  assert.deepEqual(agoOf(now - 5 * 60_000, now), { n: 5, unit: "min" });
  assert.deepEqual(agoOf(now - 2 * 3600_000, now), { n: 2, unit: "h" });
  assert.deepEqual(agoOf(now - 3 * 86400_000, now), { n: 3, unit: "d" });
  assert.deepEqual(agoOf(now - 15 * 86400_000, now), { n: 2, unit: "w" });
  assert.deepEqual(agoOf(0, now), { n: 0, unit: "now" });
  assert.deepEqual(phaseCounts(rows), [{ phase: "draft", n: 1 }, { phase: "active", n: 1 }, { phase: "review", n: 1 }, { phase: "done", n: 1 }]);
});

// localStorage as a Map
function memStore() {
  const m = new Map();
  return {
    get length() { return m.size; },
    key: (i) => [...m.keys()][i] ?? null,
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
  };
}

test("a story is kept in the browser as a fact of its own, beside the deck's room, and samples have none", () => {
  const store = memStore();
  const decks = [{ id: "a", name: "A", updated: 2 }, { id: "b", name: "B", updated: 1 }];
  changeKept(store, "r", (s) => setStory(s, "a", { type: "review", tags: ["PDF"] }));
  assert.equal(store.getItem("r/story/a"), JSON.stringify({ type: "review", phase: "active", tags: ["PDF"], color: "" }));
  const kept = readKept(store, "r");
  assert.deepEqual(storyFor(kept, "a"), { type: "review", phase: "active", tags: ["PDF"], color: "" });
  assert.deepEqual(storyFor(kept, "b"), { type: "story", phase: "active", tags: [], color: "" });
  assert.deepEqual(roomDecks(kept, GENERAL, decks, []).map((d) => [d.id, d.type, d.phase]), [["b", "story", "active"], ["a", "review", "active"]]);
  assert.equal(setStory(emptyRooms(), "sample:welcome", { type: "demo" }).stories["sample:welcome"], undefined);
  changeKept(store, "r", (s) => forgetStory(s, "a"));
  assert.equal(store.getItem("r/story/a"), null);
});
