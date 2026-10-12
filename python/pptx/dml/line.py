from pptx.dml.fill import FillFormat
from pptx.util import Emu


class LineFormat:
    def __init__(self):
        self.fill = FillFormat()
        self._width = Emu(0)
        self.dash_style = None

    @property
    def color(self):
        if self.fill.type is None or self.fill._fore is None:
            self.fill.solid()
        return self.fill.fore_color

    @property
    def width(self):
        return self._width

    @width.setter
    def width(self, value):
        self._width = Emu(value or 0)
