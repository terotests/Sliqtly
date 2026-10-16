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

/** The entry types the page and the server share (list_entry_types). */
export const TYPES_JSON = path.resolve(appDir, "..", "..", "mcp-go", "assets", "warden-types.json");

/**
 * src/SpacesTypesData.rgr: the entry types' JSON as a Ranger string, so the
 * page is built with the same schema the server checks entries against.
 * Written only when it changed.
 */
export function writeTypesData() {
  const json = JSON.stringify(JSON.parse(fs.readFileSync(TYPES_JSON, "utf8")));
  const lit = json.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  const src = "; SPDX-License-Identifier: AGPL-3.0-or-later\n"
    + "; Written by apps/spaces/scripts/lib.mjs from mcp-go/assets/warden-types.json. Do not edit.\n\n"
    + "class SpTypesData {\n    sfn json:string () {\n        return \"" + lit + "\"\n    }\n}\n";
  const out = path.join(appDir, "src", "SpacesTypesData.rgr");
  if (!fs.existsSync(out) || fs.readFileSync(out, "utf8") !== src) fs.writeFileSync(out, src);
}

/** The Ranger checkout, with apps/spaces/src linked in (and its types written). */
export function spacesRanger() {
  writeTypesData();
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
