# Story as a shared agent workspace

Status: milestone 1 built (2026-10-14, Tero's ask in the project thread
"Story agent workspace"). This page maps the idea onto what Sliqtly already
has, says what milestone 1 adds and proposes the order of the rest.

## The idea

Today people share the result of their work with an assistant. In Sliqtly
they share the work itself: the Story (a presentation of a Topic) carries
the visible history it came from: people's prompts, the assistants'
answers, the files and searches used, the tool calls, the artifacts made,
comments and decisions. Anyone can **fork with context**: take the Story's
history up to a point, work on in a branch of their own with their own
assistant, and bring findings back.

Nothing here is an assistant's hidden reasoning. Only what was visibly
said and done is kept: that is the provenance.

```
Topic (room)
  └─ Story (presentation, shares/{id})
       ├─ session        the visible history         M1
       ├─ files          the deck's files            exists (Files tab)
       ├─ artifacts      deck, docs, diagrams        exists; provenance M3
       ├─ agents         who wrote each entry        M1 (names), M4 (identities)
       ├─ participants   room members and roles      exists (rooms)
       ├─ forks          branches with context       M1
       └─ reviews        findings, decisions, merge  M2
```

## What exists and is reused

| concept | already in Sliqtly | used for |
| --- | --- | --- |
| Topic, Story | rooms (ADR 0001) shown as topics; `story` field: type, phase, tags (`store/story.go`, `web/stories.js`) | the Story is a share document as before; a fork is a share too |
| append-only log with numbers | room chat: `store.ChatLog` (SQLite `chat_msgs`, memory, Firestore), seq per room, pages, gaps | the session is a ChatLog of its own per Story (`story-<deck>`), entries are messages with an `act` |
| who wrote it | `ChatFrom`: a person (browser's name) or a bot by its agent name (`roomchat.go chatSpeaker`) | each entry's author, person or agent |
| relations between things | `store.Links` + `LinkTypes` | a fork is the link `deck:<fork> forked_from deck:<parent>` |
| roles | `RoomPolicy`, room roles viewer/editor/owner | reading a session = reading the deck; writing = editor of its room |
| the deck's files | `files` on the share, Storage `shares/{id}/…` (Files tab) | the shared filesystem; a fork copies the public ones |
| two assistants on one deck | `begin_work` / `end_work`, `base_version` merges (`rgr/Work.rgr`) | unchanged; a fork is the other way to work side by side |
| review comments | `review/comments.json`, `src/PresReview.rgr`, MCP list/add/resolve_comment | M2: findings and decisions are review threads with a kind |
| versions | RangerDiff, `web/versions.js`, server `head`/`log` | M2: a proposed change is a version of a fork, merged by diff3 |
| page and assistant share operations | `roomService`: one op list for MCP tools and `POST /api/rooms/<op>` | session ops are added there, so the page and every MCP client do the same |

## Milestone 1: shared session + fork with context

Built on the folder server (Sliqtly Personal, own servers), where rooms,
folders and links already live. sliqtly.com's cloud rooms keep decks in
browsers for now (`cloudRoomOps`); the session follows when decks move
there.

**Model** (`mcp-go/store/session.go`, tests `session_test.go`, no UI):

- `SessionEntry`: n (place in the Story's timeline), seq (in the log it is
  kept in), at, by (`ChatFrom`), kind, text, tool, refs, origin (the Story
  it was written in, for inherited entries).
- Kinds: `prompt` (a person asked), `reply` (an assistant answered),
  `tool` (a tool call and its outcome), `search`, `file` (a file read or
  written), `artifact` (something made: a deck, a diagram), `decision`,
  `finding`, `note`, `fork` (written by the server when someone forks).
- Refs name what an entry used or made: `file:data/x.csv`, `deck:<id>`,
  `url:https://…`, `entry:<n>`.
- `Timeline`: a fork's history is its parent's first `at` entries
  (inherited, read through, never copied) followed by its own. A fork of a
  fork reads through both, at most 8 levels. So every branch has the same
  context up to its fork point and nothing is duplicated.
- `ForkPlan`: what a fork takes from its parent. A whitelist of share
  fields (name, md, theme, css, room, folder, story type and tags,
  visibility) and the deck's files except hidden ones (a path part starting
  with `.`: `.collab/`, `.versions/`, `.env`), anything that looks like a
  secret (`*.pem`, `*.key`, `*.p12`, `id_rsa*`, `credentials*`, `secrets/`,
  `*.env`) and `review/comments.json` (a fork reviews afresh; findings come
  back by merge in M2). Owner keys, edit keys, connectors, links and chat
  are never copied. The fork is a draft owned by whoever forked, in the
  parent's room and folder, as private as the parent. Its name is
  "<parent> · <fork name>" and the fork name is added to its story tags.

