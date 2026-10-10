// node --test: a 3-D world's picture keeps its box's shape (web/three3d.js),
// so the camera's aspect matches the box and pixels stay square.
import test from "node:test";
import assert from "node:assert/strict";
import { worldPictureSize, worldPicture, pixelOf } from "../three3d.js";

test("a small box is drawn at its own size", () => {
  assert.deepEqual(worldPictureSize(400, 225, 2, 1024), [800, 450]);
});

test("a wide box past the limit shrinks as a whole, not one side", () => {
  const [w, h] = worldPictureSize(1600, 900, 2, 1024);
  assert.equal(w, 1024);
  assert.equal(h, 576);
  assert.ok(Math.abs(w / h - 16 / 9) < 0.01);
});

test("a tall box past the limit keeps its shape", () => {
  const [w, h] = worldPictureSize(300, 1200, 2, 1024);
  assert.equal(h, 1024);
  assert.equal(w, 256);
});

test("never less than a pixel a side", () => {
  assert.deepEqual(worldPictureSize(5000, 1, 1, 1024), [1024, 1]);
});

test("a world draws over the whole slide, its box framed by the camera", () => {
  const { w, h, view } = worldPicture({ x: 480, y: 270, w: 960, h: 540, sw: 1920, sh: 1080 }, 1, 2048);
  assert.deepEqual([w, h], [1920, 1080]);
  assert.deepEqual(view, [480, 270, 960, 540]);
});

test("the whole slide past the limit keeps its shape and the box with it", () => {
  const { w, h, view } = worldPicture({ x: 480, y: 270, w: 960, h: 540, sw: 1920, sh: 1080 }, 2, 2048);
  assert.deepEqual([w, h], [2048, 1152]);
  assert.ok(Math.abs(view[2] / view[3] - 16 / 9) < 1e-9);
  assert.ok(Math.abs(view[0] - 512) < 1e-9);
});

test("a clipped world draws only its box", () => {
  const { w, h, view } = worldPicture({ x: 100, y: 50, w: 400, h: 300, sw: 1920, sh: 1080, clip: true }, 2, 2048);
  assert.deepEqual([w, h], [800, 600]);
  assert.deepEqual(view, [0, 0, 800, 600]);
});

test("a press past a program's box finds its pixel in the world's picture", () => {
  assert.deepEqual(pixelOf(0, 0, 200, 100), [0, 0]);
  assert.deepEqual(pixelOf(0.5, 0.25, 200, 100), [100, 25]);
  // the slide's far edge is the picture's last pixel, not past it
  assert.deepEqual(pixelOf(1, 1, 200, 100), [199, 99]);
  assert.deepEqual(pixelOf(-0.1, 1.2, 200, 100), [0, 99]);
});
