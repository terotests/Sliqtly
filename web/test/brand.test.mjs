// node --test: Sliqtly's intro on shared presentations (web/brand.js)
import test from "node:test";
import assert from "node:assert/strict";
import { wantsIntro, INTRO_MS } from "../brand.js";

test("every cloud share gets the intro, a PRO owner's too", () => {
  assert.equal(wantsIntro({ from: "share", owner: "Xy12abcUID" }), true);
  assert.equal(wantsIntro({ from: "share", owner: "mcp" }), true);
  assert.equal(wantsIntro({ from: "share" }), true);
});

test("a link carrying the Markdown gets the intro", () => {
  assert.equal(wantsIntro({ from: "link" }), true);
});

test("a presentation exported as a player file gets the intro", () => {
  assert.equal(wantsIntro({ from: "file" }), true);
});

test("the editor and anything else get none", () => {
  assert.equal(wantsIntro({}), false);
  assert.equal(wantsIntro(), false);
  assert.equal(wantsIntro({ from: "edit" }), false);
});

test("the intro is a couple of seconds", () => {
  assert.ok(INTRO_MS >= 1500 && INTRO_MS <= 4000);
});
