// node --test: a deck's versions in a server's Git (web/githistory.js)
import test from "node:test";
import assert from "node:assert/strict";
import { versionAddress, gitEntries, gitCheckout, gitChanges, mergeEntries, isGitVersion } from "../githistory.js";
import { linkTarget } from "../stagelink.js";

const O = "http://localhost:8787";

test("an address names a version by its commit or branch", () => {
  assert.deepEqual(versionAddress("/s/AbCdEf1234@3f2a9c1"), { path: "/s/AbCdEf1234", deck: "AbCdEf1234", rev: "3f2a9c1" });
  assert.deepEqual(versionAddress("/editor/s/AbCdEf1234@main/"), { path: "/editor/s/AbCdEf1234", deck: "AbCdEf1234", rev: "main" });
  assert.equal(versionAddress("/s/AbCdEf1234"), null);
  assert.equal(versionAddress("/s/AbCdEf1234@"), null);
  assert.equal(versionAddress("/s/AbCdEf1234@a..b"), null);
  assert.equal(versionAddress("/s/AbCdEf1234@%2e%2e%2fx"), null);
  assert.equal(versionAddress("/s/AbCdEf1234@x/y"), null);
});

test("a link to a version is the deck with its version", () => {
  const to = linkTarget("/s/AbCdEf1234@3f2a9c1#mallit", O, "https://sliqtly.com");
  assert.equal(to.deck, "AbCdEf1234");
  assert.equal(to.rev, "3f2a9c1");
  assert.equal(to.name, "#mallit");
  assert.equal(linkTarget("/s/AbCdEf1234", O, O).rev, undefined);
});

// a fake server: answers by the address, as /api/history does
function server(routes) {
  return async (url) => {
    const hit = routes[url];
    if (hit === undefined) return new Response(JSON.stringify({ error: "no such" }), { status: 404 });
    if (typeof hit === "string" || hit instanceof Uint8Array) return new Response(hit, { status: 200 });
    return new Response(JSON.stringify(hit), { status: 200, headers: { "Content-Type": "application/json" } });
  };
}

const C2 = "b".repeat(40);
const C1 = "a".repeat(40);
const get = server({
  "/api/history/AbCdEf1234?n=100": { commits: [
    { id: C2, subject: 'Deck "Mallit": Malli B', author: "Tero", when: Date.UTC(2026, 9, 21, 10), parent: C1 },
    { id: C1, subject: "Import from SQLite", author: "Sliqtly server", when: Date.UTC(2026, 9, 20, 10) },
  ] },
  ["/api/history/AbCdEf1234/" + C2]: { id: C2, when: Date.UTC(2026, 9, 21, 10), subject: "Malli B", name: "Mallit", md: "# Mallit\n\n## B\n", css: null, theme: "aurora", files: [{ path: "media/dot.png", type: "image/png" }, { path: "data/x.csv", type: "text/csv" }] },
  ["/api/history/AbCdEf1234/" + C2 + "/file/media/dot.png"]: new Uint8Array([137, 80, 78, 71]),
  ["/api/history/AbCdEf1234/" + C2 + "/file/data/x.csv"]: "a,b\n1,2\n",
  ["/api/history/AbCdEf1234/" + C2 + "/changes"]: { changes: [{ path: "deck.md", kind: "changed", text: true }, { path: "media/dot.png", kind: "added" }] },
  ["/api/history/AbCdEf1234/" + C2 + "/file/deck.md"]: "# Mallit\n\n## B\n",
  ["/api/history/AbCdEf1234/" + C1 + "/file/deck.md"]: "# Mallit\n",
});

test("the server's commits are versions of the panel", async () => {
  const es = await gitEntries(get, "AbCdEf1234");
  assert.equal(es.length, 2);
  assert.ok(isGitVersion(es[0].id));
  assert.equal(es[0].commit, C2);
  assert.equal(es[0].parent, C1);
  assert.equal(es[0].message, 'Deck "Mallit": Malli B');
  assert.match(es[0].device, /^Git bbbbbbb · Tero$/);
  // no Git on the server: none, no error
  assert.deepEqual(await gitEntries(server({}), "AbCdEf1234"), []);
  const all = mergeEntries([{ id: "local1", time: "2026-10-20T12:00:00.000Z" }], es);
  assert.deepEqual(all.map((e) => e.id), ["git:" + C2, "local1", "git:" + C1]);
});

test("a version is checked out with its files", async () => {
  const s = await gitCheckout(get, "AbCdEf1234", C2);
  assert.equal(s.md, "# Mallit\n\n## B\n");
  assert.equal(s.name, "Mallit");
  assert.equal(s.css, null);
  assert.equal(s.files.length, 2);
  assert.ok(s.files[0].data instanceof Blob);
  assert.equal(s.files[1].data, "a,b\n1,2\n");
});

test("what a version changed, lines counted", async () => {
  const rows = await gitChanges(get, "AbCdEf1234", C2, C1, (a, b) => ({ added: b.split("\n").length - a.split("\n").length, removed: 0 }));
  assert.deepEqual(rows[0], { path: "deck.md", kind: "changed", added: 2, removed: 0, text: true });
  assert.deepEqual(rows[1], { path: "media/dot.png", kind: "added", added: 0, removed: 0, text: false });
});
