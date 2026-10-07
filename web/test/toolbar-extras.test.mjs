// node --test: the page's own bar buttons sent again unchanged do not ask
// for a new frame. The page sends them whenever its hidden bar is touched,
// and every frame wrote the status text there, so a deck sat repainting
// every frame with nothing moving (slow with large pictures).
//
// Runs the built app (web/dist/pres_app.js, `npm run build`).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));

test("the same bar buttons again leave the frame as it was", () => {
  const app = new globalThis.PresApp();
  app.useToolbar(true);
  app.setToolbarExtras("share\tShare\tsecondary\t");
  const rev = app.revision();
  app.setToolbarExtras("share\tShare\tsecondary\t");
  assert.equal(app.revision(), rev);
  app.setToolbarExtras("share\tJaa\tsecondary\t");
  assert.notEqual(app.revision(), rev, "a changed button is a new frame");
});
