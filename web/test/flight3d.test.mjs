// The 3-D flight's page side (web/flight3d.js): what needs no browser.
import test from "node:test";
import assert from "node:assert/strict";
import { labelStyle, wrapText, gamepadState, canvasSize } from "../flight3d.js";

test("titles are drawn larger than a line's name", () => {
  assert.ok(labelStyle("title").px > labelStyle("label").px);
  assert.ok(labelStyle("text").px >= labelStyle("title").px);
});

test("text breaks between words, and at its own line ends", () => {
  assert.deepEqual(wrapText("one two three four", 9), ["one two", "three", "four"]);
  assert.deepEqual(wrapText("a\nb c", 40), ["a", "b c"]);
  assert.deepEqual(wrapText("averyveryverylongword", 5), ["averyveryverylongword"]);
});

test("the first connected gamepad, as numbers", () => {
  const pad = { connected: true, axes: [0.5, -1, 0, 0], buttons: [{ pressed: true, value: 1 }, { pressed: false, value: 0.25 }] };
  assert.deepEqual(gamepadState([null, { connected: false }, pad]), ["0.500,-1.000,0.000,0.000", "1,0.25"]);
  assert.deepEqual(gamepadState([]), ["", ""]);
});

test("the canvas keeps the screen's shape under its cap", () => {
  assert.deepEqual(canvasSize(1920, 1080, 1), [1920, 1080]);
  assert.deepEqual(canvasSize(1920, 1080, 2), [2560, 1440]);
});
