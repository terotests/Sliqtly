// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A slide's script ({script=apps/fx.tsx} on its heading), run in CErXes after
// componentengine's runtime and the deck runtime (apps-runtime.js), before the
// script itself. The script draws nothing of its own: it finds the entities
// Sliqtly drew on the slide and sets their properties (src/PresScript.rgr lays
// them over the slide's commands).
//
//   tree()                     the slide's entities, the slide at the root
//   find("li"), find("chart:1 bar"), find("diagram node#B"), find("edge B->D"),
//   find("h2 word"), find("p char")        an array of entities, with
//                              .set() .reset() .remove() .each() .first() on it
//   e.id e.kind e.text e.box{x,y,w,h} e.data e.index e.parent e.children
//   e.set({x, y, scale, rotate, opacity, color, fill, stroke, clip, z,
//          visible, skew, origin})   e.reset()  e.clone(props)  e.remove()
//   e.get("x")                 what it is set to, else where it was drawn
//   add("rect"|"circle"|"text"|"image", {x, y, w, h, text, size, src, …})
//   env.reducedMotion, env.export
//   input.keys, input.pointer {x, y, down, inside} (slide px)
//   hooks: start() onEnter(from) tick(dt) build(n) onKeyDown(key)
//          onKeyUp(key) onClick(entity) onLeave(to) final()
//   onEnter(from): the slide arrived from slide `from` (1-based, 0 for none),
//   once before its first tick. onLeave(to): it is being left for slide
//   `to`; what it sets is how the slide looks while the next one arrives.
//
// The selectors are PresSel's (src/PresScript.rgr): the same language, and
// web/test/script-runtime.test.mjs runs both on one slide.
//
// __scriptFrame(arg) -> {p: {id: props}, a: [added], k: [asks], build, click}
// __scriptFinal(arg) -> the same, for where the script ends: final(), or its
// ticks run to the end (`seconds`).
// __scriptAt(arg) -> the same, `time` seconds in (no final()).
// __scriptLeave(arg) -> the same, after onLeave(a.to).
export const SCRIPT_RUNTIME = String.raw`
var __ents = [];
var __byId = {};
var __props = {};
var __adds = [];
var __addN = 0;
var __started = false;
var __lastStep = -1;
var env = { reducedMotion: false, "export": false, time: 0 };
var input = { keys: {}, pointer: { x: 0, y: 0, down: false, inside: false } };

function __isName(c) {
  return (c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || (c >= "0" && c <= "9") ||
    c === "-" || c === "_" || c === "/" || c > "\u007f";
}

// "chart:1 bar" -> [{kind, name, classes, nth, from, to}], or {error}
function __parseSel(text) {
  var toks = String(text).split(" ");
  var steps = [];
  for (var t = 0; t < toks.length; t++) {
    var tok = toks[t];
    if (!tok) continue;
    var arrow = tok.indexOf("->");
    if (arrow >= 0) {
      var last = steps[steps.length - 1];
      if (!last || last.kind !== "edge") return { error: '"' + tok + '" names an edge\'s ends: write it after edge (edge B->D)' };
      last.from = tok.slice(0, arrow);
      last.to = tok.slice(arrow + 2);
      continue;
    }
    var st = { kind: "", name: "", classes: [], nth: 0, from: "", to: "" };
    var i = 0;
    if (tok.charAt(0) === "*") { st.kind = "*"; i = 1; }
    while (i < tok.length && __isName(tok.charAt(i))) i++;
    if (!st.kind) st.kind = tok.slice(0, i);
    while (i < tok.length) {
      var c = tok.charAt(i);
      var j = i + 1;
      while (j < tok.length && __isName(tok.charAt(j))) j++;
      var word = tok.slice(i + 1, j);
      if (!word) return { error: '"' + tok + '" is not a selector' };
      if (c === "#") st.name = word;
      else if (c === ".") st.classes.push(word);
      else if (c === ":") {
        var n = Number(word);
        if (!(n >= 0) || Math.floor(n) !== n) return { error: '":' + word + '" is no number: write kind:2 for the second' };
        st.nth = n;
      } else return { error: '"' + tok + '" is not a selector' };
      i = j;
    }
    steps.push(st);
  }
  if (!steps.length) return { error: "an empty selector" };
  return { steps: steps };
}

function __stepMatches(st, e) {
  if (st.kind && st.kind !== "*" && e.kind !== st.kind) return false;
  if (st.name && e.name !== st.name && e.id !== st.name) return false;
  for (var i = 0; i < st.classes.length; i++) if (e.classes.indexOf(st.classes[i]) < 0) return false;
  if (st.nth > 0 && e.index + 1 !== st.nth) return false;
  if (st.from && (e.from !== st.from || e.to !== st.to)) return false;
  return true;
}

function __within(e, anc) {
  while (e) {
    if (e === anc) return true;
    e = e.parent;
  }
  return false;
}

function __find(sel, scope) {
  var out = __list([]);
  var p = __parseSel(sel);
  if (p.error) {
    console.log('find("' + sel + '"): ' + p.error);
    return out;
  }
  var steps = p.steps;
  var n = steps.length;
  for (var i = 1; i < __ents.length; i++) {
    var e = __ents[i];
    if (scope && (e === scope || !__within(e, scope))) continue;
    if (!__stepMatches(steps[n - 1], e)) continue;
    var k = n - 2;
    var up = e.parent;
    while (k >= 0 && up) {
      if (scope && up === scope) {
        if (__stepMatches(steps[k], up)) k--;
        break;
      }
      if (__stepMatches(steps[k], up)) k--;
      up = up.parent;
    }
    if (k < 0) out.push(e);
  }
  return out;
}

function find(sel) { return __find(sel, null); }
function tree() { return __ents[0] || null; }

// An array of entities that sets, resets and removes them all at once.
function __list(a) {
  a.set = function (p) { for (var i = 0; i < a.length; i++) a[i].set(p); return a; };
  a.reset = function () { for (var i = 0; i < a.length; i++) a[i].reset(); return a; };
  a.remove = function () { for (var i = 0; i < a.length; i++) a[i].remove(); return a; };
  a.each = function (f) { for (var i = 0; i < a.length; i++) f(a[i], i); return a; };
  a.first = function () { return a.length ? a[0] : null; };
  return a;
}

var __DEFAULTS = { scale: 1, rotate: 0, skew: 0, opacity: 1, visible: true, z: 0 };

function __Ent(o) {
  var b = o.b || [0, 0, 0, 0];
  this.id = o.id;
  this.kind = o.k;
  this.text = o.t || "";
  this.box = { x: b[0], y: b[1], w: b[2], h: b[3] };
  this.data = o.d === undefined ? null : o.d;
  this.index = o.i || 0;
  this.name = o.u || "";
  this.classes = o.c ? String(o.c).split(" ") : [];
  this.from = o.f || "";
  this.to = o.to || "";
  this.parent = null;
  this.children = [];
}
__Ent.prototype.set = function (p) {
  var cur = __props[this.id];
  if (!cur) { cur = {}; __props[this.id] = cur; }
  for (var k in p) {
    var v = p[k];
    if (v === undefined || typeof v === "function") continue;
    if (v === null) delete cur[k];
    else cur[k] = v;
  }
  return this;
};
__Ent.prototype.get = function (k) {
  var cur = __props[this.id];
  if (cur && cur[k] !== undefined) return cur[k];
  if (k === "x") return this.box.x;
  if (k === "y") return this.box.y;
  return __DEFAULTS[k];
};
__Ent.prototype.reset = function () { delete __props[this.id]; return this; };
__Ent.prototype.remove = function () { return this.set({ visible: false }); };
__Ent.prototype.find = function (sel) { return __find(sel, this); };
__Ent.prototype.clone = function (p) {
  var o = { of: this.id };
  for (var k in p || {}) o[k] = p[k];
  return add("clone", o);
};

// An element of the program's own, in the slide's list: drawn in the theme's
// colours unless it says otherwise.
function __Added(kind, p) {
  __addN++;
  this.id = p && p.id ? String(p.id) : "add-" + __addN;
  this.kind = kind;
  this.rec = { id: this.id, k: kind };
  this.set(p || {});
}
__Added.prototype.set = function (p) {
  for (var k in p) {
    var v = p[k];
    if (v === undefined || typeof v === "function" || k === "id") continue;
    if (v === null) delete this.rec[k];
    else this.rec[k] = v;
  }
  return this;
};
__Added.prototype.get = function (k) { return this.rec[k]; };
__Added.prototype.remove = function () {
  var i = __adds.indexOf(this);
  if (i >= 0) __adds.splice(i, 1);
  return this;
};
__Added.prototype.reset = __Added.prototype.remove;

function add(kind, p) {
  var a = new __Added(String(kind), p);
  __adds.push(a);
  return a;
}

function __setTree(list) {
  __ents = [];
  __byId = {};
  for (var i = 0; i < list.length; i++) {
    var e = new __Ent(list[i]);
    __ents.push(e);
    __byId[e.id] = e;
  }
  for (var j = 0; j < list.length; j++) {
    var p = list[j].p;
    if (p >= 0 && p < __ents.length && p !== j) {
      __ents[j].parent = __ents[p];
      __ents[p].children.push(__ents[j]);
    }
  }
  // what was set on an entity that is gone with an edit is dropped
  for (var id in __props) if (!__byId[id]) delete __props[id];
}

function __hook(name) {
  return typeof globalThis[name] === "function" ? globalThis[name] : null;
}

function __out() {
  var a = [];
  for (var i = 0; i < __adds.length; i++) a.push(__adds[i].rec);
  return JSON.stringify({ p: __props, a: a, k: __asks, build: !!__hook("build"), click: !!__hook("onClick"), leave: !!__hook("onLeave") });
}

function __begin(a) {
  if (a.tree) __setTree(a.tree);
  __deckState(a.deck || {});
  var e = a.env || {};
  env.reducedMotion = !!e.reducedMotion;
  env["export"] = !!e["export"];
  input.keys = a.keys || {};
  if (a.pointer) input.pointer = a.pointer;
  __asks = [];
  if (!__started) {
    __started = true;
    var start = __hook("start");
    if (start) start();
    var enter = __hook("onEnter");
    if (enter) enter((a.deck && a.deck.from) || 0);
  }
}

function __scriptLeave(arg) {
  var a = JSON.parse(arg);
  __begin(a);
  var leave = __hook("onLeave");
  if (leave) leave(a.to || 0);
  return __out();
}

function __scriptFrame(arg) {
  var a = JSON.parse(arg);
  __begin(a);
  env.time = a.time || 0;
  var evs = a.events || [];
  for (var i = 0; i < evs.length; i++) {
    var ev = evs[i];
    var h = null;
    if (ev.type === "keydown") { h = __hook("onKeyDown"); if (h) h(ev.key, ev); }
    else if (ev.type === "keyup") { h = __hook("onKeyUp"); if (h) h(ev.key, ev); }
    else if (ev.type === "click") { h = __hook("onClick"); if (h) h(ev.id ? __byId[ev.id] || null : null, ev); }
  }
  var build = __hook("build");
  if (build && slide.step !== __lastStep) {
    __lastStep = slide.step;
    build(slide.step);
  }
  var tick = __hook("tick");
  if (tick) tick(a.dt || 0);
  return __out();
}

// Where the script ends, for a thumbnail, the PDF and a stage before the
// first frame: final() when it has one, else its ticks run for a.seconds (at
// 30 a second, at most 20 s), every build step taken first.
function __scriptFinal(arg) {
  var a = JSON.parse(arg);
  env["export"] = true;
  __begin(a);
  env["export"] = true;
  __buildTo(a.steps || 0);
  var fin = __hook("final");
  if (fin) fin();
  else __tickFor(a.seconds);
  return __out();
}

// The slide a.time seconds after it arrived, at build step a.steps: its
// ticks run that long (at 30 a second, at most 20 s) and final() is not
// called. render_slide(time) and render_strip draw this.
function __scriptAt(arg) {
  var a = JSON.parse(arg);
  __begin(a);
  __buildTo(a.steps || 0);
  __tickFor(a.time);
  return __out();
}

function __buildTo(step) {
  var build = __hook("build");
  if (!build) return;
  __lastStep = step;
  slide.step = step;
  build(step);
}

function __tickFor(seconds) {
  var tick = __hook("tick");
  if (!tick) return;
  var secs = Math.min(20, Math.max(0, Number(seconds) || 0));
  var n = Math.round(secs * 30);
  for (var i = 0; i < n; i++) {
    env.time = i / 30;
    tick(1 / 30);
  }
}
`;
