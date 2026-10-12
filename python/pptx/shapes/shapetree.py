"""Shapes on a slide and the shape tree that adds them."""

from pptx.chart.chart import Chart
from pptx.dml.fill import FillFormat
from pptx.dml.line import LineFormat
from pptx.enum.shapes import MSO_SHAPE, MSO_SHAPE_TYPE, PP_PLACEHOLDER
from pptx.text.text import TextFrame
from pptx.util import Emu, Inches


class _ActionSetting:
    def __init__(self):
        from pptx.text.text import _Hyperlink
        self.hyperlink = _Hyperlink()
        self.target_slide = None


class _Shadow:
    def __init__(self):
        self.inherit = True


class BaseShape:
    _kind = "Shape"
    shape_type = None
    has_text_frame = False
    has_chart = False
    has_table = False
    is_placeholder = False

    def __init__(self, tree, shape_id, name, left, top, width, height):
        self._tree = tree
        self.shape_id = shape_id
        self.name = name
        self.left = Emu(left or 0)
        self.top = Emu(top or 0)
        self.width = Emu(width or 0)
        self.height = Emu(height or 0)
        self.rotation = 0.0
        self.click_action = _ActionSetting()
        self.shadow = _Shadow()
        # Sliqtly additions: an animation and attributes for the block
        self._anim = None
        self._attrs = {}

    @property
    def id(self):
        return self.shape_id

    @property
    def part(self):
        return self._tree._slide.part

    @property
    def has_graphic_frame(self):
        return False

    @property
    def placeholder_format(self):
        raise ValueError("shape is not a placeholder")

    def _set_geometry(self, left, top, width, height):
        self.left, self.top, self.width, self.height = Emu(left), Emu(top), Emu(width), Emu(height)


class _TextShape(BaseShape):
    has_text_frame = True

    def __init__(self, *a):
        BaseShape.__init__(self, *a)
        self._text_frame = TextFrame()

    @property
    def text_frame(self):
        return self._text_frame

    @property
    def text(self):
        return self._text_frame.text

    @text.setter
    def text(self, value):
        self._text_frame.text = value


class Shape(_TextShape):
    """An autoshape (rectangle, oval, arrow...) or a text box."""

    def __init__(self, tree, shape_id, name, left, top, width, height, auto_shape_type=None):
        _TextShape.__init__(self, tree, shape_id, name, left, top, width, height)
        self.fill = FillFormat()
        self.line = LineFormat()
        self._auto_shape_type = auto_shape_type
        self.adjustments = [0.0, 0.0, 0.0, 0.0]

    @property
    def auto_shape_type(self):
        if self._auto_shape_type is None:
            raise ValueError("shape is not an auto shape")
        return self._auto_shape_type

    @property
    def shape_type(self):
        if self._auto_shape_type is None:
            return MSO_SHAPE_TYPE.TEXT_BOX
        return MSO_SHAPE_TYPE.AUTO_SHAPE


class Connector(BaseShape):
    shape_type = MSO_SHAPE_TYPE.LINE

    def __init__(self, tree, shape_id, name, begin_x, begin_y, end_x, end_y):
        BaseShape.__init__(self, tree, shape_id, name, min(begin_x, end_x), min(begin_y, end_y),
                           abs(end_x - begin_x), abs(end_y - begin_y))
        self.begin_x, self.begin_y, self.end_x, self.end_y = begin_x, begin_y, end_x, end_y
        self.line = LineFormat()

    def begin_connect(self, shape, cxn_pt_idx):
        pass

    def end_connect(self, shape, cxn_pt_idx):
        pass


