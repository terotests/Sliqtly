// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Who is working on the Stories now: the assistants calling the server's MCP
// tools on them and the people who have them open in the editor
// (mcp-go/presence.go, POST /api/rooms/presence). Nothing is asked of an
// assistant: it shows up as it works. No DOM here: main.js asks the server,
// this makes a Story card's light and counts (PresTopicView) and the rows of
// the window that names who is there (PresPanels' presentations window,
// list "who").
//
// The server's answer: { decks: [{ deck_id, agents: [{ id, name, client,
// state: "working" | "idle", busy, tool, calls, since_s, seen_s, slides,
// whole, note }], people: [{ id, name, color, pages, editing, edited_s }] }]
// }; a Story nobody is on is left out. A person is `editing` after Edit and
// until Save (src/PresEditMode.rgr), else only reading it; `edited_s` is how
// long since their last change (-1: none). A server that does not say counts
// everyone with it open as editing, as before.

// deck id → { agents, people }
export function presenceByDeck(answer) {
  const out = new Map();
  for (const d of answer?.decks || []) {
    if (!d?.deck_id) continue;
    out.set(d.deck_id, { agents: Array.isArray(d.agents) ? d.agents : [], people: Array.isArray(d.people) ? d.people : [] });
  }
  return out;
}

// How many are there and the light that says it: "working" while an
// assistant works, "here" while only idle assistants or people are, "" for
// nobody. `entries` one Story's { agents, people }, or several (a room's): an
// assistant or a person on several Stories is counted once.
export function presenceSummary(...entries) {
  const agents = new Map();
  const people = new Set();
  for (const e of entries) {
    for (const a of e?.agents || []) agents.set(a.id, agents.get(a.id) === "working" ? "working" : a.state);
    for (const p of e?.people || []) people.add(p.id);
  }
  const working = [...agents.values()].filter((s) => s === "working").length;
  const light = working > 0 ? "working" : agents.size + people.size > 0 ? "here" : "";
  return { agents: agents.size, working, people: people.size, light };
}

// "3 agents working · 1 person", "2 agents, 1 working", "1 agent idle", ""
// for nobody. t translates.
// i18n: "1 agent working" "{n} agents working" "{n} agents, {w} working" "1 agent idle" "{n} agents idle" "1 person" "{n} people"
export function countsText(s, t) {
  const parts = [];
  const n = (key, v, w = 0) => t(key).replace("{n}", String(v)).replace("{w}", String(w));
  if (s.agents > 0) {
    if (s.working === s.agents) parts.push(s.agents === 1 ? t("1 agent working") : n("{n} agents working", s.agents));
    else if (s.working > 0) parts.push(n("{n} agents, {w} working", s.agents, s.working));
    else parts.push(s.agents === 1 ? t("1 agent idle") : n("{n} agents idle", s.agents));
  }
  if (s.people > 0) parts.push(s.people === 1 ? t("1 person") : n("{n} people", s.people));
  return parts.join(" · ");
}

// What the card is handed: its light and its words, or null for nobody.
export function cardPresence(entry, t) {
  const s = presenceSummary(entry);
  return s.light ? { light: s.light, text: countsText(s, t) } : null;
}

// What the room's head is handed: always something, so it shows whether an
// assistant is working; "none" (a grey light) when nobody is.
// i18n: "No agents working"
export function headPresence(s, t) {
  if (s.agents > 0) return { light: s.light, text: countsText(s, t) };
  return { light: s.people > 0 ? "here" : "none", text: [t("No agents working"), countsText(s, t)].filter(Boolean).join(" · ") };
}

// "2–4, 7"
export function slidesText(slides) {
  const ns = [...new Set((slides || []).filter((n) => Number.isInteger(n) && n > 0))].sort((a, b) => a - b);
  const out = [];
  for (let i = 0; i < ns.length; i++) {
    let j = i;
    while (j + 1 < ns.length && ns[j + 1] === ns[j] + 1) j++;
    out.push(j > i ? `${ns[i]}–${ns[j]}` : String(ns[i]));
    i = j;
  }
  return out.join(", ");
}

