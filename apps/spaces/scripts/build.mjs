#!/usr/bin/env node
/**
 * npm run spaces:build — compile SpacesApp.rgr and assemble apps/spaces/web/dist.
 * npm run warden:build — the same app on a server, apps/spaces/web/dist-warden:
 * titled Warden, with <meta name="app-server"> naming the path it is served
 * at (/apps/warden/), where its calls (api/<op>) and sign-in (login) are.
 *
 * The output is static: any file server can serve it.
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { log } from "../../../scripts/lib.mjs";
import { minify } from "../../../scripts/build.mjs";
import { stampImports } from "../../../scripts/stamp.mjs";
import { appDir, spacesRanger, compileSpaces, spacesCss } from "./lib.mjs";

const webDir = path.join(appDir, "web");
export const distDir = path.join(webDir, "dist");
export const wardenDir = path.join(webDir, "dist-warden");
export const WARDEN_PATH = "/apps/warden/";

/** Build the page into dist, or with `warden` the server's page into dist-warden. */
export function buildSpaces({ warden = false, ranger } = {}) {
  ranger = ranger || spacesRanger();
  const out = warden ? wardenDir : distDir;
  const name = path.relative(path.join(appDir, "..", ".."), out);
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(path.join(out, "gl"), { recursive: true });
  const appJs = path.join(out, "spaces_app.js");
  compileSpaces(ranger, "SpacesApp.rgr", appJs);
  // a classic script that publishes what the page needs: the app, and the
  // measurer classes evg-measure.js installs the browser's text widths into
  fs.writeFileSync(appJs, "// Spaces (apps/spaces/src), compiled by Ranger.\n(function () {\n"
    + fs.readFileSync(appJs, "utf8")
    + "\n;globalThis.SpacesModule = { SpacesApp, EVGHostTextMeasurer, EVGDefaultMeasurer };\n})();\n");
  minify(appJs);
  for (const f of ["index.html", "main.js"]) fs.copyFileSync(path.join(webDir, f), path.join(out, f));
  if (warden) {
    const html = path.join(out, "index.html");
    fs.writeFileSync(html, fs.readFileSync(html, "utf8")
      .replace("<title>Spaces</title>", "<title>Warden</title>")
      // the base: its files resolve the same at /apps/warden as at /apps/warden/
      .replace('<meta name="build"', `<base href="${WARDEN_PATH}" />\n<meta name="app-server" content="${WARDEN_PATH}" />\n<meta name="build"`));
  }
  fs.writeFileSync(path.join(out, "spaces.css"), spacesCss(ranger));
  for (const f of ["evg-webgl.js", "evg-a11y.js", "evg-measure.js", "evg-textinput.js"]) {
    fs.copyFileSync(path.join(ranger, "lib/evg/gl", f), path.join(out, "gl", f));
  }
  fs.copyFileSync(path.join(appDir, "..", "..", "brand", "sliqtly-icon.svg"), path.join(out, "favicon.svg"));
  // every URL the page loads carries the build's hash
  const h = crypto.createHash("sha1");
  for (const f of fs.readdirSync(out, { recursive: true }).sort()) {
    const p = path.join(out, f);
    if (fs.statSync(p).isFile()) h.update(f).update(fs.readFileSync(p));
  }
  const stamp = h.digest("hex").slice(0, 10);
  for (const f of ["index.html", "main.js"]) {
    const p = path.join(out, f);
    fs.writeFileSync(p, stampImports(fs.readFileSync(p, "utf8").split("__BUILD__").join(stamp), stamp));
  }
  log(`build  ${name} (${stamp})`);
  return out;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    buildSpaces({ warden: process.argv.includes("--warden") });
  } catch (e) {
    log(e.message);
    process.exit(1);
  }
}
