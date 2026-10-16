// node --test: the public viewer's addresses and keys (web/viewlink.js)
import test from "node:test";
import assert from "node:assert/strict";
import { linkOf, linkAt, linkGo, slideOfName, slideLink, viewUrl, exportUrl, exportName, picturesOf, lookFacesOf, slideForKey, fitSlide, pinchView, panView, isZoomed } from "../viewlink.js";

test("/s/{id} is a shared presentation, its picked slides and the slide shown", () => {
  assert.deepEqual(linkOf("/s/AbCdEf1234", "", ""), { id: "AbCdEf1234", slides: "", slide: 0 });
  assert.deepEqual(linkOf("/s/AbCdEf1234/", "?slides=intro,plan", "#slide=3"), { id: "AbCdEf1234", slides: "intro,plan", slide: 2 });
  // the editor's ?edit is gone: the presentation is shown
  assert.equal(linkOf("/s/AbCdEf1234", "?edit", "").id, "AbCdEf1234");
});

test("one slide's link: ?slide= in the query, which the server's link card reads", () => {
  assert.equal(linkOf("/s/AbCdEf1234", "?slide=4", "").slide, 3);
  // the hash, which the viewer writes as one moves on, wins
  assert.equal(linkOf("/s/AbCdEf1234", "?slide=4", "#slide=2").slide, 1);
  assert.equal(linkOf("/s/AbCdEf1234", "?slide=x", "").slide, 0);
  assert.equal(slideLink("https://sliqtly.com", { id: "AbCdEf1234", slides: "" }, 2), "https://sliqtly.com/s/AbCdEf1234?slide=3");
  assert.equal(slideLink("https://sliqtly.com", { id: "AbCdEf1234", slides: "a,b" }, 0), "https://sliqtly.com/s/AbCdEf1234?slides=a%2Cb&slide=1");
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

test("a click on a slide's link: its area, a line's height around the words", () => {
  const list = { links: [{ x: 10, y: 20, w: 50, h: 12, href: "#plan", slide: 2 }, { x: 10, y: 40, w: 30, h: 12, href: "/s/AbCdEf1234", slide: -1 }] };
  assert.equal(linkAt(list, 30, 25).href, "#plan");
  assert.equal(linkAt(list, 30, 33).href, "#plan");
  assert.equal(linkAt(list, 25, 45).href, "/s/AbCdEf1234");
  assert.equal(linkAt(list, 100, 25), null);
  assert.equal(linkAt({ cmds: [] }, 30, 25), null);
});

test("where a link goes from the viewer: a slide here, a deck in this tab, the web in a new one", () => {
  const o = "https://sliqtly.com";
  assert.deepEqual(linkGo({ href: "#plan", slide: 2 }, o, o), { slide: 2 });
  assert.equal(linkGo({ href: "#gone", slide: -1 }, o, o), null);
  assert.deepEqual(linkGo({ href: "https://sliqtly.com/s/AbCdEf1234#slide=3", slide: -1 }, o, o), { open: "https://sliqtly.com/s/AbCdEf1234#slide=3" });
  assert.deepEqual(linkGo({ href: "/s/AbCdEf1234#export", slide: -1 }, o, o), { open: "https://sliqtly.com/s/AbCdEf1234#export" });
  // the editor's address of a deck opens in the viewer
  assert.deepEqual(linkGo({ href: "https://sliqtly.com/editor/s/AbCdEf1234", slide: -1 }, o, o), { open: "https://sliqtly.com/s/AbCdEf1234" });
  assert.deepEqual(linkGo({ href: "https://sliqtly.com/editor/d/AbCdEf1234", slide: -1 }, o, o), { open: "https://sliqtly.com/s/AbCdEf1234" });
  // a server of one's own: its decks here, the public site's in their place
  const local = "http://localhost:8080";
  assert.deepEqual(linkGo({ href: "/s/AbCdEf1234", slide: -1 }, local, o), { open: "http://localhost:8080/s/AbCdEf1234" });
  assert.deepEqual(linkGo({ href: "https://sliqtly.com/s/AbCdEf1234", slide: -1 }, local, o), { open: "https://sliqtly.com/s/AbCdEf1234" });
  assert.deepEqual(linkGo({ href: "https://example.com/x", slide: -1 }, o, o), { tab: "https://example.com/x" });
  assert.deepEqual(linkGo({ href: "https://sliqtly.com/editor", slide: -1 }, o, o), { tab: "https://sliqtly.com/editor" });
  assert.equal(linkGo({ href: "javascript:alert(1)", slide: -1 }, o, o), null);
  assert.equal(linkGo({ href: "mailto:a@b.c", slide: -1 }, o, o), null);
  assert.equal(linkGo(null, o, o), null);
});

test("a link from another deck to a heading: /s/{id}#heading", () => {
  const names = [["Intro"], ["Mihin raha menee?", "money"], ["Kolmas"], ["Äänet → tulos"]];
  assert.equal(slideOfName(names, "#mihin-raha-menee"), 1);
  assert.equal(slideOfName(names, "#money"), 1);
  assert.equal(slideOfName(names, "#Mihin%20raha%20menee%3F"), 1);
  assert.equal(slideOfName(names, "#Kolmas"), 2);
  assert.equal(slideOfName(names, "#%C3%A4%C3%A4net-tulos"), 3);
  assert.equal(slideOfName(names, "#slide=3"), -1);
  assert.equal(slideOfName(names, "#ei-ole"), -1);
  assert.equal(slideOfName(names, ""), -1);
  assert.equal(slideOfName(undefined, "#intro"), -1);
});
