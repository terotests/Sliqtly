"""Sliqtly additions to the python-pptx object model.

    from sliqtly import animate, transition, deck

    animate(box).rise(duration=0.5)       # the block comes in rising
    animate(body).fade().build()          # a list, one item at a time
    transition(slide, "zoom", seconds=0.6)
    deck(prs, lang="fi", footer_right="{page} / {pages}")

Shapes also have add_vega(spec), add_mermaid(text), add_svg(text),
add_diagram(language, text) and add_markdown(text) (see pptx.shapes)."""

_ANIMS = ("fade", "rise", "fly", "zoom")


class Animation:
    def __init__(self, shape):
        self._shape = shape

    def _set(self, name, duration):
        a = {"anim": name}
        if duration is not None:
            a["seconds"] = duration
        self._shape._anim = a
        return self

    def fade(self, duration=None):
        return self._set("fade", duration)

    def rise(self, duration=None):
        return self._set("rise", duration)

    def fly(self, duration=None):
        return self._set("fly", duration)

    def zoom(self, duration=None):
        return self._set("zoom", duration)

    def build(self):
        """A list comes in one item at a time (it does so whenever it is animated)."""
        if self._shape._anim is None:
            self._shape._anim = {"anim": "fade"}
        return self


def animate(shape, effect=None, duration=None):
    """The shape's block animates in: animate(shape).rise(0.5) or animate(shape, "zoom")."""
    a = Animation(shape)
    if effect is not None:
        if effect not in _ANIMS:
            raise ValueError("effect must be one of %s" % ", ".join(_ANIMS))
        a._set(effect, duration)
    return a


def transition(slide, kind="fade", seconds=None):
    """The slide's transition: fade, slide, zoom, morph or none."""
    slide._attrs["transition"] = kind
    if seconds is not None:
        slide._attrs["seconds"] = seconds


def attrs(target, **values):
    """Sliqtly attributes on a slide's heading or a shape's block: attrs(slide, fx="starfield")."""
    for k, v in values.items():
        target._attrs[k.replace("_", "-")] = v


def deck(prs, **front_matter):
    """Front matter keys for the deck: deck(prs, lang="fi", footer_right="{page}")."""
    for k, v in front_matter.items():
        prs.front_matter[k.replace("_", "-")] = v


def to_markdown(prs):
    """The presentation as Sliqtly Markdown."""
    return prs.to_markdown()
