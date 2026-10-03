# Writing a Sliqtly deck

A Sliqtly presentation is one Markdown document plus a theme (CSS). The
slides are drawn by the Sliqtly player at https://sliqtly.com.

## Structure

````markdown
---
title: Quarterly review
transition: fade        # default for every slide: fade | slide | zoom | none
seconds: 0.6            # transition length in seconds
step: 1.2               # seconds between build steps when played
hold: 2.5               # seconds after the last step
---

# Quarterly review

The opening line under the title.
{.lead}

## Results {transition=slide fx=starfield}

1. Revenue up 12 %
2. Two new markets
3. Churn halved
{.build anim=rise}

::: notes
Speaker notes. Not shown on the slide.
:::
````

- `#` opens the deck (title slide); every `##` starts a new slide.
- An attribute block `{…}` on the line after a paragraph, list, table or
  fence applies to that block. On a heading line it applies to the slide.
- Slide attributes: `transition=fade|slide|zoom|none`, `seconds=0.5`,
  `duration=8`, `fx=<effect>`, `bg=media/<picture>` (a picture covering the
  slide), `bg-dim=0.4` (paper laid over it for legibility, 0–1),
  `art=waves` (line art behind the slide; `art-seed=3` draws another
  picture of it, `art=off` none; `art: waves` in the front matter puts it
  behind every slide). Line art is drawn only for signed-in PRO decks.
- Block attributes: `.build` (a list revealed one item at a time),
  `anim=fade|rise|fly|zoom`, `seconds=0.8`, classes `.lead` (larger intro
  text), `.kicker` (small label), `.c2` / `.c3` (two or three columns).
- Effects (`fx=`): `starfield`, `plasma-wave`, `smoke`, `ambient-light`,
  `liquid-glass`, `raindrop`. Parameters as `fx-density=1.6`, `fx-hue=228`.
- `::: notes … :::` holds speaker notes for the slide above it.
- Keep a slide short: a heading and 3–6 bullets, or a heading and one
  picture, chart, table or diagram. A slide that runs over is split.

## Pictures

Write `![Alt text](media/<name>)` and pass the picture in the tool call's
`images` list with the same `name` (for example `name: "team.jpg"` →
`![](media/team.jpg)`). A picture can also cover the slide:
`## Title {bg=media/cover.jpg bg-dim=0.4}`. Give each picture either a
public `https` URL or base64 data. PNG, JPEG, GIF, WebP and SVG, up to 5 MB.
Pictures, data files and workbooks are stored only when the user is signed
in; without sign-in a deck is text only and is deleted 30 days after its
last change.
When text on a picture is hard to read ("low contrast"), raise that slide's
`bg-dim` (0.6–0.8) with update_presentation; the picture stays as it is.

## Charts, diagrams, math, tables

