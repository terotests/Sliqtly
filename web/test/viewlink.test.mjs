// node --test: the public viewer's addresses and keys (web/viewlink.js)
import test from "node:test";
import assert from "node:assert/strict";
import { linkOf, viewUrl, exportUrl, exportName, picturesOf, lookFacesOf, slideForKey, fitSlide, pinchView, panView, isZoomed } from "../viewlink.js";

test("/s/{id} is a shared presentation, its picked slides and the slide shown", () => {
  assert.deepEqual(linkOf("/s/AbCdEf1234", "", ""), { id: "AbCdEf1234", slides: "", slide: 0 });
  assert.deepEqual(linkOf("/s/AbCdEf1234/", "?slides=intro,plan", "#slide=3"), { id: "AbCdEf1234", slides: "intro,plan", slide: 2 });
  // the editor's ?edit is gone: the presentation is shown
  assert.equal(linkOf("/s/AbCdEf1234", "?edit", "").id, "AbCdEf1234");
});

test("the assistant's preview gives the link in <meta>", () => {
  assert.deepEqual(linkOf("", "", "", "#share=AbCdEf1234"), { id: "AbCdEf1234", slides: "", slide: 0 });
  assert.deepEqual(linkOf("/", "", "#share=AbCdEf1234&slide=2"), { id: "AbCdEf1234", slides: "", slide: 1 });
});

test("a link carrying the Markdown is told apart; the front page is none", () => {
  assert.deepEqual(linkOf("/", "", "#md=abc&mode=show"), { md: true });
  assert.equal(linkOf("/", "", ""), null);
  assert.equal(linkOf("/s/no", "", ""), null);
  assert.equal(linkOf("/", "", "#share=../x"), null);
});

test("the slides' address", () => {
  assert.equal(viewUrl({ id: "AbCdEf1234", slides: "" }), "/api/view/AbCdEf1234");
  assert.equal(viewUrl({ id: "AbCdEf1234", slides: "a b,c" }), "/api/view/AbCdEf1234?slides=a%20b%2Cc");
});

test("downloads: the address per format and the file's name", () => {
  assert.equal(exportUrl({ id: "AbCdEf1234", slides: "" }, "pdf"), "/api/export/AbCdEf1234/pdf");
  assert.equal(exportUrl({ id: "AbCdEf1234", slides: "a,b" }, "md"), "/api/export/AbCdEf1234/md?slides=a%2Cb");
  assert.equal(exportName("Säästöt: Q3/2026", "pptx"), "Säästöt- Q3-2026.pptx");
  assert.equal(exportName("", "md"), "presentation.md");
});

test("pictures by the name the commands draw them by", () => {
  const deck = { files: [{ path: "media/a.png", type: "image/png", url: "https://x/a" }, { path: "", url: "https://x/b" }] };
  assert.deepEqual(picturesOf(deck), [{ src: "/media/a.png", url: "https://x/a", type: "image/png", path: "media/a.png" }]);
  assert.deepEqual(picturesOf({}), []);
});

test("a diagram look's faces only when its text uses them", () => {
  assert.deepEqual(lookFacesOf([{ cmds: [{ font: "Open Sans" }] }]), []);
  assert.deepEqual(lookFacesOf([{ cmds: [{ font: "Gloria Hallelujah" }, { font: "Josefin Sans" }] }]), ["Gloria Hallelujah", "Josefin Sans-Bold"]);
});

test("keys go round the slides and stop at the ends", () => {
  assert.equal(slideForKey("ArrowRight", 0, 3), 1);
  assert.equal(slideForKey(" ", 2, 3), 2);
  assert.equal(slideForKey("ArrowLeft", 0, 3), 0);
  assert.equal(slideForKey("PageUp", 2, 3), 1);
  assert.equal(slideForKey("Home", 2, 3), 0);
  assert.equal(slideForKey("End", 0, 3), 2);
  assert.equal(slideForKey("x", 0, 3), -1);
});

test("the slide fits the window, centred", () => {
  assert.deepEqual(fitSlide(1920, 1080, 960, 540), { x: 0, y: 0, scale: 2 });
  const tall = fitSlide(400, 800, 960, 540);
  assert.equal(tall.x, 0);
  assert.ok(Math.abs(tall.y - (800 - 540 * tall.scale) / 2) < 1e-9);
});

test("a pinch zooms about the fingers, between the fitted slide and six times it", () => {
  const fit = fitSlide(400, 800, 960, 540);
  // spread to twice apart about the slide's middle: twice the size, the middle kept
  const z = pinchView(fit, 400, 800, 960, 540, 2, 200, 400, 0, 0);
  assert.ok(Math.abs(z.scale - fit.scale * 2) < 1e-9);
  assert.ok(Math.abs(z.x - (200 - 480 * z.scale)) < 1e-9);
  assert.ok(isZoomed(z, 400, 800, 960, 540));
  // the fingers moved together: the slide goes with them
  const moved = pinchView(z, 400, 800, 960, 540, 1, 230, 400, 30, 0);
  assert.ok(Math.abs(moved.x - (z.x + 30)) < 1e-9);
  assert.equal(pinchView(z, 400, 800, 960, 540, 100, 200, 400, 0, 0).scale, fit.scale * 6);
  // pinched in past the fitted size: fitted, not smaller
  assert.deepEqual(pinchView(z, 400, 800, 960, 540, 0.1, 200, 400, 0, 0), fit);
  assert.ok(!isZoomed(fit, 400, 800, 960, 540));
  assert.ok(!isZoomed(null, 400, 800, 960, 540));
});

test("a zoomed slide is dragged no further than its edges", () => {
  const z = pinchView(fitSlide(400, 800, 960, 540), 400, 800, 960, 540, 3, 200, 400, 0, 0);
  assert.equal(panView(z, 400, 800, 960, 540, 5000, 0).x, 0);
  assert.equal(panView(z, 400, 800, 960, 540, -5000, 0).x, 400 - 960 * z.scale);
  // shorter than the window: it stays centred up and down
  assert.equal(panView(z, 400, 800, 960, 540, 0, 300).y, (800 - 540 * z.scale) / 2);
});
