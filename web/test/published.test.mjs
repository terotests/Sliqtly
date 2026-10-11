// node --test: the published window's list (web/published.js)
import test from "node:test";
import assert from "node:assert/strict";
import { publishedRows, removal, afterRemoval, sortPublished, nextSort, sizeText, shortUrl, publishedListJson } from "../published.js";

const O = "https://sliqtly.com";
const H1 = "0123456789abcdef01234567";
const H2 = "fedcba9876543210fedcba98";
const shares = [
  {
    id: "Deck01", name: "Sales", visibility: "private",
    files: [
      { path: "media/logo.png", size: 10 },
      { path: "recordings/web-" + H1 + ".sliqrec", size: 300000, at: 500 },
      { path: "recordings/web-" + H2 + ".webm", size: 12 * 1048576, at: 600 },
      { path: "recordings/take.json", size: 50 },
    ],
  },
  { id: "Deck02", name: "Old open one", visibility: "link", files: [{ path: "recordings/web-" + H1 + ".sliqrec", size: 2048, at: 900 }] },
  { id: "Deck03", name: "Private with a recording", visibility: "private", files: [{ path: "recordings/web-" + H2 + ".webm", size: 1, at: 700 }] },
];
const links = [
  { id: "LinkB", of: "Deck01", created: 400 },
  { id: "LinkA", of: "Deck01", created: 300 },
  { id: "Gone01", of: "Deleted", created: 100 },
];

test("viewing links, an open presentation and its recordings are rows; other files are not", () => {
  const rows = publishedRows(shares, links, O);
  assert.deepEqual(rows.map((r) => r.id).sort(), [
    "file:Deck01:recordings/web-" + H1 + ".sliqrec",
    "file:Deck01:recordings/web-" + H2 + ".webm",
    "file:Deck02:recordings/web-" + H1 + ".sliqrec",
    "file:Deck03:recordings/web-" + H2 + ".webm",
    "link:Gone01", "link:LinkA", "link:LinkB", "open:Deck02",
  ]);
  const by = new Map(rows.map((r) => [r.id, r]));
  assert.deepEqual(by.get("link:LinkB"), { id: "link:LinkB", kind: "link", deck: "Deck01", name: "Sales", path: "", created: 400, size: 0, url: O + "/s/LinkB" });
  assert.equal(by.get("open:Deck02").url, O + "/s/Deck02");
  assert.equal(by.get("link:Gone01").name, "");
});

test("a recording is shown through the first viewing link, the open address, or not at all", () => {
  const by = new Map(publishedRows(shares, links, O).map((r) => [r.id, r]));
  const page = by.get("file:Deck01:recordings/web-" + H1 + ".sliqrec");
  assert.equal(page.kind, "page");
  assert.equal(page.url, O + "/r/LinkA/" + H1);
  assert.equal(page.created, 500);
  const video = by.get("file:Deck01:recordings/web-" + H2 + ".webm");
  assert.equal(video.kind, "video");
  assert.equal(video.size, 12 * 1048576);
  assert.equal(by.get("file:Deck02:recordings/web-" + H1 + ".sliqrec").url, O + "/r/Deck02/" + H1);
  assert.equal(by.get("file:Deck03:recordings/web-" + H2 + ".webm").url, "");
});

test("a share with no visibility is open to anyone with its address", () => {
  const rows = publishedRows([{ id: "Old001", name: "Old", files: [] }], [], O);
  assert.deepEqual(rows.map((r) => r.id), ["open:Old001"]);
});

test("removal names what a delete removes, and nothing else", () => {
  assert.deepEqual(removal("link:LinkA"), { what: "link", link: "LinkA" });
  assert.deepEqual(removal("open:Deck02"), { what: "private", deck: "Deck02" });
  assert.deepEqual(removal("file:Deck01:recordings/web-" + H2 + ".webm"), { what: "file", deck: "Deck01", path: "recordings/web-" + H2 + ".webm" });
  assert.equal(removal("file:Deck01:media/logo.png"), null);
  assert.equal(removal("file:Deck01:recordings/take.json"), null);
  assert.equal(removal("link:"), null);
  assert.equal(removal("x"), null);
});

test("a viewing link removed moves its recordings to the next link, or leaves them unshown", () => {
  const rows = publishedRows(shares, links, O);
  const page = "file:Deck01:recordings/web-" + H1 + ".sliqrec";
  const one = afterRemoval(rows, "link:LinkA", O);
  assert.equal(one.find((r) => r.id === page).url, O + "/r/LinkB/" + H1);
  const none = afterRemoval(one, "link:LinkB", O);
  assert.equal(none.find((r) => r.id === page).url, "");
  assert.equal(none.length, rows.length - 2);
  // the open address made private: its recording is no longer shown
  const closed = afterRemoval(rows, "open:Deck02", O);
  assert.equal(closed.find((r) => r.id === "file:Deck02:recordings/web-" + H1 + ".sliqrec").url, "");
  // a file removed takes only its row
  assert.equal(afterRemoval(rows, page, O).length, rows.length - 1);
});

test("sorted newest first by default; by size or name, and turned round", () => {
  const rows = publishedRows(shares, links, O);
  assert.equal(sortPublished(rows)[0].id, "file:Deck02:recordings/web-" + H1 + ".sliqrec");
  assert.equal(sortPublished(rows, "size")[0].kind, "video");
  assert.equal(sortPublished(rows, "name", "asc")[0].name, "");
  assert.equal(sortPublished(rows, "created", "asc").at(-1).created, 900);
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "created"), { by: "created", dir: "asc" });
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "name"), { by: "name", dir: "asc" });
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "updated"), { by: "created", dir: "desc" });
});

test("sizes and short addresses", () => {
  assert.equal(sizeText(0), "");
  assert.equal(sizeText(300), "1 kB");
  assert.equal(sizeText(300000), "293 kB");
  assert.equal(sizeText(12 * 1048576), "12.0 MB");
  assert.equal(shortUrl(O + "/r/LinkA/" + H1), "sliqtly.com/r/LinkA/012345…");
  assert.equal(shortUrl(O + "/s/LinkA"), "sliqtly.com/s/LinkA");
});

test("the window's JSON: its own title, heads and request prefix", () => {
  const t = (s) => s;
  const data = JSON.parse(publishedListJson(publishedRows(shares, links, O), "size", t, "note"));
  assert.equal(data.list, "pub");
  assert.equal(data.head1, "created");
  assert.equal(data.head2, "size");
  assert.equal(data.sort, "size");
  assert.equal(data.dir, "desc");
  assert.equal(data.note, "note");
  const first = data.rows[0];
  assert.equal(first.modified, "12.0 MB");
  assert.equal(first.where, "Cloud video (.webm) · sliqtly.com/r/LinkA/fedcba…");
  assert.equal(first.canDelete, true);
  const unshown = data.rows.find((r) => r.id === "file:Deck03:recordings/web-" + H2 + ".webm");
  assert.equal(unshown.where, "Cloud video (.webm) · not shown: no viewing link");
  assert.equal(data.rows.find((r) => r.id === "link:Gone01").name, "presentation");
});
