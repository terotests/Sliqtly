// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The 3-D flight: the presentation as a world flown through, a station per
// slide, each slide drawn again as a hologram in the flight's own look
// (src/PresFlight.rgr makes the world of the deck, src/PresFlight3D.rgr in
// pres_3d.js draws it, src/PresFlightCam.rgr flies the camera). This page
// draws the hologram's words and hands it the slides' pictures.
//
// Full screen only. While it flies it has the keys, the mouse and the
// gamepad: Space / → next station, ← back, Home / End, W A S D fly, R / F up
// and down, Q / E turn, Z / X look up and down, Shift fast, the mouse drags
// the view; a gamepad's sticks fly and look, A / B next and back, Back
// leaves. Esc leaves the flight and goes back to the slide it ended at.
//
// It draws into the one GL canvas the slides' 3-D worlds use (web/three3d.js
// hands it over), shown over the whole screen while it flies.

// A line of words drawn in light (PresFlight3D.picSpec of a "text": its
// size in world units and its runs) into canvas `c`, `k` pixels a world
// unit: each run where the slide sets it, in its weight, slant and colour,
// a glow round it and a bright core. [pixels across, down, world units
// across, down, how far right of the line's middle the picture's middle
// is]: the picture has room round the words for the glow, and for words
// that run on past the line's box.
export function drawWords(c, spec, k) {
  const g = c.getContext("2d", { willReadFrequently: true });
  const big = Math.max(0.01, ...spec.runs.map((r) => r.fs || 0));
  const pad = Math.ceil(big * k * 0.45) + 2;
  const fontOf = (r) => {
    const px = Math.max(4, r.fs * k);
    const face = r.m ? '"JetBrains Mono", "SF Mono", Menlo, Consolas, monospace' : 'Inter, "Segoe UI", system-ui, sans-serif';
    return `${r.i ? "italic " : ""}${r.b ? 700 : 400} ${px}px ${face}`;
  };
  // where each run starts, px from the line's left
  const box = spec.w * k;
  let left = 0;
  let right = box;
  const at = spec.runs.map((r) => {
    g.font = fontOf(r);
    const tw = g.measureText(r.t).width;
    const room = r.w * k;
    let x = r.dx * k;
    if (r.al === "center") x += (room - tw) / 2;
    else if (r.al === "right") x += room - tw;
    left = Math.min(left, x);
    right = Math.max(right, x + tw);
    return x;
  });
  const w = Math.max(4, Math.ceil(right - left) + pad * 2);
  const h = Math.max(4, Math.ceil(spec.h * k) + pad * 2);
  c.width = w;
  c.height = h;
  g.clearRect(0, 0, w, h);
  g.textBaseline = "middle";
  for (const glow of [true, false]) {
    spec.runs.forEach((r, n) => {
      const px = Math.max(4, r.fs * k);
      g.font = fontOf(r);
      const [cr, cg, cb] = (r.c || [0.6, 0.9, 1]).map((v) => Math.round(Math.min(1, v) * 255));
      if (glow) {
        // words on a line of light (spec.soft) glow less, so they read
        g.shadowColor = `rgba(${cr}, ${cg}, ${cb}, ${spec.soft ? 0.45 : 0.9})`;
        g.shadowBlur = Math.round(px * (spec.soft ? 0.15 : 0.4));
        g.fillStyle = `rgb(${cr}, ${cg}, ${cb})`;
      } else {
        g.shadowBlur = 0;
        // the core: the colour lifted towards white
        g.fillStyle = `rgba(${Math.round((cr + 255) / 2)}, ${Math.round((cg + 255) / 2)}, ${Math.round((cb + 255) / 2)}, 0.85)`;
      }
      g.fillText(r.t, pad + at[n] - left, h / 2);
    });
  }
  // the box's middle is box / 2 from its left; the picture's (left + right) / 2
  return [w, h, w / k, h / k, ((left + right) / 2 - box / 2) / k];
}

