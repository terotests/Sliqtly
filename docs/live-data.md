# Live chart data

A chart or a table can name where its data comes from instead of carrying the
numbers. The deck then stays up to date: update the Google Sheet (or whatever
serves the CSV / JSON) and the slides show the new numbers the next time the
presentation is opened, or straight away with a refresh while presenting.

```
Google Sheet / CSV / JSON URL  →  Sliqtly fetches it  →  Vega-Lite chart / table
```

The data connection is Sliqtly's own feature: it works without an AI agent or
an MCP server in the loop. MCP is for setting a binding up, not for carrying
the data (see [MCP](#mcp)).

## V1 (built)

### Sources

In a ```` ```vega-lite ```` fence, `data` can be:

| `data` | Read from |
| --- | --- |
| `{"url": "https://example.com/km.csv"}` | the CSV (or `.json`, `.tsv`) at that address |
| `{"source": "google-sheets", "id": "<id or link>", "sheet": "Monthly", "range": "A:B"}` | a tab and range of a Google Sheet |
| `{"source": "google-sheets", "id": "<id>", "range": "Monthly!A:B"}` | the same, the tab named in the range |
| `{"source": "google-sheets", "id": "<id>", "gid": 0}` | a tab by its gid |
| `{"url": "https://docs.google.com/spreadsheets/d/<id>/edit#gid=0"}` | a sheet's ordinary link (the tab from `#gid=`) |
| `{"url": "sheet://<id>/Monthly!A:B"}` | short form |

```vega-lite
{
  "data": {"source": "google-sheets", "id": "1AbC…", "range": "Monthly!A:B"},
  "mark": "bar",
  "encoding": {
    "x": {"field": "Month", "type": "nominal", "sort": null},
    "y": {"field": "Km", "type": "quantitative"}
  }
}
```

The first row of the range is the header; numbers are read as numbers.

A ```` ```table ```` fence takes the same addresses on its file line:

````
```table
https://docs.google.com/spreadsheets/d/1AbC…/edit#gid=0
rows: 8
```
````

### Linking by pasting

Paste a Google Sheet's link (or a `.csv` / `.tsv` / `.json` address) on its
own into the editor. Sliqtly reads it and opens **Link live data** with the
first rows: **Make a chart** (pick the columns as in the file import),
**Make a table**, or **Paste as text**. The chart it makes reads the link
live: the sheet's own columns, folded into series and summed or averaged by
the category the dialog picked, no numbers copied into the deck. A chart of
"each row on its own" or an x/y chart is still made from a copy of the rows.

### Google Sheets

A sheet shared as **Anyone with the link → Viewer** (or published to the
web) is read through its CSV export (`/gviz/tq?tqx=out:csv`) with no Google
sign-in.

A **private sheet** (only on your own Drive) is read through the Sheets API
as the signed-in PRO user (`web/sliqtly.js` `readSheet`):

- The scope is `drive.file`: Sliqtly may read only the files you pick in
  Google's Picker, nothing else on your Drive. Pasting the link the first
  time opens a Google popup (permission) and the Picker, open on that sheet.
- The token lasts an hour and is kept for the tab only. On open without one
  the editor shows the saved copy; R while presenting signs in again.
- Every good read is kept with the deck as `data/live/<hash>.csv` (only when
  it changed). Readers of a shared deck cannot read your sheet, so they see
  the copy your editor last kept; it is as fresh as your last open.

Google Cloud setup (once): Sheets API and Google Picker API enabled in the
Firebase project; the OAuth consent screen with sliqtly.com and
sliqtly.web.app; the Picker uses the Firebase web API key unless
`<meta name="google-picker-key">` in `web/index.html` names another.

The resolution of a source to an address is
`MdVegaRender.sourceUrl` in Ranger's markdown module (`gallery/markdown`),
so anything built on that module reads the same
sources.

### PRO

Live data is a PRO feature. It is fetched when the editor is signed in to
PRO, and in a presentation opened from a cloud share (`/s/{id}`, which only
a PRO user can make). Signed out, the editor says so once and the chart
stays without data. Local development (`localhost`) fetches always.

### When the data is fetched

- **When the presentation opens**: every time, editor or shared link
  (`/s/{id}`). Live data is not stored with the deck; a file under `data/` is.
- **When presenting starts** (F5 / ⛶ Present).
- **R while presenting**, or the ⟳ button in the shared view's bottom bar
  (shown only when the deck has live data).

### Live presentation vs. export

The browser presentation is live. **PDF and PPTX are snapshots**: they hold
the numbers that were on the slides when exported. A downloaded file never
changes afterwards. Regenerating exports automatically is part of V3.

## Roadmap (not built yet)

### V2: named data sources

Sources declared once per deck and referred to by name, so several charts and
tables share one connection and the address is changed in one place.

```yaml
---
data:
  monthly_km: { source: google-sheets, id: 1AbC…, range: "Monthly!A:B" }
  sales: { url: https://example.com/sales.csv }
---
```

```vega-lite
{ "data": {"name": "monthly_km"}, "mark": "line", … }
```

Also: a "Data sources" list on the Files tab (status, last fetched, rows),
and the chart editor offering a named source when a chart is made.

### V3: refresh policy

Per source (or per deck) when to fetch:

- `on-open` (the V1 behaviour, default)
- `every: 5m` while the presentation is open (kiosk / dashboard decks)
- `manual` (only R / ⟳)
- `webhook`: a POST to a Sliqtly endpoint marks the source stale; open
  presentations refetch, and shared exports (PDF/PPTX) can be regenerated by
  a pipeline.

Plus: the last good data kept as a fallback when a fetch fails (offline
presenting), and "data as of …" shown in the presenter view.

### V4: more connectors

A server that reads private sheets for readers of shared decks (the
owner's refresh token kept server side) instead of the saved copy, SQL (through a small proxy, never credentials in the deck),
Airtable, Notion databases, analytics APIs (GA4, Plausible). Credentials stay
server side; the deck only names the connection.

## MCP

MCP is the control interface for agents (ChatGPT / Claude → Sliqtly MCP →
"bind this chart to sheet X"), not a data pipe between MCP servers. V1 adds
`bind_chart_data` to the Sliqtly MCP server (owned by the MCP work in `mcp/`
and `mcp-go/`): it points a deck's chart at a URL or a Google Sheet without
rewriting the rest of the spec. Later: listing and configuring connectors
(V4) and named sources (V2) through MCP.
