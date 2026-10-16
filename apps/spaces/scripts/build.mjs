#!/usr/bin/env node
/**
 * npm run spaces:build — compile SpacesApp.rgr and assemble apps/spaces/web/dist.
 *
 * The output is static: any file server can serve it.
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { log } from "../../../scripts/lib.mjs";
import { minify } from "../../../scripts/build.mjs";
import { appDir, spacesRanger, compileSpaces, spacesCss } from "./lib.mjs";

const webDir = path.join(appDir, "web");
const distDir = path.join(webDir, "dist");

try {
  const ranger = spacesRanger();
  fs.rmSync(distDir, { recursive: true, force: true });
  fs.mkdirSync(path.join(distDir, "gl"), { recursive: true });
  const appJs = path.join(distDir, "spaces_app.js");
  compileSpaces(ranger, "SpacesApp.rgr", appJs);
  // a classic script that publishes what the page needs: the app, and the
  // measurer classes evg-measure.js installs the browser's text widths into
  fs.writeFileSync(appJs, "// Spaces (apps/spaces/src), compiled by Ranger.\n(function () {\n"
    + fs.readFileSync(appJs, "utf8")
    + "\n;globalThis.SpacesModule = { SpacesApp, EVGHostTextMeasurer, EVGDefaultMeasurer };\n})();\n");
  minify(appJs);
  for (const f of ["index.html", "main.js"]) fs.copyFileSync(path.join(webDir, f), path.join(distDir, f));
  fs.writeFileSync(path.join(distDir, "spaces.css"), spacesCss(ranger));
  for (const f of ["evg-webgl.js", "evg-a11y.js", "evg-measure.js"]) {
    fs.copyFileSync(path.join(ranger, "lib/evg/gl", f), path.join(distDir, "gl", f));
  }
  fs.copyFileSync(path.join(appDir, "..", "..", "brand", "sliqtly-icon.svg"), path.join(distDir, "favicon.svg"));
  // every URL the page loads carries the build's hash
  const h = crypto.createHash("sha1");
  for (const f of fs.readdirSync(distDir, { recursive: true }).sort()) {
    const p = path.join(distDir, f);
    if (fs.statSync(p).isFile()) h.update(f).update(fs.readFileSync(p));
  }
  const stamp = h.digest("hex").slice(0, 10);
  for (const f of ["index.html", "main.js"]) {
    const p = path.join(distDir, f);
    fs.writeFileSync(p, fs.readFileSync(p, "utf8").split("__BUILD__").join(stamp));
  }
  log(`build  apps/spaces/web/dist (${stamp})`);
} catch (e) {
  log(e.message);
  process.exit(1);
}
