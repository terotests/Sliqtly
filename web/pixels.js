// SPDX-License-Identifier: AGPL-3.0-or-later

// How many canvas pixels the page draws per CSS pixel.
//
// Browser zoom raises devicePixelRatio and shrinks the page (in CSS pixels)
// by the same factor, so a canvas drawn at the screen's own ratio stays the
// size of the window in real pixels: at 500% on a Retina screen the ratio is
// 10 and the page 288×180. Held at 2, the old cap, the page was drawn at a
// fifth of the screen's pixels and scaled up blurred.
//
// The screen's ratio is used as long as the canvas stays within a large
// screen's worth of pixels (BUDGET) — a big window at 2 keeps its 2, as it
// always had — and no side longer than the GPU takes for a texture
// (`maxSide`, MAX_TEXTURE_SIZE): a larger drawing buffer is cut down by the
// browser and the page drawn wrong.

export const BUDGET = 3840 * 2400;

export function canvasDpr(device, w, h, maxSide = 8192) {
  const want = device > 0 ? device : 1;
  let k = Math.min(want, 2);
  if (want > 2 && w > 0 && h > 0) k = Math.max(k, Math.min(want, Math.sqrt(BUDGET / (w * h))));
  const side = Math.max(w, h);
  if (side > 0 && maxSide > 0 && side * k > maxSide) k = maxSide / side;
  return k;
}
