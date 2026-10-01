#!/usr/bin/env node
/** npm run check: the deck and timeline checks, under Node. */
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
} catch (e) {
  log(e.message);
  process.exit(1);
}
