# Writing a Sliqtly deck

A Sliqtly presentation is one Markdown document plus a theme (CSS). The
slides are drawn by the Sliqtly player at https://sliqtly.com.

## Structure

````markdown
---
title: Deck title
transition: fade        # default for every slide: fade | slide | zoom | none
seconds: 0.6            # transition length in seconds
step: 1.2               # seconds between build steps when played
hold: 2.5               # seconds after the last step
---

# Deck title

## A slide {transition=slide}

- A point
- Another point
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
- `container=box` or `container=bubble` under a paragraph, a list, a
  chart or diagram fence, or a (`## Title {container=box}`, also with `bg=media/x.jpg` on the
  same heading: the plate goes round the title, the picture fills the slide)
  sets it on a rounded plate so its text reads over a busy picture: `box`
  spans the column, `bubble` is as wide as the text with a speech-bubble
  tail. `background=#ffffffcc` picks the plate's colour (default: the
  slide's colour, see-through; the text turns dark or light to read on
  it). `padding=12px` and `radius=8px` set the plate's padding and corner
  rounding. Under a chart's fence `container=box` puts a plate round the chart,
  so its axes read over a bright background picture without dimming the
  whole picture. For every plate in `css`: `container { background-color; border-radius }`.
- Effects (`fx=`): `starfield`, `plasma-wave`, `smoke`, `ambient-light`,
  `liquid-glass`, `drops` (rain running down a window),
  `raindrops2` (rain whose running drops leave lines of water), `bubbles` (round
  drops). Parameters as `fx-density=1.6`, `fx-hue=228`, `fx-rain=2`.
- `::: notes … :::` holds speaker notes for the slide above it.
- A slide whose content runs over its height is split onto the next slide.

## Placing content on a slide

A slide's blocks go one under the other from the top, unless the slide or
a container says otherwise.

````markdown
## Before and after {layout=comparison}

### Before
- Slides made by hand

### After
- Markdown

## Results {layout=image-right}

- Growth continued
- Costs fell

![Chart](media/growth.png)

::: columns
```ts
const total = sum(rows);
```
{width=55%}

- `sum` adds the rows
:::
````

- `::: columns` … `:::` puts what is in it side by side. The columns are
  read from what is inside, the first rule that applies: `::: col` blocks
  (one column each; nest them in `:::: columns` with four colons, or write
  `::: col` blocks one after another without a wrapper; `::: col Title`
  sets a title over the column); `---` lines between the parts; headings
  (a column starts at each heading of the highest level there, text
  before the first one goes across above the columns); pictures, charts,
  diagrams and galleries next to other blocks (the pictures in one column,
  the rest in the other, on the side the first block is on); otherwise
  each block is a column of its own.
- Any block fits in a column: headings, lists, code, tables, pictures,
  charts, diagrams. `{width=40%}` under a column's only block sets that
  column's width (the block then fills its column);
  `{widths="60 40"}` under the closing `:::` sets them all.
- On a slide's heading, `layout=` does the same for the slide's content:
  `columns` or `comparison` (columns by sub-headings, else by blocks),
  `two-column`, `image-right` / `image-left` (pictures, charts and
  diagrams in a column at that side, the rest in the other).
  `widths="60 40"` goes on the heading too.
- `valign=center` (or `layout=center`) or `valign=bottom` on a slide's
  heading sets the content under the title in the middle or at the foot
  of the room under it; `valign: center` in the front matter does it for
  every slide that does not say `valign=` itself.
- `layout=section` (also `layout=title`): the title and the lines under it
  together in the middle of the slide, centred.
- `layout=statement`: the text under the title set at the title's size, in
  the middle of the slide; the title is not drawn but still names the
  slide. A statement slide with no text under it shows its title larger,
  in the middle.
- `{float=top-right width=8%}` under a picture (also `top-left`,
  `bottom-right`, `bottom-left`) sets it in that corner of the slide, out
  of the flow: the title and the text after it go beside it. Write it right
  under the slide's heading.
- `{width=50%}` under a picture, chart, table or code block puts the
  blocks after it beside it (paragraphs, lists, quotes), down to its
  bottom.
- Text beside a picture or chart, or in a column, is set as large as the
  slide has room for, as text alone on a slide is.

## Style is yours to choose

Nothing in this guide is a house style or a recommended structure: what
the deck says, which slides it has, how it opens and what it looks like
are up to the content and the person asking. The notes below say what
options do, not when to use them.

- `{.lead}` under a paragraph sets it as a lead line (larger, the theme's
  accent colour).
