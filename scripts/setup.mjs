#!/usr/bin/env node
/** npm run setup [-- --update]: find or clone Ranger, link src/ into it. */
import { ensureRanger, log } from "./lib.mjs";

try {
  const ranger = ensureRanger({ update: process.argv.includes("--update") });
  log(`ready  Ranger ${ranger}`);
} catch (e) {
  log(`setup failed: ${e.message}`);
  process.exit(1);
}
