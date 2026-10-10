// node --test: the "ranger:three" module (web/apps-runtime.js). A file
// written for Ranger v2's 3-D façade, courtyard_live.tsx as Ranger has it
// (scripts/fixtures), runs as it is: init() once, tick(dt) in milliseconds,
// and each frame's tree is the <scene3d> its objects make.
//
// Runs the built page runtime (web/dist, `npm run build`) in a vm context, as
// CErXes runs it; the import is the one line read here as CErXes reads it.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { DECK_RUNTIME } from "../apps-runtime.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const runtimeJs = path.join(here, "..", "dist", "cerxes-runtime.js");
if (!fs.existsSync(runtimeJs)) throw new Error("web/dist/cerxes-runtime.js is missing: run `npm run build` first");
const { RUNTIME } = await import(runtimeJs);
const courtyard = fs.readFileSync(path.join(here, "..", "..", "scripts", "fixtures", "courtyard_live.tsx"), "utf8");

function run(source) {
  const modules = {};
  const ctx = vm.createContext({ console, JSON, Math, defineModule: (name, exports) => { modules[name] = exports; } });
  vm.runInContext(RUNTIME + "\n" + DECK_RUNTIME, ctx);
  ctx.__modules = modules;
  const code = source.replace(/^import \* as (\w+) from "([^"]+)";$/m, (_, name, from) => `var ${name} = __modules[${JSON.stringify(from)}];`);
  vm.runInContext(code, ctx);
  return ctx;
}

function frame(ctx, dt) {
  const arg = { w: 480, h: 270, dt, time: 0, keys: [], pointer: { x: 0, y: 0, down: false }, events: [], deck: {} };
  const text = ctx.__deckFrame(JSON.stringify(arg));
  return JSON.parse(text.slice(0, text.lastIndexOf("\n")));
}

const tags = (n) => (n.c || []).map((k) => k.t);

test("courtyard_live.tsx as Ranger has it is a world", () => {
  const ctx = run(courtyard);
  const w = frame(ctx, 0);
  assert.equal(w.t, "scene3d");
  assert.deepEqual(w.a, { legacy: "true", ambient: "0" }, "drawn as Ranger draws it");
  assert.deepEqual(tags(w), ["perspectiveCamera", "mesh", "mesh", "mesh", "mesh", "mesh", "mesh", "mesh", "mesh", "mesh",
    "ambientLight", "directionalLight", "directionalLight", "directionalLight"]);
  const floor = w.c[1];
  assert.deepEqual(tags(floor), ["planeGeometry", "meshLambertMaterial"]);
  assert.equal(floor.c[0].a.args, "60,60,24,24");
  assert.equal(floor.a.rotation, "-1.5708,0,0");
  const red = w.c[2];
  assert.equal(red.a.position, "-6,3,-6", "a box rests on the floor");
  assert.equal(red.c[1].a.color, "#d9534f");
  const sun = w.c[11];
  assert.deepEqual(sun.a, { color: "#ffffff", intensity: "1.2", position: "0.4,0.85,0.3" });
  const cam = w.c[0].a;
  assert.equal(cam.fov, "55");
  assert.equal(cam.rotation, "-0.34,0.6,0");
});

test("init() runs once and tick(dt) gets milliseconds", () => {
  const ctx = run(courtyard);
  frame(ctx, 0);
  // a second at 0.00024 rad/ms turns the orbit 0.24 rad
  const w = frame(ctx, 1);
  const rot = w.a ? w.c[0].a.rotation.split(",").map(Number) : [];
  assert.ok(Math.abs(rot[1] - 0.84) < 1e-9, `the camera's yaw after one second, got ${rot[1]}`);
  assert.equal(tags(w).filter((t) => t === "mesh").length, 9, "the scene was built once");
});

test("groups, models, scale and removal", () => {
  const ctx = run(`import * as THREE from "ranger:three";
let s, g, m, gone;
function init() {
  s = new THREE.Scene();
  new THREE.PerspectiveCamera(50, 1, 0.1, 100);
  g = new THREE.Group();
  s.add(g);
  g.setTransform(1, 0, 0, 0, 0, 0);
  m = new THREE.Mesh(new THREE.OctahedronGeometry(2), new THREE.MeshPhongMaterial(255, 16777215, 40));
  g.add(m);
  m.setScale(2, 0, 2);
  const model = new THREE.GLTFModel("pkg://data/robot.gltf");
  s.add(model);
  gone = new THREE.Mesh(new THREE.BoxGeometry(1, 1, 1), new THREE.MeshBasicMaterial(0));
  s.add(gone);
  s.remove(gone);
}
`);
  const w = frame(ctx, 0);
  assert.deepEqual(tags(w), ["perspectiveCamera", "group", "sliqGltf"]);
  const mesh = w.c[1].c[0];
  assert.equal(mesh.a.scale, "2,1,2", "0 is read as 1");
  assert.equal(mesh.c[0].t, "octahedronGeometry");
  assert.deepEqual(mesh.c[1].a, { color: "#0000ff", specular: "#ffffff", shininess: "40" });
  assert.equal(w.c[2].a.src, "data/robot.gltf");
});

test("OrbitControls turns the camera round its target and looks at it", () => {
  const ctx = run(`import * as THREE from "ranger:three";
let cam, orbit;
function init() {
  new THREE.Scene();
  cam = new THREE.PerspectiveCamera(50, 1, 0.1, 100);
  cam.setPose(0, 0, 10, 0, 0, 0);
  orbit = new THREE.OrbitControls(cam);
  orbit.setViewport(400, 400);
}
function tick() {
  orbit.pointerDown(0, 0, 0);
  orbit.pointerMove(-100, 0);
  orbit.pointerUp();
  orbit.apply();
}
`);
  const w = frame(ctx, 0);
  const pos = w.c[0].a.position.split(",").map(Number);
  // a quarter of the viewport's height turns it a quarter round: to +x
  assert.ok(Math.abs(pos[0] - 10) < 1e-9 && Math.abs(pos[2]) < 1e-9, `got ${pos}`);
  const rot = w.c[0].a.rotation.split(",").map(Number);
  assert.ok(Math.abs(rot[1] - Math.PI / 2) < 1e-9, `facing -x, got ${rot}`);
});

test("a program with view() is not a ranger:three file", () => {
  const ctx = run(`function init() { throw new Error("not called"); }
function view() { return { type: "scene3d", props: {}, children: [] }; }
`);
  assert.equal(frame(ctx, 0).t, "scene3d");
});
