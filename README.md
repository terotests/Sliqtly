# Sliqtly

A presentation editor where the source is Markdown and the rendering is EVG.
The Markdown is on the left, in Ranger's code editor. On the right are the
slides, a timeline for each slide and a filmstrip. Everything on the canvas is
drawn by EVG through WebGL 2. The toolbar and the browser APIs are plain HTML.

The plan is in [PLAN_UI.md](PLAN_UI.md). This is **phase 1**: the frame, the
syntax, the player, pictures and effects. Speech (TTS) and the agent come
later.

```
npm install          # playwright-core, only for npm run check:web
npm start            # builds when needed, serves http://localhost:8770/
npm run check        # deck + timeline checks under Node
npm run check:web    # the page in headless Chromium (build, typing, presenting, exports)
```

Ranger is cloned into `.deps/Ranger` on first run, from the branch in
`presentation.config.json`. It points at `claude/laughing-lamport-77aaec-nifty`,
the Ranger line Sliqtly is built on (well ahead of `master`, not merged into
it); set it back to `master` once it is. To use an existing
checkout instead, set `RANGER_DIR=/path/to/Ranger`. `src/` is linked into the
checkout as `gallery/presentation`, the same way EvgHarness does it, and
compiled with Ranger's own compiler.

The UI controls (UiHost, the menus, the windows, the crop control…) come
from [terotests/EVGUI](https://github.com/terotests/EVGUI), not from
Ranger's `gallery/ui` (Ranger is the compiler; its gallery holds examples).
EVGUI is cloned into `.deps/EVGUI` from the ref in `presentation.config.json`
(`EVGUI_DIR=/path/to/EVGUI` uses a checkout of your own) and linked into the
Ranger checkout as `gallery/evgui`; `src/` imports it as `../evgui/src/`.

Every script that builds checks the clones in `.deps` (Ranger, EVGUI,
RangerFlow, RangerMarkdown, RangerPPTX, RangerDiff) against their branch on
GitHub and fetches the new head when the branch has moved, so a build never
compiles against an old copy of `main`. Offline, the clones are used as they
are. A checkout of your own (`*_DIR`) is never updated for you.

EVG (`lib/evg`) is no longer tracked in Ranger: Ranger's `npm run deps`
fetches it from [terotests/evg](https://github.com/terotests/evg) at the
commit its root `ranger.json` pins. `npm run setup` (and every script that
needs Ranger) runs it in the checkout when the checkout has it, so
`src/ranger.json` keeps `"evg": { "path": "../../lib/evg" }` and the slides
get the same EVG as the Ranger modules they import.

## Markdown

````markdown
---
transition: fade        # the default for every slide: fade | slide | zoom | none
seconds: 0.6            # transition length
step: 1.2               # seconds between build steps when played
hold: 2.5               # seconds after the last step
fx: starfield           # optional default surface effect
style: cartoon          # optional look for every diagram and chart: mermaid | jurassic | cartoon | romantic
chart-style: neon        # optional, charts only (flat | forge | neon | glass or a look); File → Document settings → Style
diagram-style: sketch    # optional, diagrams only (sketch or a look)
---

## Title {#id transition=slide seconds=0.5 fx=starfield fx-density=1.6 duration=8}

1. Revealed
2. one item
3. at a time
{.build anim=rise}

A paragraph that animates in
{anim=zoom seconds=0.8}

::: notes
The speaker's words. [[1]] marks where step 1 lands (used later by speech).
:::
````

- A slide starts at `#` and `##` (the aurora theme sets `deck { split-level: 2 }`).
- **Header and footer** on every slide, from the front matter or the theme.
  Each edge has three places, `-left`, `-center` and `-right`; `{page}`,
  `{pages}` and `{title}` are filled in, and a place can hold a picture
  written as in the text:

  ```yaml
  header-left: "{title}"
  header-right: ![](media/logo.png)
  footer-left: Luottamuksellinen
  footer-right: "{page} / {pages}"
  header-skip: first last      # no header on the cover and the last slide
  footer-skip: first           # first, last, slide numbers (3 7)
  footer-background: "#1e1b4b" # a band to the slide's edge
  ```

  Also `header-color`, `header-size`, `header-image-height` (and the
  footer's). `## Title {header=off}`, `{footer=off}` or `{furniture=off}`
  leaves a slide without it. In the theme: `footer { content-left: "…";
  content-right: "{page}"; color; font-size; font-weight: bold;
  background-color; border-color (a hairline); height (the picture) }`. A
  tall header or logo widens the margin so it does not cover the content.
  The stage, the PDF and the PPTX draw the same header and footer; it is
  laid out by Ranger's markdown module (`MdLayout.emitHeadFoot`).
  `samples/raportti.md` shows it. **Document settings** (File menu, the
  page picked on the slide, or the popover of a front matter line) edits
  these keys, the title and the transition in a window and writes them back
  into the front matter (`PresDocSettings.rgr`).
- **A book**: `mode: book` in the front matter shows the slides as the pages
  of a book, two at a time: page 1 alone on the right (`book-start: left`
  pairs 1 and 2), then 2–3, 4–5. `margin`, `margin-top`, `margin-bottom`,
  `margin-inside` (at the binding) and `margin-outside` set the page's
  margins; a right-hand page has its inside margin on the left. Which page
  faces which and the mirrored margins are RangerMarkdown's (`MdBook`);
  the stage shows the page being edited with the one facing it, presenting
  goes a spread at a time ("2–3 / 12", `src/PresBook.rgr`), and the shared
  viewer shows spreads too. `render: realistic` draws the book as paper,
  in the viewer and while presenting in the editor, and turns a page
  dragged by its outer edge round a cylinder (`web/book.js` the geometry,
  `web/bookturn.js` a turn between frames, `web/bookgl.js` the WebGL). The
  PDF keeps single pages.
- A slide with room is set larger: text, headings and spacing together, up to
  1.6×, as long as it fits and a one-line title stays one line. Slides with a
  picture or a diagram keep their sizes. `slide-grow: 1.3` in the front matter
  changes the limit, `slide-grow: off` turns it off.
- `anim`: `fade`, `rise`, `fly` or `zoom`.
- `fx`: EVG surface effects (`starfield`, `plasma-wave`, `smoke`,
  `ambient-light`, `liquid-glass`, `drops`, `raindrops2`, `bubbles`). `fx-<name>=<number>`
  is the same as `evg-fx-<name>` in CSS. `drops` is rain on a window: the
  pane is dry when the slide comes on, drops land, run together and run down
  leaving trails (`fx-rain`, `fx-size`, `fx-mist`, `fx-speed`, `fx-refract`).
  `raindrops2` is the same rain where a running drop leaves a line of water,
  stops at its lower end when spent and runs on when another drop runs down
  the line into it; a line empties from its top down into the drop at its
  foot, which then runs on. What lands is mostly very fine and dries away,
  smallest first, leaving a matte haze of specks that running drops wipe up
  (`fx-spread`, `fx-dry`); drops a few pixels across do not glint (`fx-matte`).
  No drop grows past a maximum size, however many it takes in, and one heavy
  enough to run goes at once. The letters are in the way, by their own shapes:
  a drop goes round them, and held up long enough its water seeps through a
  letter and runs on below it (`fx-text=0` lets the rain fall over the text).
  `bubbles` is the round-drop effect that was called `raindrop`, and
  `raindrop` still draws it. An effect's clock is the time its slide has been
  on screen and goes on while the slide waits for a click.
- Diagrams (```mermaid, ```dot, ```d2, ```plantuml, ```xstate) are animated on the
  slide as one guided pass. The camera zooms in on each box as it appears and
  holds it for its reading time (15 characters a second, at least 1.3 s). A
  packet of light then travels the next arrow and draws it as it goes. The
  arrow's time includes its label. A walk that ends at an end box stays on it;
  one that ends in a loop pulls back to the whole diagram. The layout is chosen among left-to-right, top-to-bottom, and a long
  top-to-bottom flow cut into columns by the flow engine (`FlowWrap` in
  Ranger's rangerflow). The one that draws the boxes largest in the space on
  the slide wins. The default style is `holo`: glass
  panels and curved arrows. Only the arrow being travelled glows and moves, in a
  warm orange; the others are thin, dim and still, so the eye has one place to
  go. A grid moves with the camera. Under the
  fence: `{style=sketch}` (hand-drawn), `{style=mermaid|jurassic|cartoon|romantic}`
  (RangerFlow's looks, `core/FlowLook.rgr`: pastel cards, a poster, speech
  bubbles, black and terracotta; each has a light- and a dark-ground
  variant, and its faces are fetched the first time a deck uses it), `{tour=off}` (everything drawn at
  once), `{zoom=3}` (the largest scale a box is drawn at),
  `{layout=keep}` (the direction as written) or `{diagram=classic}` (the
  original drawing, still, on the slide's background). PDF and PPTX use the original drawing.
- A sequence diagram is shown whole, with its messages as straight rows and
  no tour.
- TeX math: `$…$` in a line, `$$…$$` as a display (on its own line, or right
  under the sentence that introduces it), and ```math fences. The subset
  covers scripts, fractions, roots, sums and integrals with limits,
  `\left…\right`, accents, `\text`, `\mathbf` / `\mathbb` / `\mathcal`,
  `\color`, and the `matrix` / `pmatrix` / `cases` / `aligned` environments.
  Formulas are drawn as outlines of the KaTeX fonts, so the stage, the PDF
  and the PPTX show the same shapes with no font to install. `$5` and `$10`
  stay text.
- HTML in the Markdown: `<mark>`, `<u>`, `<s>`, `<sub>`, `<sup>`, `<kbd>`,
  `<abbr>`, `<small>`, `<br>` and colours in a `style` change the text instead
  of showing as markup; `<div>`/`<p>`/`<center>` with `align` or
  `text-align` align what is inside them; `<table>` is drawn with `rowspan`,
  `colspan` and `<thead>`/`<th>` headers. In the PPTX these HTML blocks are
  drawn shapes, and inline formatting inside native text boxes is plain.
- A list item is never cut between two slides. When a slide runs over by a
  little (at most 30 % of a slide), it is set up to 20 % smaller instead of
  getting a continuation slide.
- Emoji in the PDF come from Noto Emoji (monochrome), which the page loads
  in the background after start-up; the stage keeps the browser's colour emoji.
- Hints in the editor: hover over (or click) a value in an attribute block
  (`{fx=starfield}`), the front matter, a class, a fence's language or a
  theme's CSS property, and a popover says what it does and offers the
  alternatives — a list for a fixed set, a colour picker, a slider with a
  number, the faces there are — plus the effect's parameters to add and a
  button to remove the item. A choice is an ordinary edit (Ctrl+Z undoes it).
  The themes are written one declaration per line.
- **? Ohje** opens a panel listing what the selected slide is made of — a
  highlight, key caps, a list, a table, formulas, a chart — each with how it
  is written and the theme rules that style it (`mark { background-color;
  color }`, `kbd { background-color; border-color }`, `list`, `code`, …) at
  their current values. Only what the slide has is listed. Clicking a
  property opens the theme at that line (adding it if the theme has none)
  with its value popover.
- **Layouts from lists.** A plain Markdown list with
  `{list-style=process}`, `swot` or `timeline` under it draws chevron steps,
  a SWOT grid or a timeline from its `Title: description` items (an indented
  `- point` belongs to the item above). The theme can say it instead:
  `#heading-anchor list { list-style: swot }` for the lists under one
  heading, `.swot { list-style: swot }` for a list marked `{.swot}`. A fence
  named `process`, `swot` or `timeline` with one item per line draws the
  same. They are computed by Ranger's markdown module (`MdFigure`) in the
  theme's colours (`figure { colors: #a #b #c #d }`, else the chart's), set
  as large as the room under the heading allows, and go into the PDF and
  the PPTX as vector shapes and text boxes. `{art=waves}` on a heading (or
  `art: waves` in the front matter) draws line art behind the slide,
  `{art-seed=3}` another picture of it, `{art=off}` none; in the PPTX it is
  a group of strokes at the back. Signed out (not PRO) a fence's slot says
  so and no art is drawn (`PresApp.setPro`, from `liveAllowed()` in
  main.js). `samples/mallit.md` shows them.
- A slide's background effect (`{fx=…}`) goes into the PDF and the PPTX as a
  picture: before an export each effect is drawn in the browser at the moment
  the thumbnails show it (two seconds in; `drops` and `raindrops2` thirty, when it has rained
  for a while), and put under the slide's content (PDF) or as the slide
  background (PPTX). Text and shapes stay editable on top. Drops, bubbles and
  liquid glass are drawn over the bare paper there, so in the exports the
  drops do not bend the text as they do on the stage.
- Pasting a whole document (into an empty editor or over a select-all) opens
  it at the first slide.
- In the theme, hovering or clicking a selector (`page`, `code`, `.lead`…)
  lists the properties it has and the ones it can have, with what each does;
  a click adds or opens one with its value popover.
- **Live chart data** (PRO): a chart's or a table's data can be a CSV/JSON URL or a
  Google Sheet (`"data": {"source": "google-sheets", "id": "…", "range":
  "Monthly!A:B"}`), fetched each time the presentation opens and again with R
  (or ⟳) while presenting. PDF and PPTX are snapshots. See
  [docs/live-data.md](docs/live-data.md), which also has the V2–V4 roadmap.
- **Chart settings**: a click on the `vega-lite` word of a ```` ```vega-lite ````
  fence, or a double click on the chart on the stage, opens a movable window
  built from EVGUI controls in the EVGUI playground's light look
  (`WindowCtl`, `TabsCtl`, `SliderCtl`, `SwitchCtl`, `ButtonCtl`, `InputCtl`):
  - *Kaavio*: the width and height (sliders), the kind (20; the ones the
    table cannot make are disabled and say why), the title and the legend.
  - *Ulkoasu*: a palette (one series of bars gets a colour per bar), or one
    colour and the text colour from EVGUI's colour picker (`ColorPickerCtl`,
    copied into `src/`, drawn by `PresColorPanel` as EVGUI's demo draws it:
    the area, hue and alpha, HEX / RGB / HSL fields, presets) in a card beside
    the window; the stage style (flat / forge / neon / glass, or one of the
    diagrams' looks: mermaid / jurassic / cartoon / romantic), glow, shadow and
    gradient, and a line's width.
  - *Tiedot*: the data as a table, categories down the side and a column per
    series, rows and series added and removed.

  Every change rewrites the fence through the spreadsheet's chart generator
  (gallery/datagrid `ChartData.specJson`, drawn by Vela), so the slide is the
  preview; Ctrl+Z undoes it after the window is closed. The stage settings
  travel in the spec's `usermeta`; other renderers ignore them. The theme can
  turn the effects on for every chart: `chart { chart-effects: glow gradient; }`.
  A chart in a look (`PresChart.dressLook`) takes the look's series colours,
  bar corners, outline, shadow and faces from RangerFlow's `FlowLook`; the
  chart's own choice wins, then the document's `style:` in the front matter,
  then the theme's `chart-style`. A diagram with no `{style=…}` of its own
  takes the document's `style:` too.
  An axis title longer than its axis is set a little smaller, then on two
  lines (Vela `VlText.fitTitle`, document mode), and cut only when it would
  get very small; the whole title then shows in a tip under the pointer.
  A chart with layers, transforms or data from a URL is not a table and the
  window says so.
- **Live spreadsheets** ([EVGSheets](https://github.com/terotests/EVGSheets)).
  When you drop an `.xlsx`, the import dialog offers *Live spreadsheet*. The
  workbook is kept under `data/` next to one CSV per sheet, and this fence is
  inserted:

  ````markdown
  ```sheet
  data/sales.xlsx
  sheet: Sales
  data: data/sales-Sales.csv
  rows: 8
  ```
  ````

  - **On the slide:** the slide paints the sheet's CSV as a table. Exports,
    thumbnails and transitions use that picture.
  - **While presenting:** once the slide has stopped moving, the workbook itself
    is laid over the box, read-only. It is inert, so the arrows still turn
    slides.
  - **Editing:** *Edit* on the sheet, or **E**, moves the keyboard into the
    sheet, where the arrows move between cells. Typing edits a cell, and a
    short ribbon appears.
  - **Leaving:** *Done*, **Esc** (when no cell is being edited) or
    **Ctrl+Enter** returns the keyboard to the slides. In full screen the
    browser keeps Esc for itself, so there Esc leaves the sheet and full screen,
    not the presentation.
  - **Saving:** an edited workbook is saved back to `data/` together with its
    CSVs, so the next export shows the change.
  - **Accessibility:** a reader hears "A spreadsheet is on this slide. Press E…".
    The open sheet has its own accessibility tree, with the grid, the cells and
    the ribbon.
  - **Files tab:** opening an `.xlsx` there shows the full editor in a dialog.

  EVGSheets is loaded from beside the page (`web/dist/sheets/`) when the build
  finds a built copy (`EVGSHEETS_DIST=<EVGSheets>/dist npm run build`, or
  `.deps/EVGSheets/dist`). Otherwise it comes from `presentation.config.json`
  `evgsheets.base`, which defaults to `https://terotests.github.io/EVGSheets/`.
  The page-side code is `web/sheets-live.js`.
- **Ctrl+V** in the editor pastes a picture from the clipboard. It is stored
  under `media/` and written into the Markdown as `![](media/…)`. Dropping an
  image onto the canvas does the same.
- **Pictures in the Files tab:** hovering a picture's row shows it beside
  the panel with its pixel size. Clicking the row (or *Edit*) opens the image
  editor: the crop frame, brightness, contrast, saturation, warmth and tint,
  each −100…+100 with a live preview. *Save* writes the picture back over the
  same file, so every slide that shows it changes; *Cancel*, **Esc** or a
  click outside leaves the file as it was. The pixel work is
  `web/image-adjust.js`. SVG pictures only get the preview.
- **Drawing on a slide**: a small pill at the foot of the stage (beside the
  Comment bar in review mode): ✎ turns drawing on and off (also Slide → Draw
  on slide, or the slide's context menu), the next button opens the tools
  upward (select, pen, arrow, line, ellipse, text) and the colour dot the six
  colours and three sizes. The drawing is a file of the deck (`drawings/<slide>.ink`,
  JSON in slide units, `PresSketch`) and the slide refers to it like a
  picture, `![](drawings/<slide>.ink)`, which takes no room on the slide;
  deleting that line takes the drawing off. When the slide plays, the items
  appear in the order they were drawn after the slide's content (with its
  build step when the line is inside a `{.build}` block). With the select
  tool (↖) items are picked, dragged, recoloured, deleted (Delete) and moved
  with the arrow keys; a double click edits text; Ctrl+Z undoes. The palette
  is EVGUI's `DrawToolsCtl` (compact), the editing `src/PresSketchUi.rgr`. PDF shows
  the drawings; the PPTX, Word and HTML exports and the MCP server do not
  include them yet.

## Using it

| | |
| --- | --- |
| Click a thumbnail | select the slide (the editor caret moves to it) |
| Click / drag a track | move the playhead within the slide |
| ▶ Play, Ctrl+Enter | play from the selected slide |
| In the editor: ⌃⌘Space (Mac), Ctrl+Shift+Space | the emoji picker at the caret (EVGUI's EmojiPickerCtl): the emojis the slides and their PDF can draw, Recent first; type to search (English or Finnish names), arrows + Enter or a click writes it, Tab changes the group, Esc closes |
| ⛶ Present, F5 | full screen from the start (Shift: from the current slide) |
| While presenting: → / space / click | next build step or slide |
| While presenting: ← | previous slide |
| While presenting: PageDown / PageUp | the next / previous slide shown whole: no build steps, no transition |
| While presenting: Home, ⏮ / End | the first slide from its start / the last slide |
| While presenting: a number + Enter, or press the "3 / 12" counter | that slide, shown whole (Esc forgets the number) |
| While presenting: S | speaker view (next slide, notes, clock) |
| While presenting: A | steps advance by themselves |
| While presenting: Esc | end |
| While presenting: ✎ in the bar | the pen: a press on the slide that moves draws (a press that does not still goes on), the pointer over the slide is drawn as an arrow; the bar's next buttons pick what it draws (line, arrow, straight line, ellipse) and its colour; Backspace or ⌫ wipes the drawing, a new slide starts clean |
| Record ▸ ● Record presentation | asks first: with your voice or without. Then 3, 2, 1 (Esc cancels) and it presents from the start with the pen on; a REC badge at the top shows the time, "no sound" when recorded without, ⏸ Pause and ■ Stop (Esc too), and what can be done. Drag to draw; with the pen's Aa tool, click and type to write on the slide in a hand-written face (Enter ends, Shift+Enter a new line), and while writing letters and Space never change the slide, only ←/→ and PageUp/PageDown. Without Aa the keys stay the presentation's (S the speaker view). The voice and everything the presentation did (slides, build steps, the pointer, what was drawn and written) are kept in the deck as `recordings/take.json` and `recordings/take.webm` (`src/PresRecord.rgr`, `web/recorder.js`); a new take replaces the old one |
| Files: the REC row, Record ▸ ✂ Edit recording… | the recording as one row with ▶ Play, Edit and ✕. Edit shows it on the timeline (the slides, the voice's loudness, what was drawn): drag to mark a part, ✂ Cut leaves it out, ↺ Put back returns a cut part, a click shows the slide as it was then. Cuts are kept in the take; nothing is removed from the sound |
| Record ▸ ▶ Play recording, or the Speech lane | presents again from the recording, from where it first shows the selected slide: Space pauses, ←/→ seek 5 s (⏪ ⏩ 10 s), Esc stops. A shared link's ⋯ menu has it too |
| Record ▸ Voice | the filter the voice plays through: Clean (no rumble, a little presence, even loudness), Warm, Radio, Phone, Echo, Robot, or As recorded. The file stays as spoken |
| At a diagram's question: click, ←/→ + Enter, or 1–9 | choose the way on |
| While playing, at a question: Ohita » (bottom right) | skip to the next slide; untouched, it skips by itself after 8 s, so a loop never traps the room |
| In a diagram: ‹ ring or Backspace | one step back (press again for more) |
| In a diagram: click a box | the camera zooms to it (a class, not the package round it); ‹ at the top left goes back |
| …then ←/→ + Enter, or click a way's label | the ways on from that box: select one (warm), go to the box it leads to |
| Click a line | go to the box it leads to |
| Drag on a diagram | move the view (back returns to where it was) |
| ⋯ at a diagram's top right | Aloita alusta (the walk from the start, choices undone) or Näytä kaikki (every box and line, whole diagram; ‹ leaves) |
| ▶ Play / ⛶ Present | the diagrams start again from their own camera |
| In a diagram: + / − / 100 % buttons, keys + − 0 | zoom in, out; 100 % shows the whole diagram (‹ or a choice returns to the walk) |
| Markdown / Teema (CSS) tabs over the editor | the deck's text, or the theme's stylesheet (Ranger UI TabsCtl). An edit to the theme shows on the slides as you type; the edited theme is kept for the session under its name and travels in a share link (`&css=…`). Chart and diagram colours are CSS: `chart { color: …; accent-color: … }` (columns; grid, axes, labels) and `diagram { color: …; accent-color: … }` (the active line and packet; boxes and lines). `chart { chart-style: … }` picks the look: `flat` (plain columns, the default), `forge` (warm burning into rust, grooves, a glowing cap, a scan pass), `neon` (lit edges round a faint body), `glass` (a clear gradient with a bright rim); the PDF and PPTX exports draw the same style. The older `/* pres: warm=… accent=… */` comment still works as a fallback |
| Teema | dark themes aurora, nebula (starfield), carbon, ember (ambient light), midnight; light corporate, editorial. A theme sets chart and diagram colours with `chart { … }` / `diagram { … }` rules and its background effect with `deck { fx: starfield; fx-hue: 280 }` (a slide's own `{fx=…}` wins, `fx: none` turns it off; the older `/* pres: fx=… */` comment is still read) |
| 🔗 Jaa | a dialog with two links, each with its own Kopioi: **Esitys** opens straight into the presentation (no toolbar or editor; ◀ ▶ ⛶ in a corner that fades; Esc only leaves full screen), **Muokkaus** opens the editor. The Markdown is compressed into the link (`#md=…`, `&mode=show` for the presentation) |
| PDF / PPTX | export. The PPTX includes notes, transitions and build steps per paragraph, and opens in Keynote; its text is set in Arial (the page's own faces are not on every machine) |

## Structure

| File | Contents |
| --- | --- |
| `src/PresSource.rgr` | Lifts `:::` blocks out of the Markdown (masks them without changing offsets) |
| `src/PresDeck.rgr` | Markdown → slides with the markdown module's layout; slide attributes, groups, the effect layer |
| `src/PresDiagram.rgr` | Diagram animation: the holo and sketch styles, the FlowLook looks, curved edges, reveal, the tour and the camera keyframes |
| `src/PresChart.rgr` | (colours the spec states itself win: a mark `color`, `labelColor`/`titleColor`, `gridColor`/`domainColor`/`tickColor`) A ```vega-lite chart dressed for the stage: grid in the accent, columns of warm light burning into rust with a glowing cap, rising in turn, one scan pass |
| `src/PresInk.rgr`, `src/PresRecord.rgr` | Drawing on the slide while presenting and the pointer; a recording's operations, its file and its replay |
| `web/recorder.js` | The microphone into a file, and the voices it plays through (Web Audio) |
| `src/PresTimeline.rgr` | (deck, slide, t) → display list. Deterministic: no clock of its own |
| `src/PresApp.rgr` | The editor: panels, tracks, filmstrip, presenting, exports |
| `src/PresCheck.rgr` | Node checks |
| `web/` | `index.html` (toolbar), `main.js` (WebGL, clock, keyboard, paste), `pres.css` (EVG chrome), `sheets-live.js` (live spreadsheets: EVGSheets over a ```sheet box, and the .xlsx editor dialog) |
| `samples/*.md` | Example decks: talous (a Vega chart), ymparisto (a wrapped chain), urheilu and ohjelmointi (questions with a loop), kulttuuri (Graphviz). `<key>.en.md` is the English deck, shown unless the interface is in Finnish (`<key>.md`). `esittely.md` is the deck `check:web` drives (`?sample=esittely`) |
| `themes/*.css` | Themes; `aurora` is the default (dark 16:9) |
| `web/sliqtly.js` | PRO: Google sign-in, behind the bar's PRO button |
| `brand/` | The logo as SVG; `make_logo.py` writes them. The build copies the icon as `favicon.svg` |
| `.github/workflows/` | CI checks and the deploys of the hosted service ([docs/operations.md](docs/operations.md)) |
| `scripts/` | setup, build, start (local server), check, check-web |
| `mcp-go/` | The server: MCP for AI assistants, the share store and the web app; Ranger compiled to Go |
| `ops/` | Maintenance scripts for the hosted service |
| `web/connect.html` | How to connect Claude, ChatGPT, Cursor and others to it |

## For AI assistants (MCP)

`mcp-go/` is an MCP server: Claude, ChatGPT, Cursor and other MCP clients make
a presentation from Markdown, a theme, CSS and pictures and get its share link
back. How to connect each client: `web/connect.html`
(`/connect.html`). How it works, tests and deploy:
[mcp-go/README.md](mcp-go/README.md). It is the only MCP server: the Node one
that was `mcp/` is retired, and CI fails if it comes back.

## Languages

The interface is written in English in the source: `PresI18n.t("…")` in
`src/*.rgr`, `t("…")` in `web/*.js`, `data-i18n` attributes in
`web/index.html`. `web/i18n/<code>.json` maps each English string to another
language (`fi.json` now); a missing entry stays English. The language comes
from `?lang=`, the one chosen before with the bar's 🌐 button, or the
browser's preference.

A new language: copy `fi.json` to `<code>.json`, translate the values, add the
code to `LANGS` in `web/i18n.js`. `npm run i18n` lists the strings a table
lacks and the entries nothing uses any more.

## Running the server locally

`npm start` serves the editor alone. `npm run serve` builds and starts the
whole server from the sources (the web app, the MCP endpoint at `/mcp` and
the decks in a local folder); see [mcp-go/README.md](mcp-go/README.md) for
its options.

## Sliqtly Personal

The Personal server runs on your own computer: the MCP endpoint your
assistant writes presentations through, and a viewer that plays them. Out
of the box only the computer itself can connect.

```
# Ubuntu / Debian
curl -fLo /tmp/sliqtly-personal.deb https://sliqtly.com/download/sliqtly-personal_amd64.deb
sudo apt install /tmp/sliqtly-personal.deb

# macOS
brew install terotests/sliqtly/sliqtly
brew services start sliqtly

# Docker
docker run -d --name sliqtly --restart unless-stopped -p 127.0.0.1:8080:8080 \
  -v sliqtly-data:/data -v sliqtly-backup:/backup ghcr.io/terotests/sliqtly-personal:latest
```

Then open http://localhost:8080/ and connect an assistant to
`http://localhost:8080/mcp`. Networks, firewall, backups and the rest:
[sliqtly.com/local.html](https://sliqtly.com/local.html) (`web/local.html`).

## Sharing

Signed out, Share packs the Markdown into the link itself. Signed in (PRO),
a shared deck is stored on the server under a short random id and opened at
`/s/{id}`. A deck is either `link` (anyone with the link) or `private` (its
owner, after signing in); only the owner can change or delete it, and
`/s/{id}?edit` opens it as a new deck of the reader's own.

## Hosting

How sliqtly.com is deployed and configured (hosting, sign-in,
storage rules, the domain) is in [docs/operations.md](docs/operations.md).
