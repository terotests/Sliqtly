from pptx import presentation


def Presentation(pptx=None):
    """A new presentation. A template file can't be read here: the deck starts
    from python-pptx's default template, and the theme is Sliqtly's."""
    prs = presentation.Presentation()
    if pptx is not None:
        prs.warnings.append("Presentation(%r): templates are not read; the default layouts are used" % (pptx if isinstance(pptx, str) else "file",))
    return prs
