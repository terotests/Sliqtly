// The 3-D flight's page side (web/flight3d.js): what needs no browser.
import test from "node:test";
import assert from "node:assert/strict";
import { drawWords, gamepadState, canvasSize, createFlight3d } from "../flight3d.js";

// a canvas whose letters are half as wide as they are tall
function fakeCanvas() {
  const drawn = [];
  const g = {
    font: "",
    measureText(t) {
      return { width: t.length * Number(/(\d+(\.\d+)?)px/.exec(this.font)[1]) * 0.5 };
    },
    clearRect() {},
    fillText(t, x, y) {
      drawn.push({ t, x, y, font: this.font });
    },
  };
  return { width: 0, height: 0, getContext: () => g, drawn };
}

test("a line of words is drawn where its runs are set, in their weight and slant", () => {
  const c = fakeCanvas();
  const spec = { w: 4, h: 0.5, runs: [{ t: "ab", dx: 0, w: 1, fs: 0.4 }, { t: "cd", dx: 2, w: 1, fs: 0.4, b: true, i: true }] };
  const [w, h, ww, hh, ox] = drawWords(c, spec, 100);
  assert.equal(c.width, w);
  assert.ok(ww > 4 && hh > 0.5, "room round the words for their glow");
  assert.equal(ox, 0);
  // glow pass and core pass, each run where the slide has it
  const bold = c.drawn.filter((d) => d.t === "cd");
  assert.equal(bold.length, 2);
  assert.ok(/italic 700/.test(bold[0].font));
  const plain = c.drawn.find((d) => d.t === "ab");
  assert.equal(bold[0].x - plain.x, 200);
});

test("words that run on past their box widen the picture to the right", () => {
  const c = fakeCanvas();
  const spec = { w: 1, h: 0.5, runs: [{ t: "abcdefgh", dx: 0, w: 1, fs: 0.5 }] };
  const [, , , , ox] = drawWords(c, spec, 100);
  // 8 letters of 25 px: 200 px for a 100 px box, its middle 50 px right
  assert.ok(Math.abs(ox - 0.5) < 1e-9);
  const centred = drawWords(fakeCanvas(), { w: 4, h: 0.5, runs: [{ t: "ab", dx: 0, w: 4, fs: 0.4, al: "center" }] }, 100);
  assert.equal(centred[4], 0);
});

test("the first connected gamepad, as numbers", () => {
  const pad = { connected: true, axes: [0.5, -1, 0, 0], buttons: [{ pressed: true, value: 1 }, { pressed: false, value: 0.25 }] };
  assert.deepEqual(gamepadState([null, { connected: false }, pad]), ["0.500,-1.000,0.000,0.000", "1,0.25"]);
  assert.deepEqual(gamepadState([]), ["", ""]);
});

test("the canvas keeps the screen's shape under its cap", () => {
  assert.deepEqual(canvasSize(1920, 1080, 1), [1920, 1080]);
  assert.deepEqual(canvasSize(1920, 1080, 2), [2560, 1440]);
});

// A page just enough for start(): full screen granted, listeners counted.
function fakePage() {
  const listeners = new Map();
  const on = (k) => (type, f) => listeners.set(k + type, f);
  const off = (k) => (type) => listeners.delete(k + type);
  const doc = {
    fullscreenElement: null,
    documentElement: { async requestFullscreen() { doc.fullscreenElement = {}; } },
    async exitFullscreen() { doc.fullscreenElement = null; },
    createElement: () => ({ style: {}, setAttribute() {}, querySelector: () => ({ style: {} }), remove() {}, getContext: () => ({}) }),
    body: { appendChild() {} },
    addEventListener: on("doc:"),
    removeEventListener: off("doc:"),
  };
  globalThis.document = doc;
  globalThis.window = { addEventListener: on("win:"), removeEventListener: off("win:"), innerWidth: 800, innerHeight: 600 };
  globalThis.requestAnimationFrame = () => 1;
  globalThis.cancelAnimationFrame = () => {};
  const canvas = { id: "gl", style: { cssText: "display:none" }, addEventListener: on("canvas:"), removeEventListener: off("canvas:") };
  const paused = [];
  const three3d = { glReady: async () => canvas, pause: (on) => paused.push(on), problem: () => null };
  return { doc, listeners, canvas, paused, three3d };
}

test("a flight that cannot start leaves the page as it was, out of full screen", async () => {
  const page = fakePage();
  // 2026-10-11: an old page with a newer pres_3d.js threw here and left a
  // black canvas over the page that no key took away
  globalThis.PresFlight3D = class {
    attach() { return true; }
    load() { return ""; }
    startAt() { throw new TypeError("world.labelCount is not a function"); }
  };
  const toasts = [];
  const flight = createFlight3d({ three3d: page.three3d, app: { flightJson: () => "{}", selectedSlide: () => 11 }, picturePixels: () => null, toast: (s) => toasts.push(s), t: (s) => s, onExit() {} });
  await flight.start();
  assert.equal(flight.flying(), false);
  assert.equal(page.canvas.style.cssText, "display:none");
  assert.equal(page.listeners.size, 0);
  assert.equal(page.doc.fullscreenElement, null);
  assert.match(toasts[0], /could not start: world\.labelCount/);
});

test("a throw after the canvas covers the page is undone too", async () => {
  const page = fakePage();
  globalThis.PresFlight3D = class {
    attach() { return true; }
    load() { return ""; }
    startAt() {}
  };
  // the HUD is made after the canvas is shown
  page.doc.body.appendChild = () => { throw new Error("no body"); };
  const toasts = [];
  const flight = createFlight3d({ three3d: page.three3d, app: { flightJson: () => "{}", selectedSlide: () => 0 }, picturePixels: () => null, toast: (s) => toasts.push(s), t: (s) => s, onExit() {} });
  await flight.start();
  assert.equal(flight.flying(), false);
  assert.equal(page.canvas.style.cssText, "display:none");
  assert.deepEqual(page.paused, [true, false]);
  assert.equal(page.listeners.size, 0);
  assert.equal(toasts.length, 1);
});
