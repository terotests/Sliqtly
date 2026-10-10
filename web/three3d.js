// SPDX-License-Identifier: AGPL-3.0-or-later
//
// 3-D worlds on slides: a program (```app with `allow: 3d`) that puts a
// <scene3d> in its view() gets a box that shows a picture named after it
// (src/PresPlay.rgr). This draws those pictures. The worlds themselves are
// Ranger's Three.js port (gallery/game_engine/three) driven by
// src/Pres3D.rgr, compiled to pres_3d.js and loaded the first time a world
// is on the stage.
//
//   app.playScenesJson()   the worlds on the shown slide, where they are
//   Pres3D.setSlide        a small picture of the slide: the room the
//                          worlds stand in, mirrored by shiny surfaces
//   Pres3D.draw            one world into the shared WebGL canvas, which is
//                          then copied into that world's own picture
//   Pres3D.missing/setFile the deck's .gltf files a <SliqGltf src> names,
//                          read with readFile and handed over once (again
//                          when the deck's copy changes)
//
// One GL canvas serves every world (Ranger's GL layer keeps one context);
// each world has a 2-D canvas of its own that the slide's painter shows,
// told by imageChanged() that its pixels are new.

import { loadScript } from "./sitescript.js";

const CANVAS_ID = "sliqtly-3d";
// a world's picture at most this many device pixels a side (the picture
// is the whole slide unless the world is clipped to its box)
const MAX_SIDE = 2048;
// the slide's picture the room is made of
const ROOM_W = 96;
const ROOM_H = 54;

/**
 * A world's picture in device pixels for its box (w x h slide units, k
 * device pixels per unit): the box's own shape, scaled down as a whole when
 * a side would pass `max`. The camera's aspect is the picture's, and the
 * painter stretches the picture over the box, so a side clamped alone would
 * squash the world (spheres drawn as ellipses). [w, h]
 */
export function worldPictureSize(w, h, k, max = MAX_SIDE) {
  let pw = w * k;
  let ph = h * k;
  const over = Math.max(pw, ph) / max;
  if (over > 1) {
    pw /= over;
    ph /= over;
  }
  return [Math.max(1, Math.round(pw)), Math.max(1, Math.round(ph))];
}

/**
 * Where a world {x, y, w, h, clip, sw, sh} (slide units, PresPlayView
 * scenesOn) is drawn: its picture covers the whole sw x sh slide, so the
 * world may reach past its element's box (the deck paints it over the
 * slide's ground, under its text), or only its box when it clips (or no
 * slide size came). w, h: the picture in device pixels at k per unit;
 * view: the element's box in it, which the camera frames.
 */
export function worldPicture(s, k, max = MAX_SIDE) {
  const whole = !s.clip && s.sw > 0 && s.sh > 0;
  const rx = whole ? 0 : s.x;
  const ry = whole ? 0 : s.y;
  const rw = whole ? s.sw : s.w;
  const rh = whole ? s.sh : s.h;
  const [w, h] = worldPictureSize(rw, rh, k, max);
  const f = w / rw;
  return { w, h, view: [(s.x - rx) * f, (s.y - ry) * f, s.w * f, s.h * f] };
}

/**
 * app: playScenesJson() and selectedSlide(); pictures: the painter's
 * src → picture map the worlds' canvases go in; imageChanged(gl, src) tells
 * the painter a picture's pixels are new; slidePicture(w, h) the shown
 * slide as an ImageData (or null); scale() CSS px per slide unit.
 * Used by the editor (web/main.js) and the public viewer (web/viewplay.js).
 */
