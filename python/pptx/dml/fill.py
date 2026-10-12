from pptx.dml.color import ColorFormat
from pptx.enum.dml import MSO_FILL_TYPE


class FillFormat:
    """A shape's or a cell's fill: none set, solid, or background (none)."""

    def __init__(self):
        self._type = None
        self._fore = None

    @property
    def type(self):
        return self._type

    def solid(self):
        self._type = MSO_FILL_TYPE.SOLID
        if self._fore is None:
            self._fore = ColorFormat()

    def background(self):
        self._type = MSO_FILL_TYPE.BACKGROUND

    def gradient(self):
        # a gradient is drawn in its first colour
        self.solid()

    def patterned(self):
        self.solid()

    @property
    def fore_color(self):
        if self._type != MSO_FILL_TYPE.SOLID:
            raise TypeError("a fill's fore_color needs fill.solid() first")
        return self._fore

    @property
    def back_color(self):
        return ColorFormat()

    def _css(self):
        if self._type == MSO_FILL_TYPE.SOLID and self._fore is not None:
            return self._fore._css()
        return None
