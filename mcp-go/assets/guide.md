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
  `liquid-glass`, `drops` (rain running down a window),
  `raindrops2` (rain whose running drops leave lines of water), `bubbles` (round
  drops). Parameters as `fx-density=1.6`, `fx-hue=228`, `fx-rain=2`.
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
- Diagrams: a ```` ```mermaid ````, ```` ```plantuml ```` (or `puml`),
  ```` ```dot ```` (or `graphviz`) or ```` ```d2 ```` fence. The slide shows the
  whole diagram, as large as it fits; ▶ beside the zoom buttons (T while
  presenting) starts a guided tour, box by box. Under the fence:
  `{tour=on}` (the tour starts with the slide), `{style=sketch}`
  (hand-drawn), `{layout=keep}` (keep the direction as written),
  `{ball=off}`, `{choose=off}` (do not stop at named branches),
  `{zoom=2}` (largest scale a box is drawn at, default 3),
  `{diagram=classic}` (a still drawing, no tour). Colours come from the
  theme's `diagram` rule. A diagram that cannot be read shows the reason
  in its place (DOT gives the line).
- Mermaid: `flowchart`/`graph` (TD, TB, BT, LR, RL; every node shape and
  link, `subgraph`, `classDef`/`class`/`style`), `sequenceDiagram`,
  `classDiagram`, `stateDiagram-v2`, `erDiagram`, `mindmap`, `timeline`,
  `journey`, `gantt`, `gitGraph`, `pie`, `quadrantChart`, `xychart`,
  `sankey`, `block`, `architecture`, `kanban`, `requirement`, `C4Context`,
  `packet`, `radar-beta`, `treemap`. `click` and `linkStyle` are ignored;
  a node id may not contain `-` or `.`.

  ````markdown
  ```mermaid
  flowchart LR
    A[Plan] --> B{Approved?}
    B -->|yes| C[Build]
    B -->|no| A
    C --> D([Launch])
  ```
  ````
- PlantUML (`@startuml … @enduml`): sequence, class, object, component,
  deployment, use case and activity diagrams. Not drawn: state, timing,
  mind map, WBS, Gantt, JSON/YAML, salt, ditaa (the slot says so). Activity
  uses the current syntax (`start`, `:action;`, `if (…) then (…)`/`else`/
  `endif`, `while`, `repeat`, `fork`, `stop`); the old `(*) -->` form is
  misread. Swimlanes are read but not drawn as columns. `!include`,
  `!define` and other `!` lines, `skinparam` and colours are dropped. Only
  the first `@startuml` block is drawn. A body with nothing but `A --> B`
  lines is a sequence diagram; add `class`, `component`, `[A]` or `(A)` to
  make it a class or component diagram.

  ````markdown
  ```plantuml
  @startuml
  class Order {
    +id: int
    +total(): Money
  }
  class Line
  Order *-- "1..*" Line : contains
  @enduml
  ```
  ````
- Graphviz DOT: `graph`/`digraph`, `strict`, `rankdir`, clusters
  (`subgraph cluster_x { label="…" }`), node and edge defaults, `label`,
  `shape`, `color`, `fillcolor`, `fontcolor`, `style`, `penwidth`,
  `arrowhead`, `dir`. `layout=neato|fdp|sfdp` is drawn as a force layout,
  `twopi|circo` as rings, anything else ranked. Text Graphviz would reject
  is refused, not guessed.

  ````markdown
  ```dot
  digraph {
    rankdir=LR; node [shape=box];
    plan -> build -> ship;
    build -> plan [label="rework", style=dashed];
  }
  ```
  ````
- D2: shapes, containers, connections and arrowheads, labels, `direction`,
  `style.*`, `classes`, `vars`, globs, `sql_table`, `class`. Not drawn:
  `@imports`, `layers`/`scenarios`/`steps` (only the root board),
  `grid-columns` (laid out as an ordinary container), `icon` and
  `shape: image` (an empty box), `near` to another shape, LaTeX.
  `shape: sequence_diagram` comes out as a box of participants, not
  lifelines: use Mermaid or PlantUML for sequences.

  ````markdown
  ```d2
  direction: right
  user: User { shape: person }
  app: App {
    api: API
    db: Database { shape: cylinder }
    api -> db: query
  }
  user -> app.api: request
  ```
  ````
