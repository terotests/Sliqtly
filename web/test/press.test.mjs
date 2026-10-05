// node --test: a press's button and keys (web/press.js)
import test from "node:test";
import assert from "node:assert/strict";
import { secondaryPress, pickKeyHeld } from "../press.js";

const ev = (o) => ({ button: 0, ctrlKey: false, metaKey: false, ...o });

test("the right button is the secondary press everywhere", () => {
  assert.equal(secondaryPress(ev({ button: 2 }), true), true);
  assert.equal(secondaryPress(ev({ button: 2 }), false), true);
});

test("Control + click is the secondary press on a Mac only", () => {
  assert.equal(secondaryPress(ev({ ctrlKey: true }), true), true);
  assert.equal(secondaryPress(ev({ ctrlKey: true }), false), false);
  // ⌘ with it is a ⌘ + click
  assert.equal(secondaryPress(ev({ ctrlKey: true, metaKey: true }), true), false);
  assert.equal(secondaryPress(ev({}), true), false);
});

test("⌘ picks on a Mac, Ctrl elsewhere; Control + click on a Mac does not pick", () => {
  assert.equal(pickKeyHeld(ev({ metaKey: true }), true), true);
  assert.equal(pickKeyHeld(ev({ ctrlKey: true }), true), false);
  assert.equal(pickKeyHeld(ev({ ctrlKey: true }), false), true);
  assert.equal(pickKeyHeld(ev({}), false), false);
});
