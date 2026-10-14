// node --test: the published window's list (web/published.js)
import test from "node:test";
import assert from "node:assert/strict";
import { publishedRows, removal, afterRemoval, sortPublished, nextSort, sizeText, shortUrl, publishedListJson, publishedTitle, termText, endsText } from "../published.js";
import { DAY_MS } from "../linkdays.js";

const O = "https://example.test";
const H1 = "0123456789abcdef01234567";
const H2 = "fedcba9876543210fedcba98";
// the links below were made before links had a term: 30 days from `created`
const NOW = 1000;
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
  const rows = publishedRows(shares, links, O, NOW);
  assert.deepEqual(rows.map((r) => r.id).sort(), [
    "file:Deck01:recordings/web-" + H1 + ".sliqrec",
    "file:Deck01:recordings/web-" + H2 + ".webm",
    "file:Deck02:recordings/web-" + H1 + ".sliqrec",
    "file:Deck03:recordings/web-" + H2 + ".webm",
    "link:Gone01", "link:LinkA", "link:LinkB", "open:Deck02",
  ]);
  const by = new Map(rows.map((r) => [r.id, r]));
  assert.deepEqual(by.get("link:LinkB"), { id: "link:LinkB", kind: "link", deck: "Deck01", name: "Sales", path: "", created: 400, size: 0, url: O + "/s/LinkB", via: "LinkB", ends: 400 + 30 * DAY_MS, hasDeck: true });
  assert.equal(by.get("open:Deck02").url, O + "/s/Deck02");
  assert.equal(by.get("link:Gone01").name, "");
  assert.equal(by.get("link:Gone01").hasDeck, false);
});

test("a recording is shown through the first viewing link, the open address, or not at all", () => {
  const by = new Map(publishedRows(shares, links, O, NOW).map((r) => [r.id, r]));
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
  const rows = publishedRows(shares, links, O, NOW);
  const page = "file:Deck01:recordings/web-" + H1 + ".sliqrec";
  const one = afterRemoval(rows, "link:LinkA", O, NOW);
  assert.equal(one.find((r) => r.id === page).url, O + "/r/LinkB/" + H1);
  const none = afterRemoval(one, "link:LinkB", O, NOW);
  assert.equal(none.find((r) => r.id === page).url, "");
  assert.equal(none.length, rows.length - 2);
  // the open address made private: its recording is no longer shown
  const closed = afterRemoval(rows, "open:Deck02", O, NOW);
  assert.equal(closed.find((r) => r.id === "file:Deck02:recordings/web-" + H1 + ".sliqrec").url, "");
  // a file removed takes only its row
  assert.equal(afterRemoval(rows, page, O, NOW).length, rows.length - 1);
});

test("sorted newest first by default; by size or name, and turned round", () => {
  const rows = publishedRows(shares, links, O, NOW);
  assert.equal(sortPublished(rows)[0].id, "file:Deck02:recordings/web-" + H1 + ".sliqrec");
  assert.equal(sortPublished(rows, "size")[0].kind, "video");
  assert.equal(sortPublished(rows, "name", "asc")[0].name, "");
  assert.equal(sortPublished(rows, "created", "asc").at(-1).created, 900);
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "created"), { by: "created", dir: "asc" });
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "name"), { by: "name", dir: "asc" });
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "updated"), { by: "created", dir: "desc" });
  // by when the link expires: the soonest first, one without a term last
  const ends = sortPublished([{ id: "a", ends: 0, created: 1 }, { id: "b", ends: 50, created: 1 }, { id: "c", ends: 20, created: 1 }], "ends");
  assert.deepEqual(ends.map((r) => r.id), ["c", "b", "a"]);
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "ends"), { by: "ends", dir: "asc" });
});

test("sizes and short addresses", () => {
  assert.equal(sizeText(0), "");
  assert.equal(sizeText(300), "1 kB");
  assert.equal(sizeText(300000), "293 kB");
  assert.equal(sizeText(12 * 1048576), "12.0 MB");
  assert.equal(shortUrl(O + "/r/LinkA/" + H1), "example.test/r/LinkA/012345…");
  assert.equal(shortUrl(O + "/s/LinkA"), "example.test/s/LinkA");
});

