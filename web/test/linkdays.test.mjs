// node --test: how long a viewing link works (web/linkdays.js)
import test from "node:test";
import assert from "node:assert/strict";
import { MAX_DAYS, DAY_MS, DEFAULT_DAYS, linkDays, linkEnds, linkExpired, choiceLines, untilText } from "../linkdays.js";

test("days the cloud takes: whole, 1 to 30, else the default", () => {
  assert.equal(MAX_DAYS, 30);
  assert.equal(linkDays(1), 1);
  assert.equal(linkDays("7"), 7);
  assert.equal(linkDays(30), 30);
  assert.equal(linkDays(31), 30);
  assert.equal(linkDays(365), 30);
  assert.equal(linkDays(2.4), 2);
  assert.equal(linkDays(0), DEFAULT_DAYS);
  assert.equal(linkDays(-3), DEFAULT_DAYS);
  assert.equal(linkDays("x"), DEFAULT_DAYS);
  assert.equal(linkDays(undefined), DEFAULT_DAYS);
});

test("a link ends `days` after `since`, an old one 30 days after it was made", () => {
  assert.equal(linkEnds({ since: 1000, days: 7 }), 1000 + 7 * DAY_MS);
  assert.equal(linkEnds({ since: 1000, days: 90 }), 1000 + 30 * DAY_MS);
  assert.equal(linkEnds({ created: 500 }), 500 + 30 * DAY_MS);
  assert.equal(linkEnds({ since: 0, days: 7, created: 500 }), 500 + 30 * DAY_MS);
  assert.equal(linkEnds({}), 0);
  assert.equal(linkEnds(null), 0);
  assert.equal(linkExpired({ since: 0, days: 1 }, DAY_MS), false);
  assert.equal(linkExpired({ since: 1, days: 1 }, DAY_MS), false);
  assert.equal(linkExpired({ since: 1, days: 1 }, DAY_MS + 1), true);
});

test("the window's chips and the date said", () => {
  const t = (s) => s.toUpperCase();
  assert.equal(choiceLines(t), "1=1 DAY\n7=7 DAYS\n30=30 DAYS");
  const en = (s) => s;
  assert.equal(untilText(new Date(2026, 9, 19, 12).getTime(), en), "Works until 19.10.2026.");
  assert.equal(untilText(0, en), "");
});
