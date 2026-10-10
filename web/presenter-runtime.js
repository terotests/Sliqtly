// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The presenter in a slide's script: the cut-out character of `::: story`
// slides (src/PresPuppet.rgr), led by the script. Runs after
// web/script-runtime.js, in the same engine.
//
//   import { presenter } from "Sliqtly"
//   const p = presenter.create()              // hidden until show()
//   p.show({ fade: 0.5, from: "right" })      // fades (and slides) in
//    .say(presentation.activeSlide.find("p").first())   // a paragraph's words,
//                                             // the paragraph itself hidden
//    .say("Mutta RGB-tilassa vastaus on aina nolla.", { pose: "chin" })
//    .call(() => presentation.activeSlide.find("code").set({ opacity: 1 }))
//    .wait(1)
//    .hide({ fade: 0.4 })
//
// Each command starts when the one before it ends, and not before the moment
// it was given (a say() in build(n) starts at that build step). A say()
// lasts as long as the same line in a `::: story` block: the pose, the
// bubble's pop and the time to read it (__Story, src/PresStory.rgr, the same
// code). While a line is read nothing of the presenter moves.
//
// What it draws is one added element of kind "presenter" (src/PresScript.rgr
// draws it with PresPuppet): its lines, how many are shown and how long the
// last one has been, and its fade and offset as the element's opacity, x and
// y. The plan is a pure function of the commands: where the presenter is at
// a time is read off it, and a thumbnail or the PDF shows it at its end.
export const PRESENTER_RUNTIME = String.raw`
var __presenters = [];
var __presenterN = 0;
var __presenterClock = 0;
var __POSES = { pen: 1, chin: 1, open: 1, finger: 1, aim: 1, thumb: 1 };

function __presenterSay(msg) { console.log("presenter: " + msg); }

var presenter = {
  // a presenter, hidden until show() (visible: true shows it at once)
  create: function (o) {
    o = o || {};
    __presenterN++;
    var p = new __Presenter(o, "presenter-" + __presenterN);
    __presenters.push(p);
    __presenterDraw(p);
    return p;
  },
  all: function () { return __presenters.slice(); },
  // where every presenter's plan ends, in seconds from the slide's start
  end: function () {
    var m = 0;
    for (var i = 0; i < __presenters.length; i++) m = Math.max(m, __presenters[i].plan().end);
    return m;
  }
};

function __Presenter(o, id) {
  this.id = o.id ? String(o.id) : id;
  this.visible = o.visible === true;
  this.cmds = [];
  this.base = { x: 0, y: 0 };
  this._plan = null;
  this.el = __add("presenter", { id: this.id });
}
__Presenter.prototype._push = function (c) {
  c.at = __presenterClock;
  this.cmds.push(c);
  this._plan = null;
  return this;
};
__Presenter.prototype.show = function (o) {
  o = o || {};
  return this._push({ k: "show", secs: __presenterSecs(o.fade, 0.4), from: __presenterFrom(o.from) });
};
__Presenter.prototype.hide = function (o) {
  o = o || {};
  return this._push({ k: "hide", secs: __presenterSecs(o.fade, 0.4), from: __presenterFrom(o.to) });
};
// a line in the bubble: a text, or an entity's (or a found list's first)
// words, that entity hidden unless {keep: true}
__Presenter.prototype.say = function (what, o) {
  o = o || {};
  var t = what;
  if (t && typeof t.length === "number" && typeof t !== "string" && !t.box) t = t.length ? t[0] : null;
  var text = "";
  var ent = null;
  if (t && t.box) {
    ent = t;
    text = String(t.text || "");
  } else if (t !== null && t !== undefined) {
    text = String(t);
  }
  text = text.replace(/\s+/g, " ").trim();
  if (!text) {
    __presenterSay("say(): no words to say");
    return this;
  }
  var pose = "";
  if (o.pose !== undefined) {
    pose = String(o.pose);
    if (!__POSES[pose]) {
      __presenterSay('say(): no pose "' + pose + '" (pen, chin, open, finger, aim, thumb)');
      pose = "";
    }
  }
  if (ent && !o.keep) ent.set({ opacity: 0 });
  return this._push({ k: "say", text: text, pose: pose });
};
__Presenter.prototype.wait = function (secs) { return this._push({ k: "wait", secs: Math.max(0, Number(secs) || 0) }); };
__Presenter.prototype.call = function (fn) { return this._push({ k: "call", fn: fn, done: false }); };
// moves it from where it stands (x, y in slide px), scales it, fades it
__Presenter.prototype.set = function (p) {
  p = p || {};
  if (typeof p.x === "number") this.base.x = p.x;
  if (typeof p.y === "number") this.base.y = p.y;
  var rest = {};
  for (var k in p) if (k !== "x" && k !== "y" && k !== "opacity") rest[k] = p[k];
  this.el.set(rest);
  if (typeof p.opacity === "number") this.base.opacity = p.opacity;
  __presenterDraw(this);
  return this;
};
__Presenter.prototype.remove = function () {
  var i = __presenters.indexOf(this);
  if (i >= 0) __presenters.splice(i, 1);
  this.el.remove();
  return this;
};
__Presenter.prototype.plan = function () {
  if (!this._plan) this._plan = __presenterPlan(this);
  return this._plan;
};
__Presenter.prototype.done = function () { return __presenterClock >= this.plan().end; };

function __presenterSecs(v, d) {
  var n = Number(v);
  if (v === undefined || isNaN(n)) return d;
  return Math.max(0, n);
}

function __presenterFrom(v) {
  if (v === "right" || v === "bottom" || v === "left") return v;
  return "none";
}

// The beat a line is, as a ::: story block reads it.
function __presenterBeat(text, pose) {
  var line = pose ? text + " {pose=" + pose + "}" : text;
  var beats = __Story.PresStory.parse(line);
  return beats.length ? beats[0] : null;
}

// The commands laid out in time: fades {t0, t1, a0, a1, from}, lines
// {t0, beat}, calls {t}. Pure: the same commands give the same plan.
function __presenterPlan(p) {
  var t = 0;
  var shown = p.visible;
  var fades = [];
  var lines = [];
  var calls = [];
  for (var i = 0; i < p.cmds.length; i++) {
    var c = p.cmds[i];
    if (c.at > t) t = c.at;
    if (c.k === "show" || c.k === "hide") {
      var on = c.k === "show";
      if (on === shown) continue;
      fades.push({ t0: t, t1: t + c.secs, a0: on ? 0 : 1, a1: on ? 1 : 0, from: c.from });
      t += c.secs;
      shown = on;
    } else if (c.k === "say") {
      var b = __presenterBeat(c.text, c.pose);
      if (!b) continue;
      lines.push({ t0: t, beat: b, text: c.pose ? c.text + " {pose=" + c.pose + "}" : c.text });
      t += __Story.PresStory.beatSeconds(b);
    } else if (c.k === "wait") {
      t += c.secs;
    } else if (c.k === "call") {
      calls.push({ t: t, cmd: c });
    }
  }
  return { visible: p.visible, fades: fades, lines: lines, calls: calls, end: t };
}

function __presenterEase(x) {
  if (x <= 0) return 0;
  if (x >= 1) return 1;
  return x * x * (3 - 2 * x);
}

// How far it is in (1) or out (0) at t, and from where it moves.
function __presenterFade(plan, t) {
  var a = plan.visible ? 1 : 0;
  var from = "none";
  for (var i = 0; i < plan.fades.length; i++) {
    var f = plan.fades[i];
    if (t < f.t0) break;
    from = f.from;
    if (t >= f.t1 || f.t1 <= f.t0) { a = f.a1; continue; }
    var q = __presenterEase((t - f.t0) / (f.t1 - f.t0));
    a = f.a0 + (f.a1 - f.a0) * q;
  }
  return { a: a, from: from };
}

// The presenter's element where its plan has it at the clock.
function __presenterDraw(p) {
  var plan = p.plan();
  var t = __presenterClock;
  var w = __activeSlide.width;
  var h = __activeSlide.height;
  var u = Math.min(h / 540, w / 960);
  var fd = __presenterFade(plan, t);
  var dx = 0;
  var dy = 0;
  var off = 1 - fd.a;
  if (fd.from === "right") dx = 300 * u * off;
  if (fd.from === "left") dx = -300 * u * off;
  if (fd.from === "bottom") dy = 380 * u * off;
  var shown = 0;
  var since = 0;
  var said = [];
  for (var i = 0; i < plan.lines.length; i++) {
    var ln = plan.lines[i];
    said.push(ln.text);
    if (ln.t0 <= t) {
      shown = i + 1;
      since = t - ln.t0;
    }
  }
  var base = p.base.opacity === undefined ? 1 : p.base.opacity;
  p.el.set({
    w: w,
    h: h,
    story: said.join("\n"),
    shown: shown,
    since: since,
    x: p.base.x + dx,
    y: p.base.y + dy,
    opacity: base * fd.a,
    visible: fd.a > 0
  });
}

function __presentersAdvance(dt) {
  __presenterClock += dt;
  __presentersSettle();
}

// Every plan at its end: a thumbnail, the PDF, the stage before its first
// frame. A plan that calls more on the way is followed to its new end.
function __presentersToEnd() {
  for (var guard = 0; guard < 8; guard++) {
    var end = 0;
    for (var i = 0; i < __presenters.length; i++) end = Math.max(end, __presenters[i].plan().end);
    if (end <= __presenterClock) break;
    __presenterClock = end;
    __presentersCall();
  }
  __presentersSettle();
}

function __presentersSettle() {
  __presentersCall();
  for (var i = 0; i < __presenters.length; i++) __presenterDraw(__presenters[i]);
}

function __presentersCall() {
  for (var i = 0; i < __presenters.length; i++) {
    var p = __presenters[i];
    var calls = p.plan().calls;
    for (var j = 0; j < calls.length; j++) {
      var c = calls[j];
      if (c.cmd.done || __presenterClock < c.t) continue;
      c.cmd.done = true;
      if (typeof c.cmd.fn === "function") c.cmd.fn(p);
    }
  }
}

__clockUsers.push({ advance: __presentersAdvance, toEnd: __presentersToEnd });
// import { presenter } from "Sliqtly"
if (typeof __sliqtly === "object") __sliqtly.presenter = presenter;
`;
