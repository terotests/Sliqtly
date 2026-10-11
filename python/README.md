# python-pptx for Sliqtly

`pptx/` is python-pptx's object model in plain Python, with no
dependencies. Code written for python-pptx runs unchanged, and
`prs.save()` writes a Sliqtly deck (Markdown) instead of a `.pptx` file.
AI models already write a lot of python-pptx code, so they can make Sliqtly
decks without learning a new API. `sliqtly/` adds what PowerPoint does not
have, such as animations, Vega-Lite, Mermaid and front matter.

The package runs on CPython and on rpy, RangerPython's interpreter, both
native and WebAssembly, so it can run in the browser with a virtual file
system.

```sh
PYTHONPATH=python python3 python/tests/test_pptx.py
PYTHONPATH=python python3 python/examples/q3_review.py /tmp/out   # /tmp/out/q3-review.md

rpy --mount python=/lib --path /lib python/tests/test_pptx.py
rpy --mount python=/lib --path /lib python/examples/q3_review.py OUTDIR
```

```python
from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.chart.data import CategoryChartData
from pptx.enum.chart import XL_CHART_TYPE
from sliqtly import animate

prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Revenue by country"
data = CategoryChartData()
data.categories = ["Finland", "Sweden", "Norway"]
data.add_series("Revenue", (120, 180, 150))
slide.shapes.add_chart(XL_CHART_TYPE.COLUMN_CLUSTERED, Inches(0.5), Inches(1.5), Inches(6), Inches(4.5), data)
box = slide.shapes.add_textbox(Inches(7), Inches(1.5), Inches(2.5), Inches(4.5))
box.text = "Sweden leads"
animate(box).rise(duration=0.5)
prs.save("/generated/deck.pptx")   # writes /generated/deck.md
```

## Saving

- `prs.save("x.pptx")` writes `x.md`. A name ending in `.md` is kept, and a
  name with no extension gets `.md`. The pictures go to `media/` beside the
  file, and the Markdown refers to them as `media/<name>`. `save` returns
  the path it wrote.
- `prs.save(stream)` writes the Markdown to a file object as UTF-8.
- `prs.to_markdown()` returns the Markdown as a string.
- `prs.warnings` lists what could not be drawn, one entry per slide.

## How a slide becomes Markdown

Sliqtly lays out a slide from its blocks, top to bottom, with columns where
blocks sit side by side. python-pptx places shapes at coordinates instead,
so the shapes are read in reading order:

