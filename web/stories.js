// Stories: a room is a topic, and each of its presentations a story of it.
// What a story is (its type), where it is in its life (its phase), tags of
// one's own and a colour over its type's are the story's metadata, kept
// beside its room and folder (web/rooms.js `stories`, a server of one's own
// on the share: mcp-go/store/story.go), never in the deck's text. Grouping
// by type, phase or tag is only a view of the topic: a story is always
// directly in its topic.
//
// A story with nothing said is what every presentation was before stories:
// type "story", phase "active". A new one starts as a draft (NEW_STORY).

export const STORY_TYPES = ["review", "problem", "task", "pitch", "report", "lesson", "demo", "decision", "survey", "story"];
export const STORY_PHASES = ["draft", "active", "review", "done", "archived"];

// The one place a type's colour comes from: the card's edge and dot, the
// type's pill, a group's head. Accents only, never a card's background.
export const STORY_TYPE_STYLE = {
  review: { color: "#e35d4f" },
  problem: { color: "#e8862a" },
  task: { color: "#3b7ddd" },
  pitch: { color: "#8b5cf6" },
  report: { color: "#14998f" },
  lesson: { color: "#2f9e4f" },
  demo: { color: "#0ea5c6" },
  decision: { color: "#d4a514" },
  survey: { color: "#db2777" },
  story: { color: "#8a8f98" },
};

// The words the interface shows (through t(), web/i18n). The phase
// "review" is "In review", so it is not read as the type Review.
// i18n: "Review" "Problem" "Task" "Pitch" "Report" "Lesson" "Demo" "Decision" "Survey" "Story"
// i18n: "Reviews" "Problems" "Tasks" "Pitches" "Reports" "Lessons" "Demos" "Decisions" "Surveys" "Stories"
// i18n: "Draft" "Active" "In review" "Done" "Archived" "No tags"
export const TYPE_LABEL = { review: "Review", problem: "Problem", task: "Task", pitch: "Pitch", report: "Report", lesson: "Lesson", demo: "Demo", decision: "Decision", survey: "Survey", story: "Story" };
export const TYPE_GROUP_LABEL = { review: "Reviews", problem: "Problems", task: "Tasks", pitch: "Pitches", report: "Reports", lesson: "Lessons", demo: "Demos", decision: "Decisions", survey: "Surveys", story: "Stories" };
export const PHASE_LABEL = { draft: "Draft", active: "Active", review: "In review", done: "Done", archived: "Archived" };

// The groups' order when grouped by type: what needs work first.
export const TYPE_GROUP_ORDER = ["problem", "task", "review", "decision", "report", "lesson", "demo", "pitch", "survey", "story"];

export const GROUP_BYS = ["type", "phase", "tag", "none"];
export const STORY_SORTS = ["updated", "title", "created"];
export const groupByOf = (v) => (GROUP_BYS.includes(v) ? v : "type");
export const storySortOf = (v) => (STORY_SORTS.includes(v) ? v : "updated");

export const DEFAULT_STORY = Object.freeze({ type: "story", phase: "active", tags: [], color: "" });
export const NEW_STORY = Object.freeze({ type: "story", phase: "draft", tags: [], color: "" });

export const MAX_TAGS = 20;
export const MAX_TAG_LEN = 40;
const HEX = /^#[0-9a-f]{6}$/i;
const fold = (s) => String(s || "").normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();

// Tags as kept: one space between words, at most MAX_TAG_LEN characters,
// the empty ones and repeats (any case or accent) left out, at most
// MAX_TAGS. `tags` a list, or text with commas between them.
export function cleanTags(tags) {
  const list = Array.isArray(tags) ? tags : String(tags || "").split(",");
  const out = [];
  const seen = new Set();
  for (const t of list) {
    if (typeof t !== "string") continue;
    const s = [...t.replace(/\s+/g, " ").trim()].slice(0, MAX_TAG_LEN).join("").trim();
    if (!s || seen.has(fold(s))) continue;
    seen.add(fold(s));
    out.push(s);
    if (out.length === MAX_TAGS) break;
  }
  return out;
}

// A story as kept, what it does not say (or says wrong) the default's.
export function storyOf(raw) {
  const m = raw && typeof raw === "object" ? raw : {};
  return {
    type: STORY_TYPES.includes(m.type) ? m.type : DEFAULT_STORY.type,
    phase: STORY_PHASES.includes(m.phase) ? m.phase : DEFAULT_STORY.phase,
    tags: cleanTags(Array.isArray(m.tags) ? m.tags : []),
    color: typeof m.color === "string" && HEX.test(m.color) ? m.color.toLowerCase() : "",
  };
}

// The story with `patch` ({ type, phase, tags, color }, each optional): a
// type or phase not in the lists, or a colour that is not #rrggbb, leaves
// that part as it was; color "" goes back to the type's.
export function changeStory(story, patch = {}) {
  const s = storyOf(story);
  if (STORY_TYPES.includes(patch.type)) s.type = patch.type;
  if (STORY_PHASES.includes(patch.phase)) s.phase = patch.phase;
  if (patch.tags !== undefined) s.tags = cleanTags(patch.tags);
  if (patch.color === "") s.color = "";
  else if (typeof patch.color === "string" && HEX.test(patch.color)) s.color = patch.color.toLowerCase();
  return s;
}