- A ```` ```stats ```` fence draws each `Number: label` line as a card with
  the number large; ```` ```cards ````, ```` ```process ````,
  ```` ```timeline ```` and ```` ```swot ```` are the other list layouts
  (see below).
- Chart colours come from the theme unless a chart sets its own. In a
  chart coloured by a computed group, the groups take the palette in
  alphabetical order unless the colour encoding gives `"sort": [...]`.
- Over about 12 bars, horizontal bars (category on `y`, `"sort": "-x"`)
  keep the names readable.
- A table that runs a few rows over its slide is set smaller by itself.
- `lang: fi` (or `sv`, `de`, `fr`…) in the front matter writes the charts'
  numbers that language's way.
- Look at `render_overview` before saying the deck is done: nothing cut,
  too small or overlapping.

## Pictures

Write `![Alt text](media/<name>)` and pass the picture in the tool call's
`images` list with the same `name` (for example `name: "team.jpg"` →
`![](media/team.jpg)`). A picture can also cover the slide:
`## Title {bg=media/cover.jpg bg-dim=0.4}`. Give each picture either a
public `https` URL or base64 data, or an SVG's (or a SmartArt file's) source
as `text` (readable, no base64). PNG, JPEG, GIF, WebP and SVG, up to 5 MB each
and at most 20 pictures in one call (send more with update_presentation).
A Sliqtly server on the user's own computer started with import folders
(`SLIQTLY_IMPORT_DIRS`) also takes `path`: the absolute path of a file in
one of those folders, e.g. `{ "name": "cover.jpg", "path":
"/Users/me/photoalbum/cover.jpg" }`; the server reads it from disk. The
tool's description of `path` names the folders; when it has no `path`, the
server reads no files.
A picture written with a web address (`![Logo](https://…/logo.png)`, or
`![Logo][id]` with `[id]: https://…`) is fetched into `media/` when the deck
is saved and the Markdown is pointed at it: a slide shows only pictures kept
with the deck.
Pictures, data files and workbooks are stored only when the user is signed
in; without sign-in a deck is text only and is deleted 7 days after its
last change.
Limits: without sign-in, 3 presentations per conversation and 20 slides
each; signed in, 50 presentations per account and 100 slides each (on a
shared server the end of this guide says how many more you can create:
read it before writing a deck); a
presentation's pictures and files together up to 200 MB. render_slide,
render_overview and export_presentation are counted a day (100 without
sign-in, 500 signed in), two at a time.
**The Sliqtly cloud (sliqtly.com) is an experimental demo, not for private
or confidential data.** A presentation is seen by anyone who has its link:
every slide, picture and file. Say so to the user when you give the link.
Signed in, `visibility: "private"` keeps one for the user's Google account
only (it opens at its link after signing in there with that account);
`visibility: "link"` opens it again.
`delete_presentation` (deck_id) deletes one for good, pictures and files
too: its owner signed in, or the conversation that made it without sign-in.
Ask the user first.
When text on a picture is hard to read ("low contrast"), raise that slide's
`bg-dim` (0.6–0.8) with update_presentation, or put the text on a plate
(`{container=box}` under it, a chart's fence too); the picture stays as
it is.

SVG pictures, backgrounds included:

- `bg=media/x.svg` works like any picture: it covers the slide, scaled to
  fill it with its middle kept and the rest cut off (as
  `preserveAspectRatio="xMidYMid slice"`). Give a background the slide's
  shape: `viewBox="0 0 1920 1080"` for a 16:9 slide.
- The root needs `xmlns="http://www.w3.org/2000/svg"` (a browser draws an
  SVG without it as nothing); a root without it gets it, and `xmlns:xlink`
  when `xlink:` is used undeclared, when the picture is saved. Give it a
  `viewBox`.
- Shown as a picture, an SVG loads nothing from outside itself. A picture
  it links to with a public `https` address (`<image href="https://…">`)
  is fetched into it as a `data:` URL when the SVG is saved (at most 8 in
  one SVG); other addresses are left as they are and not drawn.
- Its text is drawn in each viewer's own fonts. `text_to_path: true` on the
  picture in `images` turns the text into paths in the editor's fonts when
  it is saved, so it looks the same everywhere (it is no longer editable as
  text); or write the words on the slide in Markdown.
- In a chart, a Vega-Lite `image` mark draws the deck's own pictures, SVG
  included: `"url": {"field": "img"}` with values like `"media/logo.svg"`,
  and the mark's `width` / `height` (the picture keeps its proportions
  inside them). A picture the deck does not have is not drawn.
- In PDF and PPTX an SVG picture is a raster picture, not vectors. The deck
  keeps the SVG itself.
