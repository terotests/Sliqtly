#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// Starts the binary `npm run native` built, without building:
//
//   node scripts/start-editor-native.mjs [args…]

import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { ROOT } from "./ranger.mjs";

const bin = path.join(ROOT, "native", "build-editor", "sliqtly-native");
if (!fs.existsSync(bin)) {
  console.error("no native/build-editor/sliqtly-native: run `npm run native` first");
  process.exit(1);
}
process.exit(spawnSync(bin, process.argv.slice(2), { stdio: "inherit" }).status ?? 1);
