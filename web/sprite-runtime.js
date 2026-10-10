// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Sprites in a slide's script: characters drawn from a spritesheet of the
// deck's own (sprites/robot.png) that walk on what the slide shows, jump from
// box to box and say things. Runs after web/script-runtime.js, in the same
// engine.
//
//   sprites.sheet("robot", {src: "sprites/robot.png", grid: [8, 1],
//     frame: [20, 20], feet: 1, faces: "right",
//     anims: {idle: {from: 0, frames: 2, fps: 2}, walk: {from: 2, frames: 4, fps: 8},
//             jump: {from: 6, frames: 2, loop: false}}})
//   const r = sprites.add("robot", {on: find("node#A"), size: 120})
//   r.walkTo(find("node#B")).jump("node#C").say("Done!").wait(1)
//    .face("left").play("idle", 2).call(() => find("node#C").set({scale: 1.1}))
//
// A target is an entity, a found list (its first), a selector or {x, y}; on
// an entity the sprite stands on its top edge, `at` (0..1, default 0.5)
// along it. walkTo walks on the level the sprite stands on and hops over the
// gaps between boxes; a target on another level is jumped up or down to.
//
// What a sprite does is a plan: its commands laid out in time against the
// slide's boxes (__spritePlan), and where it is at a time is read off that
// plan (__spriteAt). The plan is made again when the boxes change (an edit),
// so a sprite keeps to its boxes; a thumbnail and the PDF show every plan at
// its end.
export const SPRITE_RUNTIME = String.raw`
var __sheets = {};
var __actors = [];
var __spriteN = 0;
var __spriteClock = 0;
// the kinds a sprite can stand on: blocks, diagram nodes, chart bars
var __STANDS = { h1: 1, h2: 1, h3: 1, h4: 1, h5: 1, h6: 1, p: 1, li: 1, quote: 1, code: 1, table: 1, image: 1, chart: 1, diagram: 1, app: 1, node: 1, bar: 1 };
var __LEVEL = 3;

function __spriteSay(msg) { console.log("sprites: " + msg); }

function __sheetOf(name, d) {
  d = d || {};
  var grid = d.grid || [1, 1];
  var frame = d.frame || [1, 1];
  var anims = {};
  var src = d.anims || {};
  for (var k in src) {
    var a = src[k] || {};
    anims[k] = {
      row: a.row || 0,
      from: a.from || 0,
      frames: Math.max(1, a.frames || 1),
      fps: a.fps || 8,
      loop: a.loop !== false
    };
  }
  if (!anims.idle) anims.idle = { row: 0, from: 0, frames: 1, fps: 1, loop: true };
  var path = String(d.src || "");
  if (path && path.charAt(0) !== "/" && !/^(https?:|data:)/.test(path)) path = "/" + path;
  return {
    name: name,
    src: path,
    cols: Math.max(1, grid[0] || 1),
    rows: Math.max(1, grid[1] || 1),
    aspect: (frame[0] || 1) / (frame[1] || 1),
    feet: (d.feet || 0) / (frame[1] || 1),
    faces: d.faces === "left" ? -1 : 1,
    anims: anims
  };
}

var sprites = {
  sheet: function (name, d) {
    __sheets[String(name)] = __sheetOf(String(name), d);
    return sprites;
  },
  add: function (sheet, o) {
    o = o || {};
    var sh = __sheets[String(sheet)];
    if (!sh) __spriteSay('no sheet "' + sheet + '": give it first with sprites.sheet("' + sheet + '", {src, grid, anims})');
    __spriteN++;
    var s = new __Sprite(String(sheet), o, "sprite-" + __spriteN);
    __actors.push(s);
    __spriteDraw(s);
    return s;
  },
  all: function () { return __actors.slice(); },
  // where every sprite's plan ends, in seconds from the slide's start
  end: function () {
    var m = 0;
    for (var i = 0; i < __actors.length; i++) m = Math.max(m, __actors[i].plan().end);
    return m;
  }
};

function __Sprite(sheet, o, id) {
  this.id = o.id ? String(o.id) : id;
  this.sheet = sheet;
  this.size = o.size || 64;
  this.start = { target: o.on || o.at || { x: o.x || 0, y: o.y || 0 }, at: o.along === undefined ? 0.5 : o.along };
  this.startFace = o.face === "left" ? -1 : 1;
  this.cmds = [];
  this.born = __spriteClock;
  this._plan = null;
  this._gen = -1;
  this.img = add("image", { id: this.id });
  this.plate = null;
  this.words = null;
}
__Sprite.prototype._push = function (c) {
  this.cmds.push(c);
  this._plan = null;
  return this;
};
__Sprite.prototype.walkTo = function (target, o) {
  o = o || {};
  return this._push({ k: "walk", target: target, at: o.at === undefined ? 0.5 : o.at, speed: o.speed || 180 });
};
__Sprite.prototype.jump = function (target, o) {
  o = o || {};
  return this._push({ k: "jump", target: target, at: o.at === undefined ? 0.5 : o.at, height: o.height || 0 });
};
__Sprite.prototype.say = function (text, o) {
  o = o || {};
  return this._push({ k: "say", text: String(text), secs: o.secs || 2.5 });
};
__Sprite.prototype.wait = function (secs) { return this._push({ k: "wait", secs: Math.max(0, Number(secs) || 0) }); };
__Sprite.prototype.face = function (side) { return this._push({ k: "face", side: side === "left" ? -1 : 1 }); };
__Sprite.prototype.play = function (anim, secs) { return this._push({ k: "play", anim: String(anim), secs: Math.max(0, Number(secs) || 0) }); };
__Sprite.prototype.call = function (fn) { return this._push({ k: "call", fn: fn, done: false }); };
__Sprite.prototype.remove = function () {
  var i = __actors.indexOf(this);
  if (i >= 0) __actors.splice(i, 1);
  this.img.remove();
  if (this.plate) this.plate.remove();
  if (this.words) this.words.remove();
  return this;
};
__Sprite.prototype.plan = function () {
  if (!this._plan || this._gen !== __treeGen) {
    this._plan = __spritePlan(this);
    this._gen = __treeGen;
  }
  return this._plan;
};
// where it is now: {x, y} of its feet, the animation, the side it faces
__Sprite.prototype.state = function () { return __spriteAt(this.plan(), __spriteClock - this.born); };
__Sprite.prototype.done = function () { return __spriteClock - this.born >= this.plan().end; };

// The point a target names: on an entity, its top edge "at" along.
function __spritePoint(target, at) {
  var t = target;
  if (typeof t === "string") t = find(t);
  if (t && typeof t.length === "number" && !t.box) t = t.length ? t[0] : null;
  // the entity as the slide has it now: an edit makes new ones
  if (t && t.box && t.id && __byId[t.id]) t = __byId[t.id];
  if (t && t.box) {
    var b = __standBox(t);
    return { x: b.x + b.w * at, y: b.y, on: t };
  }
  if (t && typeof t.x === "number") return { x: t.x, y: typeof t.y === "number" ? t.y : 0, on: null };
  return null;
}

// What of an entity a sprite stands on: the words of a text (a list item's
// box is as wide as the column, its words are not), else its box.
function __standBox(e) {
  var l = Infinity, t = Infinity, r = -Infinity;
  for (var i = 0; i < e.children.length; i++) {
    var c = e.children[i];
    if (c.kind !== "word") continue;
    l = Math.min(l, c.box.x);
    t = Math.min(t, c.box.y);
    r = Math.max(r, c.box.x + c.box.w);
  }
  if (r <= l) return e.box;
  return { x: l, y: t, w: r - l, h: e.box.y + e.box.h - t };
}

// The top edges a sprite can stand on at level y: [left, right] spans,
// merged, left to right. The slide's foot is always ground.
function __spriteGround(y) {
  var spans = [];
  for (var i = 1; i < __ents.length; i++) {
    var e = __ents[i];
    if (!__STANDS[e.kind] || e.box.w < 8) continue;
    var b = __standBox(e);
    if (Math.abs(b.y - y) > __LEVEL) continue;
    spans.push([b.x, b.x + b.w]);
  }
  var root = __ents[0];
  if (root && Math.abs(root.box.y + root.box.h - y) <= __LEVEL) spans.push([root.box.x, root.box.x + root.box.w]);
  spans.sort(function (a, b) { return a[0] - b[0]; });
  var out = [];
  for (var j = 0; j < spans.length; j++) {
    var s = spans[j];
    var last = out[out.length - 1];
    if (last && s[0] <= last[1] + 1) last[1] = Math.max(last[1], s[1]);
    else out.push([s[0], s[1]]);
  }
  return out;
}

function __spanAt(spans, x) {
  for (var i = 0; i < spans.length; i++) if (x >= spans[i][0] - 0.5 && x <= spans[i][1] + 0.5) return spans[i];
  return null;
}

// A walk along level y from x0 to x1: walking where there is ground, a hop
// over each gap to the ground beyond it.
function __walkLegs(x0, x1, y, speed, size) {
  var legs = [];
  var spans = __spriteGround(y);
  var dir = x1 >= x0 ? 1 : -1;
  var x = x0;
  var guard = 0;
  while (Math.abs(x1 - x) > 0.5 && guard++ < 64) {
    var here = __spanAt(spans, x);
    if (!here) {
      legs.push({ k: "walk", x0: x, x1: x1, y0: y, y1: y, dur: Math.abs(x1 - x) / speed });
      break;
    }
    var edge = dir > 0 ? here[1] : here[0];
    if ((dir > 0 && x1 <= edge) || (dir < 0 && x1 >= edge)) {
      legs.push({ k: "walk", x0: x, x1: x1, y0: y, y1: y, dur: Math.abs(x1 - x) / speed });
      break;
    }
    // the next ground on the way
    var next = null;
    for (var i = 0; i < spans.length; i++) {
      var s = spans[i];
      if (dir > 0 && s[0] > edge && (!next || s[0] < next[0])) next = s;
      if (dir < 0 && s[1] < edge && (!next || s[1] > next[1])) next = s;
    }
    var land = next ? (dir > 0 ? next[0] : next[1]) : x1;
    if (!next || (dir > 0 ? land > x1 : land < x1)) {
      // nothing to land on before the end: walk on over it
      legs.push({ k: "walk", x0: x, x1: x1, y0: y, y1: y, dur: Math.abs(x1 - x) / speed });
      break;
    }
    var off = edge - dir * Math.min(size * 0.15, Math.abs(edge - x));
    if (Math.abs(off - x) > 0.5) legs.push({ k: "walk", x0: x, x1: off, y0: y, y1: y, dur: Math.abs(off - x) / speed });
    var on = land + dir * Math.min(size * 0.25, (next[1] - next[0]) / 2);
    if (dir > 0 ? on > x1 : on < x1) on = x1;
    legs.push(__jumpLeg(off, y, on, y, 0, size));
    x = on;
  }
  return legs;
}

function __jumpLeg(x0, y0, x1, y1, height, size) {
  var dx = Math.abs(x1 - x0);
  var dy = Math.abs(y1 - y0);
  var h = height > 0 ? height : Math.max(size * 0.5, dx * 0.25);
  return { k: "jump", x0: x0, y0: y0, x1: x1, y1: y1, rise: h + dy / 2, dur: 0.4 + Math.sqrt(dx * dx + dy * dy) / 900 };
}

// The commands of sprite s laid out in time: legs {k, t0, dur, x0, y0, x1,
// y1, face, anim, …}, from where it starts. Pure: the same commands on the
// same boxes give the same plan.
function __spritePlan(s) {
  var sh = __sheets[s.sheet];
  var p0 = __spritePoint(s.start.target, s.start.at) || { x: 0, y: 0 };
  var x = p0.x;
  var y = p0.y;
  var face = s.startFace;
  var t = 0;
  var legs = [];
  var says = [];
  var calls = [];
  function put(leg) {
    leg.t0 = t;
    if (leg.k === "walk" || leg.k === "jump") {
      if (Math.abs(leg.x1 - leg.x0) > 0.5) face = leg.x1 > leg.x0 ? 1 : -1;
    }
    leg.face = face;
    legs.push(leg);
    t += leg.dur;
    x = leg.x1;
    y = leg.y1;
  }
  for (var i = 0; i < s.cmds.length; i++) {
    var c = s.cmds[i];
    if (c.k === "walk" || c.k === "jump") {
      var to = __spritePoint(c.target, c.at);
      if (!to) {
        __spriteSay(c.k + "To: the target finds nothing on this slide");
        continue;
      }
      if (c.k === "jump") {
        put(__jumpLeg(x, y, to.x, to.y, c.height, s.size));
        continue;
      }
      if (Math.abs(to.y - y) <= __LEVEL) {
        var walk = __walkLegs(x, to.x, y, c.speed, s.size);
        for (var w = 0; w < walk.length; w++) put(walk[w]);
        continue;
      }
      // another level: to the edge of the ground under it nearest the
      // target, then up or down
      var here = __spanAt(__spriteGround(y), x);
      var off = here ? Math.min(here[1], Math.max(here[0], to.x)) : x;
      if (Math.abs(off - x) > 0.5) {
        var w2 = __walkLegs(x, off, y, c.speed, s.size);
        for (var v = 0; v < w2.length; v++) put(w2[v]);
      }
      put(__jumpLeg(x, y, to.x, to.y, 0, s.size));
    } else if (c.k === "wait") {
      put({ k: "stand", x0: x, y0: y, x1: x, y1: y, dur: c.secs });
    } else if (c.k === "play") {
      put({ k: "stand", x0: x, y0: y, x1: x, y1: y, dur: c.secs, anim: c.anim });
    } else if (c.k === "face") {
      face = c.side;
    } else if (c.k === "say") {
      says.push({ text: c.text, t0: t, t1: t + c.secs });
      put({ k: "stand", x0: x, y0: y, x1: x, y1: y, dur: c.secs, anim: sh && sh.anims.talk ? "talk" : "" });
    } else if (c.k === "call") {
      calls.push({ t: t, cmd: c });
    }
  }
  return { x: x, y: y, face: face, legs: legs, says: says, calls: calls, end: t };
}

// Where a plan has its sprite "t" seconds in.
function __spriteAt(plan, t) {
  var legs = plan.legs;
  for (var i = 0; i < legs.length; i++) {
    var g = legs[i];
    if (t >= g.t0 + g.dur) continue;
    if (t < g.t0) break;
    var p = g.dur > 0 ? (t - g.t0) / g.dur : 1;
    if (g.k === "walk") return { x: g.x0 + (g.x1 - g.x0) * p, y: g.y0, face: g.face, anim: "walk", time: t - g.t0, p: p };
    if (g.k === "jump") {
      var y = g.y0 + (g.y1 - g.y0) * p - 4 * g.rise * p * (1 - p);
      return { x: g.x0 + (g.x1 - g.x0) * p, y: y, face: g.face, anim: "jump", time: t - g.t0, p: p };
    }
    return { x: g.x0, y: g.y0, face: g.face, anim: g.anim || "idle", time: t - g.t0, p: p };
  }
  return { x: plan.x, y: plan.y, face: plan.face, anim: "idle", time: Math.max(0, t - plan.end), p: 1 };
}

// The frame of a sheet's animation: a looping one by its fps, one that does
// not loop spread over the leg (p, 0..1) and held on its last frame.
function __spriteFrame(sh, anim, time, p) {
  var a = sh.anims[anim] || (anim === "walk" || anim === "jump" ? sh.anims.walk || sh.anims.idle : sh.anims.idle);
  var n = a.frames;
  var f = a.loop ? Math.floor(time * a.fps) % n : Math.min(n - 1, Math.floor(p * n));
  if (env["export"] && a.loop) f = 0;
  var cell = a.from + f;
  return { col: cell % sh.cols, row: a.row + Math.floor(cell / sh.cols) };
}

// The sprite's image (and its words) where its plan has it now.
function __spriteDraw(s) {
  var sh = __sheets[s.sheet];
  if (!sh) {
    s.img.set({ visible: false });
    return;
  }
  var t = __spriteClock - s.born;
  var plan = s.plan();
  var st = __spriteAt(plan, t);
  var fr = __spriteFrame(sh, st.anim, st.time, st.p);
  var h = s.size;
  var w = h * sh.aspect;
  s.img.set({
    src: sh.src,
    x: st.x - w / 2,
    y: st.y - h + h * sh.feet,
    w: w,
    h: h,
    cropX: fr.col / sh.cols,
    cropY: fr.row / sh.rows,
    cropW: 1 / sh.cols,
    cropH: 1 / sh.rows,
    flipH: st.face !== sh.faces,
    visible: true
  });
  var said = null;
  for (var i = 0; i < plan.says.length; i++) {
    var sy = plan.says[i];
    if (t >= sy.t0 && t < sy.t1) said = sy;
  }
  if (!said) {
    if (s.plate) s.plate.set({ visible: false });
    if (s.words) s.words.set({ visible: false });
    return;
  }
  var fs = Math.max(20, Math.round(h * 0.24));
  var tw = said.text.length * fs * 0.55;
  var pad = fs * 0.5;
  var bx = st.x - tw / 2 - pad;
  var by = st.y - h - fs - pad * 3;
  if (!s.plate) s.plate = add("rect", { id: s.id + "-plate", fill: "#ffffff", radius: fs * 0.6 });
  if (!s.words) s.words = add("text", { id: s.id + "-words", color: "#1d2433" });
  s.plate.set({ x: bx, y: by, w: tw + pad * 2, h: fs + pad * 2, visible: true });
  s.words.set({ x: bx + pad, y: by + pad, text: said.text, size: fs, visible: true });
}

// The clock: every sprite moves on by dt, and what its plan calls on the
// way is called, in order.
function __spritesAdvance(dt) {
  __spriteClock += dt;
  __spritesSettle();
}

// Every plan at its end: a thumbnail, the PDF, the stage before its first
// frame. A plan that calls more on the way is followed to its new end.
function __spritesToEnd() {
  for (var guard = 0; guard < 8; guard++) {
    var end = 0;
    for (var i = 0; i < __actors.length; i++) {
      var s = __actors[i];
      end = Math.max(end, s.born + s.plan().end);
    }
    if (end <= __spriteClock) break;
    __spriteClock = end;
    __spritesCall();
  }
  __spritesSettle();
}

function __spritesSettle() {
  __spritesCall();
  for (var i = 0; i < __actors.length; i++) __spriteDraw(__actors[i]);
}

function __spritesCall() {
  for (var i = 0; i < __actors.length; i++) {
    var s = __actors[i];
    var calls = s.plan().calls;
    for (var j = 0; j < calls.length; j++) {
      var c = calls[j];
      if (c.cmd.done || __spriteClock - s.born < c.t) continue;
      c.cmd.done = true;
      if (typeof c.cmd.fn === "function") c.cmd.fn(s);
    }
  }
}

__clockUsers.push({ advance: __spritesAdvance, toEnd: __spritesToEnd });
`;
