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
//
// One GL canvas serves every world (Ranger's GL layer keeps one context);
// each world has a 2-D canvas of its own that the slide's painter shows,
// told by imageChanged() that its pixels are new.

import { loadScript } from "./sitescript.js";

const CANVAS_ID = "sliqtly-3d";
// a world's picture at most this many device pixels a side
const MAX_SIDE = 1024;
// the slide's picture the room is made of
const ROOM_W = 96;
const ROOM_H = 54;

/**
 * app: playScenesJson() and selectedSlide(); pictures: the painter's
 * src → picture map the worlds' canvases go in; imageChanged(gl, src) tells
 * the painter a picture's pixels are new; slidePicture(w, h) the shown
 * slide as an ImageData (or null); scale() CSS px per slide unit.
 * Used by the editor (web/main.js) and the public viewer (web/viewplay.js).
 */
export function createThree3d({ app, pictures, imageChanged, slidePicture, scale, repaint, toast }) {
  let loading = null;
  let p3 = null;
  let failed = "";
  let glCanvas = null;
  let lastJson = "";
  let roomKey = "";
  const own = new Map(); // src -> 2-D canvas

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
      if (why) console.warn("scene3d: " + why);
      const w = Math.max(1, Math.min(MAX_SIDE, Math.round(s.w * k)));
      const h = Math.max(1, Math.min(MAX_SIDE, Math.round(s.h * k)));
      if (glCanvas.width !== w || glCanvas.height !== h) {
        glCanvas.width = w;
        glCanvas.height = h;
      }
      if (!p3.draw(s.src, w, h)) continue;
      const c = pictureFor(s.src, w, h);
      const g = c.getContext("2d");
      g.clearRect(0, 0, w, h);
      g.drawImage(glCanvas, 0, 0);
      if (pictures.get(s.src) !== c) pictures.set(s.src, c);
      else imageChanged(gl, s.src);
    }
    p3.keepOnly(keys);
    for (const src of [...own.keys()]) if (!keys.includes(src)) own.delete(src);
    return true;
  }

  // Another deck: its worlds go with it.
  function reset() {
    if (p3) p3.keepOnly([]);
    lastJson = "";
    roomKey = "";
    own.clear();
  }

  return { tick, reset };
}