- Size: a diagram is laid out at the column width and then fills the room
  under the heading, up to 2.2× its own size. A wider diagram is shrunk,
  text and all: in the layout report's px (a 1920×1080 screen) its box
  text is about 20 px when four boxes stand in a row, 13 px at six, 10 px
  at eight (body text is about 40 px). For a Mermaid flowchart or DOT
  graph without `{layout=keep}` the slide also tries the other direction
  and a long top-to-bottom chain cut into columns (not with
  subgraphs/clusters), and keeps whichever draws largest. Keep one
  diagram to about 4 boxes across and 10–12 boxes in all; split a bigger
  one over slides. A sequence diagram is never toured: keep it to 4–5
  participants. `{width=60%}` (or a number of pt) under the fence
  narrows it only when something stands beside it; alone on its slide
  it still spans the full width. Put nothing under a diagram: it takes
  the room down to the next block or the bottom margin.
- Math: `$…$` inline, `$$…$$` as a display, or a ```` ```math ```` fence (TeX).
- Tables: ordinary Markdown tables, or HTML `<table>` with `rowspan`/`colspan`.
- Layouts from lists: a ```` ```process ```` fence (chevron steps),
  ```` ```swot ```` (a 2×2 grid of four items: Strengths, Weaknesses,
  Opportunities, Threats) or ```` ```timeline ````, one item per
  line as `Title: description`; an indented `- point` belongs to the item
  above. `{width=60%}` under the fence makes it narrower. They are drawn in
  the theme's colours and go into the PDF and PowerPoint as shapes, for
  every user, signed in or not.

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
a  { color: #59C3C4; }
list { padding-left: 30pt; }   li { margin-bottom: 10pt; }
blockquote { color: #c3cbff; border-color: #A321D9; border-width: 4pt; }
code  { font-size: 15pt; background-color: #151c48; }
table { border-color: #2d3a7a; background-color: #151c48; }
mark  { background-color: #ffd54a; color: #111; }
chart   { color: #B96926; accent-color: #59C3C4; chart-style: forge; } /* flat | forge | neon | glass */
diagram { color: #B96926; accent-color: #59C3C4; }
figure  { colors: #1f6feb #0f9d8a #7c4dff #f08c00; } /* process, swot, timeline (else chart's) */
.lead { font-size: 26pt; }
```

Fonts available: `Open Sans`, `Noto Sans`. Sizes in `pt` or `in`. Set
`css_mode: "replace"` only when sending a complete stylesheet of your own.

## Checking the result

Every `create_presentation` and `update_presentation` answer carries a
layout report, slide by slide, in the pixels of a 1920×1080 screen: each
element (heading, text, list, table, chart, diagram, picture) with its
place and size, the smallest text, and how much of the slide the elements
cover. Lines marked ⚠ name what looks wrong: text under 20 px, elements on
top of each other or past the slide's edge, a lone chart or picture on a
mostly empty slide, a chart's or diagram's labels drawn over each other, a
table column that wraps its cells. The report is cheap; compare it between
versions.

The report measures; it does not see. Look at the slides themselves:

- `render_slide` (deck_id, slide number or title) gives one slide as a
  picture (960×540), drawn as the player shows it at the end of its
  animations, with that slide's report. Use it when the report flags a
  slide, and for charts, diagrams and tables, whose colours, bar lengths
  and readability only a picture shows: a wrong colour or a bar of the
  wrong length often means a data error.
- `render_overview` (deck_id) gives every slide as a numbered thumbnail in
  one picture. Look once before telling the user the deck is done.

Effects (`fx=`), picture corners and SVG pictures are not drawn in these
pictures; the player draws them.

## Result

`create_presentation` returns a share link that opens straight into the
presentation (full screen button, arrow keys) and an edit link that opens a
copy in the Sliqtly editor. Keep `deck_id` and `edit_key` to change the same
deck later with `update_presentation`; the share link stays the same.