// The first gamepad that is there, as the numbers PresFlight3D.setGamepad
// takes: ["axes", "button values"], or ["", ""] for none.
export function gamepadState(pads) {
  for (const gp of pads || []) {
    if (!gp || !gp.connected) continue;
    const axes = Array.from(gp.axes || [], (v) => (Number.isFinite(v) ? v.toFixed(3) : "0")).join(",");
    const buttons = Array.from(gp.buttons || [], (b) => String(b ? (b.pressed ? 1 : b.value || 0) : 0)).join(",");
    return [axes, buttons];
  }
  return ["", ""];
}

// The canvas in device pixels for a w x h CSS px screen: the long side at
// most `max`, the shape kept.
export function canvasSize(w, h, dpr, max = 2560) {
  let cw = Math.max(1, Math.round(w * dpr));
  let ch = Math.max(1, Math.round(h * dpr));
  const over = Math.max(cw, ch) / max;
  if (over > 1) {
    cw = Math.round(cw / over);
    ch = Math.round(ch / over);
  }
  return [cw, ch];
}

// The keys the flight takes for itself while it flies (KeyboardEvent.code).
const FLIGHT_KEYS = new Set(["KeyW", "KeyA", "KeyS", "KeyD", "KeyR", "KeyF", "KeyQ", "KeyE", "KeyZ", "KeyX",
  "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Space", "PageUp", "PageDown", "Home", "End",
  "Backspace", "Enter", "NumpadEnter", "ShiftLeft", "ShiftRight", "Escape"]);

function asRangerBuffer(ab) {
  ab._view = new DataView(ab);
  return ab;
}

// The pictures of the stations this near the one flown to are made.
const PICS_NEAR = 2;

/**
 * three3d: web/three3d.js's glReady() and pause(on); app: flightJson(),
 * selectedSlide(), selectSlide(i); picturePixels(src, max) a picture of the
 * slides as ImageData, its long side at most `max` (null: not here yet);
 * toast(text); t(text) the UI's language; onExit() after the flight, back
 * in the presentation.
 */
