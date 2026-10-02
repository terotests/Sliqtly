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
`presentation.config.json`. It points at `claude/nifty-dijkstra-vq2qit`, which has
TeX math and the sequence-diagram fix; set it back to `master` once that is merged. To use an existing
checkout instead, set `RANGER_DIR=/path/to/Ranger`. `src/` is linked into the
checkout as `gallery/presentation`, the same way EvgHarness does it, and
compiled with Ranger's own compiler.

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
- A slide's background effect (`{fx=…}`) goes into the PDF and the PPTX as a
  picture: before an export each effect is drawn in the browser at the moment
  the thumbnails show it, and put under the slide's content (PDF) or as the
  slide background (PPTX). Text and shapes stay editable on top. Raindrop and
  liquid glass are drawn over the bare paper there, so in the exports the
  drops do not bend the text as they do on the stage.
- Pasting a whole document (into an empty editor or over a select-all) opens
  it at the first slide.
- In the theme, hovering or clicking a selector (`page`, `code`, `.lead`…)
  lists the properties it has and the ones it can have, with what each does;
  a click adds or opens one with its value popover.
- **Chart settings**: a click on the `vega-lite` word of a ```` ```vega-lite ````
  fence, or a double click on the chart on the stage, opens a movable window
  built from gallery/ui controls in the EVGUI playground's light look
  (`WindowCtl`, `TabsCtl`, `SliderCtl`, `SwitchCtl`, `ButtonCtl`, `InputCtl`):
  - *Kaavio*: the width and height (sliders), the kind (20; the ones the
    table cannot make are disabled and say why), the title and the legend.
  - *Ulkoasu*: a palette (one series of bars gets a colour per bar), or one
    colour and the text colour from EVGUI's colour picker (`ColorPickerCtl`,
    copied into `src/`, drawn by `PresColorPanel` as EVGUI's demo draws it:
    the area, hue and alpha, HEX / RGB / HSL fields, presets) in a card beside
    the window; the stage style (flat / forge / neon / glass), glow, shadow and
    gradient, and a line's width.
  - *Tiedot*: the data as a table, categories down the side and a column per
    series, rows and series added and removed.

  Every change rewrites the fence through the spreadsheet's chart generator
  (gallery/datagrid `ChartData.specJson`, drawn by Vela), so the slide is the
  preview; Ctrl+Z undoes it after the window is closed. The stage settings
  travel in the spec's `usermeta`; other renderers ignore them. The theme can
  turn the effects on for every chart: `chart { chart-effects: glow gradient; }`.
  A chart with layers, transforms or data from a URL is not a table and the
  window says so.
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
| `web/sliqtly.js` | PRO: Google sign-in through Firebase Auth, behind the bar's PRO button |
| `brand/` | The logo as SVG; `make_logo.py` writes them. The build copies the icon as `favicon.svg` |
| `.github/workflows/deploy.yml` | On a push to `main`: build, checks, deploy `web/dist` to Firebase Hosting (`sliqtly.web.app`) |
| `scripts/` | setup, build, start (local server), check, check-web |
| `mcp/` | The MCP server for AI assistants (a Cloud Function) |
| `web/connect.html` | How to connect Claude, ChatGPT, Cursor and others to it |

## For AI assistants (MCP)

`mcp/` is an MCP server: Claude, ChatGPT, Cursor and other MCP clients make a
presentation from Markdown, a theme, CSS and pictures and get its share link
back. How to connect each client: `web/connect.html`
(`/connect.html`). How it works, tests and deploy: [mcp/README.md](mcp/README.md).

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

## Deploy (Firebase Hosting)

The project is `sliqtly` (`.firebaserc`); `firebase.json` serves `web/dist`.
The Deploy workflow needs one secret, `FIREBASE_SERVICE_ACCOUNT`: the JSON key
of a service account with the role **Firebase Hosting Admin**.

## Google sign-in (PRO)

`web/sliqtly.js` loads Firebase from Google's CDN and the project's config from
Hosting's reserved `/__/firebase/init.js`, so it works only when served by
Firebase Hosting. In the Firebase Console, once:

1. Project settings → General → Your apps → add a **Web app**.
2. Authentication → Sign-in method → enable **Google**.
3. Authentication → Settings → Authorized domains: add `sliqtly.com` when the
   domain is connected (`sliqtly.web.app` is there already).

## Sharing (PRO)

Signed in, **Share** keeps the deck as its owner's in Firestore
(`decks/{deckId}`) and makes a copy under a short random id
(`shares/{id}`, its pictures and data files in Storage under `shares/{id}/`).
The link is `/s/{id}`: anyone with it sees the presentation, nobody can list
the ids, only the owner can change or delete the copy (`firestore.rules`,
`storage.rules`). `/s/{id}?edit` opens the copy as a new deck of the reader's
own. Signed out, Share still packs the text into the link as before.

Firebase Console, once:
1. Firestore Database → Create database (production mode).
2. Storage → Get started (needs the Blaze plan). When the Storage rules are
   first deployed, allow them to read Firestore if the console asks.
3. The Deploy workflow then deploys both rule files (its last step). The
   service account needs the role **Firebase Rules Admin** (the Admin SDK
   account generated in Project settings → Service accounts has it).
4. The bucket's CORS (`storage.cors.json`): a shared deck fetches its
   pictures and data files from Storage, and without it the browser refuses
   them (the slides open without their background images). Any origin may
   read them, since the preview in an AI assistant runs the viewer on the
   assistant's own domain (mcp/src/preview.html). The Deploy
   workflow sets it when its service account has the role **Storage Admin**;
   by hand, in Cloud Shell:
   `gcloud storage buckets update gs://sliqtly.firebasestorage.app --cors-file=storage.cors.json`

## Domain (Cloudflare)

1. Firebase Console → Hosting → Add custom domain → `sliqtly.com`.
2. Add the TXT and A records Firebase shows in Cloudflare → DNS, proxy status
   **DNS only** (grey cloud), or Firebase cannot verify the domain or issue
   its certificate.
