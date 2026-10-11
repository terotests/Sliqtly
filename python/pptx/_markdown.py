"""Draws a Presentation as Sliqtly Markdown.

Sliqtly lays a slide out from its blocks, top to bottom, with columns where
blocks sit side by side; python-pptx places shapes at coordinates. The
shapes are read in the order a reader would: the slide is cut into columns
where a vertical gap runs through all of its shapes, else into rows where a
horizontal gap does, and a row whose shapes sit side by side becomes
columns. Positions and sizes are not kept beyond that, nor fonts and
colours the theme sets; what cannot be drawn is named in prs.warnings."""

import json

from pptx.enum.chart import XL_CHART_TYPE as CT, XL_LEGEND_POSITION as LP
from pptx.enum.shapes import PP_PLACEHOLDER as PH
from pptx.enum.text import PP_ALIGN
from pptx.shapes.shapetree import (Connector, GraphicFrame, GroupShape, Picture, Shape, SliqtlyBlock)
from pptx.util import Pt


def render(prs):
    """(markdown, {media name: bytes})"""
    prs.warnings[:] = [w for w in prs.warnings if w.startswith("Presentation(")]
    ctx = _Ctx(prs)
    out = []
    front = {}
    if prs.core_properties.title:
        front["title"] = prs.core_properties.title
    front.update(prs.front_matter)
    if front:
        out.append("---")
        for k, v in front.items():
            out.append("%s: %s" % (k, _yaml(v)))
        out.append("---")
        out.append("")
    for n, slide in enumerate(prs.slides, 1):
        ctx.slide_no = n
        out.append(_slide(ctx, slide))
    return "\n".join(out).rstrip() + "\n", ctx.media


class _Ctx:
    def __init__(self, prs):
        self.prs = prs
        self.media = {}
        self.media_by_blob = {}
        self.slide_no = 0

    def warn(self, msg):
        self.prs.warnings.append("slide %d: %s" % (self.slide_no, msg))

    def media_name(self, image):
        if image.blob in self.media_by_blob:
            return self.media_by_blob[image.blob]
        name = image.filename or ("image-%s.%s" % (image.sha1[:8], image.ext))
        name = "".join(c if c.isalnum() or c in "-_." else "-" for c in name)
        base, dot, ext = name.rpartition(".")
        i = 2
        while name in self.media:
            name = "%s-%d.%s" % (base, i, ext)
            i += 1
        self.media[name] = image.blob
        self.media_by_blob[image.blob] = name
        return name


