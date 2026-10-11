"""Enumerations named as in python-pptx."""


class _Member(int):
    """An enumeration value: an int with a name."""

    def __new__(cls, name, value, doc=""):
        m = int.__new__(cls, value)
        m._name = name
        return m

    @property
    def name(self):
        return self._name

    @property
    def value(self):
        return int(self)

    def __str__(self):
        return "%s (%d)" % (self._name, int(self))

    __repr__ = __str__


def enum(cls):
    """Turns the class's UPPER_CASE int attributes into named members."""
    members = []
    for k, v in list(vars(cls).items()):
        if k.isupper() and isinstance(v, int) and not isinstance(v, bool):
            m = _Member(k, v)
            setattr(cls, k, m)
            members.append(m)
    cls._members = members
    return cls