1. The slide is split into columns where a vertical gap runs through all
   of its shapes (`:::: columns` with `::: col` blocks, and `widths=` from
   the shapes' widths).
2. Otherwise it is split into rows where a horizontal gap runs across. A row
   whose shapes sit side by side becomes columns.
3. Each shape becomes one or more blocks.

Coordinates and sizes are not kept beyond that ordering. Fonts, sizes and
theme colours come from the Sliqtly theme.

| Shape | Markdown |
|---|---|
| title placeholder | `## Title`; the Title Slide layout gives `# Title` |
| no title placeholder | a one-line text box at the top that is not set small (under 20 pt) becomes the title; otherwise `## Slide N {heading=hidden}` |
| body or content placeholder | a list, with `level` as nesting |
| text box | a paragraph per paragraph; paragraphs that all start with `•` or `-` become a list |
| filled autoshape with text | one plate: `{container=box background=#rrggbb}`, paragraphs joined with `<br>` |
| Comparison layout captions | `### Before` / `### After` over the columns |
| Section Header layout | `{layout=section}` |
| run formatting | bold, italic, `<u>`, colour as `<span style="color">`, hyperlinks as `[text](url)` |
| paragraph alignment | `{.center}` / `{.right}`; 28 pt or larger gives `{.lead}` |
| table | a Markdown table with column alignment from the cells; an HTML `<table>` when cells are merged, filled or `first_row` is off |
| chart | a `vega-lite` fence (see below) |
| picture | `![name](media/name)` |
| group | its shapes, laid out by the same rules |
| notes | `::: notes` |

## Supported API

| python-pptx | Support |
|---|---|
| `Presentation()`, `slide_width/height`, `core_properties` | yes; `title` goes into the front matter |
| `Presentation("template.pptx")` | starts from the default template and adds a warning; the theme is Sliqtly's |
| `prs.slide_layouts[0..10]`, `get_by_name` | yes, the 11 layouts of the default template with their placeholders |
| `slides.add_slide`, `slides[i]`, `index`, `get`, `_sldIdLst.remove` | yes |
| `shapes.title`, `placeholders[idx]`, `insert_picture/table/chart` | yes |
| `add_textbox`, `add_shape(MSO_SHAPE.*)` | yes; a shape without text is left out with a warning |
| `add_picture` (path or file object; PNG, JPEG, GIF, WebP, SVG) | yes; a missing width or height keeps the picture's aspect ratio |
| `add_table`, `cell()`, `merge`, `first_row`, cell `fill` | yes |
| `add_chart` with `CategoryChartData`, `XyChartData`, `BubbleChartData` | yes |
| `add_connector` | the object exists; it is not drawn |
| `add_group_shape` | yes |
| `text_frame`, `paragraphs`, `add_paragraph`, `runs`, `add_run`, `level`, `alignment`, `font.bold/italic/underline/color.rgb/size`, `hyperlink.address` | yes |
| `word_wrap`, `auto_size`, `margins`, `vertical_anchor`, `line_spacing`, `font.name`, `fit_text` | accepted; the theme decides |
| `fill.solid()`, `fore_color.rgb` | yes on autoshapes, table cells and chart series |
| `theme_color`, `line`, `shadow`, `rotation`, `adjustments` | accepted; not drawn |
| `notes_slide.notes_text_frame` | yes |
| `Inches`, `Cm`, `Mm`, `Pt`, `Emu`, `Centipoints`, `RGBColor` | yes |
| `pptx.oxml` / XML access (`_element`, `qn`) | no |

### Charts

| Chart type | Vega-Lite |
|---|---|
| `COLUMN_*`, `BAR_*` (clustered, stacked, stacked 100) | `bar`; clustered series side by side with `xOffset` |
| `LINE`, `LINE_MARKERS` (and stacked) | `line`, with `point` for markers and `monotone` for `smooth` |
| `AREA*` | `area` |
| `PIE`, `DOUGHNUT` | `arc`; only the first series is drawn |
| `XY_SCATTER*` | `point`, or `line` for the variants with lines |
| `BUBBLE` | `circle` with `size` |
| `RADAR*` | drawn as lines, with a warning |

The chart title, legend and its position, data labels, number formats
(`'0%'` → `.0%`, `'#,##0'` → `,.0f`), axis titles, `minimum_scale` /
`maximum_scale`, gridlines and series and point colours are carried over.
As in PowerPoint, a chart with one series is titled with the series' name
unless `has_title` is set.

## Sliqtly additions

```python
import sliqtly

sliqtly.animate(shape).rise(duration=0.5)   # fade, rise, fly, zoom; a list builds item by item
sliqtly.transition(slide, "zoom", seconds=0.6)
sliqtly.attrs(slide, fx="starfield")          # any attribute on the slide's heading or a shape's block
sliqtly.deck(prs, lang="fi", footer_right="{page} / {pages}")   # front matter

slide.shapes.add_vega(spec)                   # a Vega-Lite spec, as a dict or JSON text
slide.shapes.add_mermaid("flowchart LR\n  A --> B")
slide.shapes.add_diagram("dot", "digraph { a -> b }")
slide.shapes.add_svg("<svg ...>...</svg>")
slide.shapes.add_markdown("> Sliqtly Markdown as it is")
```

These blocks take the same optional `left, top, width, height` as other
shapes. With no position given, a block goes below what the slide has so
far.
