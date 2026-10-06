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
  text), `.kicker` (small label), `.c2` / `.c3` (two or three columns),
  `.center` / `.right` / `.left` (a heading or paragraph's lines, e.g.
  `# Title` then `{.center}` on the next line). In `css`, `text-align`
  does the same for a kind of block: `h1 { text-align: center }`,
  `.lead { text-align: center }`. Lists, tables and code stay left.
- `container=box` or `container=bubble` under a paragraph, a list or a
  heading (`## Title {container=box}`, also with `bg=media/x.jpg` on the
  same heading: the plate goes round the title, the picture fills the slide)
  sets it on a rounded plate so its text reads over a busy picture: `box`
  spans the column, `bubble` is as wide as the text with a speech-bubble
  tail. `background=#ffffffcc` picks the plate's colour (default: the
  slide's colour, see-through; the text turns dark or light to read on
  it). For every plate in `css`: `container { background-color; border-radius }`.
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
public `https` URL or base64 data, or an SVG's source as `text` (readable,
no base64). PNG, JPEG, GIF, WebP and SVG, up to 5 MB.
Pictures, data files and workbooks are stored only when the user is signed
in; without sign-in a deck is text only and is deleted 30 days after its
last change.
A signed-in user's presentation is private: only their Google account sees
it, at its link too. To share it, ask the user first, then call
`update_presentation` with `visibility: "link"` (anyone with the link can
view it); `visibility: "private"` closes it again.
When text on a picture is hard to read ("low contrast"), raise that slide's
`bg-dim` (0.6–0.8) with update_presentation, or put the text on a plate
(`{container=box}` under it); the picture stays as it is.

SVG pictures, backgrounds included:

- `bg=media/x.svg` works like any picture: it covers the slide, scaled to
  fill it with its middle kept and the rest cut off (as
  `preserveAspectRatio="xMidYMid slice"`). Give a background the slide's
  shape: `viewBox="0 0 1920 1080"` for a 16:9 slide.
- The root needs `xmlns="http://www.w3.org/2000/svg"`; without it a
  browser draws nothing. Give it a `viewBox`.
- Shown as a picture, an SVG loads nothing from outside itself: put
  pictures in it as `data:` URLs. Its text is drawn in each viewer's own
  fonts, so draw words as paths, or write them on the slide in Markdown.
- The player draws the SVG once to a PNG 2560 px on its longer side; the
  slides, the PDF and the PPTX all use that PNG, so in PDF and PPTX it is a
  raster picture, not vectors. The deck keeps the SVG itself.
