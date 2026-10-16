// SPDX-License-Identifier: AGPL-3.0-or-later
//
// web/videoexport.js — a presentation's video frames as pixels.
//
// The app says what frame n shows (src/PresVideo.rgr: frame n is time
// n / fps, never the clock on the wall); this draws that list with the same
// WebGL painter as the stage, into a canvas of the video's own size, and
// reads the pixels back: RGBA, rows top down, opaque. A slide of another
// shape than the video sits in the middle on black.
//
// A frame whose list (and effect clock, when it has an effect) is the same
// as the one before is not drawn again: `same` says so and the pixels are
// the last ones. An encoder takes that as "nothing changed".
//
// The encoder is not here: scripts/video.mjs pipes the pixels into FFmpeg or
// Sliqtly's own VP8 encoder (README "Video"), and File → Export → Video
// hands them to the browser's H.264 encoder (web/mp4export.js, MP4) or to
// that VP8 encoder in a worker (web/video-worker.js, WebM).

/**
 * @param {object} deps
 * @param {object} deps.app             the PresApp (videoBegin, videoFrameJson)
 * @param {Function} deps.prepareDisplayList  the WebGL painter (gl/evg-webgl.js)
 * @param {Map} deps.images             the deck's pictures by path
 * @param {Function} deps.timed         (doc, fxTime) → doc with its effects' clock set
 */
export function createVideoFrames({ app, prepareDisplayList, images, timed }) {
  let canvas = null;
  let gl = null;
  let timeline = null;
  let last = null;
  let lastKey = "";

  function begin(options = {}, fromRecording = false) {
    close();
    timeline = JSON.parse(app.videoBegin(JSON.stringify(options), !!fromRecording));
    canvas = typeof OffscreenCanvas === "function"
      ? new OffscreenCanvas(timeline.width, timeline.height)
      : Object.assign(document.createElement("canvas"), { width: timeline.width, height: timeline.height });
    gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
    if (!gl) throw new Error("video: no WebGL 2");
    last = null;
    lastKey = "";
    return timeline;
  }

  /** Frame n: { rgba, same, slide }. */
  function frame(n) {
    if (!timeline) throw new Error("video: begin first");
    const text = app.videoFrameJson(n);
    const doc = JSON.parse(text);
    // an effect moves with its clock; "none" is a slide's effect turned off
    const moving = ((doc.list && doc.list.effects) || []).some((e) => e.kind !== "none");
    const key = moving ? text : text.replace(/"fx":[^,]*,/, "");
    if (last && key === lastKey) return { rgba: last, same: true, slide: doc.slide };
    const W = timeline.width;
    const H = timeline.height;
    const s = Math.min(W / doc.width, H / doc.height);
    const x = (W - doc.width * s) / 2;
    const y = (H - doc.height * s) / 2;
    timed(doc, doc.fx);
    doc.width = W;
    doc.height = H;
    gl.viewport(0, 0, W, H);
    gl.clearColor(0, 0, 0, 1);
    gl.clear(gl.COLOR_BUFFER_BIT | gl.STENCIL_BUFFER_BIT);
    const f = prepareDisplayList(gl, doc, { dpr: 1, images, contrastGuard: true });
    f.draw(null, [x, y, s], { clear: false });
    f.dispose();
    const up = new Uint8Array(W * H * 4);
    gl.readPixels(0, 0, W, H, gl.RGBA, gl.UNSIGNED_BYTE, up);
    // GL rows run bottom up; a video's top down, and opaque
    const rgba = new Uint8Array(W * H * 4);
    const row = W * 4;
    for (let r = 0; r < H; r++) rgba.set(up.subarray((H - 1 - r) * row, (H - r) * row), r * row);
    for (let p = 3; p < rgba.length; p += 4) rgba[p] = 255;
    last = rgba;
    lastKey = key;
    return { rgba, same: false, slide: doc.slide };
  }

  function close() {
    if (gl) {
      const lose = gl.getExtension("WEBGL_lose_context");
      if (lose) lose.loseContext();
    }
    gl = null;
    canvas = null;
    last = null;
    lastKey = "";
  }

  return { begin, frame, close, timeline: () => timeline };
}
