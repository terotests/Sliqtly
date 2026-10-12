"""Slides, layouts and notes."""

from pptx.dml.fill import FillFormat
from pptx.enum.shapes import PP_PLACEHOLDER as PH
from pptx.shapes.shapetree import SlideShapes
from pptx.text.text import TextFrame

# The layouts of python-pptx's default template (a 10 x 7.5 inch slide):
# name, then its placeholders as (idx, type, name, left, top, width, height) in EMU.
_TITLE = (0, PH.TITLE, "Title", 457200, 274638, 8229600, 1143000)
_BODY = (1, PH.OBJECT, "Content Placeholder", 457200, 1600200, 8229600, 4525963)
LAYOUTS = [
    ("Title Slide", [(0, PH.CENTER_TITLE, "Title", 685800, 2130425, 7772400, 1470025),
                     (1, PH.SUBTITLE, "Subtitle", 1371600, 3886200, 6400800, 1752600)]),
    ("Title and Content", [_TITLE, _BODY]),
    ("Section Header", [(0, PH.TITLE, "Title", 722313, 4406900, 7772400, 1362075),
                        (1, PH.BODY, "Text Placeholder", 722313, 2906713, 7772400, 1500187)]),
    ("Two Content", [_TITLE, (1, PH.OBJECT, "Content Placeholder", 457200, 1600200, 4038600, 4525963),
                     (2, PH.OBJECT, "Content Placeholder", 4648200, 1600200, 4038600, 4525963)]),
    ("Comparison", [_TITLE, (1, PH.BODY, "Text Placeholder", 457200, 1535113, 4040188, 639762),
                    (2, PH.OBJECT, "Content Placeholder", 457200, 2174875, 4040188, 3951288),
                    (3, PH.BODY, "Text Placeholder", 4645025, 1535113, 4041775, 639762),
                    (4, PH.OBJECT, "Content Placeholder", 4645025, 2174875, 4041775, 3951288)]),
    ("Title Only", [_TITLE]),
    ("Blank", []),
    ("Content with Caption", [(0, PH.TITLE, "Title", 457200, 273050, 3008313, 1162050),
                              (1, PH.OBJECT, "Content Placeholder", 3575050, 273050, 5111750, 5853113),
                              (2, PH.BODY, "Text Placeholder", 457200, 1435100, 3008313, 4691063)]),
    ("Picture with Caption", [(0, PH.TITLE, "Title", 1792288, 4800600, 5486400, 566738),
                              (1, PH.PICTURE, "Picture Placeholder", 1792288, 612775, 5486400, 4114800),
                              (2, PH.BODY, "Text Placeholder", 1792288, 5367338, 5486400, 804862)]),
    ("Title and Vertical Text", [_TITLE, (1, PH.VERTICAL_BODY, "Vertical Text Placeholder", 457200, 1600200, 8229600, 4525963)]),
    ("Vertical Title and Text", [(0, PH.VERTICAL_TITLE, "Vertical Title", 6629400, 274638, 2057400, 5851525),
                                 (1, PH.VERTICAL_BODY, "Vertical Text Placeholder", 457200, 274638, 6019800, 5851525)]),
]


class _Placeholders:
    def __init__(self, layout):
        self._layout = layout

    def __iter__(self):
        return iter(self._layout._placeholders)

    def __len__(self):
        return len(self._layout._placeholders)


class _LayoutPlaceholder:
    def __init__(self, spec):
        idx, ph_type, name, left, top, width, height = spec
        from pptx.shapes.shapetree import _PlaceholderFormat
        self.placeholder_format = _PlaceholderFormat(idx, ph_type)
        self.name = name
        self.left, self.top, self.width, self.height = left, top, width, height


class SlideLayout:
    def __init__(self, master, index, name, placeholders):
        self.slide_master = master
        self.index = index
        self.name = name
        self._specs = placeholders
        self._placeholders = [_LayoutPlaceholder(p) for p in placeholders]

    @property
    def placeholders(self):
        return _Placeholders(self)

    @property
    def shapes(self):
        return list(self._placeholders)

    @property
    def used_by_slides(self):
        return tuple(s for s in self.slide_master._prs.slides if s.slide_layout is self)


class SlideLayouts:
    def __init__(self, layouts):
        self._layouts = layouts

    def __getitem__(self, i):
        return self._layouts[i]

    def __iter__(self):
        return iter(self._layouts)

    def __len__(self):
        return len(self._layouts)

    def index(self, layout):
        return self._layouts.index(layout)

    def get_by_name(self, name, default=None):
        for l in self._layouts:
            if l.name == name:
                return l
        return default


class SlideMaster:
    def __init__(self, prs):
        self._prs = prs
        self.slide_layouts = SlideLayouts([SlideLayout(self, i, n, p) for i, (n, p) in enumerate(LAYOUTS)])
        self.shapes = []


class NotesSlide:
    def __init__(self):
        self.notes_text_frame = TextFrame()

    @property
    def notes_placeholder(self):
        return self


class _Background:
    def __init__(self):
        self.fill = FillFormat()


class _Part:
    def __init__(self, slide):
        self.slide = slide


class Slide:
    def __init__(self, prs, slide_id, layout):
        self._prs = prs
        self.slide_id = slide_id
        self.slide_layout = layout
        self.name = ""
        self._last_id = 1
        self.shapes = SlideShapes(self)
        self._notes = None
        self.background = _Background()
        self.follow_master_background = True
        self.part = _Part(self)
        # Sliqtly additions: attributes on the slide's heading
        self._attrs = {}
        for spec in layout._specs:
            idx, ph_type, name, left, top, width, height = spec
            self.shapes._add_placeholder(idx, ph_type, name, left, top, width, height)

    def _slide_width(self):
        return self._prs.slide_width

    def _slide_height(self):
        return self._prs.slide_height

    @property
    def placeholders(self):
        return self.shapes.placeholders

    @property
    def has_notes_slide(self):
        return self._notes is not None

    @property
    def notes_slide(self):
        if self._notes is None:
            self._notes = NotesSlide()
        return self._notes


class _SldIdLst(list):
    """What `prs.slides._sldIdLst` is in python-pptx: removing an entry removes its slide."""

    def __init__(self, slides):
        list.__init__(self, slides._slides)
        self._owner = slides

    def remove(self, item):
        list.remove(self, item)
        self._owner._slides.remove(item)

    def insert(self, i, item):
        list.insert(self, i, item)
        if item in self._owner._slides:
            self._owner._slides.remove(item)
        self._owner._slides.insert(i, item)

    def append(self, item):
        self.insert(len(self), item)


class Slides:
    def __init__(self, prs):
        self._prs = prs
        self._slides = []
        self._next_id = 256

    def add_slide(self, slide_layout):
        s = Slide(self._prs, self._next_id, slide_layout)
        self._next_id += 1
        self._slides.append(s)
        return s

    def __getitem__(self, i):
        return self._slides[i]

    def __iter__(self):
        return iter(list(self._slides))

    def __len__(self):
        return len(self._slides)

    def index(self, slide):
        return self._slides.index(slide)

    def get(self, slide_id, default=None):
        for s in self._slides:
            if s.slide_id == slide_id:
                return s
        return default

    @property
    def _sldIdLst(self):
        return _SldIdLst(self)
