// node --test: a presentation as one .html file (web/player-file.js)
import test from "node:test";
import assert from "node:assert/strict";
import zlib from "node:zlib";
import { assetKey, embeddedAsset, embeddedDeck, fileData, playerHtml, base64, DECK_SLOT, DECK_ID } from "../player-file.js";

test("an asset is kept under its path, without ./ or the build stamp", () => {
  assert.equal(assetKey("./fonts/OpenSans-Bold.ttf?v=abc"), "fonts/OpenSans-Bold.ttf");
  assert.equal(assetKey("./pres.css"), "pres.css");
  assert.equal(assetKey("./i18n/fi.json?v=1#x"), "i18n/fi.json");
});

test("a fetch of the page's own file is answered from the table, unzipped", async () => {
  const css = "body { color: red }";
  const table = {
    "pres.css": { type: "text/css", gz: true, b64: zlib.gzipSync(css).toString("base64") },
    "raw.bin": { type: "application/octet-stream", b64: Buffer.from([1, 2, 3]).toString("base64") },
  };
  const res = await embeddedAsset("./pres.css?v=9", table);
  assert.equal(await res.text(), css);
  assert.equal(res.headers.get("content-type"), "text/css");
  const raw = await embeddedAsset("./raw.bin", table);
  assert.deepEqual([...new Uint8Array(await raw.arrayBuffer())], [1, 2, 3]);
});

test("anything else is fetched: no table, no such asset, another site", async () => {
  assert.equal(await embeddedAsset("./pres.css", null), null);
  assert.equal(await embeddedAsset("./nope.css", {}), null);
  assert.equal(await embeddedAsset("https://example.com/pres.css", { "pres.css": { b64: "" } }), null);
});

const page = (deckTag = "") => ({ getElementById: (id) => (id === DECK_ID && deckTag ? { textContent: deckTag } : null) });

test("the deck goes in the slot, and a </script> in it cannot end the element", () => {
  const html = playerHtml(`<body>${DECK_SLOT}</body>`, { md: "# A\n\n</script><script>alert(1)</script>", files: [] });
  assert.ok(!html.includes(DECK_SLOT));
  assert.equal(html.split("</script>").length, 2, "only the deck's own closing tag");
  const json = html.slice(html.indexOf(">") + 1 + html.slice(html.indexOf(">") + 1).indexOf(">") + 1, html.lastIndexOf("</script>"));
  const d = embeddedDeck(page(json));
  assert.equal(d.md, "# A\n\n</script><script>alert(1)</script>");
  assert.throws(() => playerHtml("<body></body>", { md: "" }));
});

test("a page without a deck is not a player file", () => {
  assert.equal(embeddedDeck(page()), null);
  assert.equal(embeddedDeck(page("not json")), null);
  assert.equal(embeddedDeck(page('{"name":"x"}')), null);
  const d = embeddedDeck(page('{"md":"# A"}'));
  assert.deepEqual(d, { name: "presentation", md: "# A", theme: "", css: null, files: [] });
});

test("a file comes back as text or as bytes of its type", async () => {
  assert.equal(fileData({ path: "a.csv", type: "text/csv", text: "a,b" }), "a,b");
  const bytes = new Uint8Array(70000).map((_, i) => i % 251);
  const b = fileData({ path: "p.png", type: "image/png", b64: base64(bytes) });
  assert.equal(b.type, "image/png");
  assert.deepEqual(new Uint8Array(await b.arrayBuffer()), bytes);
});
