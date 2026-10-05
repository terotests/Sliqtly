// Sliqtly's name on every shared presentation.
//
// A shared presentation opens with a short intro: the logo turns once and
// the name shows large enough to read, then the slides begin. Every share
// shows it, PRO owners' too (Tero, 2026-10-03: the name is how Sliqtly is
// advertised). Only the editor, and the owner editing their own deck, go
// without.
//
// Kept apart from main.js so the rule can be tested under Node
// (web/test/brand.test.mjs).

// how long the intro plays when nobody skips it (ms); the CSS in index.html
// times its parts to this
export const INTRO_MS = 2600;

/**
 * Whether a presentation opened for showing gets the intro.
 *   from: "link"   the Markdown packed into the address (#md=…&mode=show)
 *         "share"  a cloud share (/s/{id}, or #share={id} in an assistant's preview)
 *         "file"   a presentation exported as one .html file (web/player-file.js)
 * Anything else (the editor, the owner editing their own deck) gets none.
 */
export function wantsIntro({ from } = {}) {
  return from === "link" || from === "share" || from === "file";
}
