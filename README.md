# EVG Presentation

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
`presentation.config.json` (`master`). To use an existing
checkout instead, set `RANGER_DIR=/path/to/Ranger`. `src/` is linked into the
checkout as `gallery/presentation`, the same way EvgHarness does it, and
compiled with Ranger's own compiler.

## Markdown

````markdown
---
transition: fade        # the default for every slide: fade | slide | zoom | none
seconds: 0.6            # transition length
step: 1.2               # seconds between build steps when played
hold: 2.5               # seconds after the last step
fx: starfield           # optional default surface effect
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
- A slide with room is set larger: text, headings and spacing together, up to
  1.6×, as long as it fits and a one-line title stays one line. Slides with a
  picture or a diagram keep their sizes. `slide-grow: 1.3` in the front matter
  changes the limit, `slide-grow: off` turns it off.
- `anim`: `fade`, `rise`, `fly` or `zoom`.
- `fx`: EVG surface effects (`starfield`, `plasma-wave`, `smoke`,
  `ambient-light`, `liquid-glass`, `raindrop`). `fx-<name>=<number>` is the
  same as `evg-fx-<name>` in CSS.
- Diagrams (```mermaid, ```dot, ```d2, ```plantuml) are animated on the
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
  fence: `{style=sketch}` (hand-drawn), `{tour=off}` (everything drawn at
  once), `{zoom=3}` (the largest scale a box is drawn at),
  `{layout=keep}` (the direction as written) or `{diagram=classic}` (the
  original drawing). PDF and PPTX use the original drawing.
- **Ctrl+V** in the editor pastes a picture from the clipboard. It is stored
  under `media/` and written into the Markdown as `![](media/…)`. Dropping an
  image onto the canvas does the same.

## Using it

| | |
| --- | --- |
| Click a thumbnail | select the slide (the editor caret moves to it) |
| Click / drag a track | move the playhead within the slide |
| ▶ Play, Ctrl+Enter | play from the selected slide |
| ⛶ Present, F5 | full screen from the start (Shift: from the current slide) |
| While presenting: → / space / click | next build step or slide |
| While presenting: ← | previous slide |
| While presenting: S | speaker view (next slide, notes, clock) |
| While presenting: A | steps advance by themselves |
| While presenting: Esc | end |
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
| Markdown / Teema (CSS) tabs over the editor | the deck's text, or the theme's stylesheet (Ranger UI TabsCtl). An edit to the theme shows on the slides as you type; the edited theme is kept for the session under its name and travels in a share link (`&css=…`). Chart and diagram colours are CSS: `chart { color: …; accent-color: … }` (columns; grid, axes, labels) and `diagram { color: …; accent-color: … }` (the active line and packet; boxes and lines). `chart { chart-style: … }` picks the look: `flat` (plain columns, the default), `forge` (warm burning into rust, grooves, a glowing cap, a scan pass), `neon` (lit edges round a faint body), `glass` (a clear gradient with a bright rim); the PDF and PPTX exports draw the same style. The older `/* pres: warm=… accent=… */` line still works as a fallback |
| Teema | dark themes aurora, nebula (starfield), carbon, ember (ambient light), midnight; light corporate, editorial. A theme sets chart and diagram colours with `chart { … }` / `diagram { … }` rules and its background effect with `/* pres: fx=… */` |
| 🔗 Jaa | a dialog with two links, each with its own Kopioi: **Esitys** opens straight into the presentation (no toolbar or editor; ◀ ▶ ⛶ in a corner that fades; Esc only leaves full screen), **Muokkaus** opens the editor. The Markdown is compressed into the link (`#md=…`, `&mode=show` for the presentation) |
| PDF / PPTX | export. The PPTX includes notes, transitions and build steps per paragraph, and opens in Keynote; its text is set in Arial (the page's own faces are not on every machine) |

## Structure

| File | Contents |
| --- | --- |
| `src/PresSource.rgr` | Lifts `:::` blocks out of the Markdown (masks them without changing offsets) |
| `src/PresDeck.rgr` | Markdown → slides with the markdown module's layout; slide attributes, groups, the effect layer |
| `src/PresDiagram.rgr` | Diagram animation: the holo and sketch styles, curved edges, reveal, the tour and the camera keyframes |
| `src/PresChart.rgr` | (colours the spec states itself win: a mark `color`, `labelColor`/`titleColor`, `gridColor`/`domainColor`/`tickColor`) A ```vega-lite chart dressed for the stage: grid in the accent, columns of warm light burning into rust with a glowing cap, rising in turn, one scan pass |
| `src/PresTimeline.rgr` | (deck, slide, t) → display list. Deterministic: no clock of its own |
| `src/PresApp.rgr` | The editor: panels, tracks, filmstrip, presenting, exports |
| `src/PresCheck.rgr` | Node checks |
| `web/` | `index.html` (toolbar), `main.js` (WebGL, clock, keyboard, paste), `pres.css` (EVG chrome) |
| `samples/*.md` | Example decks: talous (a Vega chart), ymparisto (a wrapped chain), urheilu and ohjelmointi (questions with a loop), kulttuuri (Graphviz). `esittely.md` is the deck `check:web` drives (`?sample=esittely`) |
| `themes/*.css` | Themes; `aurora` is the default (dark 16:9) |
| `.github/workflows/pages.yml` | Publishes `web/dist` to GitHub Pages as a playground (no server features) |
| `scripts/` | setup, build, start (local server), check, check-web |
