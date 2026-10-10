# Copying and pasting slides

Sliqtly copies slides and single elements as plain text that a person can
read and edit, and that Sliqtly reads back when it is pasted. The rules live
in `src/PresClip.rgr` (checked in `PresCheck.clips`); the page's part is in
`web/main.js` ("Copy and paste of slides and elements") and `web/slideclip.js`.

## Copying

| Where the keys are                  | Ctrl/⌘+C copies                                   |
| ----------------------------------- | ------------------------------------------------- |
| the filmstrip                       | the picked slides, else the selected slide        |
| the slide, an element picked on it  | that element (a chart, a diagram, a table, text…) |
| the slide, nothing picked           | the selected slide                                |
| the Markdown or the theme editor    | the selected text, as before                      |

A picture goes on the clipboard with the text, for apps that do not read it
(chat, mail, Word, an image editor): a PNG of the copied slides, one under
another, as the stage draws them at rest, or of the copied element alone (its
box cut from the slide). It is there as `image/png` and as `text/html` with an
inline `<img>`; Sliqtly itself pastes the text. The page writes one
`ClipboardItem` in the key's own turn with promises of its parts, as Safari
requires; a browser without `ClipboardItem`, or one that refuses the write,
gets the text alone.

The same commands are in **Edit** (Copy slide) and in the slide's right-click
menu (Copy slide, Copy the chart…). The old **Copy ▸** submenu is now
**Copy as ▸** (Markdown, Markdown + comments, open comments).

## A whole presentation

**File → Export → Export to Clipboard → Full presentation** (also in the
bar's Export dropdown under Clipboard) copies the whole deck in the same
format: its Markdown as it is (front matter too), the theme CSS as it stands
(edits included, every rule) and every file of the deck but the review
comments, base64. Pasted with Ctrl/⌘+V anywhere in the editor (the
filmstrip, the slide, the Markdown or the theme tab), in this window, another
one or another browser, it asks "Make a new presentation “…” of the copied
one?" and makes one beside the open deck: the same Markdown, CSS and files,
and the same theme picked when that Sliqtly has it.

```
===== Sliqtly clipboard v1: presentation, 12 slides =====
from: Myynti 2026
split-level: 2
theme: aurora

----- presentation: markdown -----
---
title: Myynti 2026
---
…

----- theme: css -----
…

----- file: media/logo.png (image/png, base64) -----
…

===== end of Sliqtly clipboard =====
```

The clipboard takes only so much: a copy whose text would be over 24 MB
(`PresClip.maxText`, measured from the file sizes before anything is read) is
not made at all, so nothing half copied is pasted somewhere. A window says
how large it would be and names the three largest files, and offers the ZIP
export (File → Export → All files (.zip)) instead. A Sliqtly older than this
reads such a copy as one slide's worth of Markdown and the styles.

## The format

```
===== Sliqtly clipboard v1: 2 slides =====
from: Myynti 2026
split-level: 2

----- slide 1: markdown -----
## Tulos {.iso}
…

----- slide 2: markdown -----
## Seuraavaksi
…

----- styles: css -----
.iso { font-size: 64px; }

----- file: media/logo.png (image/png, base64) -----
iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6
kgAAAABJRU5ErkJggg==

===== end of Sliqtly clipboard =====
```

- The first line names what is in it: `N slides`, `1 element (chart)` or
  `presentation, N slides`.
- Lines before the first section are `key: value` notes: `from` (the
  presentation it came from), `theme` (a whole presentation's theme) and
  `split-level` (the heading level its slides break at; a paste into a deck
  that breaks at another level moves the headings so each slide stays a
  slide).
- A section line is `----- name -----`. A name with `css` in it is styles,
  one with `markdown` in it a slide's Markdown (or the element's), `file:`
  a file. A name Sliqtly does not know is skipped, so later versions can add
  sections; Windows line ends and a byte order mark are read too.
- **styles** are only the theme rules the copied Markdown can use: rules whose
  selector names a class or an id the Markdown has (`{.iso}`, `::: iso`,
  a heading's `{#id}` or its words, as in a slide scope `#tulos h2`), and the
  `:root` variables those rules read.
- **files** are the presentation's files that the Markdown or those rules
  name, base64 at 76 characters a line.

## Pasting

What the clipboard holds is read first: this format, a Mermaid, PlantUML or
Graphviz diagram, a Vega-Lite (or Vega) spec, Markdown, or plain text.

| Where the keys are     | What goes in                                                                                   |
| ---------------------- | ---------------------------------------------------------------------------------------------- |
| the filmstrip / slide  | slides: after the selected slide; an element, a diagram or text: onto the selected slide (after the picked element, else above the speaker notes) |
| the Markdown editor    | the format's Markdown alone at the caret; a diagram or spec as its fenced block, unless the caret is already inside a fence |
| the theme (CSS) editor | the format's styles alone at the caret                                                         |

Sliqtly always asks before such a paste ("Add 2 slides as slides 5–6?"), and
says how many style rules and files come along. Ordinary text pasted into the
editors goes in as before, without a question.

**Paste without formatting** (Ctrl/⌘+Shift+V, Edit menu, the slide menu)
leaves the styles out.

### The presentation pasted into wins

- A pasted rule is added (at the end of the theme, under
  `/* Pasted from … */`) only when every selector in it names a class or an
  id that this presentation neither styles nor uses, so it can only reach
  what was pasted. A rule for a selector this presentation has, for an
  element (`h1`, `p`, `page`), or for a name it already uses is left out, and
  so is a `:root` variable it sets. `@import` and `@font-face` come along
  once; an `@media` block only when all of its rules would.
- A file with the same name and the same bytes is the same file. A different
  data file keeps this presentation's. A different picture asks: **Replace**
  it, or **Keep both** (the pasted one becomes `logo-2.png` and the pasted
  slides point at it).

### Undo

A paste is one edit of the Markdown and one of the theme. Ctrl/⌘+Z (or Edit →
Undo) takes both back together, from either editor or the filmstrip, and the
files it added are taken out again (a replaced picture gets its old bytes
back); Redo puts all of it back.
