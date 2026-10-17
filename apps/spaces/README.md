# Spaces

A demo app built on Sliqtly's technology: one shared space for a group, with
a summary of what happens **today, later this week and next week** on the
front page, a calendar, sheets, documents and an assistant.

The same app speaks to different groups through **vocabulary packs**: Demo,
Family, Business and Sports rename the space, its wards, events and deadlines
and load their own demo data. The page is in Finnish or English.

A **ward** is what the space looks after: a child, a pet, a car, a client, a
team. An event names its ward and its **responsibilities**: who looks after
it, and how ("Takes: Juho", "Picks up: Mia").

## What it does

- **Summary**: a wall calendar with three tabs, **Tänään** (below),
  **Viikko** (Monday to Sunday as columns, each plan with its time, today
  filled) and **Tulossa** (the next one-off plans, whose they are and how
  many days away). On top the clock, the date, the week number
  and the weather (Open-Meteo, Helsinki, read by the host page). Tänään has
  three columns: **today** as cards with each ward's colour, the plan going
  on or coming next open with its times, who looks after it and its notes;
  **this week** day by day with the plans as coloured chips (today framed,
  a day opens in the calendar) and **coming up**, the next one-off plans;
  **remember**, the deadlines and reminders to tick off (an entry's
  `status` becomes `done`, saved like any change), each with a Copy chip
  that puts its title and date on the clipboard, the Add button and the
  wards' colours.
- **Calendar**: EVGUI's event calendar card (month, week, day, agenda,
  per-ward views). A move, a resize or a new event is written back to the
  space; one day of a repeating event becomes its own event.
- **Sheets**: tables of text. A sheet with a date column (and optionally time,
  title and who) feeds the summary and the calendar. Import a CSV, or paste
  cells copied from a spreadsheet; download a sheet as CSV.
- **Documents**: notes, and the summaries and plans the assistant writes.
- **Assistant**: answers about today, this week, next week, overdue items, and
  drafts a plan for next week, from the space's own data.

Entry types come from `mcp-go/assets/warden-types.json`: fields common to
every entry, and for Family about fifty types (invitations, hobbies, school,
care, health, travel, bills, purchases, repairs, reminders, changes to the
routine…), each with its group (the Add menu's headings), what it requires,
the money fields that go to the budget and the dates that make reminders.

An entry may say when its thing is to be got (`get_from`, `get_by`; a
missing thing by its day, a need by `needed_by`): the calendar shows that
window as a span, and Remember while it is open. A cost paid in parts (a
contract, a monthly fee) shows its payment days, reminded of three days
before (`remind_days`). Both can be switched off on the calendar. Every
entry has a budget estimate (from, up to), what it actually cost and whether
it is in the family budget or someone's own; a person's budget is their own
costs and the family's.

Repeating events take a subset of iCalendar RRULE: DAILY, WEEKLY (with
BYDAY), MONTHLY, INTERVAL, UNTIL, COUNT and EXDATE.

## Run it

From the repository root, after `npm run setup`:

```sh
npm run spaces:build   # compile src/ and assemble apps/spaces/web/dist
npm run spaces:serve   # http://127.0.0.1:8090/
npm run spaces:check   # the model's checks and the page in Node
npm run spaces:check:page   # both built pages in Chrome
npm run warden:build   # the Warden page, apps/spaces/web/dist-warden
```

## Warden

Warden is the same app on a server: `npm run warden:build` builds it with
the title Warden and `<meta name="app-server" content="/apps/warden/">`, and
`scripts/build-view.mjs` copies it into the public site at `/apps/warden/`.
With the meta tag the page

- calls the server with `POST /apps/warden/api/<op>` and a JSON body
  (`list_spaces`, `get_space`, `create_space`, `set_space`, `save_event`,
  `add_ward`, `set_ward`, `remove_ward`, `invite_member`, `remove_member`, `import_events`), sending the
  sign-in cookie; a 401 sends it to `/apps/warden/login`;
- saves what the calendar changes: a moved event, a new one, and one day of a
  repeating event (the series gets an exdate, the day becomes its own event);
- has an **Assistant** that takes text or pictures (a school letter, a
  timetable, an invitation; a picture is made a JPEG of at most 1600 px
  first) and sends them to `assist`: the server's model makes the entries
  with Warden's own MCP tools (`mcp-go/wardenai.go`), and the page reads the
  space again;
- reads the space again when it changed elsewhere: every five seconds,
  while the page is in view, it asks `changes` (`{changed, mine}`, the
  stamp each write sets on the space); a change an assistant made through
  the MCP server, or another member's page, is shown once no form is open
  and nothing is left to save. The page names itself in `X-Warden-Client`,
  so its own changes are not news to it;
- keeps the tab in the address (`#calendar`): Back and Forward move between
  tabs, and a link or a reload opens the same one;
- has a **Members** view (members, invites; the owner invites by email and
  removes) and a **Spaces** view (the spaces you are in, and a new one);
- offers an empty space the pack's demo data: its wards, then its events
  (deadlines as important all-day events).
- shows an entry's **offer watch** on its form: what it looks for and its
  latest finds, each opening the offer in a new tab. A watch is set by the
  assistant or over MCP (`set_watch`, `list_watches`, `run_watch`); the
  server searches Tori.fi, Vinted, Huuto.net or the web for it once a day
  and makes a reminder of new finds (`mcp-go/wardenwatch.go`).

The app keeps the calls in an outbox (`outOp`, `outBody`); `web/main.js`
sends them one at a time and hands each reply to `answer` or `failed`.
Reading and writing the JSON is `src/SpacesRemote.rgr`; dates are the
space's own wall-clock days (`YYYY-MM-DD`, `HH:MM`) and repeats an RRULE.
Sheets and documents still live in the page only.

## How it is made

- `src/SpacesModel.rgr`: the space, wards and who looks after an event,
  repeating events (RRULE in and out), sheets (CSV and tab-separated
  parsing), the summary ranges, calendar write-back and what is left to save.
  No UI.
- `src/SpacesRemote.rgr`: a space on a server, as the JSON the server speaks.
- `src/SpacesVocab.rgr`: the vocabulary packs, the page's words in Finnish and
  English, and the demo data of each pack.
- `src/SpacesAssist.rgr`: the assistant's answers and documents as Markdown.
- `src/SpacesApp.rgr`: the page, one EVG tree built with EVGUI controls.
- `src/SpacesCheck.rgr`, `test/app-check.mjs`, `scripts/check-page.mjs`: the
  checks.
- `web/`: the host page (`main.js` paints with EVG's WebGL painter, mirrors
  the accessible tree into the DOM, edits text fields through EVG's
  text-input bridge and, for Warden, talks to the server) and the stylesheet.

The build reuses Sliqtly's pipeline (`scripts/lib.mjs`): the same Ranger
checkout and EVGUI, with `src/` linked in as `gallery/spaces`.

## Not yet

Without a server the data lives in the page and resets on reload; on one,
sheets and documents still do. Editing an event's details (title, ward, who)
in a form, deleting events, `.xlsx` files and editing documents in place
come next; the assistant's language model is Warden's only (a server's).
