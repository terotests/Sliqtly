// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The vectorizer, off the page's thread: pres_trace.js (src/PresTrace.rgr,
// lib/evg's EvgBitmapTracer) traces a picture's RGBA pixels with the
// settings the image editor shows, and the SVG comes back. The worker's name
// is the build's stamp, so the bundle it loads is the same build's.

importScripts("./pres_trace.js?v=" + encodeURIComponent(self.name || ""));

self.onmessage = (e) => {
  const { seq, rgba, w, h, settings } = e.data;
  const t0 = performance.now();
  try {
    const px = rgba;
    px._view = new DataView(px);
    const r = PresTrace.traceRgba(px, w, h, settings);
    self.postMessage({
      seq, svg: r.svg, err: r.err, layers: r.layers, colors: r.colors,
      tracedW: r.tracedW, tracedH: r.tracedH, ms: Math.round(performance.now() - t0),
    });
  } catch (err) {
    self.postMessage({ seq, err: String((err && err.message) || err) });
  }
};
