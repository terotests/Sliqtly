// node --test: the text menu for drawn text (web/textmenu.js)
import test from "node:test";
import assert from "node:assert/strict";
import { textMenu, TextTarget } from "../textmenu.js";

test("selected slide text while presenting: Copy only, the press kept from the slide", () => {
  assert.deepEqual(textMenu({ overSlide: true, slide: "Hello" }), { text: "Hello", editable: false, keep: true });
  // nothing selected: the slide's own press (and no text menu)
  assert.equal(textMenu({ overSlide: true, slide: "" }), null);
});

test("the View page being read: its selection to copy", () => {
  assert.deepEqual(textMenu({ overDoc: true, doc: "a b", overEditor: true }), { text: "a b", editable: false, keep: true });
  // no selection on the page and not editing: no text menu
  assert.equal(textMenu({ overDoc: true, doc: "", overEditor: true, editing: false }), null);
  // …nor the Markdown's selection, which the page does not show
  assert.equal(textMenu({ overDoc: true, doc: "", overEditor: true, editor: "x", editing: false }), null);
  // edited, the page's selection is the Markdown's
  assert.deepEqual(textMenu({ overDoc: true, doc: "", overEditor: true, editor: "x", editing: true }), { text: "x", editable: true, keep: true });
});

test("the editor: Cut, Copy and Paste while editing, Copy only while reading", () => {
  assert.deepEqual(textMenu({ overEditor: true, editor: "x = 1", editing: true }), { text: "x = 1", editable: true, keep: true });
  assert.deepEqual(textMenu({ overEditor: true, editor: "x = 1", editing: false }), { text: "x = 1", editable: false, keep: true });
  // nothing selected while editing: Paste where the press puts the caret
  assert.deepEqual(textMenu({ overEditor: true, editor: "", editing: true }), { text: "", editable: true, keep: false });
  assert.equal(textMenu({ overEditor: true, editor: "", editing: false }), null);
});

test("a window's selection (a field, the chat): Copy over the window only", () => {
  assert.deepEqual(textMenu({ overLayer: true, read: "link", overEditor: false }), { text: "link", editable: false, keep: true });
  assert.equal(textMenu({ overLayer: true, read: "" }), null);
  // a window over the editor takes the press: not the editor's text
  assert.equal(textMenu({ overLayer: true, read: "", overEditor: true, editor: "x", editing: true }), null);
  assert.equal(textMenu({ overLayer: false, read: "link" }), null);
});

test("anywhere else: the app's menus", () => {
  assert.equal(textMenu({}), null);
});

// a <textarea> enough for the target
class FakeArea extends EventTarget {
  constructor() {
    super();
    this.attrs = {};
    this.style = { cssText: "" };
    this.value = "";
    this.readOnly = false;
    this.sel = null;
    this.focused = false;
  }
  setAttribute(k, v) { this.attrs[k] = v; }
  focus() { this.focused = true; }
  setSelectionRange(a, b) { this.sel = [a, b]; }
}
const clipEvent = (type) => {
  const ev = new Event(type, { cancelable: true });
  const data = {};
  ev.clipboardData = { setData: (k, v) => { data[k] = v; }, getData: (k) => data[k] || "" };
  ev.data = data;
  return ev;
};

test("placed under the pointer with the text selected, moved away once the menu opened", () => {
  const el = new FakeArea();
  const timers = [];
  const t = new TextTarget(el, (f) => timers.push(f));
  assert.equal(el.attrs["aria-hidden"], "true");
  assert.match(el.style.cssText, /pointer-events:none/);
  t.place(100, 50, { text: "Hello", editable: false }, {}, null);
  assert.equal(t.open, true);
  assert.equal(el.value, "Hello");
  assert.equal(el.readOnly, true);
  assert.deepEqual(el.sel, [0, 5]);
  assert.equal(el.focused, true);
  assert.match(el.style.cssText, /left:82px;top:32px;pointer-events:auto/);
  el.dispatchEvent(new Event("contextmenu"));
  timers.forEach((f) => f());
  assert.match(el.style.cssText, /pointer-events:none/);
  // the text and the keys stay for the menu's command
  assert.equal(el.value, "Hello");
  assert.equal(t.open, true);
});

test("Copy with no hook copies the text; the keys go back", () => {
  const el = new FakeArea();
  const keys = { focused: false, focus() { this.focused = true; } };
  const t = new TextTarget(el);
  t.place(0, 0, { text: "Hi", editable: false }, {}, keys);
  const ev = clipEvent("copy");
  el.dispatchEvent(ev);
  assert.equal(ev.data["text/plain"], "Hi");
  assert.equal(ev.defaultPrevented, true);
  assert.equal(keys.focused, true);
  assert.equal(t.open, false);
  assert.equal(el.value, "");
});

test("Cut, Paste and Delete go to the hooks, with the keys back first", () => {
  const el = new FakeArea();
  const keys = { focused: false, focus() { this.focused = true; } };
  const t = new TextTarget(el);
  const got = [];
  const hooks = {
    cut: (ev) => got.push(["cut", keys.focused, ev.type]),
    paste: (ev) => got.push(["paste", keys.focused, ev.type]),
    remove: () => got.push(["remove", keys.focused]),
  };
  t.place(0, 0, { text: "abc", editable: true }, hooks, keys);
  assert.equal(el.readOnly, false);
  el.dispatchEvent(clipEvent("cut"));
  keys.focused = false;
  t.place(0, 0, { text: "", editable: true }, hooks, keys);
  el.dispatchEvent(clipEvent("paste"));
  keys.focused = false;
  t.place(0, 0, { text: "abc", editable: true }, hooks, keys);
  const del = new Event("beforeinput", { cancelable: true });
  del.inputType = "deleteContentBackward";
  el.dispatchEvent(del);
  assert.equal(del.defaultPrevented, true);
  assert.deepEqual(got, [["cut", true, "cut"], ["paste", true, "paste"], ["remove", true]]);
});

test("typing into it is refused; a key gives the keys back", () => {
  const el = new FakeArea();
  const keys = { focused: false, focus() { this.focused = true; } };
  const t = new TextTarget(el);
  let removed = false;
  t.place(0, 0, { text: "abc", editable: false }, { remove: () => { removed = true; } }, keys);
  const typed = new Event("beforeinput", { cancelable: true });
  typed.inputType = "insertText";
  el.dispatchEvent(typed);
  assert.equal(typed.defaultPrevented, true);
  // read only: no Delete either
  const del = new Event("beforeinput", { cancelable: true });
  del.inputType = "deleteContentBackward";
  el.dispatchEvent(del);
  assert.equal(removed, false);
  el.dispatchEvent(new Event("keydown"));
  assert.equal(keys.focused, true);
  assert.equal(t.open, false);
});