export function createThree3d({ app, pictures, imageChanged, slidePicture, scale, repaint, toast, readFile }) {
  let loading = null;
  let p3 = null;
  let failed = "";
  let glCanvas = null;
  let lastJson = "";
  let roomKey = "";
  const own = new Map(); // src -> 2-D canvas
  const said = new Map(); // src -> the warnings last told the console
  const given = new Map(); // .gltf path -> the Blob or text handed over
  const reading = new Set(); // .gltf paths being read
  let filesRev = null;
  let deckGen = 0;

  function load() {
    if (!loading) {
      loading = loadScript("pres_3d.js").then(() => {
        const Pres3D = globalThis.Pres3D;
        if (!Pres3D) throw new Error("pres_3d.js did not load");
        glCanvas = document.createElement("canvas");
        glCanvas.id = CANVAS_ID;
        glCanvas.width = 64;
        glCanvas.height = 64;
        glCanvas.style.display = "none";
        document.body.appendChild(glCanvas);
        const world = new Pres3D();
        if (!world.attach(CANVAS_ID)) throw new Error("no WebGL for 3-D worlds");
        p3 = world;
        lastJson = "";
        roomKey = "";
        repaint();
      }).catch((e) => {
        failed = String((e && e.message) || e);
        toast(failed);
      });
    }
    return loading;
  }

  function asRangerBuffer(ab) {
    ab._view = new DataView(ab);
    return ab;
  }

  // The room: the slide as it is drawn, without its worlds, once per slide
  // and per change of the deck.
  function room(rev) {
    const key = app.selectedSlide() + ":" + rev;
    if (key === roomKey) return false;
    const img = slidePicture(ROOM_W, ROOM_H);
    if (!img) return false;
    roomKey = key;
    p3.setSlide(asRangerBuffer(img.data.buffer.slice(0)), img.width, img.height);
    return true;
  }

  // A file a world asked for, read and handed over; the worlds are drawn
  // again with it. The same Blob is not read twice.
  function fetchFile(path) {
    if (reading.has(path) || !readFile) return;
    reading.add(path);
    const gen = deckGen;
    Promise.resolve(readFile(path)).then(async (got) => {
      if (gen !== deckGen || !p3) return;
      if (given.has(path) && given.get(path) === got) return;
      const text = got == null ? "" : typeof got === "string" ? got : await got.text();
      if (gen !== deckGen) return;
      given.set(path, got);
      p3.setFile(path, text);
      lastJson = "";
      repaint();
    }).catch((e) => console.warn("scene3d: " + path, e)).finally(() => reading.delete(path));
  }

  function pictureFor(src, w, h) {
    let c = own.get(src);
    if (!c) {
      c = document.createElement("canvas");
      own.set(src, c);
    }
    if (c.width !== w || c.height !== h) {
      c.width = w;
      c.height = h;
    }
    return c;
  }

  // Once a frame, after the programs' frames came in: each world on the
  // stage drawn again when its element or the room changed. True when a
  // picture changed (the stage is painted again).
  function tick(rev, gl, dpr) {
    const json = app.playScenesJson();
    if (json === "[]") {
      if (lastJson !== "[]" && p3) p3.keepOnly([]);
      lastJson = json;
      return false;
    }
    if (failed) return false;
    if (!p3) {
      load();
      return false;
    }
    const roomChanged = room(rev);
    // the deck changed: the files handed over may have too
    if (rev !== filesRev) {
      filesRev = rev;
      for (const path of given.keys()) fetchFile(path);
    }
    const k = scale() * dpr;
    if (json === lastJson && !roomChanged && k === tick.k) return false;
    lastJson = json;
    tick.k = k;
    let list = [];
    try { list = JSON.parse(json); } catch (_) { list = []; }
    const keys = [];
    for (const s of list) {
      keys.push(s.src);
      const why = p3.setScene(s.src, JSON.stringify(s.scene));
      const need = p3.missing(s.src);
      if (need) for (const path of need.split("\n")) fetchFile(path);
      // once per change: a scene is set again every frame it moves
      if (why && said.get(s.src) !== why) console.warn("scene3d: " + why);
      said.set(s.src, why);
      if (!(s.w > 0 && s.h > 0)) continue;
      const { w, h, view } = worldPicture(s, k);
      if (glCanvas.width !== w || glCanvas.height !== h) {
        glCanvas.width = w;
        glCanvas.height = h;
      }
      if (!p3.draw(s.src, w, h, view[0], view[1], view[2], view[3])) continue;
      const c = pictureFor(s.src, w, h);
      const g = c.getContext("2d");
      g.clearRect(0, 0, w, h);
      g.drawImage(glCanvas, 0, 0);
      if (pictures.get(s.src) !== c) pictures.set(s.src, c);
      else imageChanged(gl, s.src);
    }
    p3.keepOnly(keys);
    for (const src of [...own.keys()]) if (!keys.includes(src)) own.delete(src);
    for (const src of [...said.keys()]) if (!keys.includes(src)) said.delete(src);
    return true;
  }

  // An export's worlds (PDF, PPTX): each in `list` ({src, scene, x, y, w,
  // h, clip, sw, sh}, the slides' worlds as their programs last drew them)
  // drawn at k device pixels per slide unit, as PNG bytes with their alpha.
  // The stage's own pictures are drawn again on the next tick.
  async function stills(list, k) {
    if (!list.length) return [];
    await load();
    if (!p3) return [];
    const out = [];
    for (const s of list) {
      if (!(s.w > 0 && s.h > 0)) continue;
      p3.setScene(s.src, JSON.stringify(s.scene));
      const { w, h, view } = worldPicture(s, k);
      glCanvas.width = w;
      glCanvas.height = h;
      if (!p3.draw(s.src, w, h, view[0], view[1], view[2], view[3])) continue;
      const c = document.createElement("canvas");
      c.width = w;
      c.height = h;
      c.getContext("2d").drawImage(glCanvas, 0, 0);
      const blob = await new Promise((r) => c.toBlob(r, "image/png"));
      if (blob) out.push({ src: s.src, w, h, bytes: await blob.arrayBuffer() });
    }
    lastJson = "";
    return out;
  }

  // Another deck: its worlds go with it.
  function reset() {
    if (p3) {
      p3.keepOnly([]);
      p3.clearFiles();
    }
    deckGen++;
    given.clear();
    filesRev = null;
    lastJson = "";
    roomKey = "";
    own.clear();
    said.clear();
  }

  return { tick, reset, stills };
}
