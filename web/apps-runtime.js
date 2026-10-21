// SPDX-License-Identifier: AGPL-3.0-or-later
//
// What a program on a slide (```app) can ask of its deck, run in CErXes after
// componentengine's runtime (cerxes-runtime.js: __frame, the JSX tree's
// serializer) and before the program itself.
//
//   deck.data            the deck's own keys ({key} in a header or footer)
//   deck.set(key, v)     a key's value for as long as the deck is open    allow: deck.data
//   slide.number/count   where the deck is (1-based), slide.home: the program's own slide
//   slide.next() prev() go(n) build()                                     allow: slide.nav
//   el("#id"/".class").style({...}) show() hide() reset()                 allow: slide.style
//   machine.send(event, data)                                             allow: machine
//   presentation.code.open("src/a.cpp#L40-88", {mode}) openNode(box) close()
//     setMode(m) goToLine(n) scroll(rows) setZoom(level) showTab(n)
//     nextChange() prevChange(); isOpen path tab tabs mode zoom line        allow: code
//   <SliqRod from to radius length />  a rod between two points in a <scene3d>    allow: 3d
//   <SliqGltf src="data/x.glb" />   a .gltf/.glb of the deck in a <scene3d>           allow: 3d
//   import * as THREE from "ranger:three"  Ranger v2's 3-D façade (init, tick)     allow: 3d
//   sound.play("shot") tone({wave, freq, to, dur, vol}) noise({dur, from, to})
//     stop()   synthesized on the page, web/sound.js                       allow: sound
//   presentation.video({ seconds })  how long the slide stays when the deck
//     plays on its own (video, light player, autoplay): a demo that runs on
//     its own clock is seen through; said with the next frame
//   import { vec3, lerp, clamp, smoothstep, mixColor, orbit, camera } from "Sliqtly"
//     the helpers programs keep writing: vector math, easing, colours, and
//     a camera that projects a world point to the program's own units as
//     <scene3d fit="box"> with <perspectiveCamera> draws it (labels on 3-D)
//   the deck's own libraries (apps/x.ts, .tsx, .js) a program imports by
//     its path, each run once (__deckFiles)
//
// Nothing here changes the deck: each call is a request the page reads after
// the frame (PresApp.playAsks), and grants or refuses by the fence's allow:.
// __deckFrame wraps __frame: the deck's state in, the tree and the asks out
// (two JSON texts, a line break between).
export const DECK_RUNTIME = String.raw`
var __asks = [];
var __deck = {
  data: {},
  set: function (key, value) { __asks.push({ k: "deck.set", key: String(key), value: value === undefined || value === null ? "" : String(value) }); },
  get: function (key) { var v = __deck.data[String(key).toLowerCase()]; return v === undefined ? "" : v; }
};
var __slide = {
  number: 1, count: 1, home: 1, step: 0, presenting: false, focused: false,
  next: function () { __asks.push({ k: "slide.next" }); },
  prev: function () { __asks.push({ k: "slide.prev" }); },
  go: function (n) { __asks.push({ k: "slide.go", n: Math.floor(Number(n) || 0) }); },
  build: function () { __asks.push({ k: "slide.step" }); }
};
// the names a program uses; the runtime keeps its own (__deck, __slide),
// so a program's own const slide = … does not take them from it
var deck = __deck;
var slide = __slide;
var __UNITLESS = { opacity: 1, zIndex: 1, flex: 1, fontWeight: 1, lineHeight: 1 };
function el(sel) {
  sel = String(sel);
  return {
    style: function (s) {
      var out = {};
      for (var k in s) {
        var v = s[k];
        if (v === null || v === undefined) continue;
        out[k] = typeof v === "number" && !__UNITLESS[k] ? v + "px" : String(v);
      }
      __asks.push({ k: "el.style", sel: sel, style: out });
      return this;
    },
    show: function () { __asks.push({ k: "el.show", sel: sel }); return this; },
    hide: function () { __asks.push({ k: "el.hide", sel: sel }); return this; },
    reset: function () { __asks.push({ k: "el.reset", sel: sel }); return this; }
  };
}
// Sliqtly's own 3-D pieces beside Three's (allow: 3d, src/Pres3DTree.rgr):
// a component per piece, an element the world reads.
function SliqRod(p) { return __jsx("sliqRod", p); }
function SliqGltf(p) { return __jsx("sliqGltf", p); }
var machine = {
  state: "",
  send: function (event, data) { __asks.push({ k: "machine.send", event: String(event), data: data === undefined ? null : data }); }
};
// Sounds the page synthesizes (web/sound.js): a preset by name, or parts
// of one's own; the page plays them with the frame (allow: sound).
var sound = {
  play: function (what) {
    if (typeof what === "string") __asks.push({ k: "sound.play", name: what });
    else if (Array.isArray(what)) __asks.push({ k: "sound.play", parts: what });
  },
  tone: function (p) { __asks.push({ k: "sound.tone", part: p || {} }); },
  noise: function (p) { __asks.push({ k: "sound.noise", part: p || {} }); },
  stop: function () { __asks.push({ k: "sound.stop" }); }
};
// The "Sliqtly" module: import { presentation } from "Sliqtly". The slide
// shown is presentation.activeSlide; moving between slides asks as
// slide.next() does (allow: slide.nav), presentation.set as deck.set
// (allow: deck.data).
var __activeSlide = {};
Object.defineProperty(__activeSlide, "index", { enumerable: true, get: function () { return __slide.number; } });
Object.defineProperty(__activeSlide, "step", { enumerable: true, get: function () { return __slide.step; } });
Object.defineProperty(__activeSlide, "from", { enumerable: true, get: function () { return __slide.from || 0; } });
Object.defineProperty(__activeSlide, "presenting", { enumerable: true, get: function () { return __slide.presenting; } });
__activeSlide.build = function () { __slide.build(); };
var presentation = {
  activeSlide: __activeSlide,
  next: function () { __slide.next(); },
  prev: function () { __slide.prev(); },
  go: function (n) { __slide.go(n); },
  get: function (key) { return __deck.get(key); },
  set: function (key, value) { __deck.set(key, value); }
};
Object.defineProperty(presentation, "slides", { enumerable: true, get: function () { return __slide.count; } });
Object.defineProperty(presentation, "data", { enumerable: true, get: function () { return __deck.data; } });
// The source code viewer over a presented slide (src/PresCodeViewUi.rgr):
// what it shows read each frame, asks as the rest (allow: code). Opens
// only while presenting; line is the new version's line at its top.
var __code = {
  isOpen: false, path: "", tab: 0, tabs: 0, mode: "", zoom: 0, line: 0,
  open: function (target, o) { __asks.push({ k: "code.open", value: String(target || ""), key: o && o.mode ? String(o.mode) : "" }); },
  openNode: function (box) { __asks.push({ k: "code.node", value: String(box || "") }); },
  close: function () { __asks.push({ k: "code.close" }); },
  setMode: function (m) { __asks.push({ k: "code.mode", value: String(m) }); },
  goToLine: function (n) { __asks.push({ k: "code.line", n: Math.floor(Number(n) || 0) }); },
  scroll: function (rows) { __asks.push({ k: "code.scroll", n: Math.round(Number(rows) || 0) }); },
  setZoom: function (level) { __asks.push({ k: "code.zoom", n: Math.round(Number(level) || 0) }); },
  showTab: function (n) { __asks.push({ k: "code.tab", n: Math.floor(Number(n) || 0) }); },
  nextChange: function () { __asks.push({ k: "code.change", n: 1 }); },
  prevChange: function () { __asks.push({ k: "code.change", n: -1 }); }
};
presentation.code = __code;
// How long the program wants its slide when the deck plays on its own: a
// number or { seconds }. Said once, with the frame after the call (no
// allow: needed; it changes no slide); a program calls it at its top, so
// the page learns it from the first frame (PresPlayAsks.read caps it).
var __video = null;
presentation.video = function (o) {
  var s = Number(o !== null && typeof o === "object" ? o.seconds : o);
  __video = { k: "video", seconds: isFinite(s) && s > 0 ? s : 0 };
};
function __sayVideo() {
  if (__video) __asks.push(__video);
  __video = null;
}
// Helpers for programs (the "Sliqtly" module): numbers, vectors as
// [x, y, z] arrays, "#rrggbb" colours, and the camera a <scene3d> draws
// with. Plain functions, no state: a program or a library of the deck
// imports what it needs. Kept in a function of their own: the program's
// own names (its lerp, its orbit) are not taken from it.
var __helpers = (function () {
  function lerp(a, b, k) { return a + (b - a) * k; }
  function clamp(x, lo, hi) {
    if (lo === undefined) lo = 0;
    if (hi === undefined) hi = 1;
    return x < lo ? lo : x > hi ? hi : x;
  }
  // 0 before edge0, 1 after edge1, eased in between (GLSL's smoothstep):
  // smoothstep(2, 4, t) is a step that plays from second 2 to second 4
  function smoothstep(edge0, edge1, x) {
    if (edge1 === edge0) return x < edge0 ? 0 : 1;
    var k = clamp((x - edge0) / (edge1 - edge0), 0, 1);
    return k * k * (3 - 2 * k);
  }
  var vec3 = {
    add: function (a, b) { return [a[0] + b[0], a[1] + b[1], a[2] + b[2]]; },
    sub: function (a, b) { return [a[0] - b[0], a[1] - b[1], a[2] - b[2]]; },
    scale: function (a, k) { return [a[0] * k, a[1] * k, a[2] * k]; },
    dot: function (a, b) { return a[0] * b[0] + a[1] * b[1] + a[2] * b[2]; },
    cross: function (a, b) { return [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]]; },
    length: function (a) { return Math.sqrt(a[0] * a[0] + a[1] * a[1] + a[2] * a[2]); },
    distance: function (a, b) { return vec3.length(vec3.sub(a, b)); },
    normalize: function (a) { var l = vec3.length(a) || 1; return [a[0] / l, a[1] / l, a[2] / l]; },
    lerp: function (a, b, k) { return [lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)]; }
  };
  function __rgbOf(c) {
    var h = String(c).replace("#", "");
    if (h.length === 3) h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2];
    var n = parseInt(h.slice(0, 6), 16);
    if (!(n >= 0)) n = 0;
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  }
  function __hex2(n) {
    var v = Math.max(0, Math.min(255, Math.round(n)));
    return (v < 16 ? "0" : "") + v.toString(16);
  }
  // colour a to colour b ("#rgb" or "#rrggbb") by k from 0 to 1
  function mixColor(a, b, k) {
    k = clamp(k, 0, 1);
    var x = __rgbOf(a), y = __rgbOf(b);
    return "#" + __hex2(lerp(x[0], y[0], k)) + __hex2(lerp(x[1], y[1], k)) + __hex2(lerp(x[2], y[2], k));
  }
  // a point on a circle round the y axis: at angle 0 on +z, height h
  function orbit(radius, height, angle) { return [Math.sin(angle) * radius, height, Math.cos(angle) * radius]; }
  // The camera of a <scene3d fit="box"> with <perspectiveCamera position fov
  // lookAt>: project(p) is where the world point p is drawn, in the
  // program's own units (its size:) from the box's top left, as
  // [x, y, depth]; depth <= 0 is behind the camera. Vertical fov in degrees
  // (50 when left out), up +y, width / height the program's size.
  function camera(o) {
    o = o || {};
    var cam = {
      position: o.position || [0, 0, 5],
      lookAt: o.lookAt || [0, 0, 0],
      fov: o.fov === undefined ? 50 : o.fov,
      width: o.width || 480,
      height: o.height || 270
    };
    cam.project = function (p) {
      var f = vec3.normalize(vec3.sub(cam.lookAt, cam.position));
      var side = vec3.cross(f, [0, 1, 0]);
      // looking straight up or down: the screen's up is -z
      if (vec3.length(side) < 1e-9) side = vec3.cross(f, [0, 0, -1]);
      var r = vec3.normalize(side), u = vec3.cross(r, f);
      var d = vec3.sub(p, cam.position), z = vec3.dot(d, f);
      var k = 1 / Math.tan(cam.fov * Math.PI / 360);
      var zz = Math.abs(z) < 1e-9 ? 1e-9 : z;
      return [(vec3.dot(d, r) / zz * k / (cam.width / cam.height) + 1) / 2 * cam.width, (1 - vec3.dot(d, u) / zz * k) / 2 * cam.height, z];
    };
    return cam;
  }
  return { vec3: vec3, lerp: lerp, clamp: clamp, smoothstep: smoothstep, mixColor: mixColor, orbit: orbit, camera: camera };
})();
var __sliqtly = { presentation: presentation, el: el, machine: machine, sound: sound };
Object.keys(__helpers).forEach(function (k) { __sliqtly[k] = __helpers[k]; });
if (typeof defineModule === "function") defineModule("Sliqtly", __sliqtly);
// The deck's files a program imports (src/PresPlayFiles.rgr puts this call
// on its first line): import rows from "data/sales.csv". Each is a module:
//   .json        default: the value          .csv / .tsv  default: rows as
//   objects by the header row (number-like cells as numbers), rows: the
//   cells as text; any other file  default: its text. All have text.
// A file the deck lacks, or JSON that does not parse, throws where the
// program reads it, naming the file.
function __csvRows(text, sep) {
  var rows = [], row = [], cell = "", i = 0, n = text.length, quoted = false;
  if (text.charCodeAt(0) === 0xfeff) i = 1;
  for (; i < n; i++) {
    var c = text[i];
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') { cell += '"'; i++; } else quoted = false;
      } else cell += c;
    } else if (c === '"' && cell === "") quoted = true;
    else if (c === sep) { row.push(cell); cell = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cell); cell = "";
      rows.push(row); row = [];
    } else cell += c;
  }
  if (cell !== "" || row.length > 0) { row.push(cell); rows.push(row); }
  return rows.filter(function (r) { return r.length > 1 || r[0] !== ""; });
}
function __csvValue(s) {
  var t = s.trim();
  return t !== "" && /^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$/.test(t) ? Number(t) : s;
}
function __deckFile(path, text, why) {
  var m = {};
  var fail = function (why) {
    var thrower = function () { throw new Error(why); };
    Object.defineProperty(m, "default", { enumerable: true, get: thrower });
    Object.defineProperty(m, "text", { enumerable: true, get: thrower });
    Object.defineProperty(m, "rows", { enumerable: true, get: thrower });
    return m;
  };
  if (text === null) return fail(why || path + " is not a file of the presentation (send it with the deck's files)");
  var ext = path.slice(path.lastIndexOf(".") + 1).toLowerCase();
  m.text = text;
  if (ext === "json" || ext === "gltf") {
    try { m["default"] = JSON.parse(text); } catch (e) { return fail(path + " is not JSON: " + e.message); }
  } else if (ext === "csv" || ext === "tsv") {
    var rows = __csvRows(text, ext === "tsv" ? "\t" : ",");
    var head = rows.length ? rows[0].map(function (h) { return h.trim(); }) : [];
    m.rows = rows;
    m["default"] = rows.slice(1).map(function (r) {
      var o = {};
      head.forEach(function (h, k) { o[h] = __csvValue(r[k] === undefined ? "" : r[k]); });
      return o;
    });
  } else m["default"] = text;
  return m;
}
// A library of the deck (apps/x.ts, .tsx, .js): its code, run once on its
// first import (CErXes defineSourceModule); its own imports are read from
// its folder. A data file is known by its import and its path, so a
// library's import of it (by path) finds the same module.
function __deckFiles(list) {
  if (typeof defineModule !== "function") return;
  list.forEach(function (f) {
    if (/\.(ts|tsx|js|jsx|mjs)$/i.test(f[1])) {
      if (f[2] === null) defineModule(f[0], __deckFile(f[1], null));
      else if (typeof defineSourceModule === "function") defineSourceModule(f[0], f[2], f[1]);
      else defineModule(f[0], __deckFile(f[1], null, "this viewer's program engine is too old to import " + f[1] + " (reload the page)"));
      return;
    }
    var m = __deckFile(f[1], f[2]);
    defineModule(f[0], m);
    if (f[1] !== f[0]) defineModule(f[1], m);
  });
}
function __deckState(d) {
  __deck.data = d.data || {};
  __slide.number = d.slide || 1;
  __slide.count = d.slides || 1;
  __slide.home = d.home || 1;
  __slide.step = d.step || 0;
  __slide.from = d.from || 0;
  __slide.presenting = d.mode === "present";
  __slide.focused = !!d.focused;
  var c = d.code || {};
  __code.isOpen = !!c.isOpen;
  __code.path = c.path || "";
  __code.tab = c.tab || 0;
  __code.tabs = c.tabs || 0;
  __code.mode = c.mode || "";
  __code.zoom = c.zoom || 0;
  __code.line = c.line || 0;
}
// The "ranger:three" module: Ranger v2's live 3-D façade
// (gallery/game_engine/v2/modules/ranger_three), so a file written for that
// engine runs on a slide as it is: import * as THREE from "ranger:three",
// init() once, tick(dt) with dt in milliseconds, no view(). Its objects are
// kept here and handed to the page as the <scene3d> they make (the same
// world src/Pres3DTree.rgr reads from R3F JSX), drawn as Ranger draws them:
// no colour management, no room light (<scene3d legacy ambient={0}>).
var __three = (function () {
  var ids = 0;
  var scenes = [];
  var cameras = [];
  var started = false;
  function hex(c) {
    var v = Math.floor(Number(c) || 0) % 16777216;
    if (v < 0) v += 16777216;
    var s = v.toString(16);
    while (s.length < 6) s = "0" + s;
    return "#" + s;
  }
  function num(v, d) { var n = Number(v); return isFinite(n) ? n : d; }
  function el(type, props, kids) { return { type: type, props: props, children: kids || [] }; }
  // what an object has: a place, a turn (Euler XYZ, radians), a scale, a parent
  function body(o) {
    o.id = ++ids;
    o.parent = null;
    o.kids = [];
    o.pos = [0, 0, 0];
    o.rot = [0, 0, 0];
    o.size = [1, 1, 1];
  }
  function detach(o) {
    if (o.parent) {
      var at = o.parent.kids.indexOf(o);
      if (at >= 0) o.parent.kids.splice(at, 1);
      o.parent = null;
    }
  }
  function adopt(parent, o) {
    if (!o || typeof o !== "object") return;
    detach(o);
    o.parent = parent;
    parent.kids.push(o);
  }
  function place(o, px, py, pz, ex, ey, ez) {
    o.pos = [num(px, 0), num(py, 0), num(pz, 0)];
    o.rot = [num(ex, 0), num(ey, 0), num(ez, 0)];
  }
  function scaleOf(o, sx, sy, sz) {
    // the engine reads 0 as 1
    o.size = [num(sx, 1) || 1, num(sy, 1) || 1, num(sz, 1) || 1];
  }
  function placed(o, props) {
    props.position = o.pos;
    props.rotation = o.rot;
    if (o.size[0] !== 1 || o.size[1] !== 1 || o.size[2] !== 1) props.scale = o.size;
    return props;
  }
  function kidsOf(o) {
    var out = [];
    for (var i = 0; i < o.kids.length; i++) out.push(o.kids[i].element());
    return out;
  }
  function transformable(C) {
    C.prototype.setTransform = function (px, py, pz, ex, ey, ez) { place(this, px, py, pz, ex, ey, ez); };
    C.prototype.setScale = function (sx, sy, sz) { scaleOf(this, sx, sy, sz); };
  }

  function Scene() { body(this); scenes.push(this); }
  Scene.prototype.add = function (o) { adopt(this, o); };
  Scene.prototype.remove = function (o) { if (o && o.parent === this) detach(o); };

  function PerspectiveCamera(fov, aspect, near, far) {
    body(this);
    this.fov = num(fov, 50);
    this.near = num(near, 0.1);
    this.far = num(far, 2000);
    cameras.push(this);
  }
  PerspectiveCamera.prototype.setPose = function (px, py, pz, ex, ey, ez) { place(this, px, py, pz, ex, ey, ez); };
  PerspectiveCamera.prototype.element = function () {
    return el("perspectiveCamera", { fov: this.fov, near: this.near, far: this.far, position: this.pos, rotation: this.rot });
  };

  function geometry(tag, args) { return { id: ++ids, element: function () { return el(tag, { args: args }); } }; }
  function BoxGeometry(w, h, d) { return geometry("boxGeometry", [num(w, 1), num(h, 1), num(d, 1)]); }
  function OctahedronGeometry(r) { return geometry("octahedronGeometry", [num(r, 1)]); }
  function SphereGeometry(r, ws, hs) { return geometry("sphereGeometry", [num(r, 1), ws || 24, hs || 16]); }
  function CylinderGeometry(rt, rb, h, rs) { return geometry("cylinderGeometry", [num(rt, 1), num(rb, 1), num(h, 1), rs || 24]); }
  function PlaneGeometry(w, h, ws, hs) { return geometry("planeGeometry", [num(w, 1), num(h, 1), ws || 1, hs || 1]); }
  function TeapotGeometry(size, seg) { return geometry("teapotGeometry", [num(size, 1), seg || 10]); }

  function material(tag, props) {
    var m = { id: ++ids, props: props };
    m.setOpacity = function (o) { props.opacity = num(o, 1); props.transparent = props.opacity < 1; };
    // a picture on the surface: said by the world (textures are not drawn yet)
    m.setMap = function (path) { props.map = String(path); };
    m.element = function () { return el(tag, props); };
    return m;
  }
  function MeshBasicMaterial(c) { return material("meshBasicMaterial", { color: hex(c) }); }
  function MeshLambertMaterial(c) { return material("meshLambertMaterial", { color: hex(c) }); }
  function MeshPhongMaterial(c, spec, shine) {
    return material("meshPhongMaterial", { color: hex(c), specular: hex(spec), shininess: num(shine, 30) });
  }

  function AmbientLight(c, intensity) { body(this); this.color = hex(c); this.intensity = num(intensity, 1); }
  AmbientLight.prototype.element = function () { return el("ambientLight", { color: this.color, intensity: this.intensity }); };
  // shines from (dx, dy, dz) toward the origin, as Three's directional light
  function DirectionalLight(c, intensity, dx, dy, dz) {
    body(this);
    this.color = hex(c);
    this.intensity = num(intensity, 1);
    this.pos = [num(dx, 0), num(dy, 1), num(dz, 0)];
  }
  DirectionalLight.prototype.element = function () {
    return el("directionalLight", { color: this.color, intensity: this.intensity, position: this.pos });
  };

  function Mesh(geometry, material) { body(this); this.geometry = geometry; this.material = material; }
  transformable(Mesh);
  Mesh.prototype.element = function () {
    var kids = [];
    if (this.geometry && this.geometry.element) kids.push(this.geometry.element());
    if (this.material && this.material.element) kids.push(this.material.element());
    return el("mesh", placed(this, {}), kids.concat(kidsOf(this)));
  };

  function Group() { body(this); }
  transformable(Group);
  Group.prototype.add = function (o) { adopt(this, o); };
  Group.prototype.element = function () { return el("group", placed(this, {}), kidsOf(this)); };

  // a .gltf or .glb of the deck (<SliqGltf src>); "pkg://" names the deck's own file
  function GLTFModel(uri) { body(this); this.src = String(uri || "").replace(/^pkg:\/\//, "").replace(/^\.?\/+/, ""); }
  transformable(GLTFModel);
  GLTFModel.prototype.element = function () { return el("sliqGltf", placed(this, { src: this.src }), kidsOf(this)); };

  // Three's OrbitControls: drag turns the camera round the target, the
  // wheel moves it nearer or further; apply() sets the camera's pose.
  function OrbitControls(camera) {
    this.id = ++ids;
    this.camera = camera;
    this.w = 640;
    this.h = 360;
    this.target = [0, 0, 0];
    this.down = false;
    this.x = 0;
    this.y = 0;
    this.dTheta = 0;
    this.dPhi = 0;
    this.scale = 1;
  }
  OrbitControls.prototype.setViewport = function (w, h) { this.w = num(w, 640) || 640; this.h = num(h, 360) || 360; };
  OrbitControls.prototype.setTarget = function (x, y, z) { this.target = [num(x, 0), num(y, 0), num(z, 0)]; };
  OrbitControls.prototype.pointerDown = function (x, y) { this.down = true; this.x = num(x, 0); this.y = num(y, 0); };
  OrbitControls.prototype.pointerMove = function (x, y) {
    if (!this.down) return;
    x = num(x, 0);
    y = num(y, 0);
    this.dTheta -= (2 * Math.PI * (x - this.x)) / this.h;
    this.dPhi -= (2 * Math.PI * (y - this.y)) / this.h;
    this.x = x;
    this.y = y;
  };
  OrbitControls.prototype.pointerUp = function () { this.down = false; };
  OrbitControls.prototype.wheel = function (delta) {
    var d = num(delta, 0);
    if (d > 0) this.scale /= 0.95;
    else if (d < 0) this.scale *= 0.95;
  };
  OrbitControls.prototype.apply = function () {
    var c = this.camera;
    if (!c || !c.pos) return;
    var t = this.target;
    var ox = c.pos[0] - t[0], oy = c.pos[1] - t[1], oz = c.pos[2] - t[2];
    var r = Math.sqrt(ox * ox + oy * oy + oz * oz) || 1;
    var theta = Math.atan2(ox, oz) + this.dTheta;
    var phi = Math.acos(Math.max(-1, Math.min(1, oy / r))) + this.dPhi;
    var eps = 0.000001;
    phi = Math.max(eps, Math.min(Math.PI - eps, phi));
    r = r * this.scale;
    this.dTheta = 0;
    this.dPhi = 0;
    this.scale = 1;
    var p = [t[0] + r * Math.sin(phi) * Math.sin(theta), t[1] + r * Math.cos(phi), t[2] + r * Math.sin(phi) * Math.cos(theta)];
    place(c, p[0], p[1], p[2], 0, 0, 0);
    c.rot = lookRotation(p, t);
  };
  // the Euler angles (XYZ) that turn a camera at eye to look at at
  function lookRotation(eye, at) {
    var z = [eye[0] - at[0], eye[1] - at[1], eye[2] - at[2]];
    var zl = Math.sqrt(z[0] * z[0] + z[1] * z[1] + z[2] * z[2]) || 1;
    z = [z[0] / zl, z[1] / zl, z[2] / zl];
    var x = [z[2], 0, -z[0]]; // up (0, 1, 0) x z
    var xl = Math.sqrt(x[0] * x[0] + x[2] * x[2]);
    if (xl < 0.000001) x = [1, 0, 0];
    else x = [x[0] / xl, 0, x[2] / xl];
    var y = [z[1] * x[2] - z[2] * x[1], z[2] * x[0] - z[0] * x[2], z[0] * x[1] - z[1] * x[0]];
    var m13 = Math.max(-1, Math.min(1, z[0]));
    var ry = Math.asin(m13);
    if (Math.abs(m13) < 0.9999999) return [Math.atan2(-z[1], z[2]), ry, Math.atan2(-y[0], x[0])];
    return [Math.atan2(y[2], y[1]), ry, 0];
  }

  // drawn by the page from the <scene3d> each frame makes
  function Renderer3D() {}
  Renderer3D.prototype.render = function () {};
  function SceneSprite3D(opts) {
    opts = opts || {};
    this.scene = opts.scene || null;
    this.camera = opts.camera || null;
    this.target = opts.target || null;
    this.sprite = opts.sprite || null;
  }
  SceneSprite3D.prototype.invalidate = function () {};
  SceneSprite3D.prototype.sync = function () {};

  function byId(list, id) {
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
    return null;
  }
  return {
    module: {
      Scene: Scene, PerspectiveCamera: PerspectiveCamera,
      BoxGeometry: BoxGeometry, OctahedronGeometry: OctahedronGeometry, SphereGeometry: SphereGeometry,
      CylinderGeometry: CylinderGeometry, PlaneGeometry: PlaneGeometry, TeapotGeometry: TeapotGeometry,
      MeshBasicMaterial: MeshBasicMaterial, MeshLambertMaterial: MeshLambertMaterial, MeshPhongMaterial: MeshPhongMaterial,
      AmbientLight: AmbientLight, DirectionalLight: DirectionalLight,
      Mesh: Mesh, Group: Group, GLTFModel: GLTFModel, OrbitControls: OrbitControls,
      Renderer3D: Renderer3D, SceneSprite3D: SceneSprite3D
    },
    // a program written for it: init() and no view()
    owns: function () { return typeof init === "function" && typeof view !== "function"; },
    start: function () {
      if (started) return;
      started = true;
      init();
    },
    // the world the program shows: the scene and camera it names
    // (sceneGuestId / cameraGuestId), else its first scene and last camera
    element: function () {
      var sc = typeof sceneGuestId === "function" ? byId(scenes, sceneGuestId()) : null;
      if (!sc) sc = scenes[0] || null;
      var cam = typeof cameraGuestId === "function" ? byId(cameras, cameraGuestId()) : null;
      if (!cam) cam = cameras[cameras.length - 1] || null;
      var kids = [];
      if (cam) kids.push(cam.element());
      if (sc) kids = kids.concat(kidsOf(sc));
      return el("scene3d", { legacy: true, ambient: 0 }, kids);
    }
  };
})();
if (typeof defineModule === "function") defineModule("ranger:three", __three.module);
function __deckFrame(arg) {
  var a = JSON.parse(arg);
  __deckState(a.deck || {});
  __asks = [];
  // a ranger:three file: init() first, tick(dt) in milliseconds, its world
  // the tree
  if (__three.owns()) {
    __three.start();
    a.dt = (Number(a.dt) || 0) * 1000;
    __frame(JSON.stringify(a));
    var out = [];
    __ser(__three.element(), out);
    __sayVideo();
    return JSON.stringify(out[0]) + "\n" + JSON.stringify(__asks);
  }
  var tree = __frame(arg);
  __sayVideo();
  // JSON holds no raw line break: the page splits at the last one
  return tree + "\n" + JSON.stringify(__asks);
}
`;
