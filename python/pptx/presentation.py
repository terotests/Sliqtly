"""The presentation; save() writes it as a Sliqtly deck."""

from pptx.slide import SlideMaster, Slides
from pptx.util import Emu


class CoreProperties:
    def __init__(self):
        self.title = ""
        self.subject = ""
        self.author = ""
        self.keywords = ""
        self.comments = ""
        self.category = ""
        self.last_modified_by = ""
        self.revision = 1
        self.created = None
        self.modified = None
        self.language = ""


class Presentation:
    def __init__(self):
        self._slide_width = Emu(9144000)
        self._slide_height = Emu(6858000)
        self.core_properties = CoreProperties()
        self._master = SlideMaster(self)
        self.slides = Slides(self)
        # Sliqtly additions: the deck's front matter and what could not be drawn
        self.front_matter = {}
        self.warnings = []

    @property
    def slide_width(self):
        return self._slide_width

    @slide_width.setter
    def slide_width(self, value):
        self._slide_width = Emu(value)

    @property
    def slide_height(self):
        return self._slide_height

    @slide_height.setter
    def slide_height(self, value):
        self._slide_height = Emu(value)

    @property
    def slide_master(self):
        return self._master

    @property
    def slide_masters(self):
        return [self._master]

    @property
    def slide_layouts(self):
        return self._master.slide_layouts

    @property
    def notes_master(self):
        return None

    def to_markdown(self):
        """The deck as Sliqtly Markdown."""
        from pptx._markdown import render
        return render(self)[0]

    def save(self, file):
        """Writes the deck as Sliqtly Markdown, and its pictures in media/ beside it.

        A name ending in .pptx is saved as .md (deck.pptx -> deck.md); a file
        object gets the Markdown as UTF-8. Returns the path written."""
        import os
        from pptx._markdown import render
        text, media = render(self)
        if not isinstance(file, str):
            file.write(text.encode("utf-8"))
            return None
        path = file
        root, ext = os.path.splitext(path)
        if ext.lower() in (".pptx", ".ppt", ".potx", ".pptm", ""):
            path = root + ".md"
        folder = os.path.dirname(path)
        if folder:
            os.makedirs(folder, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        if media:
            mdir = os.path.join(folder, "media") if folder else "media"
            os.makedirs(mdir, exist_ok=True)
            for name, blob in media.items():
                with open(os.path.join(mdir, name), "wb") as f:
                    f.write(blob)
        self.saved_path = path
        return path
