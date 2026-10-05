// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Bitmap tracer: converts PNG images to SVG using the Ranger tracer algorithm
// (lib/evg/tools/evg_trace_cli.rgr from terotests/ranger).
//
// Exports vectorizeImage() which takes PNG blob data and tracer parameters,
// calls the tracer subprocess, and returns SVG path data.

import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");
// Path to pre-compiled tracer from ranger repo (compiled via: npm run compile:tracer)
// Or from ranger's own build
const TRACER_BIN = path.join(ROOT, "lib/evg/bin/evg_trace_cli.js");
const RANGER_TRACER = path.join(ROOT, "..", "ranger", "lib/evg/bin", "evg_trace_cli.js");

// Tracer parameters with defaults
const DEFAULT_PARAMS = {
  colorCount: 4,
  paletteMode: "quantize",
  lumaWeight: 3.0,
  snapRatio: 1.0,
  alphamax: 0.5,
  opttolerance: 0.25,
  turdsize: 2,
};

/**
 * Vectorize a PNG image using the bitmap tracer.
 * @param {Buffer|Uint8Array} imageData - PNG image bytes
 * @param {Object} params - Tracer parameters (colorCount, alphamax, opttolerance, etc.)
 * @returns {Promise<string>} SVG content
 */
export async function vectorizeImage(imageData, params = {}) {
  const config = { ...DEFAULT_PARAMS, ...params };

  // Find tracer binary
  let tracerBin = TRACER_BIN;
  if (!fs.existsSync(TRACER_BIN) && fs.existsSync(RANGER_TRACER)) {
    tracerBin = RANGER_TRACER;
  }
  if (!fs.existsSync(tracerBin)) {
    throw new Error(`Tracer not found at ${TRACER_BIN} or ${RANGER_TRACER}. Run 'npm run compile:tracer'`);
  }

  // Create temp directory and files
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "sliqtly-tracer-"));
  const inputPath = path.join(tempDir, "input.png");
  const outputPath = path.join(tempDir, "output.svg");

  try {
    // Write input image
    if (imageData instanceof Buffer) {
      fs.writeFileSync(inputPath, imageData);
    } else {
      fs.writeFileSync(inputPath, Buffer.from(imageData));
    }

    // Build tracer arguments
    const args = [
      tracerBin,
      inputPath,
      outputPath,
      "--colorCount",
      String(config.colorCount),
      "--paletteMode",
      config.paletteMode,
      "--lumaWeight",
      String(config.lumaWeight),
      "--snapRatio",
      String(config.snapRatio),
      "--alphamax",
      String(config.alphamax),
      "--opttolerance",
      String(config.opttolerance),
      "--turdsize",
      String(config.turdsize),
    ];

    // Run tracer
    const result = spawnSync("node", args, {
      cwd: ROOT,
      encoding: "utf8",
      timeout: 30000, // 30 second timeout
      maxBuffer: 10 * 1024 * 1024, // 10MB max output
    });

    if (result.error) {
      throw new Error(`Tracer failed to start: ${result.error.message}`);
    }

    if (result.status !== 0) {
      const stderr = result.stderr || "";
      const stdout = result.stdout || "";
      throw new Error(`Tracer exited with code ${result.status}: ${stderr || stdout}`);
    }

    // Read output SVG
    if (!fs.existsSync(outputPath)) {
      throw new Error("Tracer did not produce output SVG");
    }

    const svg = fs.readFileSync(outputPath, "utf8");
    return svg;
  } finally {
    // Cleanup temp files
    try {
      fs.rmSync(tempDir, { recursive: true, force: true });
    } catch (_) {
      // Ignore cleanup errors
    }
  }
}

/**
 * Extract SVG paths from traced SVG output.
 * Returns array of {fill, d} objects for each path.
 */
export function extractPaths(svg) {
  const paths = [];
  const pathRegex = /<path[^>]*d="([^"]+)"[^>]*\/>/g;
  const fillRegex = /fill="#([0-9A-Fa-f]{6})"/;

  let match;
  while ((match = pathRegex.exec(svg)) !== null) {
    const pathStr = match[0];
    const d = match[1];
    const fillMatch = fillRegex.exec(pathStr);
    const fill = fillMatch ? `#${fillMatch[1]}` : "#000000";
    paths.push({ fill, d });
  }

  return paths;
}

/**
 * Extract viewBox from traced SVG.
 */
export function extractViewBox(svg) {
  const match = /viewBox="([^"]+)"/.exec(svg);
  return match ? match[1] : "0 0 100 100";
}