// The tools' doings in words; another tool is named as it is.
// i18n: "editing slides" "editing" "reading the presentation" "claiming slides" "letting go of its slides" "commenting" "resolving a comment" "drawing a slide" "drawing the slides" "writing in the session" "reading the session" "forking the Story" "reading comments" "reading files" "making a presentation" "exporting"
const DOING = {
  update_presentation: "editing slides", edit_pptx: "editing", bind_chart_data: "editing", write_workbook: "editing",
  get_presentation: "reading the presentation", begin_work: "claiming slides", end_work: "letting go of its slides",
  add_comment: "commenting", resolve_comment: "resolving a comment", list_comments: "reading comments",
  render_slide: "drawing a slide", render_overview: "drawing the slides", render_strip: "drawing the slides",
  log_session: "writing in the session", get_session: "reading the session", fork_story: "forking the Story",
  list_files: "reading files", read_file: "reading files", create_presentation: "making a presentation", export_presentation: "exporting",
};
export function doingText(tool, t) {
  return DOING[tool] ? t(DOING[tool]) : String(tool || "");
}

// "45 s", "12 min", "3 h"
export function spanText(secs) {
  const s = Math.max(0, Math.round(Number(secs) || 0));
  if (s < 60) return s + " s";
  if (s < 3600) return Math.floor(s / 60) + " min";
  return Math.floor(s / 3600) + " h";
}

// The window's rows: one an assistant or a person on a Story. `names` deck
// id → the Story's name; `only` a deck id to list that Story alone (null:
// all of them). → [{ id, kind, deck, story, name, client, state, busy, doing,
// slides, whole, note, since, seen, pages, color }]
export function whoRows(byDeck, names, only = null) {
  const rows = [];
  for (const [deck, e] of byDeck) {
    if (only && deck !== only) continue;
    const story = names?.get?.(deck) || "";
    for (const a of e.agents || []) {
      rows.push({
        id: "agent:" + a.id + ":" + deck, kind: "agent", deck, story, name: a.name || "", client: a.client && a.client !== a.name ? a.client : "",
        state: a.state === "working" ? "working" : "idle", busy: !!a.busy, tool: a.tool || "", slides: a.slides || [], whole: !!a.whole, note: a.note || "",
        since: a.since_s || 0, seen: a.busy ? 0 : a.seen_s || 0, pages: 0, color: "",
      });
    }
    for (const p of e.people || []) {
      const editing = p.editing !== false;
      const ago = Number.isFinite(p.edited_s) ? p.edited_s : -1;
      rows.push({
        id: "person:" + p.id + ":" + deck, kind: "person", deck, story, name: p.name || "", client: "", state: editing ? "editing" : "viewing", busy: false, tool: "",
        slides: [], whole: false, note: "", since: -1, seen: ago >= 0 ? ago : 0, edited: ago, pages: p.pages || 1, color: p.color || "",
      });
    }
  }
  return rows;
}

