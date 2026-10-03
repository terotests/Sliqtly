// node --test: every file a sample deck's chart reads ("url": "data/…") is
// in samples/data, which the build serves beside the page (web/dist/data). A
// missing one is a 404 in the console of every visitor who opens the deck.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const samples = path.join(root, "samples");

test("every data/ file the sample decks read is in samples/data", () => {
  const wanted = new Set();
  for (const f of fs.readdirSync(samples).filter((f) => f.endsWith(".md"))) {
    for (const m of fs.readFileSync(path.join(samples, f), "utf8").matchAll(/"url":\s*"(?:\.\/)?data\/([\w.-]+\.\w+)"/g)) wanted.add(m[1]);
  }
  assert.ok(wanted.size > 0);
  const missing = [...wanted].filter((f) => !fs.existsSync(path.join(samples, "data", f)));
  assert.deepEqual(missing, []);
});
