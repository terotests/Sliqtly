// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The colour adjustments of the image editor (PresChartEditor's "adjust"
// mode), done on pixels: the preview while the sliders move, and the picture
// written back over its file on Save. Every value is -100…100, 0 leaving the
// picture as it is.
//
//   bright    adds light or takes it away, evenly
//   contrast  spreads the tones from the middle grey, or gathers them to it
//   sat       saturation: -100 is grey, +100 twice the colour
//   temp      colour temperature: warmer (more red, less blue) or cooler
//   tint      green (−) or magenta (+), the other axis of the colour balance

export function isNeutral(a) {
  return !a || (!a.bright && !a.contrast && !a.sat && !a.temp && !a.tint);
}

// RGBA bytes adjusted in place.
export function adjustPixels(data, a) {
  if (isNeutral(a)) return data;
  const bright = (a.bright || 0) * 1.28;
  const k = (a.contrast || 0) / 100;
  const contrast = k >= 0 ? 1 + k * 2 : 1 + k;
  const sat = 1 + (a.sat || 0) / 100;
  const temp = (a.temp || 0) * 0.3;
  const tint = (a.tint || 0) * 0.3;
  // the per-channel part (balance, light, contrast) as three lookup tables
  const lut = (shift) => {
    const t = new Float32Array(256);
    for (let v = 0; v < 256; v += 1) t[v] = (v + shift + bright - 128) * contrast + 128;
    return t;
  };
  const lr = lut(temp + tint * 0.5);
  const lg = lut(-tint);
  const lb = lut(-temp + tint * 0.5);
  const clamp = (v) => (v < 0 ? 0 : v > 255 ? 255 : v);
  for (let i = 0; i < data.length; i += 4) {
    let r = lr[data[i]];
    let g = lg[data[i + 1]];
    let b = lb[data[i + 2]];
    if (sat !== 1) {
      const y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
      r = y + (r - y) * sat;
      g = y + (g - y) * sat;
      b = y + (b - y) * sat;
    }
    data[i] = clamp(r);
    data[i + 1] = clamp(g);
    data[i + 2] = clamp(b);
  }
  return data;
}

// A canvas the painter can take as a picture: it reads a source's size from
// naturalWidth / naturalHeight, as an <img> has them.
export function asPicture(canvas) {
  Object.defineProperty(canvas, "naturalWidth", { value: canvas.width });
  Object.defineProperty(canvas, "naturalHeight", { value: canvas.height });
  return canvas;
}

// `bmp` drawn at most `most` pixels on its longer side, and its pixels.
export function scaled(bmp, most) {
  const k = Math.min(1, most / Math.max(bmp.width, bmp.height));
  const c = document.createElement("canvas");
  c.width = Math.max(1, Math.round(bmp.width * k));
  c.height = Math.max(1, Math.round(bmp.height * k));
  const g = c.getContext("2d", { willReadFrequently: true });
  g.drawImage(bmp, 0, 0, c.width, c.height);
  return { width: c.width, height: c.height, pixels: g.getImageData(0, 0, c.width, c.height) };
}

// The preview: the scaled pixels adjusted, as a fresh canvas (a new object,
// so the painter uploads it again).
export function previewOf(base, a) {
  const img = new ImageData(new Uint8ClampedArray(base.pixels.data), base.width, base.height);
  adjustPixels(img.data, a);
  const c = document.createElement("canvas");
  c.width = base.width;
  c.height = base.height;
  c.getContext("2d").putImageData(img, 0, 0);
  return asPicture(c);
}

// The whole picture, cut to `crop` ([x, y, w, h] in its pixels, or null) and
// adjusted, as the bytes of `type` (a photo stays a JPEG or WebP, anything
// else becomes a PNG).
export async function render(blob, crop, a) {
  const bmp = crop ? await createImageBitmap(blob, crop[0], crop[1], crop[2], crop[3]) : await createImageBitmap(blob);
  const c = document.createElement("canvas");
  c.width = bmp.width;
  c.height = bmp.height;
  const g = c.getContext("2d", { willReadFrequently: true });
  g.drawImage(bmp, 0, 0);
  bmp.close();
  if (!isNeutral(a)) {
    const img = g.getImageData(0, 0, c.width, c.height);
    adjustPixels(img.data, a);
    g.putImageData(img, 0, 0);
  }
  const out = /^image\/(jpeg|webp)$/.test(blob.type) ? blob.type : "image/png";
  const made = await new Promise((r) => c.toBlob(r, out, 0.92));
  if (!made) return null;
  return { bytes: await made.arrayBuffer(), type: made.type || out, w: c.width, h: c.height };
}
