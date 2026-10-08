// SPDX-License-Identifier: AGPL-3.0-or-later
//
// `render: realistic` — a book's spread drawn as paper, and a page turned
// by its corner in 3D. web/book.js works out where every point of the leaf
// goes; this only draws it, with WebGL 2: on a canvas of its own laid over
// the viewer's, or into the editor's own WebGL context between its layers
// (an EVG display list has no textured mesh, and a page turn is a screen
// thing: the PDF has single pages).
//
// Each page is drawn once by EVG into a picture (the caller's `paint`),
// kept as a texture, and drawn here as a quad or, while it turns, as strips
// along the fold bent round the cylinder (book.js leafMesh). The leaf's front is the page being turned, its
// back the page on the other side of the sheet (sampled mirrored, so it reads
// the right way round once it lies on the other side). Light: the spine's
// shadow on both pages, the roll shaded by how far it faces away, and the
// page underneath darkened where the curl hangs over it.

import { leafMesh } from "./book.js";

const VERT = `#version 300 es
in vec3 aPos;      // page units: x from the spine, y down, z towards the reader
in vec2 aUV;
in float aShade;
uniform vec2 uCanvas;  // device pixels
uniform vec3 uPlace;   // the spine's x and the top's y in device pixels, device pixels per page unit
uniform vec2 uPage;    // W, H
uniform float uFocal;  // page units from the eye: the 3D's strength
out vec2 vUV;
out float vShade;
out vec2 vPage;
void main() {
  float f = uFocal / max(uFocal - aPos.z, uFocal * 0.2);
  vec2 p = vec2(aPos.x * f, (aPos.y - uPage.y * 0.5) * f + uPage.y * 0.5);
  vec2 px = vec2(uPlace.x + p.x * uPlace.z, uPlace.y + p.y * uPlace.z);
  gl_Position = vec4(px.x / uCanvas.x * 2.0 - 1.0, 1.0 - px.y / uCanvas.y * 2.0, -aPos.z / (uPage.x * 4.0), 1.0);
  vUV = aUV;
  vShade = aShade;
  vPage = aPos.xy;
}`;

const FRAG = `#version 300 es
precision highp float;
in vec2 vUV;
in float vShade;
in vec2 vPage;
uniform sampler2D uFront;
uniform sampler2D uBack;
uniform int uMode;       // 0 a page, 1 the turning leaf, 2 the book's shadow, 3 a flat colour
uniform vec4 uColor;
uniform vec2 uPage;
uniform vec4 uCurl;      // the fold: direction (x, y), point (x, y)
uniform float uRadius;   // 0: nothing turns
uniform float uHasBack;
uniform vec4 uRect;      // the shadow's rectangle, page units
out vec4 outColor;
float spine(float x) {
  // the binding's shade, on both sides of it
  return 1.0 - 0.28 * exp(-abs(x) / (uPage.x * 0.045)) - 0.05 * exp(-abs(x) / (uPage.x * 0.25));
}
float curlShadow(vec2 p) {
  if (uRadius <= 0.0) return 1.0;
  float s = dot(p - uCurl.zw, uCurl.xy);
  if (s < 0.0) return 1.0;
  return 1.0 - 0.45 * exp(-s / (uRadius * 1.6 + 2.0));
}
void main() {
  if (uMode == 2) {
    vec2 d = max(max(uRect.xy - vPage, vPage - uRect.zw), 0.0);
    float a = 0.45 * exp(-dot(d, d) / (uPage.x * uPage.x * 0.0012));
    outColor = vec4(0.0, 0.0, 0.0, a);
    return;
  }
  if (uMode == 3) {
    outColor = uColor;
    return;
  }
  vec4 c;
  float light = vShade;
  if (uMode == 1 && !gl_FrontFacing) {
    c = uHasBack > 0.5 ? texture(uBack, vec2(1.0 - vUV.x, vUV.y)) : vec4(0.97, 0.96, 0.94, 1.0);
    light *= 0.96;
  } else {
    c = texture(uFront, vUV);
    if (uMode == 0) light *= curlShadow(vPage);
  }
  // a page facing up has the spine's shade where it lies flat by it
  if (vShade > 0.995) light *= spine(vPage.x);
  outColor = vec4(c.rgb * light, 1.0);
}`;

