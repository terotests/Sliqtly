#!/usr/bin/env node
/** npm run check: the deck and timeline checks, and web/test/*.test.mjs, under Node. */
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { ensureRanger, compile, root, log } from "./lib.mjs";

try {
  const ranger = ensureRanger();
  const out = path.join(root, "bin", "pres_check.js");
  compile(ranger, "PresCheck.rgr", out, "-nodecli");
  const r = spawnSync(process.execPath, [out], { encoding: "utf8" });
  const text = `${r.stdout || ""}${r.stderr || ""}`;
  process.stdout.write(text);
  if (r.status !== 0 || /CHECK FAILED/.test(text) || !/passed/.test(text)) process.exit(1);
  // the page's own rules that need no browser (web/brand.js, …)
  const dir = path.join(root, "web", "test");
  const tests = fs.readdirSync(dir).filter((f) => f.endsWith(".test.mjs")).map((f) => path.join(dir, f));
  const t = spawnSync(process.execPath, ["--test", ...tests], { stdio: "inherit" });
  if (t.status !== 0) process.exit(1);
} catch (e) {
  log(e.message);
  process.exit(1);
}