- Charts: a ```` ```vega-lite ```` fence with a Vega-Lite JSON spec. Bar,
  line, area, point, arc (pie) and more. Use
  `"background": "rgba(0,0,0,0)"` so the theme shows through. An encoding's
  `type` is `quantitative`, `ordinal`, `nominal` or `temporal` (never
  `"point"`: that puts every mark at 0). The data is
  inline `data.values`, or live, read each time the deck opens:
  `"data": {"url": "https://…/x.csv"}` (or `.json`), a Google Sheet
  `{"source": "google-sheets", "id": "<id or link>", "sheet": "Monthly", "range": "A:B"}`
  (or `"range": "Monthly!A:B"`, or `"gid": 123`), or its link as `url`. The
  sheet must be shared as "Anyone with the link". `bind_chart_data` points
  an existing chart at such a source. PDF and PPTX exports are snapshots of
  the data when exported.
- Diagrams: ```` ```mermaid ````, ```` ```dot ```` (Graphviz), ```` ```d2 ````,
  ```` ```plantuml ````. They are animated as a guided tour, box by box.
  Under the fence: `{style=sketch}`, `{tour=off}`, `{layout=keep}`.
- Math: `$…$` inline, `$$…$$` as a display, or a ```` ```math ```` fence (TeX).
- Tables: ordinary Markdown tables, or HTML `<table>` with `rowspan`/`colspan`.
- Layouts from lists: a ```` ```process ```` fence (chevron steps),
  ```` ```swot ```` (a 2×2 grid of four items: Strengths, Weaknesses,
  Opportunities, Threats) or ```` ```timeline ````, one item per
  line as `Title: description`; an indented `- point` belongs to the item
  above. `{width=60%}` under the fence makes it narrower. They are drawn in
  the theme's colours and go into the PDF and PowerPoint as shapes. Only
  signed-in PRO decks draw them; otherwise the slot says it is a PRO
  layout, so do not use them when the user is not on PRO.

  ````markdown
  ```process
  - Plan: goals, budget and schedule
  - Build: code, content and tests
  - Launch: open to everyone
  ```
  ````
- The deck's own data: `list_files` (and `get_presentation`) list the files a
  deck keeps. For each `.xlsx` workbook they give its sheets, columns and row
  counts, and the name a sheet is read by (e.g. `data/sales-Sales.csv`). The
  editor derives that CSV from the workbook; it is not a separate file. Use
  the name in a chart (`"data": {"url": "data/sales-Sales.csv"}`), in a paged
  table:

  ````markdown
  ```table
  data/sales-Sales.csv
  rows: 8
  columns: Region, Revenue
  ```
  ````

  or as the workbook itself on the slide, which a presenter can open and edit
  during the show (press E):

  ````markdown
  ```sheet
  data/sales.xlsx
  sheet: Sales
  rows: 8
  ```
  ````
- Reading data: `read_file` with the deck_id and a path from `list_files`
  gives the values: a workbook's sheet (`data/sales.xlsx` with `sheet`, or
  `data/sales-Sales.csv`) or a CSV as rows, 200 at a time (`offset`,
  `limit` up to 2000); a JSON or text file as its text. The answer says how
  a workbook's dates are written.
- Adding data: `create_presentation` and `update_presentation` take `files`:
  `{ "name": "sales.xlsx", "data_base64": "…" }` (or `url`), or
  `{ "name": "sales.csv", "text": "Region,Revenue\nNorth,120\n" }`. Each is
  kept as `data/<name>` (.xlsx, .csv, .tsv, .json, .txt; 10 MB each) and read
  by the names above; the result lists a workbook's sheets.
- Tidying a workbook: `write_workbook` with the deck_id, the workbook's
  `path` and every sheet in full (`{ "name": "Costs", "rows": [["Month",
  "Rent"], ["2026-01", 950]] }`, or `csv` text) writes a new .xlsx in its
  place. A cell can be a formula with the value it gives:
  `{ "f": "=SUM(B2:B13)", "v": 11400 }`. Formatting, colours, filters and
  column widths are not kept. If sheets are renamed, update the charts and
  tables that read them. Write workbooks you made yourself this way too:
  an .xlsx sent as `data_base64` is easily corrupted when it is long. A
  workbook in a deck that only lives in the user's browser is not
  reachable: ask the user to attach it, then write the result with
  `write_workbook`.
- Inline HTML: `<mark>`, `<u>`, `<s>`, `<sub>`, `<sup>`, `<kbd>`, `<small>`,
  `<br>`, and `<span style="color:#e33">`.

## Themes

Pass `theme` as one of:

| theme | look |
| --- | --- |
| `aurora` | dark navy, cyan and orange accents (the default) |
| `nebula` | dark with a starfield |
| `carbon` | dark graphite |
| `ember` | dark with warm ambient light |
| `midnight` | deep dark blue |
| `corporate` | light, business |
| `editorial` | light, magazine-like |

`css` adds rules on top of the theme (later rules win). The selectors are
element names, not HTML tags:

```css
page     { background-color: #0b1030; padding: 0.75in; }  /* the slide */
document { font-family: Open Sans; font-size: 20pt; color: #e8ecff; }
heading  { margin-bottom: 18pt; }
h1 { font-size: 48pt; }   h2 { font-size: 36pt; }   h3 { font-size: 24pt; }
p  { margin-bottom: 14pt; }
a  { color: #a9b8ff; }
list { padding-left: 30pt; }   li { margin-bottom: 10pt; }
blockquote { color: #c3cbff; border-color: #7c8cff; border-width: 4pt; }
code  { font-size: 15pt; background-color: #151c48; }
table { border-color: #2d3a7a; background-color: #151c48; }
mark  { background-color: #ffd54a; color: #111; }
chart   { color: #ffa546; accent-color: #5ce1ff; chart-style: forge; } /* flat | forge | neon | glass */
diagram { color: #ffa546; accent-color: #5ce1ff; }
figure  { colors: #1f6feb #0f9d8a #7c4dff #f08c00; } /* process, swot, timeline (else chart's) */
.lead { font-size: 26pt; }
```

Fonts available: `Open Sans`, `Noto Sans`. Sizes in `pt` or `in`. Set
`css_mode: "replace"` only when sending a complete stylesheet of your own.

## Result

`create_presentation` returns a share link that opens straight into the
presentation (full screen button, arrow keys) and an edit link that opens a
copy in the Sliqtly editor. Keep `deck_id` and `edit_key` to change the same
deck later with `update_presentation`; the share link stays the same.