// The story's colour: its own, else its type's.
export const storyColor = (story) => {
  const s = storyOf(story);
  return s.color || STORY_TYPE_STYLE[s.type].color;
};

// rows: [{ id, name, created, updated, ...story }] in a new array: last
// changed first ("updated"), by title, or newest first ("created"); ties
// by id, so an order never changes on its own.
export function sortStories(rows, by = "updated") {
  // the pinned ones (web/rooms.js setPinned) first, each part sorted
  if (rows.some((r) => r.pinned) && rows.some((r) => !r.pinned)) return [...sortStories(rows.filter((r) => r.pinned), by), ...sortStories(rows.filter((r) => !r.pinned), by)];
  const made = (r) => r.created || r.updated || 0;
  const changed = (r) => r.updated || made(r);
  const byId = (a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  const out = [...rows];
  if (by === "title") {
    const coll = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
    return out.sort((a, b) => coll.compare(String(a.name || ""), String(b.name || "")) || byId(a, b));
  }
  if (by === "created") return out.sort((a, b) => made(b) - made(a) || byId(a, b));
  return out.sort((a, b) => changed(b) - changed(a) || byId(a, b));
}

// The topic's stories in groups, each { key, label, color, rows }, rows
// sorted by `sort`. by "type": the types that have stories, in
// TYPE_GROUP_ORDER, each in its colour; "phase": the phases in their order;
// "tag": a group a tag (by name), a story in each of its tags' groups, then
// "No tags"; "none": one group of all (key "", no label). phases: only the
// stories in those phases ([] all). Labels are English, for t().
export function groupStories(rows, { by = "type", sort = "updated", phases = [] } = {}) {
  const want = new Set(phases.filter((p) => STORY_PHASES.includes(p)));
  const list = sortStories(rows.map((r) => ({ ...r, ...storyOf(r) })).filter((r) => !want.size || want.has(r.phase)), sort);
  if (by === "none") return list.length ? [{ key: "", label: "", color: "", rows: list }] : [];
  if (by === "phase") {
    return STORY_PHASES.map((p) => ({ key: p, label: PHASE_LABEL[p], color: "", rows: list.filter((r) => r.phase === p) })).filter((g) => g.rows.length);
  }
  if (by === "tag") {
    const names = new Map();
    for (const r of list) for (const t of r.tags) if (!names.has(fold(t))) names.set(fold(t), t);
    const coll = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
    const groups = [...names.entries()]
      .sort((a, b) => coll.compare(a[1], b[1]))
      .map(([k, name]) => ({ key: "tag:" + k, label: name, color: "", rows: list.filter((r) => r.tags.some((t) => fold(t) === k)) }));
    const bare = list.filter((r) => !r.tags.length);
    if (bare.length) groups.push({ key: "notag", label: "No tags", color: "", rows: bare });
    return groups;
  }
  return TYPE_GROUP_ORDER.map((ty) => ({ key: ty, label: TYPE_GROUP_LABEL[ty], color: STORY_TYPE_STYLE[ty].color, rows: list.filter((r) => r.type === ty) })).filter((g) => g.rows.length);
}

// How long ago `at` was from `now`, for "Updated 2 h ago": { n, unit }
// with unit "now", "min", "h", "d", "w", "mo" or "y".
export function agoOf(at, now = Date.now()) {
  const s = Math.max(0, Math.floor((now - (at || 0)) / 1000));
  if (!at || s < 60) return { n: 0, unit: "now" };
  const m = Math.floor(s / 60);
  if (m < 60) return { n: m, unit: "min" };
  const h = Math.floor(m / 60);
  if (h < 24) return { n: h, unit: "h" };
  const d = Math.floor(h / 24);
  if (d < 7) return { n: d, unit: "d" };
  if (d < 30) return { n: Math.floor(d / 7), unit: "w" };
  if (d < 365) return { n: Math.floor(d / 30), unit: "mo" };
  return { n: Math.floor(d / 365), unit: "y" };
}

// How many of the stories are in each phase, the phases that have some:
// "3 Draft · 4 In review" for a topic's line in the rail. → [{ phase, n }]
export function phaseCounts(rows) {
  const n = {};
  for (const r of rows) {
    const p = storyOf(r).phase;
    n[p] = (n[p] || 0) + 1;
  }
  return STORY_PHASES.filter((p) => n[p]).map((p) => ({ phase: p, n: n[p] }));
}

// A row of a topic's Actions, as what Apply does to each ticked story
// (PresToolbar.bulkRows): "mvf-<folder>" into that folder of the room,
// "mvf-top" out of its folder, "sty-<type>", "stp-<phase>". → { folder } or
// { part, value }, null for anything else.
export function bulkAction(choice) {
  const c = String(choice || "");
  if (c === "mvf-top") return { folder: "" };
  if (c.startsWith("mvf-") && c.length > 4 && c !== "mvf-new") return { folder: c.slice(4) };
  if (c.startsWith("sty-") && STORY_TYPES.includes(c.slice(4))) return { part: "type", value: c.slice(4) };
  if (c.startsWith("stp-") && STORY_PHASES.includes(c.slice(4))) return { part: "phase", value: c.slice(4) };
  return null;
}
