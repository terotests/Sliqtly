// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The 3-D flight: the presentation as a world flown through, a station per
// slide, its diagrams and charts as holograms (src/PresFlight.rgr makes the
// world of the deck, src/PresFlight3D.rgr in pres_3d.js draws it, and
// src/PresFlightCam.rgr flies the camera).
//
// Full screen only. While it flies it has the keys, the mouse and the
// gamepad: Space / → next station, ← back, Home / End, W A S D fly, R / F up
// and down, Q / E turn, Z / X look up and down, Shift fast, the mouse drags
// the view; a gamepad's sticks fly and look, A / B next and back, Back
// leaves. Esc leaves the flight and goes back to the slide it ended at.
//
// It draws into the one GL canvas the slides' 3-D worlds use (web/three3d.js
// hands it over), shown over the whole screen while it flies.

// A label's picture: how big its text is drawn (CSS px a line) and how.
export function labelStyle(kind) {
  if (kind === "text") return { px: 88, weight: 700 };
  if (kind === "title") return { px: 64, weight: 700 };
  // a slide's own words under its title, left aligned (src/PresFlight.rgr
  // addBody: 46 characters a line)
  if (kind === "body") return { px: 40, weight: 500, wrap: 46, left: true };
  if (kind === "glass" || kind === "group") return { px: 44, weight: 600 };
  return { px: 34, weight: 500 };
}

// A text in lines no longer than `max` characters, broken between words.
export function wrapText(text, max) {
  const out = [];
  for (const para of String(text).split("\n")) {
    let line = "";
    for (const word of para.split(/\s+/).filter(Boolean)) {
      if (line && (line + " " + word).length > max) {
        out.push(line);
        line = word;
      } else {
        line = line ? line + " " + word : word;
      }
    }
    out.push(line);
  }
  return out;
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

/**
 * three3d: web/three3d.js's glReady() and pause(on); app: flightJson(),
 * selectedSlide(), selectSlide(i); toast(text); t(text) the UI's language;
 * onExit() after the flight, back in the presentation.
 */
export function createFlight3d({ three3d, app, toast, t, onExit }) {
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

  function labelPictures() {
    const n = world.labelCount();
    const c = document.createElement("canvas");
    const g = c.getContext("2d");
    for (let i = 0; i < n; i++) {
      const kind = world.labelKind(i);
      const st = labelStyle(kind);
      const lines = wrapText(world.labelText(i), st.wrap || (kind === "glass" ? 22 : kind === "text" ? 28 : 40));
      const font = `${st.weight} ${st.px}px Inter, "Segoe UI", system-ui, sans-serif`;
      g.font = font;
      const line = Math.round(st.px * 1.25);
      const pad = Math.round(st.px * 0.4);
      const w = Math.max(4, Math.ceil(Math.max(...lines.map((l) => g.measureText(l).width))) + pad * 2);
      const h = line * lines.length + pad * 2;
      c.width = w;
      c.height = h;
      g.clearRect(0, 0, w, h);
      g.font = font;
      g.textAlign = st.left ? "left" : "center";
      g.textBaseline = "middle";
      // white, tinted by the label's colour; a soft glow behind it
      g.shadowColor = "rgba(150, 235, 255, 0.85)";
      g.shadowBlur = Math.round(st.px * 0.35);
      g.fillStyle = "#ffffff";
      lines.forEach((l, k) => g.fillText(l, st.left ? pad : w / 2, pad + line * (k + 0.5)));
      const img = g.getImageData(0, 0, w, h);
      world.setLabel(i, asRangerBuffer(img.data.buffer.slice(0)), w, h, line);
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
    hud.querySelector(".help").textContent = t("Space / → next · ← back · W A S D fly · Q E turn · Shift fast · Esc leaves");
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
    const dt = last ? (now - last) / 1000 : 0;
    last = now;
    world.setKeys([...held].join(","));
    const [axes, buttons] = gamepadState(navigator.getGamepads ? navigator.getGamepads() : []);
    const said = world.setGamepad(axes, buttons);
    if (said.split("\n").includes("exit")) {
      stop();
      return;
    }
    world.frame(dt);
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
    if (!ev.repeat) world.key(ev.code);
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

  async function start() {
    if (flying) return;
    // full screen first, while the press that asked still counts
    if (!document.fullscreenElement) {
      try {
        await document.documentElement.requestFullscreen();
      } catch (_) {
        toast(t("The 3-D flight needs full screen"));
        return;
      }
    }
    fullAtStart = true;
    const gl = await three3d.glReady();
    if (!gl || !globalThis.PresFlight3D) {
      toast(t("The 3-D flight needs WebGL"));
      return;
    }
    if (!world) {
      world = new globalThis.PresFlight3D();
      if (!world.attach(gl.id)) {
        world = null;
        toast(t("The 3-D flight needs WebGL"));
        return;
      }
    }
    const why = world.load(app.flightJson());
    if (why) {
      toast(why);
      return;
    }
    world.startAt(app.selectedSlide());
    three3d.pause(true);
    canvas = gl;
    canvas.style.cssText = "display:block;position:fixed;inset:0;width:100vw;height:100vh;z-index:2147483000;background:#02040a;cursor:grab;touch-action:none";
    labelPictures();
    makeHud();
    shown = -1;
    hintUntil = performance.now() + 5000;
    held.clear();
    flying = true;
    last = 0;
    // Esc to the page, not out of full screen, where the browser lets it
    try { await navigator.keyboard?.lock?.(["Escape"]); } catch (_) { /* not offered */ }
    window.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("keyup", onKeyUp, true);
    window.addEventListener("blur", onBlur);
    document.addEventListener("fullscreenchange", onFullscreen);
    canvas.addEventListener("pointerdown", onPointerDown);
    canvas.addEventListener("pointermove", onPointerMove);
    canvas.addEventListener("pointerup", onPointerUp);
    canvas.addEventListener("pointercancel", onPointerUp);
    raf = requestAnimationFrame(frame);
  }

  function stop() {
    if (!flying) return;
    flying = false;
    cancelAnimationFrame(raf);
    try { navigator.keyboard?.unlock?.(); } catch (_) { /* not offered */ }
    window.removeEventListener("keydown", onKeyDown, true);
    window.removeEventListener("keyup", onKeyUp, true);
    window.removeEventListener("blur", onBlur);
    document.removeEventListener("fullscreenchange", onFullscreen);
    canvas.removeEventListener("pointerdown", onPointerDown);
    canvas.removeEventListener("pointermove", onPointerMove);
    canvas.removeEventListener("pointerup", onPointerUp);
    canvas.removeEventListener("pointercancel", onPointerUp);
    canvas.style.cssText = "display:none";
    hud?.remove();
    hud = null;
    held.clear();
    drag = null;
    const at = world.stationNow();
    three3d.pause(false);
    // back in the presentation at the slide the flight ended at
    app.selectSlide(at);
    onExit(fullAtStart);
  }

  return { start, stop, flying: () => flying };
}