export const WHO_SORTS = ["name", "since", "seen"];
export function firstWhoDir(by) {
  return by === "name" || by === "seen" ? "asc" : "desc";
}
export function nextWhoSort(cur, by) {
  if (!WHO_SORTS.includes(by)) return cur;
  if (cur && cur.by === by) return { by, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { by, dir: firstWhoDir(by) };
}

// The people editing first, then the working assistants, the people only
// reading, the idle assistants; within that as asked (name, how long here,
// how long since the last call or change).
export function sortWho(rows, by = "name", dir = firstWhoDir(by)) {
  const rank = (r) => (r.kind === "person" ? (r.state === "viewing" ? 2 : 0) : r.state === "working" ? 1 : 3);
  const name = (r) => String(r.name || "").toLocaleLowerCase();
  const sign = dir === "desc" ? -1 : 1;
  const key = by === "since" ? (a, b) => a.since - b.since : by === "seen" ? (a, b) => a.seen - b.seen : (a, b) => name(a).localeCompare(name(b));
  return rows.slice().sort((a, b) => rank(a) - rank(b) || sign * key(a, b) || name(a).localeCompare(name(b)) || a.id.localeCompare(b.id));
}

// The window's title: who is on one Story, or on the room's
// i18n: "Agents and people" "Who is on {story}"
export function whoTitle(t, story = "") {
  return story ? t("Who is on {story}").replace("{story}", story) : t("Agents and people");
}

// What the presentations window is sent (PresPanels decksDialog, list
// "who"): a row's name is who it is (an assistant's client beside its name),
// "where" what it is doing and on which slides and Story; "added" how long
// it has been on the Story, "modified" how long since its last call. A row
// opens its Story ("who:open:<id>"). t translates.
// i18n: "Agent" "Person" "working" "idle" "editing in the editor" "reading, not editing" "{n} pages" "slides {s}" "the whole presentation" "now" "{t} ago" "Here for" "Last call" "Name"
export function whoListJson(rows, { by = "name", dir, t, story = "", room = false, note = "" } = {}) {
  const key = WHO_SORTS.includes(by) ? by : "name";
  const way = dir === "asc" || dir === "desc" ? dir : firstWhoDir(key);
  return JSON.stringify({
    list: "who",
    title: whoTitle(t, story),
    head1: "since",
    head1Label: t("Here for"),
    head2: "seen",
    head2Label: t("Last call"),
    empty: t("Nobody is working here now. An assistant connected to this server's MCP shows up here as soon as it works on a presentation, and so does a person who opens it."),
    sort: key,
    dir: way,
    rows: sortWho(rows, key, way).map((r) => {
      const what = r.kind === "person"
        ? [t("Person"), r.state === "viewing" ? t("reading, not editing") : t("editing in the editor"), r.pages > 1 ? t("{n} pages").replace("{n}", String(r.pages)) : ""]
        : [
          t("Agent") + " " + t(r.state),
          r.busy || r.state === "working" ? doingText(r.tool, t) : "",
          r.whole ? t("the whole presentation") : r.slides.length ? t("slides {s}").replace("{s}", slidesText(r.slides)) : "",
          r.note,
        ];
      if (room && r.story) what.push(r.story);
      return {
        id: r.id,
        name: r.client ? `${r.name} (${r.client})` : r.name,
        where: what.filter(Boolean).join(" · "),
        added: r.since >= 0 ? spanText(r.since) : "",
        modified: r.kind === "person"
          ? (r.edited >= 0 ? (r.edited < 10 ? t("now") : t("{t} ago").replace("{t}", spanText(r.edited))) : "")
          : r.busy ? t("now") : t("{t} ago").replace("{t}", spanText(r.seen)),
        light: r.kind === "person" ? "person" : r.state,
        color: r.color,
        canDelete: false,
        current: false,
      };
    }),
    note,
  });
}

// The window's note: how many on how many Stories, and where they come from.
// i18n: "{who} on {n} presentations." "Assistants show up as they call this server's MCP tools; nothing needs to be set up for it."
export function whoNote(byDeck, t, only = null) {
  const entries = [...byDeck].filter(([d]) => !only || d === only).map(([, e]) => e);
  const s = presenceSummary(...entries);
  const who = countsText(s, t);
  const lead = !who ? "" : only || entries.length === 1 ? who + "." : t("{who} on {n} presentations.").replace("{who}", who).replace("{n}", String(entries.length));
  return [lead, t("Assistants show up as they call this server's MCP tools; nothing needs to be set up for it.")].filter(Boolean).join(" ");
}

// The cards would show the same as before: each Story's light and counts
// (the times in the window change on every answer, the cards do not)
export function samePresence(a, b) {
  const key = (m) => JSON.stringify([...(m || new Map())].map(([d, e]) => [d, presenceSummary(e)]).sort((x, y) => (x[0] < y[0] ? -1 : x[0] > y[0] ? 1 : 0)));
  return key(a) === key(b);
}
