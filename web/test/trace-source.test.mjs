// node --test: where a vectorized picture came from (web/trace-source.js).
import test from "node:test";
import assert from "node:assert/strict";
import { stampSvg, unstampSvg, readStamp, retraceSource, svgTarget, settingsLine } from "../trace-source.js";

const SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="60" height="40" viewBox="0 0 60 40"><path d="M0 0h10v10z"/></svg>';

test("a stamp goes right after the svg tag and reads back", () => {
  const s = stampSvg(SVG, "media/kuva.png", "preset=photo\ncolorCount=24\n");
  assert.ok(s.startsWith('<svg xmlns="http://www.w3.org/2000/svg" width="60" height="40" viewBox="0 0 60 40"><metadata id="sliqtly-trace"'));
  assert.deepEqual(readStamp(s), { source: "media/kuva.png", settings: "preset=photo;colorCount=24" });
  assert.equal(unstampSvg(s), SVG);
});

test("stamping again replaces the stamp", () => {
  const s = stampSvg(stampSvg(SVG, "a.png", "preset=logo"), "b.png", "preset=poster");
  assert.equal((s.match(/sliqtly-trace/g) || []).length, 1);
  assert.equal(readStamp(s).source, "b.png");
});

test("odd characters in the path survive", () => {
  const s = stampSvg(SVG, 'media/a "b" & <c>.png', "");
  assert.equal(readStamp(s).source, 'media/a "b" & <c>.png');
});

test("an SVG without a stamp has none", () => {
  assert.equal(readStamp(SVG), null);
  assert.equal(readStamp(""), null);
});

test("settings become one line", () => {
  assert.equal(settingsLine("preset=photo\r\n\ncolorCount=24;smooth=2\n"), "preset=photo;colorCount=24;smooth=2");
});

test("the source: stamped while it is there, else the same name", () => {
  const paths = ["media/x.png", "media/x.svg", "media/y.JPG", "media/y.svg", "media/z.svg"];
  assert.equal(retraceSource("media/x.svg", { source: "media/x.png" }, paths), "media/x.png");
  assert.equal(retraceSource("media/x-2.svg", { source: "media/x.png" }, paths), "media/x.png");
  // the stamped picture deleted: one of the SVG's own name
  assert.equal(retraceSource("media/x.svg", { source: "media/gone.png" }, paths), "media/x.png");
  assert.equal(retraceSource("media/y.svg", null, paths), "media/y.JPG");
  assert.equal(retraceSource("media/z.svg", null, paths), "");
});

test("where a trace is saved", () => {
  assert.deepEqual(svgTarget("media/x.png", ["media/x.png"]), { existing: "", fresh: "media/x.svg" });
  assert.deepEqual(svgTarget("media/x.png", ["media/x.png", "media/x.svg"]), { existing: "media/x.svg", fresh: "media/x-2.svg" });
  assert.deepEqual(svgTarget("media/x.png", ["media/x.svg", "media/x-2.svg"]), { existing: "media/x.svg", fresh: "media/x-3.svg" });
});

test("a flat picture and a photo", async () => {
  const { looksFlat } = await import("../trace-source.js");
  const w = 200, h = 100;
  const flat = new Uint8ClampedArray(w * h * 4);
  const photo = new Uint8ClampedArray(w * h * 4);
  let seed = 7;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  for (let i = 0; i < w * h; i += 1) {
    const x = i % w;
    // three bands of colour, a few antialiased pixels between them
    const c = x < 66 ? [200, 30, 30] : x < 133 ? [30, 200, 30] : [30, 30, 200];
    flat.set([...c, 255], i * 4);
    photo.set([rnd() * 255, rnd() * 255, rnd() * 255, 255], i * 4);
  }
  assert.equal(looksFlat(flat, w, h), true);
  assert.equal(looksFlat(photo, w, h), false);
  assert.equal(looksFlat(new Uint8ClampedArray(0), 0, 0), false);
});
