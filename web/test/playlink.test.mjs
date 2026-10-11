// node --test: the light player's address and keys (web/playlink.js)
import test from "node:test";
import assert from "node:assert/strict";
import { playLinkOf, playUrl, recUrl, recActionForKey } from "../playlink.js";

test("/r/{id}/{hash} names a deck's recording", () => {
  const l = playLinkOf("/r/AbCdEf1234/0123456789abcdef01234567");
  assert.deepEqual(l, { id: "AbCdEf1234", hash: "0123456789abcdef01234567" });
  assert.equal(recUrl(l), "/api/view/AbCdEf1234/rec/0123456789abcdef01234567");
  assert.equal(playUrl("https://sliqtly.com", l.id, l.hash), "https://sliqtly.com/r/AbCdEf1234/0123456789abcdef01234567");
  assert.equal(playLinkOf("/r/AbCdEf1234/../x"), null);
  assert.equal(playLinkOf("/r/AbCdEf1234/0123456789ABCDEF01234567"), null);
  assert.equal(playLinkOf("/s/AbCdEf1234"), null);
});

test("keys: Space the steps, arrows the slides, A plays it all", () => {
  assert.equal(recActionForKey(" "), "next");
  assert.equal(recActionForKey("ArrowRight"), "nextSlide");
  assert.equal(recActionForKey("ArrowLeft"), "prevSlide");
  assert.equal(recActionForKey("Home"), "first");
  assert.equal(recActionForKey("End"), "last");
  assert.equal(recActionForKey("a"), "auto");
  assert.equal(recActionForKey("x"), "");
});
