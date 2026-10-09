// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A deck's own effects, given to the painter.
//
// ```fx blocks in the Markdown and `@effect` blocks in the theme are compiled
// by the deck (src/FxLang.rgr) into the one function a painter plugin is made
// of, `vec4 fxColor(vec2 p, vec2 local)`, with the parameters and their
// defaults. This page only registers what came out: the shader text is the
// compiler's, never the deck's, so a shared deck cannot hand its viewers'
// GPU anything the language does not allow.
//
// One module for the editor, the exports and the public viewer (which gets
// the same JSON from /api/view).

import { registerSurfaceEffect } from "./gl/evg-webgl.js";

// name → what it was registered with, so an unchanged effect is not
// registered again (a registration makes the painter compile every effect
// afresh)
const registered = new Map();
// name → the seconds a still shows it at
const stills = new Map();
let lastJson = "";

/** Register the effects in `json` (FxLang.json); a no-op when it is the text
 *  last given. */
export function registerDeckEffects(json) {
  if (!json || json === lastJson) return;
  lastJson = json;
  let list = [];
  try {
    list = JSON.parse(json);
  } catch (e) {
    console.warn("deck effects", e);
    return;
  }
  for (const e of list) {
    if (!e || !e.name || !e.frag) continue;
    stills.set(e.name, typeof e.still === "number" ? e.still : 2);
    const key = e.layer + "\n" + JSON.stringify(e.params || {}) + "\n" + e.frag;
    if (registered.get(e.name) === key) continue;
    registered.set(e.name, key);
    registerSurfaceEffect({ name: e.name, layer: e.layer, params: e.params || {}, frag: e.frag });
  }
}

/** The moment a still shows a deck's effect at, or undefined for one the
 *  deck did not define. */
export function deckEffectStill(kind) {
  return stills.get(kind);
}
