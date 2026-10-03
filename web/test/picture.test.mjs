// node --test: an SVG's size and the size it is drawn at (web/picture.js)
import test from "node:test";
import assert from "node:assert/strict";
import { isSvg, svgLength, svgSize, svgSizedTo, rasterSize, SVG_RASTER } from "../picture.js";

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
