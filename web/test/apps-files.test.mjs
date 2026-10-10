// node --test: the deck's files a program imports (web/apps-runtime.js
// __deckFiles, put on the program's first line by src/PresPlayFiles.rgr):
// each a module, JSON parsed, CSV and TSV rows, any other file its text.
import test from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import { DECK_RUNTIME } from "../apps-runtime.js";

function modules(list) {
  const out = {};
  const ctx = vm.createContext({ JSON, Math, defineModule: (name, exports) => { out[name] = exports; return exports; } });
  vm.runInContext(DECK_RUNTIME, ctx);
  ctx.__deckFiles(list);
  return out;
}

test("JSON is its value, CSV its rows by the header, a text file its text", () => {
  const m = modules([
    ["./world.json", "apps/world.json", '{"speed": 2, "names": ["a"]}'],
    ["../data/s.csv", "data/s.csv", '﻿name, count\r\n"Ada, ""Jr""",3\r\nBo,-1.5e2\r\n\r\nCy,\r\n'],
    ["data/t.tsv", "data/t.tsv", "a\tb\n1\tx y\n"],
    ["data/n.md", "data/n.md", "# Hei\n"],
  ]);
  assert.equal(m["./world.json"].default.speed, 2);
  assert.equal(m["./world.json"].text, '{"speed": 2, "names": ["a"]}');
  const rows = JSON.parse(JSON.stringify(m["../data/s.csv"].default));
  assert.deepEqual(rows, [{ name: 'Ada, "Jr"', count: 3 }, { name: "Bo", count: -150 }, { name: "Cy", count: "" }]);
  assert.deepEqual(JSON.parse(JSON.stringify(m["../data/s.csv"].rows[0])), ["name", " count"]);
  assert.deepEqual(JSON.parse(JSON.stringify(m["data/t.tsv"].default)), [{ a: 1, b: "x y" }]);
  assert.equal(m["data/n.md"].default, "# Hei\n");
});

test("a file the deck lacks or JSON that does not parse throws where it is read, naming the file", () => {
  const m = modules([["../data/none.json", "data/none.json", null], ["./bad.json", "apps/bad.json", "{nope"]]);
  assert.throws(() => m["../data/none.json"].default, /data\/none\.json is not a file of the presentation/);
  assert.throws(() => m["./bad.json"].default, /apps\/bad\.json is not JSON/);
  assert.throws(() => m["./bad.json"].text, /apps\/bad\.json is not JSON/);
});
