// node --test: an SVG's size and the size it is drawn at (web/picture.js)
import test from "node:test";
import assert from "node:assert/strict";
import { isSvg, isSmartArt, SMARTART_TYPE, svgLength, svgSize, svgSizedTo, rasterSize, SVG_RASTER, contentKey, pictureCache } from "../picture.js";

test("an SVG is told by its type or its name", () => {
  assert.equal(isSvg("image/svg+xml", "x.bin"), true);
  assert.equal(isSvg("", "media/temple.svg"), true);
  assert.equal(isSvg("image/png", "a.png"), false);
});

test("lengths are CSS pixels; percentages are no size", () => {
  assert.equal(svgLength("120"), 120);
  assert.equal(svgLength("72pt"), 96);
  assert.equal(svgLength("1in"), 96);
  assert.equal(svgLength("100%"), 0);
  assert.equal(svgLength(undefined), 0);
});

test("a viewBox alone gives the size (the MCP decks' pictures)", () => {
  assert.deepEqual(svgSize('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><rect/></svg>'), [1600, 900]);
});

test("width and height win; one of them takes the viewBox's shape", () => {
  assert.deepEqual(svgSize('<svg width="200" height="100" viewBox="0 0 1600 900"/>'), [200, 100]);
  assert.deepEqual(svgSize('<svg width="320" viewBox="0 0 1600 900"/>'), [320, 180]);
  assert.deepEqual(svgSize('<svg width="100%" height="100%" viewBox="0,0,40,20"/>'), [40, 20]);
});

test("an SVG that says nothing is CSS's 300 × 150; not an SVG is null", () => {
  assert.deepEqual(svgSize("<svg><circle r='4'/></svg>"), [300, 150]);
  assert.equal(svgSize("<html></html>"), null);
});

test("the root before it: a declaration, a comment, a doctype", () => {
  const text = '<?xml version="1.0"?>\n<!-- <svg width="1" height="1"> -->\n<!DOCTYPE svg>\n<svg viewBox="0 0 30 10"><svg width="5" height="5"/></svg>';
  assert.deepEqual(svgSize(text), [30, 10]);
});

test("sized to draw: width and height replaced, the rest kept", () => {
  const out = svgSizedTo('<svg xmlns="http://www.w3.org/2000/svg" width="10" height="5" viewBox="0 0 1600 900"><g/></svg>', 2560, 1440);
  assert.match(out, /^<svg xmlns="http:\/\/www.w3.org\/2000\/svg" viewBox="0 0 1600 900" width="2560" height="1440"><g\/><\/svg>$/);
});

test("no viewBox: it gets one of its own size, so it scales rather than cuts", () => {
  const out = svgSizedTo('<svg width="40pt" height="20pt"><g/></svg>', 400, 200);
  const size = svgSize(out);
  assert.deepEqual(size, [400, 200]);
  assert.match(out, /viewBox="0 0 53\.33\d* 26\.66\d*"/);
});

test("drawn with the longer side at the full-slide size", () => {
  assert.deepEqual(rasterSize(1600, 900), [SVG_RASTER, Math.round((SVG_RASTER * 900) / 1600)]);
  assert.deepEqual(rasterSize(24, 48), [Math.round(SVG_RASTER / 2), SVG_RASTER]);
});

test("a SmartArt file is told apart, and is not a picture to decode", () => {
  assert.equal(isSmartArt(SMARTART_TYPE, "x"), true);
  assert.equal(isSmartArt("", "media/steps.xml"), true);
  assert.equal(isSmartArt("application/xml", "media/STEPS.XML"), true);
  assert.equal(isSmartArt("image/png", "media/cat.png"), false);
  assert.equal(isSmartArt("image/svg+xml", "media/logo.svg"), false);
  assert.equal(isSvg(SMARTART_TYPE, "media/steps.xml"), false);
});

test("a root that closes itself keeps its slash last", () => {
  assert.equal(svgSizedTo('<svg viewBox="0 0 20 10" />', 40, 20), '<svg viewBox="0 0 20 10" width="40" height="20"/>');
});

// a decoder that counts, its picture `px` pixels square
function counting(px = 10) {
  const calls = [];
  const decode = async (bytes, type, path) => {
    calls.push(path);
    return { img: { width: px, height: px }, w: px, h: px, bytes, type };
  };
  return { calls, decode };
}
const bytesOf = (s) => new TextEncoder().encode(s).buffer;

test("a file's key is what it holds", async () => {
  assert.equal(await contentKey(bytesOf("abc")), await contentKey(new TextEncoder().encode("abc")));
  assert.notEqual(await contentKey(bytesOf("abc")), await contentKey(bytesOf("abd")));
});

test("a picture is decoded once for every deck that has it", async () => {
  const { calls, decode } = counting();
  const kept = pictureCache(decode);
  const a = await kept(bytesOf("png-1"), "image/png", "/media/a.png");
  // the deck opened again, or another deck with the same file under another name
  const b = await kept(bytesOf("png-1"), "image/png", "/media/b.png");
  assert.equal(calls.length, 1);
  assert.equal(a, b);
  // the same name holding something else is decoded
  await kept(bytesOf("png-2"), "image/png", "/media/a.png");
  assert.equal(calls.length, 2);
  // and the same bytes as an SVG are another picture than as a PNG
  await kept(bytesOf("png-1"), "image/svg+xml", "/media/a.svg");
  assert.equal(calls.length, 3);
});

test("two asks at once decode once", async () => {
  const { calls, decode } = counting();
  const kept = pictureCache(decode);
  await Promise.all([kept(bytesOf("x"), "image/png", "/a.png"), kept(bytesOf("x"), "image/png", "/a.png")]);
  assert.equal(calls.length, 1);
});

test("the least recently used go when the pixels pass the budget", async () => {
  const { calls, decode } = counting(10);
  const kept = pictureCache(decode, 250);
  await kept(bytesOf("1"), "image/png", "/1.png");
  await kept(bytesOf("2"), "image/png", "/2.png");
  await kept(bytesOf("1"), "image/png", "/1.png");
  // 300 px: "2", used least lately, goes
  await kept(bytesOf("3"), "image/png", "/3.png");
  assert.equal(calls.length, 3);
  await kept(bytesOf("1"), "image/png", "/1.png");
  assert.equal(calls.length, 3);
  await kept(bytesOf("2"), "image/png", "/2.png");
  assert.equal(calls.length, 4);
});

test("a picture that did not decode is tried again", async () => {
  let n = 0;
  const kept = pictureCache(async () => (++n === 1 ? { img: null, w: 0, h: 0 } : { img: { width: 1, height: 1 }, w: 1, h: 1 }));
  assert.equal((await kept(bytesOf("x"), "image/png", "/a.png")).img, null);
  assert.ok((await kept(bytesOf("x"), "image/png", "/a.png")).img);
  const failing = pictureCache(async () => { throw new Error("broken"); });
  await assert.rejects(failing(bytesOf("y"), "image/png", "/b.png"));
});
