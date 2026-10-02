# Dashboards on a slide (a sketch, not built)

An operative dashboard slide is a grid of cards: a title, a big number, a
change ("↑ 7 %"), a line of explanation, sometimes a small chart or table,
and coloured cards for emphasis. Markdown has no word for a grid, so this is
how Sliqtly could do it without leaving Markdown.

## 1. A `dashboard` fence with cards in Markdown

````markdown
## Q4 FY26 Performance

::: dashboard columns=4 rows=2
::: card span=1x1 tone=accent
### Total revenues
# $18.7B
Increase of $1.1 billion
:::
::: card
### Industry groups
- Communications ↑ **11 %** $3.3B
- Financial services ↑ **6 %** $3.5B
:::
::: card span=2x1
```vega-lite
…
```
:::
:::
````

- `:::` blocks are already lifted out by `PresSource`; a `dashboard` block
  would be laid out as a grid (EVG's `EVGLayout` already does rows/columns
  and percentages), each card a small Markdown document laid out by the same
  markdown module into its cell, shrunk to fit like a slide.
- `tone=accent|muted|plain` picks the card colours from the theme:
  `card { background-color; color }`, `card.accent { … }`.
- A metric line (`# $18.7B`, `↑ 7 %`) is just Markdown; a `.metric` class
  could set the big-number size, and `↑`/`↓` could be coloured by sign.
- The cards can arrive one by one as build steps (`{.build}` on the
  dashboard), using the existing timeline groups.

## 2. Data in the cards

The live chart data already in place (Google Sheets / CSV / JSON URLs,
`docs/live-data.md`) gives the numbers. A card could bind a value instead of
typing it: `{{ sheet:Q4!B2 }}` or `{{ data/q4.csv | sum revenue }}`, filled
in when the deck opens and on R while presenting. PDF and PPTX would be
snapshots, as charts are now.

## 3. Raw EVG for the rest

For layouts the grid cannot express, an ```` ```evg ```` fence with EVG's
JSX-like markup (the same the EVG showcase pages use) drawn as one block on
the slide. This is the escape hatch, not the main road: it is harder to edit
and AI assistants need the card syntax above to stay reliable.

## Order of work, if it is picked up

1. `::: dashboard` + `::: card` with the grid and theme colours (stage, PDF).
2. PPTX: each card as a group of shapes.
3. Data bindings in cards (PRO, same rules as live chart data).
4. Editor: a card-grid hint popover (columns, span, tone) and the MCP guide.
