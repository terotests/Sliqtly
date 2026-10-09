// node --test: a link clicked on a slide (web/stagelink.js)
import test from "node:test";
import assert from "node:assert/strict";
import { linkTarget, FOLLOW_MS } from "../stagelink.js";

const O = "https://sliqtly.com";

test("a Sliqtly presentation's link is a deck, with its slide", () => {
  assert.deepEqual(linkTarget("https://sliqtly.com/s/AbCdEf1234", O, O), { deck: "AbCdEf1234", slide: 0, url: "https://sliqtly.com/s/AbCdEf1234" });
  assert.equal(linkTarget("https://sliqtly.com/s/AbCdEf1234?slide=3", O, O).slide, 2);
  assert.equal(linkTarget("https://sliqtly.com/s/AbCdEf1234#slide=2", O, O).slide, 1);
  // the editor's address, and one relative to this page
  assert.equal(linkTarget("https://sliqtly.com/editor/s/AbCdEf1234?edit", O, O).deck, "AbCdEf1234");
  assert.equal(linkTarget("/s/AbCdEf1234", O, O).deck, "AbCdEf1234");
});

test("a local server's page knows the public site's links too", () => {
  const local = "http://localhost:8080";
  assert.equal(linkTarget("https://sliqtly.com/s/AbCdEf1234", local, O).deck, "AbCdEf1234");
  assert.equal(linkTarget("http://localhost:8080/s/AbCdEf1234", local, O).deck, "AbCdEf1234");
});

test("other addresses open as they are; script and mail addresses do not", () => {
  assert.deepEqual(linkTarget("https://example.com/a?b=1", O, O), { url: "https://example.com/a?b=1" });
  assert.deepEqual(linkTarget("https://example.com/s/AbCdEf1234", O, O), { url: "https://example.com/s/AbCdEf1234" });
  assert.deepEqual(linkTarget("https://sliqtly.com/pricing", O, O), { url: "https://sliqtly.com/pricing" });
  assert.equal(linkTarget("javascript:alert(1)", O, O), null);
  assert.equal(linkTarget("mailto:a@b.c", O, O), null);
  assert.equal(linkTarget("", O, O), null);
  assert.equal(linkTarget("https://sliqtly.com/s/AbCdEf1234", "http://localhost:8080", "not a url").url, "https://sliqtly.com/s/AbCdEf1234");
});

test("the wait outlasts the page's double click", () => {
  assert.ok(FOLLOW_MS > 400);
});