def _yaml(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    s = str(v)
    if s == "" or any(c in s for c in ":#{}[]&*!|>'\"%@`") or s != s.strip():
        return json.dumps(s, ensure_ascii=False)
    return s


def to_json(v, indent=""):
    """JSON as a person writes it: a value that fits on a line is kept on one."""
    flat = json.dumps(v, ensure_ascii=False)
    if len(flat) + len(indent) <= 76 or not isinstance(v, (dict, list)) or not v:
        return flat
    inner = indent + "  "
    if isinstance(v, dict):
        items = ["%s%s: %s" % (inner, json.dumps(k, ensure_ascii=False), to_json(x, inner)) for k, x in v.items()]
        return "{\n" + ",\n".join(items) + "\n" + indent + "}"
    items = [inner + to_json(x, inner) for x in v]
    return "[\n" + ",\n".join(items) + "\n" + indent + "]"


# -- attributes ---------------------------------------------------------------


def _attr_value(v):
    if isinstance(v, bool):
        v = "true" if v else "false"
    s = str(v)
    if s == "" or any(c in s for c in ' {}"'):
        return '"' + s.replace('"', '\\"') + '"'
    return s


def _attrs(classes=(), pairs=None):
    parts = ["." + c for c in classes]
    for k, v in (pairs or {}).items():
        if v is None:
            continue
        parts.append("%s=%s" % (k, _attr_value(v)))
    return "{" + " ".join(parts) + "}" if parts else ""


# -- slides -------------------------------------------------------------------

_PLAIN_BODY_LAYOUTS = ("Section Header", "Content with Caption", "Picture with Caption")


def _slide(ctx, slide):
    shapes = list(slide.shapes)
    layout = slide.slide_layout.name
    title_shape = slide.shapes.title
    title = _plain(title_shape.text_frame) if title_shape is not None else ""
    if title_shape is not None and not title:
        title_shape = None
    if title_shape is None:
        title_shape = _title_like(ctx.prs, shapes)
        if title_shape is not None:
            title = _plain(title_shape.text_frame)
    level = "#" if layout == "Title Slide" and title_shape is not None and title_shape.is_placeholder else "##"
    head_attrs = {}
    if not title:
        title = "Slide %d" % ctx.slide_no
        head_attrs["heading"] = "hidden"
    if layout == "Section Header":
        head_attrs["layout"] = "section"
    head_attrs.update(slide._attrs)
    lines = ["%s %s %s" % (level, _escape_heading(title), _attrs((), head_attrs))]
    body = [s for s in shapes if s is not title_shape]
    blocks = _arrange(ctx, body)
    for b in blocks:
        lines.append("")
        lines.append(b)
    if slide.has_notes_slide and slide.notes_slide.notes_text_frame.text.strip():
        lines.append("")
        lines.append("::: notes")
        lines.append(slide.notes_slide.notes_text_frame.text.strip())
        lines.append(":::")
    lines.append("")
    return "\n".join(l.rstrip() for l in lines)


def _title_like(prs, shapes):
    """A text box of one short line at the top of a slide with no title, not set small, read as its title."""
    best = None
    for s in shapes:
        if not isinstance(s, Shape) or s.is_placeholder or s._auto_shape_type is not None:
            continue
        paras = [p for p in s.text_frame.paragraphs if p.text.strip()]
        if len(paras) != 1 or len(paras[0].text) > 90 or s.top > prs.slide_height * 0.22:
            continue
        size = _size(paras[0])
        if size is None or size >= Pt(20) or _bold(paras[0]):
            if best is None or s.top < best.top:
                best = s
    if best is not None:
        for s in shapes:
            if s is not best and s.top + s.height <= best.top:
                return None
    return best


def _size(p):
    for r in p._runs:
        if r.font.size is not None:
            return r.font.size
    return p.font.size


def _bold(p):
    runs = [r for r in p._runs if r.text.strip()]
    return bool(runs) and all((r.font.bold if r.font.bold is not None else p.font.bold) for r in runs)


# -- placing shapes ---------------------------------------------------------------


def _box(s):
    if isinstance(s, GroupShape):
        l, t, w, h = s._bounds()
    else:
        l, t, w, h = s.left, s.top, s.width, s.height
    return (int(l), int(t), int(l + max(w, 0)), int(t + max(h, 0)))


def _cut(items, axis, size):
    """Splits items (shape, box) where a gap runs across them along axis 0 (x) or 1 (y)."""
    lo, hi = (0, 2) if axis == 0 else (1, 3)
    tol = int(size * 0.02)

    def span(b):
        a, z = b[lo], b[hi]
        d = min(tol, (z - a) // 4)
        return a + d, z - d

    items = sorted(items, key=lambda it: span(it[1]))
    groups = []
    end = None
    for it in items:
        a, z = span(it[1])
        if end is None or a >= end:
            groups.append([it])
            end = z
        else:
            groups[-1].append(it)
            end = max(end, z)
    return groups


def _stack(items):
    return [s for s, b in sorted(items, key=lambda it: (it[1][1], it[1][0]))]


def _widths(groups):
    ws = [max(b[2] for s, b in g) - min(b[0] for s, b in g) for g in groups]
    total = sum(ws) or 1
    pc = [max(1, round(100 * w / total)) for w in ws]
    even = 100 / len(ws)
    if all(abs(p - even) <= 5 for p in pc):
        return None
    return " ".join(str(p) for p in pc)


def _arrange(ctx, shapes):
    items = []
    for s in shapes:
        if not _drawable(ctx, s):
            continue
        items.append((s, _box(s)))
    if not items:
        return []
    W, H = int(ctx.prs.slide_width), int(ctx.prs.slide_height)
    cols = _cut(items, 0, W)
    if len(cols) > 1:
        return [_columns(ctx, cols)]
    out = []
    for row in _cut(items, 1, H):
        cs = _cut(row, 0, W)
        if len(cs) > 1:
            out.append(_columns(ctx, cs))
        else:
            for s in _stack(row):
                out.extend(_blocks(ctx, s))
    return out


def _columns(ctx, groups):
    lines = [":::: columns"]
    for g in groups:
        lines.append("::: col")
        inner = []
        for s in _stack(g):
            inner.extend(_blocks(ctx, s))
        lines.append("\n\n".join(inner))
        lines.append(":::")
    lines.append("::::")
    w = _widths(groups)
    if w:
        lines.append(_attrs((), {"widths": w}))
    return "\n".join(lines)


def _drawable(ctx, s):
    if isinstance(s, Connector):
        ctx.warn("%s: lines and connectors are not drawn" % s.name)
        return False
    if isinstance(s, Shape):
        if s.is_placeholder and not s.text_frame.text.strip():
            return False
        if not s.text_frame.text.strip():
            if s._auto_shape_type is not None:
                ctx.warn("%s: a shape with no text is not drawn" % s.name)
            return False
    if isinstance(s, GroupShape) and len(s.shapes) == 0:
        return False
    return True


# -- blocks --------------------------------------------------------------------------


def _blocks(ctx, s):
    """The shape as Markdown blocks."""
    if isinstance(s, GroupShape):
        return _arrange(ctx, list(s.shapes))
    if isinstance(s, SliqtlyBlock):
        return [_sliqtly_block(s)]
    if isinstance(s, Picture):
        name = ctx.media_name(s.image)
        alt = s.image.filename.rsplit(".", 1)[0] if s.image.filename else ""
        return [_with_attrs("![%s](media/%s)" % (_escape(alt), name), s, False)]
    if isinstance(s, GraphicFrame):
        if s.has_table:
            return [_with_attrs(_table(s.table), s, False)]
        return [_with_attrs("```vega-lite\n" + to_json(_chart_spec(ctx, s.chart)) + "\n```", s, False)]
    if isinstance(s, Shape):
        return _text_blocks(ctx, s)
    return []


def _with_attrs(block, s, is_list, classes=(), pairs=None):
    classes = list(classes)
    pairs = dict(pairs or {})
    if s._anim:
        if is_list:
            classes.append("build")
        pairs.update(s._anim)
    pairs.update(s._attrs)
    a = _attrs(classes, pairs)
    return block + ("\n" + a if a else "")


def _sliqtly_block(s):
    src = s.source
    if s.kind == "vega-lite":
        if not isinstance(src, str):
            spec = dict(src)
            spec.setdefault("background", "rgba(0,0,0,0)")
            src = to_json(spec)
        return _with_attrs("```vega-lite\n" + src.strip() + "\n```", s, False)
    if s.kind == "svg":
        return _with_attrs(src.strip(), s, False)
    if s.kind == "markdown":
        return src.strip()
    return _with_attrs("```" + s.kind + "\n" + src.strip("\n") + "\n```", s, False)


def _text_blocks(ctx, s):
    tf = s.text_frame
    paras = list(tf.paragraphs)
    while paras and not paras[-1].text.strip():
        paras.pop()
    while paras and not paras[0].text.strip():
        paras.pop(0)
    nonempty = [p for p in paras if p.text.strip()]
    ph = s.placeholder_format.type if s.is_placeholder else None
    layout = s._tree._root().slide_layout.name if s.is_placeholder else ""
    fill = s.fill._css() if not s.is_placeholder else None
    box = {"container": "box", "background": fill} if fill else {}
    if ph == PH.BODY and layout == "Comparison":
        return [_with_attrs("### " + _escape_heading(_plain(tf)), s, False)]
    is_list = ph in (PH.OBJECT, PH.VERTICAL_BODY) or (ph == PH.BODY and layout not in _PLAIN_BODY_LAYOUTS)
    bullets = len(nonempty) >= 2 and all(p.text.lstrip()[:1] in _BULLETS for p in nonempty)
    if is_list or bullets or any(p.level > 0 for p in nonempty):
        lines = []
        for p in nonempty:
            lines.append("  " * min(p.level, 8) + "- " + _inline(p, strip_bullet=True))
        return [_with_attrs("\n".join(lines), s, True, (), box)]
    if box:
        # a filled shape is one plate: its paragraphs as lines on it
        text = "<br>".join(_inline(p) if p.text.strip() else "" for p in paras)
        return [_with_attrs(text, s, False, _para_classes(nonempty[0]), box)]
    out = []
    for p in nonempty:
        out.append(_with_attrs(_inline(p), s, False, _para_classes(p)))
    return out


_BULLETS = ("•", "◦", "▪", "▫", "–", "-", "*", "·", "‣", "►", "✓", "✔")


def _para_classes(p):
    c = []
    if p.alignment == PP_ALIGN.CENTER:
        c.append("center")
    elif p.alignment == PP_ALIGN.RIGHT:
        c.append("right")
    size = _size(p)
    if size is not None and size >= Pt(28):
        c.append("lead")
    return c


# -- text ----------------------------------------------------------------------------


def _plain(tf):
    return " ".join(p.text.replace("\v", " ").strip() for p in tf.paragraphs if p.text.strip())


def _escape(t):
    out = []
    for ch in t:
        if ch in "\\*_`[]$":
            out.append("\\" + ch)
        elif ch == "<":
            out.append("&lt;")
        else:
            out.append(ch)
    return "".join(out)


def _escape_heading(t):
    return _escape(t).replace("{", "\\{")


def _escape_start(t):
    s = t.lstrip()
    if s[:1] in ("#", "-", "+", ">", "|", "{", ":") or (s[:1].isdigit() and s.split(" ", 1)[0].rstrip(".)").isdigit() and s.split(" ", 1)[0][-1:] in ".)"):
        i = len(t) - len(s)
        return t[:i] + "\\" + t[i:]
    return t


def _font(p, r):
    f, pf = r.font, p.font

    def pick(a, b):
        return a if a is not None else b

    color = None
    for c in (f._color, pf._color):
        if c is not None and c._css():
            color = c._css()
            break
    return (bool(pick(f.bold, pf.bold)), bool(pick(f.italic, pf.italic)),
            bool(pick(f.underline, pf.underline)), color, r.hyperlink.address)


def _inline(p, strip_bullet=False, html=False):
    pieces = []
    for r in p._runs:
        if not r.text:
            continue
        key = _font(p, r)
        if pieces and pieces[-1][0] == key:
            pieces[-1][1] += r.text
        else:
            pieces.append([key, r.text])
    if strip_bullet and pieces:
        t = pieces[0][1].lstrip()
        if t[:1] in _BULLETS:
            pieces[0][1] = t[1:].lstrip()
    out = []
    for (bold, italic, underline, color, link), text in pieces:
        parts = text.split("\v")
        if html:
            esc = [_html(x) for x in parts]
        else:
            esc = [_escape(x) for x in parts]
        body = "<br>".join(esc)
        lead = body[:len(body) - len(body.lstrip())]
        trail = body[len(body.rstrip()):]
        core = body.strip()
        if core:
            if html:
                if bold:
                    core = "<b>%s</b>" % core
                if italic:
                    core = "<i>%s</i>" % core
            else:
                if bold and italic:
                    core = "***%s***" % core
                elif bold:
                    core = "**%s**" % core
                elif italic:
                    core = "*%s*" % core
            if underline:
                core = "<u>%s</u>" % core
            if color:
                core = '<span style="color:%s">%s</span>' % (color, core)
            if link:
                core = '<a href="%s">%s</a>' % (_html(link), core) if html else "[%s](%s)" % (core, link.replace(")", "%29"))
        out.append(lead + core + trail)
    text = "".join(out).strip()
    return text if html else _escape_start(text)


def _html(t):
    return t.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


# -- tables --------------------------------------------------------------------------


def _cell_text(cell, html):
    ps = [p for p in cell.text_frame.paragraphs if p.text.strip()]
    t = "<br>".join(_inline(p, html=html) for p in ps)
    return t if html else t.replace("|", "\\|")


def _align(cells):
    for c in cells:
        for p in c.text_frame.paragraphs:
            if p.text.strip():
                if p.alignment == PP_ALIGN.CENTER:
                    return ":---:"
                if p.alignment == PP_ALIGN.RIGHT:
                    return "---:"
                return "---"
    return "---"


def _table(t):
    rows, cols = len(t.rows), len(t.columns)
    merged = any(c.is_merge_origin for c in t.iter_cells())
    filled = any(c.fill._css() for c in t.iter_cells())
    if merged or filled or not t.first_row or rows < 1:
        return _html_table(t)
    lines = ["| " + " | ".join(_cell_text(t.cell(0, c), False) for c in range(cols)) + " |"]
    lines.append("| " + " | ".join(_align([t.cell(r, c) for r in range(1, rows)]) for c in range(cols)) + " |")
    for r in range(1, rows):
        lines.append("| " + " | ".join(_cell_text(t.cell(r, c), False) for c in range(cols)) + " |")
    return "\n".join(lines)


def _html_table(t):
    rows, cols = len(t.rows), len(t.columns)
    out = ["<table>"]
    for r in range(rows):
        head = r == 0 and t.first_row
        tag = "th" if head else "td"
        if head:
            out.append("<thead>")
        cells = []
        for c in range(cols):
            cell = t.cell(r, c)
            if cell.is_spanned:
                continue
            a = ""
            if cell.span_height > 1:
                a += ' rowspan="%d"' % cell.span_height
            if cell.span_width > 1:
                a += ' colspan="%d"' % cell.span_width
            if cell.fill._css():
                a += ' style="background:%s"' % cell.fill._css()
            cells.append("<%s%s>%s</%s>" % (tag, a, _cell_text(cell, True), tag))
        out.append("<tr>" + "".join(cells) + "</tr>")
        if head:
            out.append("</thead>")
    out.append("</table>")
    return "\n".join(out)


# -- charts --------------------------------------------------------------------------

_COLUMN = {CT.COLUMN_CLUSTERED, CT.COLUMN_STACKED, CT.COLUMN_STACKED_100, CT.THREE_D_COLUMN, CT.THREE_D_COLUMN_CLUSTERED}
_BAR = {CT.BAR_CLUSTERED, CT.BAR_STACKED, CT.BAR_STACKED_100, CT.THREE_D_BAR_CLUSTERED}
_LINE = {CT.LINE, CT.LINE_MARKERS, CT.LINE_STACKED, CT.LINE_STACKED_100, CT.LINE_MARKERS_STACKED,
         CT.LINE_MARKERS_STACKED_100, CT.THREE_D_LINE}
_AREA = {CT.AREA, CT.AREA_STACKED, CT.AREA_STACKED_100, CT.THREE_D_AREA}
_PIE = {CT.PIE, CT.PIE_EXPLODED, CT.THREE_D_PIE}
_DOUGHNUT = {CT.DOUGHNUT, CT.DOUGHNUT_EXPLODED}
_SCATTER = {CT.XY_SCATTER, CT.XY_SCATTER_LINES, CT.XY_SCATTER_LINES_NO_MARKERS, CT.XY_SCATTER_SMOOTH,
            CT.XY_SCATTER_SMOOTH_NO_MARKERS}
_BUBBLE = {CT.BUBBLE, CT.BUBBLE_THREE_D_EFFECT}
_RADAR = {CT.RADAR, CT.RADAR_FILLED, CT.RADAR_MARKERS}
_STACKED = {CT.COLUMN_STACKED, CT.BAR_STACKED, CT.AREA_STACKED, CT.LINE_STACKED, CT.LINE_MARKERS_STACKED}
_NORMALIZED = {CT.COLUMN_STACKED_100, CT.BAR_STACKED_100, CT.AREA_STACKED_100, CT.LINE_STACKED_100,
               CT.LINE_MARKERS_STACKED_100}
_MARKERS = {CT.LINE_MARKERS, CT.LINE_MARKERS_STACKED, CT.LINE_MARKERS_STACKED_100, CT.RADAR_MARKERS}


def d3_format(fmt):
    """An Excel number format as a d3 format, or None."""
    if not fmt or fmt == "General":
        return None
    f = fmt.split(";")[0]
    out = []
    q = False
    for ch in f:
        if ch == '"':
            q = not q
        elif not q and ch not in "\\_*":
            out.append(ch)
    f = "".join(out)
    dec = 0
    if "." in f:
        frac = f.split(".", 1)[1]
        while dec < len(frac) and frac[dec] in "0#":
            dec += 1
    comma = "," if "," in f.split(".")[0] else ""
    cur = "$" if "$" in f else ""
    if "%" in f:
        return "%s%s.%d%%" % (cur, comma, dec)
    return "%s%s.%df" % (cur, comma, dec)


def _rgb(fmt):
    return fmt.fill._css() if fmt is not None else None


def _label(v):
    if hasattr(v, "isoformat"):
        return v.isoformat()
    return v


def _chart_spec(ctx, chart):
    ct = chart.chart_type
    data = chart._data
    plot = chart.plots[0]
    spec = {"background": "rgba(0,0,0,0)"}
    title = chart.chart_title._text() if chart.has_title else ""
    if not title and chart._has_title is None and len(chart.series) == 1 and ct not in _SCATTER | _BUBBLE:
        # PowerPoint titles a chart of one series with the series' name
        title = str(chart.series[0].name or "")
    if title:
        spec["title"] = title
    multi = len(chart.series) > 1
    vfmt = _value_format(chart, data)
    lfmt = _label_format(plot, data)
    legend = _legend(chart)
    if ct in _SCATTER or ct in _BUBBLE:
        return _xy_spec(ctx, chart, spec, ct, legend, vfmt)
    if ct in _RADAR:
        ctx.warn("radar charts are drawn as lines")
    cats = [_label(c.label) for c in data.categories]
    values = []
    for s in data:
        for i, v in enumerate(s.values):
            if v is None or i >= len(cats):
                continue
            row = {"category": cats[i], "value": v}
            if multi:
                row["series"] = str(s.name)
            values.append(row)
    spec["data"] = {"values": values}
    colors = [_rgb(s.format) for s in chart.series]
    color_enc = None
    if multi:
        color_enc = {"field": "series", "type": "nominal", "sort": [str(s.name) for s in data], "title": None}
        if all(colors):
            color_enc["scale"] = {"domain": [str(s.name) for s in data], "range": colors}
        if legend is not None:
            color_enc["legend"] = legend
    if ct in _PIE or ct in _DOUGHNUT:
        if multi:
            ctx.warn("a pie chart is drawn from its first series only")
            first = str(data[0].name)
            spec["data"]["values"] = [dict(category=v["category"], value=v["value"]) for v in values if v["series"] == first]
        mark = {"type": "arc"}
        if ct in _DOUGHNUT:
            mark["innerRadius"] = 50
        enc = {"theta": {"field": "value", "type": "quantitative", "stack": True},
               "color": {"field": "category", "type": "nominal", "sort": None, "title": None}}
        s0 = chart.series[0] if chart.series else None
        pts = [_rgb(s0._points[i].format) if s0 is not None and i in s0._points else None for i in range(len(cats))]
        if all(pts) and pts:
            enc["color"]["scale"] = {"domain": cats, "range": pts}
        if not chart.has_legend:
            enc["color"]["legend"] = None
        elif legend is not None:
            enc["color"]["legend"] = legend
        spec["mark"] = mark
        spec["encoding"] = enc
        if plot.has_data_labels:
            spec.pop("mark")
            enc_l = {"text": {"field": "value", "type": "quantitative"}}
            if lfmt:
                enc_l["text"]["format"] = lfmt
            spec["layer"] = [{"mark": mark}, {"mark": {"type": "text", "radius": 110}, "encoding": enc_l}]
        return spec
    horizontal = ct in _BAR
    cat_axis, val_axis = chart.category_axis, chart.value_axis
    cat = {"field": "category", "type": "nominal", "sort": None, "title": _axis_title(cat_axis)}
    if not horizontal:
        cat["axis"] = {"labelAngle": 0}
    if not cat_axis.visible:
        cat["axis"] = None
    val = {"field": "value", "type": "quantitative", "title": _axis_title(val_axis)}
    vaxis = {"grid": bool(val_axis.has_major_gridlines)}
    if vfmt:
        vaxis["format"] = vfmt
    if ct in _NORMALIZED:
        vaxis["format"] = ".0%"
    size = val_axis.tick_labels.font.size
    if size:
        vaxis["labelFontSize"] = round(size.pt)
    val["axis"] = vaxis if val_axis.visible else None
    scale = {}
    if val_axis.minimum_scale is not None:
        scale["domainMin"] = val_axis.minimum_scale
    if val_axis.maximum_scale is not None:
        scale["domainMax"] = val_axis.maximum_scale
    if scale:
        val["scale"] = scale
    if ct in _NORMALIZED:
        val["stack"] = "normalize"
    elif ct in _STACKED:
        val["stack"] = "zero"
    else:
        val["stack"] = None
    if ct in _LINE or ct in _RADAR:
        mark = {"type": "line"}
        if ct in _MARKERS:
            mark["point"] = True
        if any(s.smooth for s in chart.series):
            mark["interpolate"] = "monotone"
        cat["type"] = "ordinal"
    elif ct in _AREA:
        mark = {"type": "area"}
        cat["type"] = "ordinal"
        if ct == CT.AREA and multi:
            mark["opacity"] = 0.7
    else:
        mark = {"type": "bar"}
    if not multi and colors and colors[0]:
        mark["color"] = colors[0]
    enc = {}
    if horizontal:
        enc["y"], enc["x"] = cat, val
    else:
        enc["x"], enc["y"] = cat, val
    if color_enc:
        enc["color"] = color_enc
        if mark["type"] == "bar" and val["stack"] is None:
            enc["yOffset" if horizontal else "xOffset"] = {"field": "series", "sort": [str(s.name) for s in data]}
    spec["encoding"] = enc
    if plot.has_data_labels:
        text = {"type": "text"}
        if mark["type"] == "bar" and val["stack"] is None:
            if horizontal:
                text.update(align="left", dx=3)
            else:
                text.update(baseline="bottom", dy=-3)
        elif mark["type"] == "bar":
            # inside the end of each part of the stack
            if horizontal:
                text.update(align="right", dx=-4)
            else:
                text.update(baseline="top", dy=4)
        else:
            text.update(dy=-10)
        label = {"field": "value", "type": "quantitative"}
        if lfmt:
            label["format"] = lfmt
        spec["layer"] = [{"mark": mark}, {"mark": text, "encoding": {"text": label}}]
    else:
        spec["mark"] = mark
    return spec


def _xy_spec(ctx, chart, spec, ct, legend, vfmt):
    values = []
    multi = len(chart.series) > 1
    bubble = ct in _BUBBLE
    for s in chart._data:
        for pt in s.points:
            row = {"x": pt[0], "y": pt[1]}
            if bubble:
                row["size"] = pt[2]
            if multi:
                row["series"] = str(s.name)
            values.append(row)
    spec["data"] = {"values": values}
    lines = ct in (CT.XY_SCATTER_LINES, CT.XY_SCATTER_LINES_NO_MARKERS, CT.XY_SCATTER_SMOOTH, CT.XY_SCATTER_SMOOTH_NO_MARKERS)
    if lines:
        mark = {"type": "line", "point": ct in (CT.XY_SCATTER_LINES, CT.XY_SCATTER_SMOOTH)}
        if ct in (CT.XY_SCATTER_SMOOTH, CT.XY_SCATTER_SMOOTH_NO_MARKERS):
            mark["interpolate"] = "monotone"
    else:
        mark = {"type": "point", "filled": True, "size": 80}
        if bubble:
            mark["type"] = "circle"
    colors = [_rgb(s.format) for s in chart.series]
    if not multi and colors and colors[0]:
        mark["color"] = colors[0]
    xaxis = {"grid": bool(chart.category_axis.has_major_gridlines)}
    yaxis = {"grid": bool(chart.value_axis.has_major_gridlines)}
    if vfmt:
        yaxis["format"] = vfmt
    enc = {"x": {"field": "x", "type": "quantitative", "title": _axis_title(chart.category_axis), "axis": xaxis},
           "y": {"field": "y", "type": "quantitative", "title": _axis_title(chart.value_axis), "axis": yaxis}}
    for key, axis in (("x", chart.category_axis), ("y", chart.value_axis)):
        sc = {}
        if axis.minimum_scale is not None:
            sc["domainMin"] = axis.minimum_scale
        if axis.maximum_scale is not None:
            sc["domainMax"] = axis.maximum_scale
        if sc:
            enc[key]["scale"] = sc
    if bubble:
        enc["size"] = {"field": "size", "type": "quantitative", "legend": None}
    if multi:
        enc["color"] = {"field": "series", "type": "nominal", "title": None}
        if all(colors):
            enc["color"]["scale"] = {"domain": [str(s.name) for s in chart._data], "range": colors}
        if legend is not None:
            enc["color"]["legend"] = legend
        if lines:
            enc["detail"] = {"field": "series"}
    spec["mark"] = mark
    spec["encoding"] = enc
    return spec


def _axis_title(axis):
    if axis.has_title:
        t = axis.axis_title._text()
        return t or None
    return None


def _legend(chart):
    if not chart.has_legend:
        return None
    pos = chart.legend.position
    orient = {LP.BOTTOM: "bottom", LP.TOP: "top", LP.LEFT: "left", LP.RIGHT: "right", LP.CORNER: "top-right"}.get(pos)
    return {"orient": orient} if orient else {}


def _value_format(chart, data):
    tl = chart.value_axis.tick_labels
    if tl.number_format and tl.number_format != "General" and not tl.number_format_is_linked:
        return d3_format(tl.number_format)
    if tl.number_format and tl.number_format != "General":
        return d3_format(tl.number_format)
    return d3_format(getattr(data, "number_format", None))


def _label_format(plot, data):
    dl = plot.data_labels
    if dl.number_format and dl.number_format != "General":
        return d3_format(dl.number_format)
    return d3_format(getattr(data, "number_format", None))
