#!/usr/bin/env node
/**
 * npm run bench:flight: how much of what the 2-D slides draw the 3-D flight
 * shows (src/PresFlightBench.rgr), case by case (scripts/fixtures/flight-bench.md).
 * Prints the report; `-- --out file.md` writes it there too, `-- --case id`
 * prints that case's slide command by command instead.
 */
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { ensureRanger, compile, root, log } from "./lib.mjs";
import { themeNames, themeCss } from "./themes.mjs";

try {
  const ranger = ensureRanger();
  const out = path.join(root, "bin", "flight_bench.js");
  compile(ranger, "PresFlightBenchMain.rgr", out, "-nodecli");
  // a CommonJS script (it reads the cases with require("fs")) in an ES module package
  const cjs = out.replace(/\.js$/, ".cjs");
  fs.renameSync(out, cjs);
  // every built-in theme whole, as the editor uses it
  const themes = path.join(root, "bin", "flight-bench-themes");
  fs.mkdirSync(themes, { recursive: true });
  for (const name of themeNames()) fs.writeFileSync(path.join(themes, `${name}.css`), themeCss(name));
  const one = process.argv.indexOf("--case");
  const extra = one > 0 && process.argv[one + 1] ? [process.argv[one + 1]] : [];
  const r = spawnSync(process.execPath, [cjs, path.join(root, "scripts", "fixtures"), "flight-bench.md", themes, ...extra], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  const text = `${r.stdout || ""}`;
  process.stdout.write(text);
  if (r.stderr) process.stderr.write(r.stderr);
  const at = process.argv.indexOf("--out");
  if (at > 0 && process.argv[at + 1]) fs.writeFileSync(process.argv[at + 1], text);
  if (r.status !== 0) process.exit(1);
  // a broken case (its 2-D slide does not show what it names) or fewer
  // features shown than scripts/fixtures/flight-bench.floor fails
  const m = /flight-bench: shown=(\d+) valid=(\d+) cases=(\d+)/.exec(text);
  if (!m) {
    log("flight bench: no summary line");
    process.exit(1);
  }
  const [shown, valid, cases] = m.slice(1).map(Number);
  const floor = Number(fs.readFileSync(path.join(root, "scripts", "fixtures", "flight-bench.floor"), "utf8").trim());
  if (valid < cases) {
    log(`flight bench: ${cases - valid} broken cases`);
    process.exit(1);
  }
  if (shown < floor) {
    log(`flight bench: ${shown} shown, fewer than the floor ${floor} (scripts/fixtures/flight-bench.floor)`);
    process.exit(1);
  }
} catch (e) {
  log(e.message);
  process.exit(1);
}
