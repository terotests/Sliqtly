# Spaces

A demo app built on Sliqtly's technology: one shared space for a group, with
a summary of what happens **today, later this week and next week** on the
front page, a calendar, sheets, documents and an assistant.

The same app speaks to different groups through **vocabulary packs**: Demo,
Family, Business and Sports rename the space, its resources, events and
deadlines and load their own demo data. The page is in Finnish or English.

## What it does

- **Summary**: three columns (today, the rest of this week, next week) with
  events, repeating events and dated rows from sheets; overdue deadlines; the
  sheets; how busy each resource is this week and next.
- **Calendar**: EVGUI's event calendar card (month, week, day, agenda,
  resource views). A move, a resize or a new event is written back to the
  space; one day of a repeating event becomes its own event.
- **Sheets**: tables of text. A sheet with a date column (and optionally time,
  title and who) feeds the summary and the calendar. Import a CSV, or paste
  cells copied from a spreadsheet; download a sheet as CSV.
- **Documents**: notes, and the summaries and plans the assistant writes.
- **Assistant**: answers about today, this week, next week, overdue items, and
  drafts a plan for next week, from the space's own data.

Repeating events take a subset of iCalendar RRULE: DAILY, WEEKLY (with
BYDAY), MONTHLY, INTERVAL, UNTIL, COUNT and EXDATE.

## Run it

From the repository root, after `npm run setup`:

```sh
npm run spaces:build   # compile src/ and assemble apps/spaces/web/dist
npm run spaces:serve   # http://127.0.0.1:8090/
npm run spaces:check   # the model's checks and the page in Node
npm run spaces:check:page   # the built page in Chrome
```

## How it is made

- `src/SpacesModel.rgr`: the space, repeating events, sheets (CSV and
  tab-separated parsing), the summary ranges and calendar write-back. No UI.
- `src/SpacesVocab.rgr`: the vocabulary packs, the page's words in Finnish and
  English, and the demo data of each pack.
- `src/SpacesAssist.rgr`: the assistant's answers and documents as Markdown.
- `src/SpacesApp.rgr`: the page, one EVG tree built with EVGUI controls.
- `src/SpacesCheck.rgr`, `test/app-check.mjs`, `scripts/check-page.mjs`: the
  checks.
- `web/`: the host page (`main.js` paints with EVG's WebGL painter and mirrors
  the accessible tree into the DOM) and the stylesheet.

The build reuses Sliqtly's pipeline (`scripts/lib.mjs`): the same Ranger
checkout and EVGUI, with `src/` linked in as `gallery/spaces`.

## Not yet

The data lives in the page and resets on reload. Saving spaces on a server,
members and sharing, `.xlsx` files, editing documents in place and a language
model behind the assistant come next.