export function createFlight3d({ three3d, app, picturePixels, toast, t, onExit }) {
  let world = null;
  let canvas = null;
  let hud = null;
  let raf = 0;
  let last = 0;
  let flying = false;
  let shown = -1;
  let hintUntil = 0;
  const held = new Set();
  let drag = null;
  let fullAtStart = false;
  let starting = false;

  // pictures that were not there yet: tried again after this (ms)
  let picRetry = new Map();
  const wordsCanvas = document.createElement("canvas");

  // A few pictures a frame, the station flown to first, then the ones
  // nearest it; the far ones give theirs back.
  function makePictures(now) {
    world.releasePics(now, PICS_NEAR);
    const n = world.picCount();
    const t0 = performance.now();
    for (let d = 0; d <= PICS_NEAR; d++) {
      for (let i = 0; i < n; i++) {
        if (Math.abs(world.picStation(i) - now) !== d || world.picReady(i)) continue;
        if ((picRetry.get(i) || 0) > t0) continue;
        if (world.picKind(i) === "text") {
          const spec = JSON.parse(world.picSpec(i));
          const k = Math.min(110, 2000 / Math.max(0.1, spec.w));
          const [w, h, ww, hh, ox] = drawWords(wordsCanvas, spec, k);
          const img = wordsCanvas.getContext("2d", { willReadFrequently: true }).getImageData(0, 0, w, h);
          world.setPic(i, asRangerBuffer(img.data.buffer.slice(0)), w, h, ww, hh, ox);
        } else {
          const img = picturePixels(world.picSpec(i), 768);
          if (!img) {
            picRetry.set(i, t0 + 700);
            continue;
          }
          // its box on the slide, filled as the slide fills it
          world.setPic(i, asRangerBuffer(img.data.buffer.slice(0)), img.width, img.height, -1, -1, 0);
        }
        if (performance.now() - t0 > 8) return;
      }
    }
  }

  function makeHud() {
    hud = document.createElement("div");
    hud.id = "flightHud";
    hud.setAttribute("aria-live", "polite");
    hud.style.cssText = "position:fixed;left:0;right:0;bottom:24px;z-index:2147483001;pointer-events:none;"
      + "display:flex;flex-direction:column;align-items:center;gap:8px;font:500 15px Inter,system-ui,sans-serif;"
      + "color:#9feaff;text-shadow:0 0 8px rgba(92,225,255,.8);letter-spacing:.04em";
    hud.innerHTML = '<div class="where"></div><div class="help" style="opacity:.75;font-size:13px;transition:opacity .8s"></div>';
    hud.querySelector(".help").textContent = t("Space / → next · ← back · W A S D fly · Q E turn · V layout · Shift fast · Esc leaves");
    document.body.appendChild(hud);
  }

  function showStation() {
    const now = world.stationNow();
    const help = hud.querySelector(".help");
    help.style.opacity = performance.now() < hintUntil ? "0.75" : "0";
    if (now === shown) return;
    shown = now;
    const title = world.stationTitle(now);
    hud.querySelector(".where").textContent = `${now + 1} / ${world.stationCount()}${title ? "  ·  " + title : ""}`;
  }

  // V turned the stations to another layout: its name a moment in the HUD
  function showLayout() {
    const help = hud.querySelector(".help");
    const names = { path: t("Layout: path"), wall: t("Layout: wall"), ring: t("Layout: ring") };
    help.textContent = names[world.layoutName()] || world.layoutName();
    hintUntil = performance.now() + 2500;
  }

  function size() {
    const [w, h] = canvasSize(window.innerWidth, window.innerHeight, window.devicePixelRatio || 1);
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w;
      canvas.height = h;
    }
    return [w, h];
  }

  function frame(now) {
    if (!flying) return;
    try {
      step(now);
    } catch (e) {
      // a frame that throws lands the flight instead of freezing the screen
      console.error(e);
      stop();
      toast(t("The 3-D flight stopped: ") + ((e && e.message) || e));
    }
  }

  function step(now) {
    const dt = last ? (now - last) / 1000 : 0;
    last = now;
    world.setKeys([...held].join(","));
    const [axes, buttons] = gamepadState(navigator.getGamepads ? navigator.getGamepads() : []);
    const said = world.setGamepad(axes, buttons);
    if (said.split("\n").includes("exit")) {
      stop();
      return;
    }
    if (said.split("\n").includes("layout")) showLayout();
    world.frame(dt);
    makePictures(world.stationNow());
    const [w, h] = size();
    world.draw(w, h);
    showStation();
    raf = requestAnimationFrame(frame);
  }

  function onKeyDown(ev) {
    if (!flying) return;
    if (ev.metaKey || ev.ctrlKey || ev.altKey) return;
    ev.preventDefault();
    ev.stopImmediatePropagation();
    if (ev.code === "Escape") {
      stop();
      return;
    }
    if (FLIGHT_KEYS.has(ev.code)) held.add(ev.code);
    if (!ev.repeat && world.key(ev.code) === "layout") showLayout();
  }

  function onKeyUp(ev) {
    if (!flying) return;
    held.delete(ev.code);
    ev.preventDefault();
    ev.stopImmediatePropagation();
  }

  function onPointerDown(ev) {
    drag = { x: ev.clientX, y: ev.clientY };
    canvas.setPointerCapture?.(ev.pointerId);
  }
  function onPointerMove(ev) {
    if (!drag) return;
    world.look((ev.clientX - drag.x) * 0.004, -(ev.clientY - drag.y) * 0.004);
    drag = { x: ev.clientX, y: ev.clientY };
  }
  function onPointerUp() {
    drag = null;
  }
  function onBlur() {
    held.clear();
  }
  function onFullscreen() {
    // the browser's own Esc (or anything else) left full screen: so does the flight
    if (flying && !document.fullscreenElement) stop();
  }

  // Leaves full screen if the flight took it and is not flying.
  function leaveFull(entered) {
    if (entered && !flying && document.fullscreenElement) document.exitFullscreen?.().catch(() => { /* already out */ });
  }

  // Why the flight cannot fly (shown to the user), or "" once it flies.
  // Everything that can fail runs before the canvas covers the page; what
  // is set up after it is undone again by land() if it throws.
  async function takeOff() {
    const gl = await three3d.glReady();
    if (!gl || !globalThis.PresFlight3D) return t(three3d.problem?.() || "The 3-D flight needs WebGL");
    if (!world) {
      world = new globalThis.PresFlight3D();
      if (!world.attach(gl.id)) {
        world = null;
        return t("The 3-D flight needs WebGL");
      }
    }
    const why = world.load(app.flightJson());
    if (why) return why;
    world.startAt(app.selectedSlide());
    canvas = gl;
    three3d.pause(true);
    canvas.style.cssText = "display:block;position:fixed;inset:0;width:100vw;height:100vh;z-index:2147483000;background:#02040a;cursor:grab;touch-action:none";
    picRetry = new Map();
    makeHud();
    shown = -1;
    hintUntil = performance.now() + 5000;
    held.clear();
    flying = true;
    last = 0;
    window.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("keyup", onKeyUp, true);
    window.addEventListener("blur", onBlur);
    document.addEventListener("fullscreenchange", onFullscreen);
    canvas.addEventListener("pointerdown", onPointerDown);
    canvas.addEventListener("pointermove", onPointerMove);
    canvas.addEventListener("pointerup", onPointerUp);
    canvas.addEventListener("pointercancel", onPointerUp);
    // Esc to the page, not out of full screen, where the browser lets it
    try { await navigator.keyboard?.lock?.(["Escape"]); } catch (_) { /* not offered */ }
    if (flying) raf = requestAnimationFrame(frame);
    return "";
  }

  // The page as it was before the flight: the canvas hidden and given back
  // to the worlds, the keys and the pointer the page's again.
  function land() {
    flying = false;
    starting = false;
    cancelAnimationFrame(raf);
    try { navigator.keyboard?.unlock?.(); } catch (_) { /* not offered */ }
    window.removeEventListener("keydown", onKeyDown, true);
    window.removeEventListener("keyup", onKeyUp, true);
    window.removeEventListener("blur", onBlur);
    document.removeEventListener("fullscreenchange", onFullscreen);
    if (canvas) {
      canvas.removeEventListener("pointerdown", onPointerDown);
      canvas.removeEventListener("pointermove", onPointerMove);
      canvas.removeEventListener("pointerup", onPointerUp);
      canvas.removeEventListener("pointercancel", onPointerUp);
      canvas.style.cssText = "display:none";
    }
    hud?.remove();
    hud = null;
    held.clear();
    drag = null;
    three3d.pause(false);
  }

  async function start() {
    if (flying || starting) return;
    starting = true;
    // full screen first, while the press that asked still counts
    const entered = !document.fullscreenElement;
    if (entered) {
      try {
        await document.documentElement.requestFullscreen();
      } catch (_) {
        starting = false;
        toast(t("The 3-D flight needs full screen"));
        return;
      }
    }
    fullAtStart = true;
    let why = "";
    try {
      why = await takeOff();
    } catch (e) {
      // a page that could not fly stays usable (2026-10-11: a throw here
      // left a black canvas over the page that no key took away)
      console.error(e);
      land();
      why = t("The 3-D flight could not start: ") + ((e && e.message) || e);
    }
    starting = false;
    if (why) {
      toast(why);
      leaveFull(entered);
    }
  }

  function stop() {
    if (!flying) return;
    land();
    const at = world.stationNow();
    // back in the presentation at the slide the flight ended at
    app.selectSlide(at);
    onExit(fullAtStart);
  }

  return { start, stop, flying: () => flying };
}
