// SPDX-License-Identifier: AGPL-3.0-or-later
// What a press on the canvas means by its button and keys.
//
// On a Mac, Control + click is the secondary click: the browser fires
// `contextmenu` for it as for the right button, with `button` 0 and
// `ctrlKey` set on the pointerdown before it. Taken as a press it picked or
// let go the slide under it on the strip (Ctrl/⌘ + click toggles a slide in
// the pick) just as its menu opened. There ⌘ is the key that picks.

// The press opens the context menu rather than pressing.
export function secondaryPress(ev, mac) {
  return ev.button === 2 || (mac && ev.button === 0 && ev.ctrlKey && !ev.metaKey);
}

// The key held to add a slide to the pick or take it out: ⌘ on a Mac, Ctrl
// (or the system key) elsewhere.
export function pickKeyHeld(ev, mac) {
  return mac ? !!ev.metaKey : !!(ev.ctrlKey || ev.metaKey);
}
