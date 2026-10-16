/**
 * Shared by the Spaces scripts. Spaces is built with Sliqtly's own pipeline
 * (../../scripts/lib.mjs): the same Ranger checkout and EVGUI, with
 * apps/spaces/src linked into the checkout as gallery/spaces, beside
 * gallery/evgui, so `../evgui/src/…` imports resolve as Sliqtly's do.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { ensureRanger, compile, link } from "../../../scripts/lib.mjs";

export const appDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const SPACES_LINK = "gallery/spaces";

/** The Ranger checkout, with apps/spaces/src linked in. */
export function spacesRanger() {
  const ranger = ensureRanger();
  link(path.join(appDir, "src"), path.join(ranger, SPACES_LINK));
  return ranger;
}

export function compileSpaces(ranger, file, out, flag = "") {
  compile(ranger, file, out, flag, SPACES_LINK);
}

/** The calendar card's stylesheet (EVGUI) and the page's, as one sheet. */
export function spacesCss(ranger) {
  const card = fs.readFileSync(path.join(ranger, "gallery/evgui/theme/eventcal.css"), "utf8");
  const own = fs.readFileSync(path.join(appDir, "web", "spaces.css"), "utf8");
  return card + "\n" + own;
}