- `render_slide` and `render_overview` draw SVG pictures, and the result of
  create/update has a line for each SVG ("SVG ok, viewBox 1920×1080
  (16:9), 14 paths") with a ⚠ for what will go wrong in the player: no
  viewBox, a background not in the slide's shape, things loaded from
  outside, text, filter effects. The result also says what was fixed when
  the SVG was saved.

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
- Without `gallery { columns }` a grid on a slide takes as many columns
  as show the most of its pictures: portraits side by side in tall cells,
  landscapes two by two.
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

### Books

`mode: book` in the front matter makes the deck a book: its pages face
each other in spreads, and the player and the shared link show a spread at
a time.

```yaml
---
mode: book            # slides (default) | book
render: realistic     # flat (default) | realistic
book-start: right     # right (default): page 1 alone on the right; left: 1 and 2 face each other
page: 200x200mm       # the page's size, as for any deck
margin: 14mm          # every edge, or one: margin-top, margin-bottom,
margin-inside: 20mm   # margin-inside (at the binding), margin-outside
---
```

- Each slide is one page. Page 1 is a right-hand page; with
  `book-start: right` the spreads are 1, 2–3, 4–5, …
- `margin-inside` is on the right of a left-hand page and on the left of a
  right-hand one; pictures with `layout=full` are not moved by it. The
  margin keys also work without `mode: book` (inside = left).
- `render: realistic` shows the pages as paper on the shared link and
  while presenting in the editor: a
  shadow at the binding, and a page can be turned by dragging its outer
  edge or with the arrow keys. `flat` shows the two pages side by side.
- The PDF export has single pages (with `@media print`, the print page,
  bleed and crop marks), as a printing house takes them.

## Charts, diagrams, math, tables

- Charts: a ```` ```vega-lite ```` fence with a Vega-Lite JSON spec. Bar,
  line, area, point, arc (pie) and more. Use
  `"background": "rgba(0,0,0,0)"` so the theme shows through. Value labels
  beside bars are a layer of `bar` and a layer of `text` sharing the
  encoding; a `"sort": "-x"` on the shared `y` orders both. Numbers follow
  the front matter's `lang:` (see "Style is yours to choose"). An encoding's
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
  still, no tour). Node shapes, `classDef`/`style` fills, dashed
  borders and the kind of each link (`-.->` dashed, `==>` thick, `--o`,
  `--x`, `<-->`, `~~~` invisible; DOT `style=dashed`, `penwidth`) show in
  every style, and so do the colours a diagram gives its links
  (`linkStyle`, DOT `color`); other colours come from the theme's
  `diagram` rule. A long flow is cut into columns only between two boxes
  joined by a single link, never inside a loop or a branch. A diagram that
  cannot be read is not drawn: its place shows the reason with the line
  number, and the layout report flags it. A Mermaid flowchart is refused
  where Mermaid refuses it (an unclosed `[`, a link that ends at no box).
- Mermaid: `flowchart`/`graph` (TD, TB, BT, LR, RL; every node shape and
  link, `subgraph`, `classDef`/`class`/`style`), `sequenceDiagram`,
  `classDiagram`, `stateDiagram-v2`, `erDiagram`, `mindmap`, `timeline`,
  `journey`, `gantt`, `gitGraph`, `pie`, `quadrantChart`, `xychart`,
  `sankey`, `block`, `architecture`, `kanban`, `requirement`, `C4Context`,
  `packet`, `radar-beta`, `treemap`. `linkStyle n` (or `default`) takes
  `stroke`, `stroke-width` and `stroke-dasharray`; `click` is ignored. A
  node id may contain `-` and `.`; `end` is a keyword, not an id. A
  Markdown label `` "`**Bold** text`" `` is drawn as plain text without the
  marks; `"**Bold**"` without the backticks keeps the asterisks, as in
  Mermaid.

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
  and a long chain cut into columns (or a left-to-right one into rows; not with
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
  An HTML cell reads `style="background:…; color:…; font-weight:bold"`
  and `bgcolor`; `{cells=…}` on the line after `</table>` works for HTML
  tables too. A table inside a cell is drawn as its rows on lines.
  Cells coloured by their text, for a risk or status table: under the table
  `{cells="Suuri=red Korkea=red Keskisuuri=amber Matala=green"}` (the whole
  cell's text, any case; tones `red`, `amber`, `green`, `blue`, `grey` or a
  colour). A matched cell gets a muted tint of the tone and bold, readable
  text, in the PDF, PowerPoint, Word and HTML too. For every table of a
  deck: `table { cell-tones: "Done=green Late=red" }` in `css`.
- Layouts from lists: a ```` ```process ```` fence (chevron steps),
  ```` ```swot ```` (a 2×2 grid of four items: Strengths, Weaknesses,
  Opportunities, Threats), ```` ```timeline ```` (round badges on a line,
  a card under each; a title starting with a short token such as
  `Q1 Kickoff:` or `2027 Launch:` puts the token in the badge, otherwise
  the badges are numbered), ```` ```cards ```` (numbered cards in a row)
  or ```` ```stats ```` (`Number: label`, the number large on a card), one item per
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
  `ppt/diagrams/data1.xml`), send it in `images` as `name: "steps.xml"`
  with the XML as it is in `text` (or base64 in `data_base64`), and
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

  Layouts drawn today, with the items each expects (an item is a point
  connected to the document; a sub-item is connected to an item):

  | Layout | Draws | Items |
  | --- | --- | --- |
  | `process1` | steps with arrows | any; sub-items under their step |
  | `chevron1` | chevron steps | any |
  | `hProcess9` | blocks along one wide arrow | any |
  | `default` | blocks in rows that wrap | any |
  | `vList2` | a bar per item, sub-items as bullets under it | any |
  | `hList1` | a column per item, sub-items under its heading | any |
  | `lProcess2` | a column per item, sub-items as blocks in it | any |
  | `bList2` | blocks in rows (PowerPoint's pictures are not drawn) | any |
  | `cycle2` | items on a circle, arrows round it | any |
  | `cycle4` | quarters of one circle | 4 at most |
  | `radial1` | one item in the middle, ITS sub-items round it | 1 item with sub-items |
  | `venn1` | overlapping circles | any |
  | `target1` | nested rings, the first outermost | 5 at most |
  | `pyramid1` | a triangle cut into levels, the first at the apex | any |
  | `funnel1` | items poured into a funnel, the last comes out | any |
  | `gear1` | meshing gears | 3 at most |
  | `arrow2` | points on a rising arrow, each named under it | 5 at most |
  | `matrix1` | four quadrants | 4 items, or 1 item (the title, in the middle) with 4 sub-items |
  | `hierarchy1`, `orgChart1` | a tree, each item over the items under it | 1 top item; in orgChart1 a point with `type="asst"` is an assistant, beside the line down from its boss |

  Items a layout has no place for are not drawn, and the result names them.
  Another layout id is drawn as `default` and
  the result says so. Colours (`colors=`): `accent0_1` … `accent0_3`,
  `accent1_1` … `accent6_5` (one theme colour), `colorful1` …
  `colorful5` (cycling accents). `accent1_2` is what a diagram has when no
  colours are named, so writing it changes nothing. A diagram with nothing
  beside it and no `width=`/`height=` is laid out across the whole content
  width, and a row of steps (`process1`, `chevron1`) that would be a thin
  strip wraps into rows of three or more; `width=` keeps it in its own box.
  Text that would not read on what is under it (a pale column on a dark
  theme) is set in the theme's paper or ink colour instead. Styles
  (`style=`): `simple1` (the default), `simple2` (a thicker outline),
  `simple3` … `simple5` (a shadow, larger with each; on a dark theme it is
  cast in the theme's ink). Colours come from the theme. A SmartArt from PowerPoint
  also works, taken out of the .pptx as one file: a Flat OPC package
  (`pkg:package`) holding its data, layout, style, colours and, when
  PowerPoint saved one, its drawing. Such a file is drawn as PowerPoint drew it (scaled to the box),
  or laid out again from its own layout when `layout=`, `colors=` or
  `style=` is given. A file that is not a diagram, and a data model with
  no items, is shown as the reason in its place and named in the warnings. The PDF
  carries it as drawn; the PowerPoint export carries it as SmartArt that
  PowerPoint can edit.
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
  `{ "name": "sales.csv", "text": "Region,Revenue\nNorth,120\n" }`, or a
  `path` in the import folders (as for pictures). Each is
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
  or lineart; `options.colorCount` (e.g. 6) sets how many colours. Good for logos,
  icons, drawings and blurry low-resolution pictures; a photo becomes a
  poster-like drawing. Without a deck_id, `image_base64` or `image_url`
  gives the SVG back. `vectorize_image` is on every Sliqtly server; if it
  (or another tool this guide names) is not in your tool list, the
  connector's tool list is older than the server: ask the user to
  reconnect the Sliqtly connector.
- Inline HTML: `<b>`, `<strong>`, `<i>`, `<em>`, `<u>`, `<s>`, `<del>`,
  `<ins>`, `<mark>`, `<code>`, `<kbd>`, `<sub>`, `<sup>`, `<small>`, `<q>`,
  `<abbr>`, `<a href>`, `<span>` and `<br>`; entities (`&amp;`, `&copy;`,
  `&#8364;`). A `style` on a span reads `color`, `background-color`,
  `font-size` (pt, px, em, rem, %, `large`…), `font-weight`, `font-style`
  and `text-decoration`; colours as `#hex`, `rgb()` or names.
- HTML blocks: `<ul>` / `<ol start="3">` with `<li>` (nested too) become
  lists. `<div style="background:#123; color:#fff; padding:16px;
  border-radius:8px">` becomes a `container=box` plate. `<img src alt
  width height>` on a line of its own is a picture like `![alt](src)`
  (a web address is fetched into media/). An `<svg>…</svg>` on lines of its
  own is drawn as a picture; scripts, event attributes, `<foreignObject>`,
  `<image>` and outside links are removed from it. `<iframe>`, `<video>`,
  `<audio>`, `<script>` and `<style>` are not drawn and show as text.
  Tags and styles that are not drawn come back as warnings from
  `update_presentation` / `create_presentation`. `javascript:` links are
  removed.

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
figure  { card-background: #ffffff; box-shadow: 0 6pt 18pt rgba(46,58,99,.12); } /* white cards (stats, cards, swot, timeline) with a soft shadow */
container { background-color: #ffffff; border-radius: 14pt; box-shadow: 0 6pt 18pt rgba(46,58,99,.12); } /* {container=box} plates */
.lead { font-size: 26pt; }
```

Fonts available: `Open Sans`, `Noto Sans`, `Lato` and `Droid Serif` (a
serif). In a list the first of these is used (`Georgia, serif` is Droid
Serif, `sans-serif` Open Sans); a name that is none of them is said in the
warnings. Headings take theirs from `heading { font-family: … }`, not from
`h1`…`h6`. Sizes in `pt` or `in`.

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
table column that wraps its cells, a diagram that could not be read. For a
small diagram it names the side of its place that holds it ("its height
holds it: it is drawn 292×662 px in a place 1704×697 px"). A diagram with a
tour has a `tour:` line in the order the tour goes: stops joined by →, a
branch's ways as ⟨name: stops | …⟩, `(back)` where a way returns to a
branch already passed, • for a box with no words. The report is cheap;
compare it between versions.

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
  { "after_slide": 6, "markdown": "## Next steps\n\n- Pilot in May" },
  { "slide": 9, "after_slide": 2 }
] }
```

- `find` + `replace`: the text exactly as it is in the deck (spaces and
  line breaks too); it must be there once, or add `"all": true`.
- `slide` (number) or `slide_title` + `markdown`: the slide's whole new
  text from its heading; `""` deletes the slide. A slide whose text ran
  over onto the next ones is replaced with all of them.
- `after_slide` + `markdown`: new slides after that one (0 = before the
  first).
- `slide` (or `slide_title`) + `after_slide`, no `markdown`: the slide moves
  there as it is, notes and all (0 = before the first). Nothing is written
  again; a slide whose text ran over moves with all of them.

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

<!-- rooms -->
## Rooms

On this server the presentations are kept in rooms. A room is one whole piece of work: a
task, a Jira ticket, a user story, or another whole such as a project.
People may see rooms called projects (a setting in the editor). Every
presentation has one home room; one made without `room_id` lands in
General, and the user then has to move it by hand.

- Before `create_presentation`, find the room it belongs to:
  `list_rooms` with `query` (a ticket code such as "N11-1234" in the
  title, or words of the title or topic; every word must be in a room's
  name or description) or with `order: "active"` for the rooms worked in
  lately. Suggest the room that fits by its name or description (name a
  second one if two fit) and ask the user; if none fits, ask what to call
  a new one and make it with `create_room`, named as the work is known
  ("PROJ-123 Checkout retry") and with the ticket's summary or link as its
  `description`. Then give `room_id` to `create_presentation`.
- `list_rooms` gives at most 1000 rooms a page; `next_offset` is where
  the next page starts (`offset`). `move_presentation` moves a deck later.
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

<!-- /rooms -->
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
work as they are; a private one needs separate access.

## Result

`create_presentation` returns a share link that opens straight into the
presentation (full screen button, arrow keys). Keep `deck_id` to change the
same deck later with `update_presentation`; the share link stays the same.
The id finds a presentation, it does not let anyone change it: a signed-in
user's is changed by its owner, one made without sign-in only in this
connection's session (until it ends or goes unused for a day).
