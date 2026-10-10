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
//   <SliqRod from to radius length />  a rod between two points in a <scene3d>    allow: 3d
//
// Nothing here changes the deck: each call is a request the page reads after
// the frame (PresApp.playAsks), and grants or refuses by the fence's allow:.
// __deckFrame wraps __frame: the deck's state in, the tree and the asks out
// (two JSON texts, a line break between).
export const DECK_RUNTIME = String.raw`
var __asks = [];
var deck = {
  data: {},
  set: function (key, value) { __asks.push({ k: "deck.set", key: String(key), value: value === undefined || value === null ? "" : String(value) }); },
  get: function (key) { var v = deck.data[String(key).toLowerCase()]; return v === undefined ? "" : v; }
};
var slide = {
  number: 1, count: 1, home: 1, step: 0, presenting: false, focused: false,
  next: function () { __asks.push({ k: "slide.next" }); },
  prev: function () { __asks.push({ k: "slide.prev" }); },
  go: function (n) { __asks.push({ k: "slide.go", n: Math.floor(Number(n) || 0) }); },
  build: function () { __asks.push({ k: "slide.step" }); }
};
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
var machine = {
  state: "",
  send: function (event, data) { __asks.push({ k: "machine.send", event: String(event), data: data === undefined ? null : data }); }
};
function __deckState(d) {
  deck.data = d.data || {};
  slide.number = d.slide || 1;
  slide.count = d.slides || 1;
  slide.home = d.home || 1;
  slide.step = d.step || 0;
  slide.presenting = d.mode === "present";
  slide.focused = !!d.focused;
}
function __deckFrame(arg) {
  var a = JSON.parse(arg);
  __deckState(a.deck || {});
  __asks = [];
  var tree = __frame(arg);
  // JSON holds no raw line break: the page splits at the last one
  return tree + "\n" + JSON.stringify(__asks);
}
`;
