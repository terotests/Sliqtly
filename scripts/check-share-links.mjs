#!/usr/bin/env node
/**
 * npm run check:links: fails when a tracked file links to a real shared deck
 * on the hosted site (https://sliqtly.com/s/<id>, sliqtly.web.app too).
 * A share id opens someone's deck to anyone who has it, so the repository
 * carries none; describe the deck or keep its source next to the text
 * instead. Made-up ids used by the tests are listed in ALLOWED.
 */
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { root, log } from "./lib.mjs";

const ALLOWED = new Set(["AbCdEf1234"]);
const LINK = /\b(?:www\.)?sliqtly\.(?:com|web\.app|firebaseapp\.com)\/(?:editor\/)?s\/([A-Za-z0-9_-]{4,})/g;

const files = execFileSync("git", ["ls-files", "-z"], { cwd: root, encoding: "utf8" })
  .split("\0")
  .filter(Boolean);
const found = [];
for (const file of files) {
  let text;
  try {
    const buf = fs.readFileSync(path.join(root, file));
    if (buf.includes(0)) continue; // binary
    text = buf.toString("utf8");
  } catch {
    continue; // deleted in the working tree, a submodule
  }
  if (!text.includes("sliqtly.")) continue;
  text.split("\n").forEach((line, i) => {
    for (const m of line.matchAll(LINK)) {
      if (!ALLOWED.has(m[1])) found.push(`${file}:${i + 1}: ${m[0]}`);
    }
  });
}
if (found.length) {
  log(`links to shared decks (remove them, or add a made-up test id to ALLOWED in scripts/check-share-links.mjs):`);
  for (const f of found) log(`  ${f}`);
  process.exit(1);
}
log(`no share links in ${files.length} tracked files`);
