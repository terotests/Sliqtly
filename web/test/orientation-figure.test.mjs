// node --test: a list drawn as a figure (`{list-style=swot}`) is as tall as
// the room of the page it is on, also right after `orientation:` turned the
// page. The room used to come from the layout made before the turn, so a
// deck turned to landscape drew its SWOT as tall as the portrait page and
// ran it over the slide's bottom edge.
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
const css = fs.readFileSync(path.join(dist, "themes", "aurora.css"), "utf8");

const body = "## SWOT\n\n- Strengths: strong brand\n  - experienced team\n- Weaknesses: small sales team\n- Opportunities: new markets in the Nordics\n- Threats: larger competitors\n{list-style=swot}\n";

// the figure's box (kind 3) and the page it is laid out on
function figureOn(app, orientation) {
  app.setSource(`---\norientation: ${orientation}\n---\n\n` + body);
  const l = app.deck.md.edit.layout;
  const fig = l.boxes.find((b) => b.kind === 3);
  assert.ok(fig, `${orientation}: the list is drawn as a figure`);
  return { fig, pageW: l.style.pageWidth, pageH: l.style.pageHeight, margin: l.style.margin };
}

test("a turned page lays its SWOT out in its own room", () => {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  const seen = {};
  for (const o of ["portrait", "landscape", "portrait", "landscape"]) {
    const { fig, pageW, pageH, margin } = figureOn(app, o);
    assert.equal(pageH > pageW, o === "portrait", `${o}: the page is turned`);
    assert.ok(fig.y + fig.h <= pageH - margin + 0.5, `${o}: the figure ends at ${fig.y + fig.h}, over the bottom ${pageH - margin}`);
    // the same page gives the same figure, whichever page came before it
    if (seen[o] !== undefined) assert.ok(Math.abs(seen[o] - fig.h) < 0.5, `${o}: ${fig.h} after the turn, ${seen[o]} before`);
    seen[o] = fig.h;
  }
  assert.ok(seen.portrait > seen.landscape, "the tall page has the taller figure");
});