test("the window's JSON: its own title, heads and request prefix", () => {
  const t = (s) => s;
  const data = JSON.parse(publishedListJson(publishedRows(shares, links, O, NOW), "size", t, "note", undefined, NOW));
  assert.equal(data.list, "pub");
  assert.equal(data.head1, "created");
  assert.equal(data.head2, "ends");
  assert.equal(data.head2Label, "Expires");
  assert.equal(data.sort, "size");
  assert.equal(data.dir, "desc");
  assert.equal(data.note, "note");
  const first = data.rows[0];
  assert.equal(first.where, "Cloud video (.webm) · example.test/r/LinkA/fedcba… · 12.0 MB");
  assert.equal(first.modified, endsText({ ends: 300 + 30 * DAY_MS }, t, NOW));
  assert.match(first.modified, /^\d+\.\d+\.\d{4}$/);
  assert.deepEqual(first.acts, [{ id: "copy", label: "Copy link" }, { id: "deck", label: "Presentation" }]);
  assert.equal(first.canDelete, true);
  const unshown = data.rows.find((r) => r.id === "file:Deck03:recordings/web-" + H2 + ".webm");
  assert.equal(unshown.where, "Cloud video (.webm) · not shown: no viewing link · 1 kB");
  assert.equal(unshown.modified, "");
  assert.deepEqual(unshown.acts, [{ id: "deck", label: "Presentation" }]);
  const gone = data.rows.find((r) => r.id === "link:Gone01");
  assert.equal(gone.name, "presentation");
  assert.deepEqual(gone.acts, [{ id: "copy", label: "Copy link" }]);
});

test("a link with a term: its recordings shown through their own link, past its term none", () => {
  const day = DAY_MS;
  const now = 100 * day;
  const deck = [{ id: "Deck09", name: "Termed", visibility: "private", files: [
    { path: "recordings/web-" + H1 + ".sliqrec", size: 5, at: now - 10 },
    { path: "recordings/web-" + H2 + ".webm", size: 5, at: now - 10 },
  ] }];
  const termed = [
    { id: "Share9", of: "Deck09", created: now - 2 * day, since: now - 2 * day, days: 7, file: "" },
    { id: "OwnRec", of: "Deck09", created: now - day, since: now - day, days: 1, file: "recordings/web-" + H1 + ".sliqrec" },
    { id: "OldVid", of: "Deck09", created: now - 9 * day, since: now - 9 * day, days: 1, file: "recordings/web-" + H2 + ".webm" },
  ];
  const rows = publishedRows(deck, termed, O, now - 1);
  const by = new Map(rows.map((r) => [r.id, r]));
  // a recording's own link is not a row of its own
  assert.deepEqual(rows.filter((r) => r.kind === "link").map((r) => r.id), ["link:Share9"]);
  const page = by.get("file:Deck09:recordings/web-" + H1 + ".sliqrec");
  assert.equal(page.url, O + "/r/OwnRec/" + H1);
  assert.equal(page.ends, now);
  // its own link past its term: shown through the presentation's link
  const video = by.get("file:Deck09:recordings/web-" + H2 + ".webm");
  assert.equal(video.url, O + "/r/Share9/" + H2);
  // the presentation's link removed: the recording with a link of its own keeps it
  const left = afterRemoval(rows, "link:Share9", O, now - 1);
  assert.equal(left.find((r) => r.id === page.id).url, page.url);
  assert.equal(left.find((r) => r.id === video.id).url, "");
  const t = (s) => s;
  assert.equal(termText({ ends: now }, t, now + 1), "Expired");
  assert.match(termText({ ends: now }, t, now - 1), /^Works until \d+\.\d+\.1970$/);
  assert.equal(termText({ ends: 0 }, t, now), "");
});

test("a server of one's own: its links under their own title, kinds and empty text", () => {
  const t = (s) => s;
  const day = DAY_MS;
  const now = 100 * day;
  const own = [{ id: "Deck07", name: "Own", visibility: "private", files: [{ path: "recordings/web-" + H2 + ".webm", size: 2048, at: now - day }] }];
  const made = [{ id: "a".repeat(32), of: "Deck07", created: now - day, since: now - day, days: 7, file: "recordings/web-" + H2 + ".webm" }];
  const rows = publishedRows(own, made, "https://own.test", now);
  assert.deepEqual(rows.map((r) => [r.kind, r.url]), [["video", "https://own.test/r/" + "a".repeat(32) + "/" + H2]]);
  const data = JSON.parse(publishedListJson(rows, "created", t, "", undefined, now, true));
  assert.equal(data.title, "Shared links");
  assert.equal(data.rows[0].where.split(" · ")[0], "Video (.webm)");
  assert.match(JSON.parse(publishedListJson([], "created", t, "", undefined, now, true)).empty, /^No links yet\./);
  assert.equal(JSON.parse(publishedListJson([], "created", t, "", undefined, now)).title, "Published in the cloud");
  assert.equal(publishedTitle(t, true), "Shared links");
});