- `render_slide` and `render_overview` draw SVG pictures, and the result of
  create/update has a line for each SVG ("SVG ok, viewBox 1920×1080
  (16:9), 14 paths") with a ⚠ for what will go wrong in the player: no
  xmlns, no viewBox, a background not in the slide's shape, things loaded
  from outside, text, filter effects.

### Photo albums

A `gallery` fence is a photo grid or one photo per slide. Each line is a
picture with its caption (`- media/a.jpg: Caption` or `- ![Caption](media/a.jpg)`),
or plain text for a text cell. Under the fence:

````markdown
## Hietaniemi {heading=hidden}

```gallery
- media/ranta.jpg: Hietaniemi in July {focus=top}
- media/sauna.jpg: Sauna {span=2}
```
{layout=full fit=cover caption=overlay .polaroid}
````

- `layout=grid` (default) or `layout=full`: one picture per slide, edge to
  edge, without the page margins.
- `fit=cover` fills the cell and crops, `fit=contain` shows the whole
  picture. `focus=` keeps a part in the crop: `top`, `bottom left`,
  `30% 70%`. `span=2` makes a cell two columns wide.
- `caption=overlay|below|none`; an overlay caption is white text on a dark
  see-through band that reads (4.5:1) over light and dark pictures; a
  `caption { color }` set dark gets a light band. Captions below
  take a colour that reads on the slide unless `caption { color }` sets one.
- With `layout=full`, each picture's slide is named by its caption (else
  the heading and the picture's number, "Summer (2)") in the overview and
  contents; the album filling several slides is not an overflow.
- The pictures listed in a gallery count as used in the Markdown.
- `{heading=hidden}` on a slide's heading keeps it as the slide's name
  (overview, contents, screen readers) but does not draw it.
- CSS: `gallery { gap: 8pt; columns: 3; background-color: … }`,
  `cell { border-radius: 4pt; padding: 0 }`, `caption { font-size: 14pt }`,
  `cell.text { … }`, one cell `cell:nth(3) { … }`, an album's own class
  `.polaroid cell { padding: 10pt 10pt 32pt; background-color: #fff }`.

For print, put the page in `@media print` in `css`:

```css
@media print {
  page { width: 297mm; height: 210mm; bleed: 3mm; safe-area: 8mm; }
  deck { crop-marks: on; }
}
```

The screen is unchanged. The PDF export then uses the print page, runs
edge-to-edge pictures and backgrounds into the bleed and adds crop marks.
The result of create/update warns about text outside the safe area and
pictures under 300 dpi in print ("media/sauna.jpg: 180 dpi in print, under
300"). Colours stay RGB.

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
  (hand-drawn), `{style=mermaid}` (pastel cards on a dotted grid),
  `{style=jurassic}` (poster: ochre circles, grey diamonds, heavy square
  lines), `{style=cartoon}` (speech bubbles, fat outlines, offset shadows,
  numbered boxes), `{style=romantic}` (black caption boxes, terracotta
  circles, dashed lines; round nodes `((…))`/`([…])` become the circles),
  `{layout=keep}` (keep the direction as written),
  `{ball=off}`, `{choose=off}` (do not stop at named branches),
  `{zoom=2}` (largest scale a box is drawn at, default 3),
  `{diagram=classic}` (the plain drawing on the slide's own background,
  still, no tour). Node shapes, `classDef`/`style` fills and dashed
  borders show in every style; other colours come from the theme's
  `diagram` rule. A long flow is cut into columns only between two boxes
  joined by a single link, never inside a loop or a branch. A diagram that cannot be read shows the reason
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
- Code: a fence with the language (js, ts, py, rust, go, java, c, cpp,
  cs, sql, json, sh, …) is coloured. Attributes after the language:
  `.numbers` (or `numbers=40`, the first number) puts line numbers in a
  gutter; `lines=3-5,9` highlights those lines (by the numbers shown, else
  1 = first line); `lines=3-5|9|12` with `.build` highlights them one build
  step after another while the code stays on the slide.
  ```` ```js {.numbers lines=2|4-5 .build} ````
- Diffs: a ```` ```diff ```` fence (```` ```diff ts ```` colours the code
  as TypeScript) shows `+` lines on green, `-` lines on red, `@@` hunk
  headers, and file headers (`diff --git`, `---`, `+++`) dimmed. With
  `.numbers` the numbers follow the hunk headers' new-file side (a removed
  line has none), and `lines=` highlights by those numbers. Paste
  `git diff` output as it is; keep a slide to one hunk of about 15 lines.
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
- SmartArt: a diagram from PowerPoint's SmartArt, as a FILE in the deck.
  Write its data model (`dgm:dataModel`, as in a .pptx's
  `ppt/diagrams/data1.xml`), send it in `images` as `name: "steps.xml"`, and
  reference it like a picture, `![…](media/steps.xml)`. Only the picture
  form draws it: a link, `[…](media/steps.xml)`, is a link and shows only
  its text (the result warns). Options go on the line UNDER the reference:

  ````markdown
  ![The release process](media/steps.xml)
  {layout=chevron1 colors=colorful1 width=80%}
  ````

  The smallest file: a document point naming the layout, a point per item,
  and a connection from its parent to each (a sub-item connects to its item,
  not to the document):

  ```xml
  <dgm:dataModel xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
    <dgm:ptLst>
      <dgm:pt modelId="0" type="doc"><dgm:prSet loTypeId="urn:microsoft.com/office/officeart/2005/8/layout/process1"/></dgm:pt>
      <dgm:pt modelId="1"><dgm:t><a:p><a:r><a:t>Plan</a:t></a:r></a:p></dgm:t></dgm:pt>
      <dgm:pt modelId="2"><dgm:t><a:p><a:r><a:t>Build</a:t></a:r></a:p></dgm:t></dgm:pt>
    </dgm:ptLst>
    <dgm:cxnLst>
      <dgm:cxn srcId="0" destId="1"/>
      <dgm:cxn srcId="0" destId="2"/>
    </dgm:cxnLst>
  </dgm:dataModel>
  ```

  Layouts drawn today: `process1` (steps with arrows), `chevron1` (chevron
  steps), `vList2` (a bar per item, its sub-items as bullets under it),
  `hList1` (a column per item, its sub-items under its heading), `default`
  (blocks in rows that wrap), `cycle2` (items on a circle, arrows round it),
  `radial1` (the first item in the middle, the items under it round it),
  `hierarchy1` and `orgChart1` (a tree: each item over the items under it;
  in orgChart1 a point with `type="asst"` is an assistant, beside the line
  down from its boss), `pyramid1` (a triangle cut into a level per
  item, the first at the apex), `venn1` (overlapping circles), `matrix1`
  (four quadrants), `target1` (nested rings), `funnel1`, `gear1` (up to
  three gears), `arrow2` (points rising along an arrow), `bList2`
  (an item per block, without its picture), `hProcess9` (blocks along one wide arrow),
  `lProcess2` (a column per item, its sub-items as blocks) and `cycle4` (four items as
  quarters of a circle). Another layout id is drawn as `default` and
  the result says so. Colours (`colors=`): `accent0_1` … `accent0_3`,
  `accent1_1` … `accent6_5` (one theme colour), `colorful1` …
  `colorful5` (cycling accents). `accent1_2` is what a diagram has when no
  colours are named, so writing it changes nothing. A diagram with nothing
  beside it and no `width=`/`height=` is laid out across the whole content
  width, and a row of steps (`process1`, `chevron1`) that would be a thin
  strip wraps into rows of three or more; `width=` keeps it in its own box.
  Text that would not read on its fill (a pale column on a dark theme) is
  set in the theme's paper or ink colour instead. Styles (`style=`): `simple1` … `simple5` (thicker outlines,
  then shadows). They come from the theme. A whole SmartArt from a .pptx
  also works as one file: a Flat OPC package (`pkg:package`) holding its
  data, layout, style, colours and, when PowerPoint saved one, its drawing;
  `tools/extract_smartart.py deck.pptx out/` in RangerPPTX writes one per
  diagram. Such a file is drawn as PowerPoint drew it (scaled to the box),
  or laid out again from its own layout when `layout=`, `colors=` or
  `style=` is given. A file that is not a diagram, and a data model with
  no items, is shown as the reason in its place and named in the warnings. The PDF
  carries it as drawn; the PowerPoint export carries it as SmartArt that
  PowerPoint can edit. Prefer a Mermaid diagram or a
  ```` ```process ```` list when either says it; SmartArt is for when the
  deck should hold PowerPoint's own kind of diagram.
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
- Vectorizing a picture: `vectorize_image` with the deck_id and a PNG or
  JPEG's `path` traces it into an SVG (`media/<name>.svg`) and points the
  Markdown's and the theme CSS's uses of it at the SVG; the original stays
  in the files. `preset` is logo, illustration (the default), poster, photo
  or lineart; `options` sets tracer options on top of it, e.g.
  `{ "colorCount": 6, "turdsize": 8, "maxSide": 1200 }`. Good for logos,
  icons, drawings and blurry low-resolution pictures; a photo becomes a
  poster-like drawing. Without a deck_id, `image_base64` or `image_url`
  gives the SVG back. `vectorize_image` is on every Sliqtly server; if it
  (or another tool this guide names) is not in your tool list, the
  connector's tool list is older than the server: ask the user to
  reconnect the Sliqtly connector.
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
| `editorial` | a light A4 portrait document: pages, not slides (see below) |

`editorial` is for reading, not presenting: an A4 portrait page with one
narrow column, where only `#` starts a new page and `##` / `###` are
sections that run on down the page. For light slides use `corporate`; for
an editorial page that breaks at every `##`, add `deck { split-level: 2; }`
in `css`.

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

Fonts available: `Open Sans`, `Noto Sans`. Sizes in `pt` or `in`.

On `update_presentation`, `css` is added after the deck's own rules so
far, so one new rule can be sent alone; sending all of the deck's own
rules again (as `get_presentation` returns them in `css`) replaces them.
`css_mode: "own"` starts the deck's own rules over with this `css`;
`css_mode: "replace"` only when sending a complete stylesheet of your own.
A new `theme` keeps the deck's own rules. The `version` a result gives
names the Markdown (for merging edits): a change to `css` alone keeps it.

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

Effects (`fx=`) and picture corners are not drawn in these pictures; the
player draws them. SVG pictures are drawn.

## Changing a deck

For a small change, send `edits` to `update_presentation` instead of the
whole `markdown`:

```json
{ "deck_id": "…", "edits": [
  { "find": "Revenue grew 12 %", "replace": "Revenue grew 14 %" },
  { "slide": 4, "markdown": "## Costs\n\n- Rent\n- Salaries" },
  { "slide_title": "Old plan", "markdown": "" },
  { "after_slide": 6, "markdown": "## Next steps\n\n- Pilot in May" }
] }
```

- `find` + `replace`: the text exactly as it is in the deck (spaces and
  line breaks too); it must be there once, or add `"all": true`.
- `slide` (number) or `slide_title` + `markdown`: the slide's whole new
  text from its heading; `""` deletes the slide. A slide whose text ran
  over onto the next ones is replaced with all of them.
- `after_slide` + `markdown`: new slides after that one (0 = before the
  first).

Slide numbers are the ones the layout report and `render_overview` show,
before these edits; their order does not matter. An edit that does not
apply cleanly (text not found or found twice, two edits on the same text)
saves nothing and says why. The answer lists what each edit changed.

## Review comments

People comment slides in the editor's review mode: a speech bubble pinned
on a slide, with a thread of messages beside it. `list_comments` (deck_id)
reads them: each thread's id, slide number and title, place on the slide,
whether it is resolved, and its messages. Work through the open ones:

- Change the deck as asked (`update_presentation`), then answer the thread
  with `add_comment` (deck_id, thread_id, text) or close it with
  `resolve_comment` (deck_id, thread_id, optional text saying what was
  done). A resolved thread stays on the slide, dimmed; people delete them.
- A comment of your own on a slide: `add_comment` with `slide` (number) or
  `slide_title`, and `x`, `y` (0..1 of the slide) to point at something.
- `author` names you on the message; the default is "AI assistant".

## Rooms

Rooms are only on a Sliqtly server of one's own (sliqtly.com has none, so
`list_rooms` and the other room tools are missing there). On such a server
(`list_rooms` is in the tool list), the
presentations are kept in rooms. A room is one whole piece of work: a
task, a Jira ticket, a user story, or another whole such as a project.
Every presentation has one home room; new ones start in General.

- Work on a ticket or a story: find its room in `list_rooms` (newest
  first) or make one with `create_room`, named as the work is known
  ("PROJ-123 Checkout retry") and with the ticket's summary or link as its
  `description`. Then `move_presentation` the decks made for it there.
- `get_room` lists a room's presentations; `update_room` renames or
  describes it; `archive_room` puts finished work away (read only,
  nothing removed); `delete_room` removes the room and moves its decks to
  General.
- `add_link` ties a room to the ticket itself: `room:<room_id>`
  `references` `jira:PROJ-123`.

### A room's chat

Each room has a chat, like a Slack channel, where its people and the
assistants working for them talk.

- `read_room_chat` reads it, newest last; `after_seq` (the last seq you
  saw) for what came since, `thread_id` for one thread's replies,
  `mentioning` for the messages that ask you by @name.
- `post_room_message` with `agent` ("Claude"): you show as a robot. Say
  what you were asked and what you did; for a longer job keep one status
  message up to date with `message_id` instead of posting many. Answer
  in a thread with `thread_id`.
- Text: *bold*, _italic_, ~strike~, `code`, ``` blocks, > quotes, lists,
  links, :emoji:, @name, #room. `[[slides:<deck_id>]]` shows a
  presentation in the chat (`[[slides:<deck_id>#3]]` one slide). Long
  text and long code are folded with "Show more".
- Files: what people attach goes into the room's files; `list_room_files`
  lists them with their addresses. Show some with a message by
  `files` (their names) on `post_room_message`; the text may then be
  empty. Links in a message get a preview (site, title, summary) shortly
  after it is posted.

## When another assistant works on the same deck

Two assistants (two chats, or another app) can change one presentation at
the same time. So that neither undoes the other's work:

1. Before changing a deck you did not just create, call `begin_work`
   (deck_id, `agent`: who you are, e.g. "Claude (budget chat)", `slides`:
   the slides you will change by number or title, or none for the whole
   deck, `note`: what for). It returns your `work_id`, the deck's `version`
   and who else is working on it.
2. If it says *Not claimed*, another assistant holds some of those slides:
   work on other slides (call `begin_work` again with them), or tell the
   user who is working on what and ask whether to wait until it is done.
   `force: true` only when the user wants both of you on the same slides.
3. Save with `update_presentation` and send `base_version` (the version
   your Markdown started from: from `begin_work`, `get_presentation` or your
   last update) and `work_id`. Edits saved meanwhile by someone else are
   merged line by line and the answer says so; read the deck again with
   `get_presentation` before changing those slides. A change both made to
   the same lines is refused ("Not saved", with the slides): get the
   current text, make your change on it and save with its version, or ask
   the user which change to keep. Each answer gives the new version.
4. Call `end_work` (deck_id, work_id) when done. A claim also runs out
   after `minutes` (default 15) without an update.

`edits` are made on the deck as it is when they arrive, so they need no
`base_version`; a whole `markdown` without one is refused while someone
else holds a claim on the deck. `get_presentation` and every update list
the others' claims ("Also working on this deck").

## Exporting

`export_presentation` (deck_id, `format`: `pdf`, `pptx`, `docx` or
`html`, optional `slides`: [2, 5]; by the deck's owner, or by the session
that made it without sign-in) makes the file the editor's File → Export makes and returns
a download link for the user, on sliqtly.com (`https://sliqtly.com/d/…`,
working for 24 hours). The PPTX keeps text
editable, with build steps, speaker notes and transitions; charts and
diagrams are shapes. `docx` (Word) and `html` (one self-contained web
page) read the deck as a document: each slide's headings, text, lists,
tables and formulas, its speaker notes under it, and charts and diagrams
as pictures. Effects (`fx=`) are left out; the editor's own export draws
them. A deck with `@media print` rules gets a PDF on the print page with
bleed and crop marks. A new export of the same format replaces the file behind the old
link.

## A pull request as source

`read_github_pr` (`pr`: its link, or `owner/repo#12`) reads a GitHub pull
request: title, description, state, files with their patches, commits. It
returns them and a first draft of a review deck: what and why, the changed
files, the biggest changes as ```` ```diff ```` slides (one file each,
shortened), the commits on a timeline and a decision slide. Change the draft
to say what matters (pick the lines with `{lines=…}`, add `.build` to step
through them) and create it with `create_presentation`. Public repositories
work as they are; a private one needs a token on the server and a signed-in
user the server lets read private repositories.

## Result

`create_presentation` returns a share link that opens straight into the
presentation (full screen button, arrow keys) and an edit link that opens a
copy in the Sliqtly editor. Keep `deck_id` to change the same deck later
with `update_presentation`; the share link stays the same. The id finds a
presentation, it does not let anyone change it: a signed-in user's is
changed by its owner, one made without sign-in only in this connection's
session (until it ends or goes unused for a day).
