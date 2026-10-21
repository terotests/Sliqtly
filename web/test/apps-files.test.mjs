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

// A library of the deck (apps/x.ts): CErXes keeps its code under the
// program's import and its path (defineSourceModule) and runs it on its
// first import; a data file is known by its path too, so a library's import
// of it finds the same module.
test("a library is given to the engine as source, a data file by its import and its path", () => {
  const sources = [];
  const out = {};
  const ctx = vm.createContext({
    JSON, Math,
    defineModule: (name, exports) => { out[name] = exports; return exports; },
    defineSourceModule: (name, source, path) => { sources.push([name, source, path]); },
  });
  vm.runInContext(DECK_RUNTIME, ctx);
  ctx.__deckFiles([
    ["./scene.ts", "apps/scene.ts", "export const W = 1;"],
    ["apps/gone.ts", "apps/gone.ts", null],
    ["../data/s.csv", "data/s.csv", "a\n1\n"],
  ]);
  assert.deepEqual(sources, [["./scene.ts", "export const W = 1;", "apps/scene.ts"]]);
  assert.throws(() => out["apps/gone.ts"].default, /apps\/gone\.ts is not a file of the presentation/);
  assert.equal(out["../data/s.csv"], out["data/s.csv"]);
});

test("an engine without source modules says so where the library is read", () => {
  const out = modules([["./scene.ts", "apps/scene.ts", "export const W = 1;"]]);
  assert.throws(() => out["./scene.ts"].default, /too old to import apps\/scene\.ts/);
});

// The helpers of the "Sliqtly" module, and its camera: where a point is
// drawn as <scene3d fit="box"> with <perspectiveCamera> draws it.
test("the Sliqtly helpers: numbers, vectors, colours, and the camera's projection", () => {
  const ctx = vm.createContext({ JSON, Math });
  vm.runInContext(DECK_RUNTIME, ctx);
  const S = ctx.__sliqtly;
  assert.equal(S.lerp(2, 4, 0.25), 2.5);
  assert.deepEqual([S.clamp(-1), S.clamp(2), S.clamp(5, 0, 10)], [0, 1, 5]);
  assert.deepEqual([S.smoothstep(2, 4, 1), S.smoothstep(2, 4, 3), S.smoothstep(2, 4, 9)], [0, 0.5, 1]);
  assert.equal(S.mixColor("#000000", "#ffffff", 0.5), "#808080");
  assert.equal(S.mixColor("#f00", "#00f", 2), "#0000ff");
  assert.deepEqual(Array.from(S.vec3.cross([1, 0, 0], [0, 1, 0])), [0, 0, 1]);
  assert.equal(S.vec3.distance([0, 0, 0], [3, 4, 0]), 5);
  assert.deepEqual(Array.from(S.vec3.normalize([0, 0, 2])), [0, 0, 1]);
  const o = S.orbit(2, 1, Math.PI / 2);
  assert.ok(Math.abs(o[0] - 2) < 1e-9 && o[1] === 1 && Math.abs(o[2]) < 1e-9);
  const cam = S.camera({ position: [0, 0, 10], lookAt: [0, 0, 0], fov: 90, width: 400, height: 200 });
  const near = (a, b) => a.every((v, i) => Math.abs(v - b[i]) < 1e-9);
  // the point looked at is the box's centre, its distance the depth
  assert.ok(near(cam.project([0, 0, 0]), [200, 100, 10]));
  // fov 90: the top edge is as high as the point is far; across, the aspect
  assert.ok(near(cam.project([0, 10, 0]), [200, 0, 10]));
  assert.ok(near(cam.project([20, 0, 0]), [400, 100, 10]));
  assert.ok(cam.project([0, 0, 20])[2] < 0, "behind the camera");
  cam.position = [0, 10, 0];
  assert.ok(near(cam.project([0, 0, 0]), [200, 100, 10]), "looking straight down");
});
