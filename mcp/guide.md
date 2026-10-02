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
  slide), `bg-dim=0.4` (paper laid over it for legibility, 0–1).
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

## Charts, diagrams, math, tables

- Charts: a ```` ```vega-lite ```` fence with a Vega-Lite JSON spec and inline
  `data.values`. Bar, line, area, point, arc (pie) and more. Use
  `"background": "rgba(0,0,0,0)"` so the theme shows through.
- Diagrams: ```` ```mermaid ````, ```` ```dot ```` (Graphviz), ```` ```d2 ````,
  ```` ```plantuml ````. They are animated as a guided tour, box by box.
  Under the fence: `{style=sketch}`, `{tour=off}`, `{layout=keep}`.
- Math: `$…$` inline, `$$…$$` as a display, or a ```` ```math ```` fence (TeX).
- Tables: ordinary Markdown tables, or HTML `<table>` with `rowspan`/`colspan`.
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
.lead { font-size: 26pt; }
```

Fonts available: `Open Sans`, `Noto Sans`. Sizes in `pt` or `in`. Set
`css_mode: "replace"` only when sending a complete stylesheet of your own.

## Result

`create_presentation` returns a share link that opens straight into the
presentation (full screen button, arrow keys) and an edit link that opens a
copy in the Sliqtly editor. Keep `deck_id` and `edit_key` to change the same
deck later with `update_presentation`; the share link stays the same.
