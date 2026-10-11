"""python-pptx's object model, drawn as a Sliqtly deck.

Code written for python-pptx runs as it is; prs.save() writes Sliqtly
Markdown instead of a .pptx file. See python/README.md in the Sliqtly
repository for what is supported."""

from pptx.api import Presentation

__version__ = "1.0.2"

__all__ = ["Presentation"]
