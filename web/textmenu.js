// SPDX-License-Identifier: AGPL-3.0-or-later
// The browser's own text menu (Cut, Copy, Paste) for text drawn on the canvas.
//
// The browser sees only a picture under the pointer: a right click on drawn
// text, selected or not, opened the image menu (Save image as, Copy image).
// A right press where there is text takes a hidden field under the pointer
// first, holding the text selected there, so the menu that opens is the
// text menu: Copy copies that text, Cut and Paste are offered where the
// text is the editor's (and are done by the editor, as Ctrl+X and Ctrl+V).

// What a right press offers. `at` says what is under the pointer and what
// is selected there:
//   slide:  the selection on the slide being presented ("" none) and whether
//           the press is on the slide (overSlide)
//   doc:    the selection on the View page while it is read ("" none),
//           overDoc
//   editor: overEditor, the editor's selection, and editing (Edit pressed)
//   read:   text selected in a window (a field, the chat, a panel) and
//           overLayer (the press is on a window)
// Returns { text, editable, keep } or null when it is no text's menu (the
// app's own menus, or the browser's). `keep`: the press must not reach the
// editor, which would drop the selection under it.
export function textMenu(at) {
  if (at.overSlide) return at.slide ? { text: at.slide, editable: false, keep: true } : null;
  if (at.overLayer) return at.read ? { text: at.read, editable: false, keep: true } : null;
  // the page being read is its own text: the Markdown's selection behind it
  // is not shown
  if (at.overDoc && !at.editing) return at.doc ? { text: at.doc, editable: false, keep: true } : null;
  if (!at.overEditor) return null;
  const text = at.editor || "";
  if (!at.editing && !text) return null;
  return { text, editable: !!at.editing, keep: !!text };
}

const STYLE = "position:fixed;width:36px;height:36px;margin:0;padding:0;border:0;outline:none;resize:none;overflow:hidden;"
  + "opacity:0.05;color:transparent;background:transparent;caret-color:transparent;z-index:2147483646;";
const AWAY = STYLE + "left:-9999px;top:-9999px;pointer-events:none;";

// The hidden field. `el` is a <textarea> (the page makes it). It stays under
// the pointer until the menu has opened for it, then moves away (a click
// there later is the canvas's) but keeps the text and the keys, so the
// menu's command reaches it. `hooks` take what the menu did: copy(ev),
// cut(ev), paste(ev) with the clipboard event, remove() for its Delete.
// A copy with no hook copies the text.
export class TextTarget {
  constructor(el, later = (f, ms) => setTimeout(f, ms)) {
    this.el = el;
    this.later = later;
    this.hooks = null;
    this.back = null;
    this.text = "";
    el.setAttribute("aria-hidden", "true");
    el.setAttribute("tabindex", "-1");
    el.setAttribute("spellcheck", "false");
    el.style.cssText = AWAY;
    const on = (type, f) => el.addEventListener(type, f);
    on("contextmenu", () => this.later(() => { this.el.style.cssText = AWAY; }, 50));
    on("copy", (ev) => this.done(ev, "copy"));
    on("cut", (ev) => this.done(ev, "cut"));
    on("paste", (ev) => this.done(ev, "paste"));
    // nothing is typed into it: the menu's Delete is the editor's, any other
    // change is refused
    on("beforeinput", (ev) => {
      ev.preventDefault();
      if (/^delete/.test(ev.inputType || "") && !this.el.readOnly) this.done(ev, "remove");
    });
    // a key after the menu closed is the page's again
    on("keydown", () => this.close(true));
  }

  get open() {
    return !!this.hooks;
  }

  // Under the pointer at clientX, clientY with `menu` (textMenu's answer)
  // selected in it; `focused` gets the keys back when it is done.
  place(x, y, menu, hooks, focused) {
    const el = this.el;
    this.hooks = hooks || {};
    this.back = focused || null;
    this.text = menu.text;
    el.readOnly = !menu.editable;
    el.value = menu.text;
    el.style.cssText = STYLE + `left:${Math.round(x - 18)}px;top:${Math.round(y - 18)}px;pointer-events:auto;`;
    try { el.focus({ preventScroll: true }); } catch (_) { el.focus(); }
    try { el.setSelectionRange(0, menu.text.length); } catch (_) { /* detached */ }
  }

  done(ev, what) {
    const h = this.hooks;
    if (!h) return;
    const text = this.text;
    this.close(true);
    if (h[what]) {
      h[what](ev);
    } else if (what === "copy") {
      ev.preventDefault();
      if (ev.clipboardData) ev.clipboardData.setData("text/plain", text);
    } else {
      ev.preventDefault();
    }
  }

  // Done with: out of the way and empty; with `refocus` the keys go back
  // where they were.
  close(refocus) {
    if (!this.hooks) return;
    const back = this.back;
    this.hooks = null;
    this.back = null;
    this.text = "";
    this.el.value = "";
    this.el.style.cssText = AWAY;
    if (refocus && back && back !== this.el) {
      try { back.focus({ preventScroll: true }); } catch (_) { /* gone */ }
    }
  }
}
