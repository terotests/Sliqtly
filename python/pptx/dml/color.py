from pptx.enum.dml import MSO_COLOR_TYPE


class RGBColor(tuple):
    """An immutable (r, g, b) colour."""

    def __new__(cls, r, g, b):
        for v in (r, g, b):
            if not isinstance(v, int) or v < 0 or v > 255:
                raise ValueError("RGBColor() takes three integer values 0-255")
        return tuple.__new__(cls, (r, g, b))

    def __repr__(self):
        return "RGBColor(0x%02x, 0x%02x, 0x%02x)" % tuple(self)

    def __str__(self):
        return "%02X%02X%02X" % tuple(self)

    @classmethod
    def from_string(cls, rgb_hex_str):
        s = rgb_hex_str.lstrip("#")
        return cls(int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


class ColorFormat:
    """A colour set as RGB or as one of the theme's colours."""

    def __init__(self):
        self._rgb = None
        self._theme_color = None
        self.brightness = 0

    @property
    def type(self):
        if self._rgb is not None:
            return MSO_COLOR_TYPE.RGB
        if self._theme_color is not None:
            return MSO_COLOR_TYPE.SCHEME
        return None

    @property
    def rgb(self):
        return self._rgb

    @rgb.setter
    def rgb(self, value):
        if not isinstance(value, RGBColor):
            if isinstance(value, str):
                value = RGBColor.from_string(value)
            else:
                value = RGBColor(*value)
        self._rgb = value
        self._theme_color = None

    @property
    def theme_color(self):
        return self._theme_color

    @theme_color.setter
    def theme_color(self, value):
        self._theme_color = value
        self._rgb = None

    def _css(self):
        return "#" + str(self._rgb) if self._rgb is not None else None
