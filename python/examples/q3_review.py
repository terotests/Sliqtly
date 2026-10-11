"""A deck written for python-pptx, saved as a Sliqtly deck.

    PYTHONPATH=python python3 python/examples/q3_review.py /tmp/out
"""

import os
import sys

from pptx import Presentation
from pptx.chart.data import CategoryChartData, XyChartData
from pptx.dml.color import RGBColor
from pptx.enum.chart import XL_CHART_TYPE, XL_LEGEND_POSITION
from pptx.enum.shapes import MSO_SHAPE
from pptx.enum.text import PP_ALIGN
from pptx.util import Inches, Pt

from sliqtly import animate, transition

out = sys.argv[1] if len(sys.argv) > 1 else "/generated"
os.makedirs(out, exist_ok=True)

prs = Presentation()
prs.slide_width = Inches(13.333)
prs.slide_height = Inches(7.5)
prs.core_properties.title = "Q3 Review"

# a title slide
slide = prs.slides.add_slide(prs.slide_layouts[0])
slide.shapes.title.text = "Q3 Performance"
slide.placeholders[1].text = "Board meeting, October 2026"

# bullets in the body placeholder, with speaker notes
slide = prs.slides.add_slide(prs.slide_layouts[1])
slide.shapes.title.text = "Highlights"
tf = slide.placeholders[1].text_frame
tf.text = "Revenue +18% to $4.2M"
for text, level in [("Gross margin +4 pp", 0), ("Enterprise pipeline +31%", 0), ("Nordics led the growth", 1)]:
    p = tf.add_paragraph()
    p.text = text
    p.level = level
animate(slide.placeholders[1]).rise()
slide.notes_slide.notes_text_frame.text = "Start from the pipeline: it explains Q4."

# a chart with text beside it
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Revenue by country"
data = CategoryChartData(number_format="#,##0")
data.categories = ["Finland", "Sweden", "Norway", "Denmark"]
data.add_series("Q2", (110, 150, 120, 90))
data.add_series("Q3", (120, 180, 150, 95))
chart = slide.shapes.add_chart(XL_CHART_TYPE.COLUMN_CLUSTERED, Inches(0.6), Inches(1.6), Inches(7.5), Inches(5), data).chart
chart.has_legend = True
chart.legend.position = XL_LEGEND_POSITION.BOTTOM
chart.legend.include_in_layout = False
chart.plots[0].has_data_labels = True
box = slide.shapes.add_textbox(Inches(8.6), Inches(1.6), Inches(4.2), Inches(5))
box.text_frame.word_wrap = True
p = box.text_frame.paragraphs[0]
p.text = "Sweden is the largest market."
p = box.text_frame.add_paragraph()
run = p.add_run()
run.text = "Norway grew fastest: +25%"
run.font.bold = True
run.font.color.rgb = RGBColor(0x2E, 0x7D, 0x32)
animate(box).fade(duration=0.6)

# three metric cards over a line chart
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Key numbers"
for i, (label, value) in enumerate([("Revenue", "$4.2M"), ("Margin", "38%"), ("NPS", "61")]):
    card = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.6 + i * 4.2), Inches(1.6), Inches(3.8), Inches(1.4))
    card.fill.solid()
    card.fill.fore_color.rgb = RGBColor(0x1F, 0x3A, 0x5F)
    card.text_frame.text = value
    card.text_frame.paragraphs[0].font.size = Pt(32)
    card.text_frame.paragraphs[0].alignment = PP_ALIGN.CENTER
    p = card.text_frame.add_paragraph()
    p.text = label
    p.alignment = PP_ALIGN.CENTER
trend = CategoryChartData()
trend.categories = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep"]
trend.add_series("Revenue", (300, 320, 310, 360, 380, 400, 420, 430, 470))
trend.add_series("Cost", (250, 255, 260, 270, 265, 280, 285, 290, 300))
chart = slide.shapes.add_chart(XL_CHART_TYPE.LINE_MARKERS, Inches(0.6), Inches(3.3), Inches(12.1), Inches(3.8), trend).chart
chart.has_title = True
chart.chart_title.text_frame.text = "Monthly revenue and cost (k$)"
transition(slide, "zoom")

# a comparison layout
slide = prs.slides.add_slide(prs.slide_layouts[4])
slide.shapes.title.text = "Before and after"
slide.placeholders[1].text = "Before"
slide.placeholders[2].text_frame.text = "Slides made by hand\nNumbers copied from Excel"
slide.placeholders[3].text = "After"
slide.placeholders[4].text_frame.text = "Slides generated from data\nCharts update with the data"

# a table, a pie and a scatter chart
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Market share"
table = slide.shapes.add_table(5, 3, Inches(0.6), Inches(1.6), Inches(6), Inches(3)).table
rows = [("Country", "Revenue", "Share"), ("Sweden", "180", "33%"), ("Norway", "150", "27%"),
        ("Finland", "120", "22%"), ("Denmark", "95", "18%")]
for r, row in enumerate(rows):
    for c, value in enumerate(row):
        table.cell(r, c).text = value
        if c > 0:
            table.cell(r, c).text_frame.paragraphs[0].alignment = PP_ALIGN.RIGHT
pie = CategoryChartData(number_format="0%")
pie.categories = ["Sweden", "Norway", "Finland", "Denmark"]
pie.add_series("Share", (0.33, 0.27, 0.22, 0.18))
chart = slide.shapes.add_chart(XL_CHART_TYPE.DOUGHNUT, Inches(7), Inches(1.6), Inches(5.7), Inches(5), pie).chart
chart.has_legend = True
chart.has_title = False

slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Price and volume"
xy = XyChartData()
s = xy.add_series("Products")
for x, y in [(10, 120), (15, 100), (20, 90), (25, 60), (30, 52), (35, 40)]:
    s.add_data_point(x, y)
chart = slide.shapes.add_chart(XL_CHART_TYPE.XY_SCATTER, Inches(0.6), Inches(1.6), Inches(12), Inches(5.4), xy).chart
chart.category_axis.has_title = True
chart.category_axis.axis_title.text_frame.text = "Price (€)"
chart.value_axis.has_title = True
chart.value_axis.axis_title.text_frame.text = "Units sold"

# Sliqtly's own blocks
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "How the deck is made"
slide.shapes.add_mermaid("flowchart LR\n  A[CSV] --> B[Python]\n  B --> C[python-pptx API]\n  C --> D[Sliqtly deck]")

path = prs.save(os.path.join(out, "q3-review.pptx"))
print("saved", path)
for w in prs.warnings:
    print("warning:", w)
