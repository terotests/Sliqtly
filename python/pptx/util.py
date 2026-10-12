"""Lengths as python-pptx has them: an int of EMU (English Metric Units)."""


class Length(int):
    _EMUS_PER_INCH = 914400
    _EMUS_PER_CENTIPOINT = 127
    _EMUS_PER_CM = 360000
    _EMUS_PER_MM = 36000
    _EMUS_PER_PT = 12700

    def __new__(cls, emu):
        return int.__new__(cls, int(emu))

    @property
    def inches(self):
        return self / float(self._EMUS_PER_INCH)

    @property
    def centipoints(self):
        return self // self._EMUS_PER_CENTIPOINT

    @property
    def cm(self):
        return self / float(self._EMUS_PER_CM)

    @property
    def mm(self):
        return self / float(self._EMUS_PER_MM)

    @property
    def pt(self):
        return self / float(self._EMUS_PER_PT)

    @property
    def emu(self):
        return int(self)


class Inches(Length):
    def __new__(cls, inches):
        return Length.__new__(cls, round(inches * Length._EMUS_PER_INCH))


class Centipoints(Length):
    def __new__(cls, centipoints):
        return Length.__new__(cls, round(centipoints * Length._EMUS_PER_CENTIPOINT))


class Cm(Length):
    def __new__(cls, cm):
        return Length.__new__(cls, round(cm * Length._EMUS_PER_CM))


class Emu(Length):
    def __new__(cls, emu):
        return Length.__new__(cls, emu)


class Mm(Length):
    def __new__(cls, mm):
        return Length.__new__(cls, round(mm * Length._EMUS_PER_MM))


class Pt(Length):
    def __new__(cls, points):
        return Length.__new__(cls, round(points * Length._EMUS_PER_PT))


def lazyproperty(f):
    return property(f)
