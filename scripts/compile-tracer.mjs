#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later

import { execSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import fs from "node:fs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");

// Path to Ranger repo with the tracer source
const RANGER_REPO = path.resolve(ROOT, "..", "ranger");
const TRACER_SRC = path.join(RANGER_REPO, "lib/evg/tools/evg_trace_cli.rgr");
const OUTPUT_DIR = path.join(ROOT, "lib/evg/bin");

if (!fs.existsSync(TRACER_SRC)) {
  console.error(`Error: Tracer source not found at ${TRACER_SRC}`);
  console.error("Make sure terotests/ranger is cloned alongside sliqtly");
  process.exit(1);
}

console.log("Compiling bitmap tracer (lib/evg/tools/evg_trace_cli.rgr)...");

try {
  // Compile using Ranger's rgrc compiler
  const cmd = [
    "node",
    path.join(RANGER_REPO, "dist/rgrc.js"),
    "-es6",
    TRACER_SRC,
    `-d=${OUTPUT_DIR}`,
    "-o=evg_trace_cli.js",
    "-nodecli",
  ];

  const env = {
    ...process.env,
    RANGER_LIB: `${path.join(RANGER_REPO, "compiler/Lang.rgr")}:${path.join(RANGER_REPO, "lib/stdops.rgr")}`,
  };

  const output = execSync(cmd.join(" "), {
    cwd: RANGER_REPO,
    env,
    encoding: "utf8",
  });

  if (output.includes("[FAIL]") || output.includes("Compilation FAILED")) {
    console.error("Compilation failed:");
    console.error(output);
    process.exit(1);
  }

  console.log(`✓ Tracer compiled to ${path.join(OUTPUT_DIR, "evg_trace_cli.js")}`);
  console.log(output);
} catch (err) {
  console.error("Compilation error:", err.message);
  process.exit(1);
}
