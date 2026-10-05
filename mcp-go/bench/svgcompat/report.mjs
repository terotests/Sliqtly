// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The benchmark's results.json as a table: each chapter of the suite, how
// many of its tests the preview draws as Chromium does.
//
//   node report.mjs results-w3c-svg11.json
import fs from "node:fs";

const results = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const classes = ["same", "close", "differs", "player-shows-nothing"];
const by = new Map();
for (const r of results) {
  if (!by.has(r.chapter)) by.set(r.chapter, Object.fromEntries(classes.map((c) => [c, 0])));
  by.get(r.chapter)[r.class]++;
}
const total = Object.fromEntries(classes.map((c) => [c, results.filter((r) => r.class === c).length]));
const pct = (n) => `${((100 * n) / results.length).toFixed(1)} %`;
const lines = ["| chapter | tests | same | close | differs | player shows nothing |", "| --- | ---: | ---: | ---: | ---: | ---: |"];
for (const [ch, c] of [...by].sort()) {
  const n = classes.reduce((s, k) => s + c[k], 0);
  lines.push(`| ${ch} | ${n} | ${c.same} | ${c.close} | ${c.differs} | ${c["player-shows-nothing"]} |`);
}
lines.push(`| **all** | **${results.length}** | **${total.same}** (${pct(total.same)}) | **${total.close}** (${pct(total.close)}) | **${total.differs}** (${pct(total.differs)}) | **${total["player-shows-nothing"]}** |`);
const ms = results.map((r) => r.ms).sort((a, b) => a - b);
lines.push("", `Preview drawing time at 480×360: median ${ms[ms.length >> 1].toFixed(0)} ms, 90th percentile ${ms[Math.floor(ms.length * 0.9)].toFixed(0)} ms, slowest ${ms[ms.length - 1].toFixed(0)} ms.`);
lines.push("", "Differs:", "", ...results.filter((r) => r.class === "differs").map((r) => `- ${r.name} (${r.diff_percent.toFixed(1)} %)`));
console.log(lines.join("\n"));
