"""Text frames, paragraphs, runs and fonts."""

from pptx.dml.color import ColorFormat
from pptx.dml.fill import FillFormat
from pptx.enum.text import MSO_AUTO_SIZE
from pptx.util import Emu, Inches, Pt


class Font:
    def __init__(self):
        self.name = None
        self.size = None
        self.bold = None
        self.italic = None
        self.underline = None
        self._color = None
        self.fill = FillFormat()

    @property
    def color(self):
        if self._color is None:
            self._color = ColorFormat()
        return self._color


class _Hyperlink:
    def __init__(self):
        self.address = None


class _Run:
    def __init__(self, text=""):
        self.text = text
        self.font = Font()
        self.hyperlink = _Hyperlink()


class _Paragraph:
    def __init__(self):
        self._runs = []
        self.font = Font()
        self.alignment = None
        self.level = 0
        self.line_spacing = None
        self.space_before = None
        self.space_after = None

    @property
    def runs(self):
        return tuple(self._runs)

    def add_run(self):
        r = _Run()
        self._runs.append(r)
        return r

    def add_line_break(self):
        self._runs.append(_Run("\v"))

    def clear(self):
        self._runs = []
        return self

    @property
    def text(self):
        return "".join(r.text for r in self._runs)

    @text.setter
    def text(self, value):
        # as in python-pptx: "\n" and "\v" in a paragraph's text are line breaks
        self._runs = []
        if value:
            self.add_run().text = str(value).replace("\n", "\v")


class TextFrame:
    def __init__(self):
        self._paragraphs = [_Paragraph()]
        self.word_wrap = None
        self.auto_size = None
        self.vertical_anchor = None
        self.margin_left = Inches(0.1)
        self.margin_right = Inches(0.1)
        self.margin_top = Inches(0.05)
        self.margin_bottom = Inches(0.05)

    @property
    def paragraphs(self):
        return tuple(self._paragraphs)

    def add_paragraph(self):
        p = _Paragraph()
        self._paragraphs.append(p)
        return p

    def clear(self):
        first = self._paragraphs[0]
        first.clear()
        self._paragraphs = [first]

    @property
    def text(self):
        return "\n".join(p.text for p in self._paragraphs)

    @text.setter
    def text(self, value):
        # "\n" starts a paragraph; "\v" is a line break inside one
        self.clear()
        lines = str(value).split("\n")
        self._paragraphs[0].text = lines[0]
        for line in lines[1:]:
            self.add_paragraph().text = line

    def fit_text(self, font_family="Calibri", max_size=18, bold=False, italic=False, font_file=None):
        self.auto_size = MSO_AUTO_SIZE.NONE
        for p in self._paragraphs:
            for r in p._runs:
                r.font.size = Pt(max_size)
                r.font.bold = bold
                r.font.italic = italic