function shader(gl, type, src) {
  const s = gl.createShader(type);
  gl.shaderSource(s, src);
  gl.compileShader(s);
  if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s));
  return s;
}

export class BookGL {
  /**
   * `target`: a canvas of its own, or a WebGL 2 context another painter
   * draws with too (the editor's): then each frame is drawn over what is
   * there, and the context's state is left as it was found.
   */
  constructor(target) {
    this.shared = typeof WebGL2RenderingContext !== "undefined" && target instanceof WebGL2RenderingContext;
    const gl = this.shared ? target : target.getContext("webgl2", { antialias: true, premultipliedAlpha: false, depth: false, alpha: true });
    if (!gl) throw new Error("WebGL 2 is not available");
    this.canvas = gl.canvas;
    this.gl = gl;
    const p = gl.createProgram();
    gl.attachShader(p, shader(gl, gl.VERTEX_SHADER, VERT));
    gl.attachShader(p, shader(gl, gl.FRAGMENT_SHADER, FRAG));
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p));
    this.prog = p;
    this.loc = {};
    for (const n of ["aPos", "aUV", "aShade"]) this.loc[n] = gl.getAttribLocation(p, n);
    for (const n of ["uCanvas", "uPlace", "uPage", "uFocal", "uFront", "uBack", "uMode", "uColor", "uCurl", "uRadius", "uHasBack", "uRect"]) {
      this.loc[n] = gl.getUniformLocation(p, n);
    }
    this.buf = gl.createBuffer();
    this.vao = gl.createVertexArray();
    const vao0 = gl.getParameter(gl.VERTEX_ARRAY_BINDING);
    const buf0 = gl.getParameter(gl.ARRAY_BUFFER_BINDING);
    gl.bindVertexArray(this.vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buf);
    const stride = 6 * 4;
    gl.enableVertexAttribArray(this.loc.aPos);
    gl.vertexAttribPointer(this.loc.aPos, 3, gl.FLOAT, false, stride, 0);
    gl.enableVertexAttribArray(this.loc.aUV);
    gl.vertexAttribPointer(this.loc.aUV, 2, gl.FLOAT, false, stride, 12);
    gl.enableVertexAttribArray(this.loc.aShade);
    gl.vertexAttribPointer(this.loc.aShade, 1, gl.FLOAT, false, stride, 20);
    gl.bindVertexArray(vao0);
    gl.bindBuffer(gl.ARRAY_BUFFER, buf0);
    this.tex = new Map();
  }

  /** Page `page`'s picture (a canvas or an image), kept as its texture. */
  setPage(page, source, key) {
    const gl = this.gl;
    const saved = this.shared ? this.save() : null;
    let t = this.tex.get(page);
    if (t && t.key === key) return;
    if (!t) {
      t = { tex: gl.createTexture(), key: "" };
      this.tex.set(page, t);
    }
    gl.bindTexture(gl.TEXTURE_2D, t.tex);
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, source);
    gl.generateMipmap(gl.TEXTURE_2D);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    t.key = key;
    if (saved) this.restore(saved);
  }

  // What a frame changes in a shared context, to put back after it.
  save() {
    const gl = this.gl;
    const unit = gl.getParameter(gl.ACTIVE_TEXTURE);
    const tex = [];
    for (const u of [0, 1]) {
      gl.activeTexture(gl.TEXTURE0 + u);
      tex.push(gl.getParameter(gl.TEXTURE_BINDING_2D));
    }
    gl.activeTexture(unit);
    const on = {};
    for (const k of ["BLEND", "DEPTH_TEST", "CULL_FACE", "SCISSOR_TEST", "STENCIL_TEST"]) on[k] = gl.isEnabled(gl[k]);
    return {
      unit, tex, on,
      prog: gl.getParameter(gl.CURRENT_PROGRAM),
      vao: gl.getParameter(gl.VERTEX_ARRAY_BINDING),
      buf: gl.getParameter(gl.ARRAY_BUFFER_BINDING),
      viewport: gl.getParameter(gl.VIEWPORT),
      blend: [gl.getParameter(gl.BLEND_SRC_RGB), gl.getParameter(gl.BLEND_DST_RGB), gl.getParameter(gl.BLEND_SRC_ALPHA), gl.getParameter(gl.BLEND_DST_ALPHA)],
      frontFace: gl.getParameter(gl.FRONT_FACE),
      unpackFlip: gl.getParameter(gl.UNPACK_FLIP_Y_WEBGL),
    };
  }

  restore(s) {
    const gl = this.gl;
    for (const u of [0, 1]) {
      gl.activeTexture(gl.TEXTURE0 + u);
      gl.bindTexture(gl.TEXTURE_2D, s.tex[u]);
    }
    gl.activeTexture(s.unit);
    for (const k of Object.keys(s.on)) {
      if (s.on[k]) gl.enable(gl[k]);
      else gl.disable(gl[k]);
    }
    gl.useProgram(s.prog);
    gl.bindVertexArray(s.vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, s.buf);
    gl.viewport(s.viewport[0], s.viewport[1], s.viewport[2], s.viewport[3]);
    gl.blendFuncSeparate(s.blend[0], s.blend[1], s.blend[2], s.blend[3]);
    gl.frontFace(s.frontFace);
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, s.unpackFlip);
  }

  hasPage(page, key) {
    const t = this.tex.get(page);
    return !!t && t.key === key;
  }

  /** Every texture gone (a new deck). */
  clear() {
    for (const t of this.tex.values()) this.gl.deleteTexture(t.tex);
    this.tex.clear();
  }

  /**
   * One frame. `place`: { spineX, top, scale } in CSS pixels (scale: CSS
   * pixels per page unit); W, H the page; `left`, `right` the pages lying
   * still (-1 none); `turn`: null, or { side, curl, front, back, under } —
   * the leaf (front and back pages) turning over `under`, the page it uncovers.
   */
  draw(frame) {
    if (!this.shared) {
      this.paint(frame);
      return;
    }
    const saved = this.save();
    try {
      this.paint(frame);
    } finally {
      this.restore(saved);
    }
  }

  paint({ place, W, H, dpr, left, right, turn }) {
    const gl = this.gl;
    let cw;
    let ch;
    if (this.shared) {
      cw = gl.drawingBufferWidth;
      ch = gl.drawingBufferHeight;
      gl.disable(gl.SCISSOR_TEST);
      gl.disable(gl.STENCIL_TEST);
    } else {
      cw = Math.round(this.canvas.clientWidth * dpr);
      ch = Math.round(this.canvas.clientHeight * dpr);
      if (this.canvas.width !== cw || this.canvas.height !== ch) {
        this.canvas.width = cw;
        this.canvas.height = ch;
      }
    }
    gl.viewport(0, 0, cw, ch);
    if (!this.shared) {
      gl.clearColor(0, 0, 0, 0);
      gl.clear(gl.COLOR_BUFFER_BIT);
    }
    gl.useProgram(this.prog);
    gl.bindVertexArray(this.vao);
    gl.uniform2f(this.loc.uCanvas, cw, ch);
    gl.uniform3f(this.loc.uPlace, place.spineX * dpr, place.top * dpr, place.scale * dpr);
    gl.uniform2f(this.loc.uPage, W, H);
    gl.uniform1f(this.loc.uFocal, W * 4);
    gl.uniform1i(this.loc.uFront, 0);
    gl.uniform1i(this.loc.uBack, 1);
    const c = turn ? turn.curl : null;
    gl.uniform4f(this.loc.uCurl, c ? c.dx : 1, c ? c.dy : 0, c ? c.px : 0, c ? c.py : 0);
    gl.uniform1f(this.loc.uRadius, 0);
    gl.enable(gl.BLEND);
    // the alpha left in the picture is "over" too: an opaque canvas stays
    // opaque under the book's shadow
    gl.blendFuncSeparate(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
    gl.disable(gl.DEPTH_TEST);
    gl.disable(gl.CULL_FACE);

    // where paper lies: the shadow under it and the edges of the pages below
    const hasL = left >= 0 || (turn && turn.side < 0) || (turn && turn.side > 0 && c && c.qx < 0);
    const hasR = right >= 0 || (turn && turn.side > 0) || (turn && turn.side < 0 && c && c.qx > 0);
    const x0 = hasL ? -W : 0;
    const x1 = hasR ? W : 0;
    if (x1 > x0) {
      const m = W * 0.08;
      gl.uniform1i(this.loc.uMode, 2);
      gl.uniform4f(this.loc.uRect, x0, 0, x1, H);
      this.quad(x0 - m, -m, x1 + m, H + m, 1);
      // a few sheets' edges under each side
      gl.uniform1i(this.loc.uMode, 3);
      for (let k = 3; k >= 1; k -= 1) {
        const o = k * 1.1;
        const g = 0.78 + k * 0.04;
        gl.uniform4f(this.loc.uColor, g, g * 0.985, g * 0.96, 1);
        if (hasR) this.quad(0, o * 0.6, W + o, H + o, 1);
        if (hasL) this.quad(-W - o, o * 0.6, 0, H + o, 1);
      }
    }

    // the pages lying still, and the one a turn uncovers
    gl.uniform1i(this.loc.uMode, 0);
    const still = (page, x) => {
      if (page < 0 || !this.tex.has(page)) return;
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, this.tex.get(page).tex);
      this.quad(x, 0, x + W, H, 1, true);
    };
    if (turn) {
      gl.uniform1f(this.loc.uRadius, c.R + W * 0.02);
      if (turn.side > 0) {
        still(left, -W);
        still(turn.under, 0);
      } else {
        still(right, 0);
        still(turn.under, -W);
      }
      gl.uniform1f(this.loc.uRadius, 0);
      this.leaf(turn, W, H);
    } else {
      still(left, -W);
      still(right, 0);
    }
  }

  // An upright rectangle of page units, as two triangles.
  quad(x0, y0, x1, y1, shade, uv = false) {
    const gl = this.gl;
    const u0 = 0;
    const u1 = uv ? 1 : 0;
    const v = new Float32Array([
      x0, y0, 0, u0, 0, shade, x1, y0, 0, u1, 0, shade, x1, y1, 0, u1, 1, shade,
      x0, y0, 0, u0, 0, shade, x1, y1, 0, u1, 1, shade, x0, y1, 0, u0, 1, shade,
    ]);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buf);
    gl.bufferData(gl.ARRAY_BUFFER, v, gl.STREAM_DRAW);
    gl.drawArrays(gl.TRIANGLES, 0, 6);
  }

  // The turning leaf, its two faces apart: strips along the fold in the
  // order they lie on each other (book.js leafMesh), so no depth test.
  leaf(turn, W, H) {
    const gl = this.gl;
    const xa = turn.side > 0 ? 0 : -W;
    const tri = leafMesh(turn.curl, xa, W, H).flat();
    gl.disable(gl.DEPTH_TEST);
    // the rectangle runs right then down on a screen whose y is down:
    // clockwise there, so that is the front
    gl.frontFace(gl.CW);
    gl.uniform1i(this.loc.uMode, 1);
    gl.activeTexture(gl.TEXTURE0);
    const front = this.tex.get(turn.front);
    if (front) gl.bindTexture(gl.TEXTURE_2D, front.tex);
    gl.activeTexture(gl.TEXTURE1);
    const back = turn.back >= 0 ? this.tex.get(turn.back) : null;
    gl.bindTexture(gl.TEXTURE_2D, back ? back.tex : (front ? front.tex : null));
    gl.uniform1f(this.loc.uHasBack, back ? 1 : 0);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.buf);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(tri), gl.STREAM_DRAW);
    gl.drawArrays(gl.TRIANGLES, 0, tri.length / 6);
    gl.activeTexture(gl.TEXTURE0);
    gl.frontFace(gl.CCW);
  }
}