class _Image:
    def __init__(self, blob, ext, filename):
        self.blob = blob
        self.ext = ext
        self.filename = filename
        self.content_type = {"png": "image/png", "jpg": "image/jpeg", "gif": "image/gif",
                             "svg": "image/svg+xml", "webp": "image/webp", "bmp": "image/bmp"}.get(ext, "image/" + ext)
        self.size = _image_size(blob, ext)

    @property
    def sha1(self):
        """A short name for the bytes (FNV-1a over a sample of them; not SHA-1,
        which needs hashlib)."""
        b = self.blob
        step = max(1, len(b) // 4096)
        h = 0xCBF29CE484222325 ^ len(b)
        for i in range(0, len(b), step):
            h = ((h ^ b[i]) * 0x100000001B3) & 0xFFFFFFFFFFFFFFFF
        return "%016x" % h


def _image_ext(blob, name):
    if blob[:8] == b"\x89PNG\r\n\x1a\n":
        return "png"
    if blob[:3] == b"\xff\xd8\xff":
        return "jpg"
    if blob[:4] == b"GIF8":
        return "gif"
    if blob[:4] == b"RIFF" and blob[8:12] == b"WEBP":
        return "webp"
    if blob[:2] == b"BM":
        return "bmp"
    head = blob[:300].lstrip()
    if head.startswith(b"<svg") or head.startswith(b"<?xml"):
        return "svg"
    if "." in name:
        return name.rsplit(".", 1)[1].lower()
    return "png"


def _image_size(blob, ext):
    """The picture's size in pixels, read from its header; (0, 0) when unknown."""
    try:
        if ext == "png":
            return int.from_bytes(blob[16:20], "big"), int.from_bytes(blob[20:24], "big")
        if ext == "gif":
            return int.from_bytes(blob[6:8], "little"), int.from_bytes(blob[8:10], "little")
        if ext == "jpg":
            i = 2
            while i + 9 < len(blob):
                if blob[i] != 0xFF:
                    i += 1
                    continue
                marker = blob[i + 1]
                seg = int.from_bytes(blob[i + 2:i + 4], "big")
                if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
                    return int.from_bytes(blob[i + 7:i + 9], "big"), int.from_bytes(blob[i + 5:i + 7], "big")
                i += 2 + seg
    except Exception:
        pass
    return 0, 0


def _read_image(image_file):
    if isinstance(image_file, str):
        with open(image_file, "rb") as f:
            blob = f.read()
        name = image_file.replace("\\", "/").rsplit("/", 1)[-1]
    else:
        blob = image_file.read()
        name = getattr(image_file, "name", "") or ""
        name = str(name).replace("\\", "/").rsplit("/", 1)[-1]
    ext = _image_ext(blob, name)
    if not name or "." not in name:
        name = ""
    return _Image(blob, ext, name)


class Picture(BaseShape):
    shape_type = MSO_SHAPE_TYPE.PICTURE

    def __init__(self, tree, shape_id, name, image, left, top, width, height):
        BaseShape.__init__(self, tree, shape_id, name, left, top, width, height)
        self.image = image
        self.crop_left = self.crop_right = self.crop_top = self.crop_bottom = 0.0
        self.line = LineFormat()
        self.auto_shape_type = MSO_SHAPE.RECTANGLE


class _Column:
    def __init__(self, width):
        self.width = Emu(width)


class _Cell:
    def __init__(self, table, r, c):
        self._table = table
        self._rc = (r, c)
        self._text_frame = TextFrame()
        self.fill = FillFormat()
        self.margin_left = Inches(0.1)
        self.margin_right = Inches(0.1)
        self.margin_top = Inches(0.05)
        self.margin_bottom = Inches(0.05)
        self.vertical_anchor = None
        self._span = (1, 1)
        self._origin = None

    @property
    def text_frame(self):
        return self._text_frame

    @property
    def text(self):
        return self._text_frame.text

    @text.setter
    def text(self, value):
        self._text_frame.text = value

    def merge(self, other_cell):
        r0, c0 = self._rc
        r1, c1 = other_cell._rc
        top, bottom, left, right = min(r0, r1), max(r0, r1), min(c0, c1), max(c0, c1)
        origin = self._table.cell(top, left)
        origin._span = (bottom - top + 1, right - left + 1)
        for r in range(top, bottom + 1):
            for c in range(left, right + 1):
                if (r, c) != (top, left):
                    self._table.cell(r, c)._origin = origin

    def split(self):
        h, w = self._span
        r0, c0 = self._rc
        for r in range(r0, r0 + h):
            for c in range(c0, c0 + w):
                self._table.cell(r, c)._origin = None
        self._span = (1, 1)

    @property
    def is_merge_origin(self):
        return self._span != (1, 1)

    @property
    def is_spanned(self):
        return self._origin is not None

    @property
    def span_height(self):
        return self._span[0]

    @property
    def span_width(self):
        return self._span[1]


class _Row:
    def __init__(self, table, r, height):
        self._table = table
        self._r = r
        self.height = Emu(height)

    @property
    def cells(self):
        return [self._table.cell(self._r, c) for c in range(len(self._table.columns))]


class Table:
    def __init__(self, rows, cols, width, height):
        self.columns = [_Column(width // cols) for _ in range(cols)]
        self.rows = [_Row(self, r, height // rows) for r in range(rows)]
        self._cells = [[_Cell(self, r, c) for c in range(cols)] for r in range(rows)]
        self.first_row = True
        self.first_col = False
        self.last_row = False
        self.last_col = False
        self.horz_banding = True
        self.vert_banding = False

    def cell(self, row_idx, col_idx):
        return self._cells[row_idx][col_idx]

    def iter_cells(self):
        for row in self._cells:
            for cell in row:
                yield cell


class GraphicFrame(BaseShape):
    """A table or a chart."""

    def __init__(self, tree, shape_id, name, left, top, width, height, table=None, chart=None):
        BaseShape.__init__(self, tree, shape_id, name, left, top, width, height)
        self._table = table
        self._chart = chart

    @property
    def has_graphic_frame(self):
        return True

    @property
    def has_table(self):
        return self._table is not None

    @property
    def has_chart(self):
        return self._chart is not None

    @property
    def shape_type(self):
        return MSO_SHAPE_TYPE.TABLE if self._table is not None else MSO_SHAPE_TYPE.CHART

    @property
    def table(self):
        if self._table is None:
            raise ValueError("shape does not contain a table")
        return self._table

    @property
    def chart(self):
        if self._chart is None:
            raise ValueError("shape does not contain a chart")
        return self._chart


class SliqtlyBlock(BaseShape):
    """A Sliqtly block: a Vega-Lite chart, a Mermaid diagram, SVG or Markdown."""

    shape_type = MSO_SHAPE_TYPE.EMBEDDED_OLE_OBJECT

    def __init__(self, tree, shape_id, name, kind, source, left, top, width, height):
        BaseShape.__init__(self, tree, shape_id, name, left, top, width, height)
        self.kind = kind
        self.source = source


class _PlaceholderFormat:
    def __init__(self, idx, ph_type):
        self.idx = idx
        self.type = ph_type


class _Placeholder:
    is_placeholder = True

    @property
    def placeholder_format(self):
        return self._ph

    @property
    def shape_type(self):
        return MSO_SHAPE_TYPE.PLACEHOLDER


class SlidePlaceholder(_Placeholder, Shape):
    """A placeholder from the slide's layout: a title, a body or an object."""

    def __init__(self, tree, shape_id, name, left, top, width, height, idx, ph_type):
        Shape.__init__(self, tree, shape_id, name, left, top, width, height)
        self._ph = _PlaceholderFormat(idx, ph_type)

    def _replace_with(self, shape):
        tree = self._tree
        shape._ph = self._ph
        i = tree._shapes.index(self)
        tree._shapes[i] = shape
        return shape

    def insert_picture(self, image_file):
        image = _read_image(image_file)
        pic = PlaceholderPicture(self._tree, self.shape_id, self.name, image, self.left, self.top, self.width, self.height)
        return self._replace_with(pic)

    def insert_table(self, rows, cols):
        t = Table(rows, cols, self.width, self.height)
        return self._replace_with(PlaceholderGraphicFrame(self._tree, self.shape_id, self.name, self.left, self.top, self.width, self.height, table=t))

    def insert_chart(self, chart_type, chart_data):
        c = Chart(chart_type, chart_data)
        return self._replace_with(PlaceholderGraphicFrame(self._tree, self.shape_id, self.name, self.left, self.top, self.width, self.height, chart=c))


class PlaceholderPicture(_Placeholder, Picture):
    pass


class PlaceholderGraphicFrame(_Placeholder, GraphicFrame):
    pass


class _BaseShapes:
    def __init__(self, slide):
        self._slide = slide
        self._shapes = []

    def __iter__(self):
        return iter(list(self._shapes))

    def __len__(self):
        return len(self._shapes)

    def __getitem__(self, i):
        return self._shapes[i]

    def index(self, shape):
        return self._shapes.index(shape)

    def _next_id(self):
        root = self._slide
        root._last_id += 1
        return root._last_id

    def _root(self):
        return self._slide

    def _add(self, shape):
        self._shapes.append(shape)
        return shape

    def _name(self, kind, sid):
        return "%s %d" % (kind, sid - 1)

    def add_textbox(self, left, top, width, height):
        sid = self._next_id()
        return self._add(Shape(self, sid, self._name("TextBox", sid), left, top, width, height))

    def add_shape(self, autoshape_type_id, left, top, width, height):
        sid = self._next_id()
        name = getattr(autoshape_type_id, "name", "Shape").replace("_", " ").title()
        return self._add(Shape(self, sid, self._name(name, sid), left, top, width, height, autoshape_type_id))

    def add_picture(self, image_file, left, top, width=None, height=None):
        image = _read_image(image_file)
        px_w, px_h = image.size
        # a missing size follows the picture's own shape; with no size known, 1 inch per 96 px
        if width is None and height is None:
            width, height = Emu(px_w * 9525), Emu(px_h * 9525)
        elif width is None:
            width = int(height * px_w / px_h) if px_h else height
        elif height is None:
            height = int(width * px_h / px_w) if px_w else width
        sid = self._next_id()
        return self._add(Picture(self, sid, self._name("Picture", sid), image, left, top, width, height))

    def add_table(self, rows, cols, left, top, width, height):
        sid = self._next_id()
        t = Table(rows, cols, width, height)
        return self._add(GraphicFrame(self, sid, self._name("Table", sid), left, top, width, height, table=t))

    def add_chart(self, chart_type, x, y, cx, cy, chart_data):
        sid = self._next_id()
        c = Chart(chart_type, chart_data)
        return self._add(GraphicFrame(self, sid, self._name("Chart", sid), x, y, cx, cy, chart=c))

    def add_connector(self, connector_type, begin_x, begin_y, end_x, end_y):
        sid = self._next_id()
        return self._add(Connector(self, sid, self._name("Connector", sid), begin_x, begin_y, end_x, end_y))

    def add_group_shape(self, shapes=()):
        sid = self._next_id()
        g = GroupShape(self, sid, self._name("Group", sid))
        for s in shapes:
            if s in self._shapes:
                self._shapes.remove(s)
            g.shapes._shapes.append(s)
        return self._add(g)

    # Sliqtly additions

    def _block(self, kind, source, left, top, width, height):
        sid = self._next_id()
        slide = self._root()
        w = slide._slide_width()
        h = slide._slide_height()
        # with no place given, the block goes under what the slide has so far
        if top is None:
            top = max([s.top + s.height for s in slide.shapes if s.has_text_frame is False or s.text.strip()] + [Inches(1.5)])
        left = Inches(0.5) if left is None else left
        width = w - 2 * left if width is None else width
        height = max(h - top - Inches(0.5), Inches(1)) if height is None else height
        return self._add(SliqtlyBlock(self, sid, self._name(kind.title(), sid), kind, source, left, top, width, height))

    def add_vega(self, spec, left=None, top=None, width=None, height=None):
        """A Vega-Lite chart (a dict or its JSON text)."""
        return self._block("vega-lite", spec, left, top, width, height)

    def add_mermaid(self, source, left=None, top=None, width=None, height=None):
        """A Mermaid diagram."""
        return self._block("mermaid", source, left, top, width, height)

    def add_svg(self, svg, left=None, top=None, width=None, height=None):
        """An SVG drawing (its text)."""
        return self._block("svg", svg, left, top, width, height)

    def add_markdown(self, markdown, left=None, top=None, width=None, height=None):
        """Sliqtly Markdown, put on the slide as it is."""
        return self._block("markdown", markdown, left, top, width, height)

    def add_diagram(self, language, source, left=None, top=None, width=None, height=None):
        """A diagram in any fence language Sliqtly draws (mermaid, dot, d2, plantuml...)."""
        return self._block(language, source, left, top, width, height)


class GroupShapes(_BaseShapes):
    def _next_id(self):
        return self._slide._tree._next_id()

    def _root(self):
        return self._slide._tree._root()


class GroupShape(BaseShape):
    shape_type = MSO_SHAPE_TYPE.GROUP

    def __init__(self, tree, shape_id, name):
        BaseShape.__init__(self, tree, shape_id, name, 0, 0, 0, 0)
        self.shapes = GroupShapes(self)

    def _bounds(self):
        ss = list(self.shapes)
        if not ss:
            return self.left, self.top, self.width, self.height
        l = min(s.left for s in ss)
        t = min(s.top for s in ss)
        r = max(s.left + s.width for s in ss)
        b = max(s.top + s.height for s in ss)
        return l, t, r - l, b - t


class SlideShapes(_BaseShapes):
    @property
    def title(self):
        for s in self._shapes:
            if s.is_placeholder and s.placeholder_format.type in (PP_PLACEHOLDER.TITLE, PP_PLACEHOLDER.CENTER_TITLE):
                return s
        return None

    @property
    def placeholders(self):
        return SlidePlaceholders(self)

    def _add_placeholder(self, idx, ph_type, name, left, top, width, height):
        sid = self._next_id()
        return self._add(SlidePlaceholder(self, sid, "%s %d" % (name, sid - 1), left, top, width, height, idx, ph_type))


class SlidePlaceholders:
    def __init__(self, tree):
        self._tree = tree

    def _all(self):
        return [s for s in self._tree._shapes if s.is_placeholder]

    def __getitem__(self, idx):
        for s in self._all():
            if s.placeholder_format.idx == idx:
                return s
        raise KeyError("no placeholder on this slide with idx == %d" % idx)

    def __iter__(self):
        return iter(sorted(self._all(), key=lambda s: s.placeholder_format.idx))

    def __len__(self):
        return len(self._all())
