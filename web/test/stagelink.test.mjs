// node --test: a link clicked on a slide (web/stagelink.js)
import test from "node:test";
import assert from "node:assert/strict";
import { linkTarget, FOLLOW_MS, returnTo } from "../stagelink.js";

const O = "https://sliqtly.com";

test("a Sliqtly presentation's link is a deck, with its slide", () => {
  assert.deepEqual(linkTarget("https://sliqtly.com/s/AbCdEf1234", O, O), { deck: "AbCdEf1234", slide: 0, name: "", url: "https://sliqtly.com/s/AbCdEf1234" });
  assert.equal(linkTarget("https://sliqtly.com/s/AbCdEf1234?slide=3", O, O).slide, 2);
  assert.equal(linkTarget("https://sliqtly.com/s/AbCdEf1234#slide=2", O, O).slide, 1);
  // the editor's address, and one relative to this page
  assert.equal(linkTarget("https://sliqtly.com/editor/s/AbCdEf1234?edit", O, O).deck, "AbCdEf1234");
  assert.equal(linkTarget("/s/AbCdEf1234", O, O).deck, "AbCdEf1234");
  assert.equal(linkTarget("https://sliqtly.com/editor/d/AbCdEf1234", O, O).deck, "AbCdEf1234");
  // a heading of the other deck, found when it is open
  assert.equal(linkTarget("/s/AbCdEf1234#pdf-export", O, O).name, "#pdf-export");
  assert.equal(linkTarget("/s/AbCdEf1234#slide=2", O, O).name, "");
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

test("Back and Forward open the deck the step was kept with", () => {
  const open = { deck: "Child12345", doc: "d2" };
  // the master deck the link was followed from
  assert.deepEqual(returnTo({ deck: "Master1234", doc: "d1" }, O + "/editor/d/Master1234#tab=view", O, O, open), { deck: "Master1234" });
  // a step whose state was dropped: its address names the deck
  assert.deepEqual(returnTo(null, O + "/editor/d/Master1234#tab=view&slide=3", O, O, open), { deck: "Master1234" });
  assert.deepEqual(returnTo(null, "http://localhost:8080/s/Master1234?edit", "http://localhost:8080", O, open), { deck: "Master1234" });
  // a deck kept in this browser only, which has no address of its own
  assert.deepEqual(returnTo({ deck: "", doc: "d1" }, "http://localhost:8080/#tab=view", "http://localhost:8080", O, open), { doc: "d1" });
});

test("a step within the deck shown opens nothing", () => {
  const open = { deck: "Child12345", doc: "d2" };
  assert.equal(returnTo({ deck: "Child12345", doc: "d2" }, O + "/editor/d/Child12345#slide=2", O, O, open), null);
  assert.equal(returnTo(null, O + "/editor/d/Child12345#slide=2", O, O, open), null);
  assert.equal(returnTo(null, O + "/editor/#tab=view", O, O, open), null);
  assert.equal(returnTo({ deck: "", doc: "d2" }, "http://localhost:8080/", "http://localhost:8080", O, { deck: "", doc: "d2" }), null);
});