**Storage**: the session is the ChatLog room `story-<deck_id>` (SQLite
`chat_msgs` on the folder server), entries as messages with an `act`
(`ChatMsg.Act`: kind, tool, refs). The fork itself: a new share whose
`fork` field is `{from, at, name, by, made}`, the link `forked_from`, and a
`fork` entry in the parent's session ("Anna forked security-review here").

**Operations** (MCP tools and `POST /api/rooms/<op>`, `storysession.go`):

| op | what |
| --- | --- |
| `get_session` | a Story's timeline (inherited + own), pages by `after` / `limit`, with its fork point and forks |
| `log_session` | append entries (kind, text, tool, refs) as the agent or the page's person |
| `fork_story` | fork at entry `at` (default: the newest) with a `name`; returns the new deck_id |
| `list_forks` | the Story's forks: name, who, when, fork point, how many entries since |

Assistants' own deck tools (`update_presentation`, `write_workbook`,
`add_comment`, …) are logged as `tool` entries by the server itself, so
what an agent did shows even when it never calls `log_session`.

**UI** (minimal): the editor's right dock gets a **Session** panel (EVGUI
`ChatCtl`, as the deck chat and the AI drawer): the timeline with each
entry's kind, author and refs, inherited entries marked with the Story they
came from, the forks listed at the top, and the actions **Fork with
context** and **Note**. Opened from the Presentation menu.

## Later milestones (proposed order)

**M2 Review → merge.** A fork's findings, decisions and proposed changes
come back to the main Story. Findings and decisions are review threads
with a `kind` (`PresReview` already has threads, severity, resolved,
merge by id); a proposed change is the fork's deck diff against its fork
point (RangerDiff `merge3`, as `base_version` does). The owner sees per
fork "3 findings, 1 proposed change" and picks Accept / Reject / Discuss;
an accepted item is copied into the main Story with a `decision` entry
pointing at the fork entry (`entry:` ref). MCP: `create_review`,
`propose_change`, `get_decisions`, `comment`. The fork's deck content at
fork time (not "now") comes from version history here.

**M3 Artifact provenance.** "Where did this come from?" for a deck, a
slide, a file, a diagram: each `artifact`/`tool` entry already refs what it
made and used; M3 keeps a provenance record per file and slide
(`generated by`, `session`, `based on` refs, `modified by` from versions and
collab authors) and shows it in the Files tab and the slide's context menu.
MCP `get_artifact` / `create_artifact`.

**M4 Agent identities and permissions.** Agents as members of a Story
(personal, Topic-wide, organisation's), `@security review this Story`
in the room chat makes a review fork for that agent. Story roles
Owner / Editor / Reviewer / Viewer / Agent over the room's roles, and
capabilities (read files, modify files, run tools, secrets, network, fork,
publish) checked by the same Policy. Fork rules become configurable on top
of M1's fixed whitelist.

**M5 Cloud.** The same on sliqtly.com once its decks are in the store:
the session as a Firestore ChatLog (`fsChat` exists) and rules for it.

## Known limits of M1

- A fork copies the deck as it is when forked, not as it was at the
  chosen entry.
- Deleting a Story leaves its session entries in the log (unreadable,
  since reading needs the deck); a fork of a deleted Story keeps its own
  entries and loses the inherited ones. Removing a log with its deck
  belongs with M2's merge work.
- The folder server only.
- The editor names a deck by its first heading when it saves, so a fork
  opened in the editor shows the parent's title; the fork name stays in
  its tags, in `list_forks` and in the Session panel's buttons.
- Fork from an earlier entry (`at`) is MCP only; the panel forks at the
  newest entry.
