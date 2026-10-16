#!/usr/bin/env node
/**
 * npm run spaces:check — the model's checks (src/SpacesCheck.rgr) and the
 * page's (test/app-check.mjs: every view lays out, at desktop and phone
 * width, with no stylesheet errors and a clean accessible tree).
 */
import path from "node:path";
import { spawnSync } from "node:child_process";
import { log } from "../../../scripts/lib.mjs";
import { appDir, spacesRanger, compileSpaces } from "./lib.mjs";

try {
  const ranger = spacesRanger();
  const check = path.join(appDir, "bin", "spaces_check.js");
  compileSpaces(ranger, "SpacesCheck.rgr", check, "-nodecli");
  const r = spawnSync(process.execPath, [check], { encoding: "utf8" });
  const text = `${r.stdout || ""}${r.stderr || ""}`;
  process.stdout.write(text);
  if (r.status !== 0 || /CHECK FAILED/.test(text) || !/passed/.test(text)) process.exit(1);
  compileSpaces(ranger, "SpacesApp.rgr", path.join(appDir, "bin", "SpacesApp.cjs"), "-nodemodule");
  const t = spawnSync(process.execPath, [path.join(appDir, "test", "app-check.mjs"), ranger], { stdio: "inherit" });
  if (t.status !== 0) process.exit(1);
} catch (e) {
  log(e.message);
  process.exit(1);
}
