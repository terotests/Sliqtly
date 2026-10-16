// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The presenter view on another screen (P while presenting, ⋯ → Presenter
// view on another screen): the page opens itself again as the audience's
// window, `?audience=<pair>`, which shows the slide alone, and this window
// turns to the speaker view. What the two say to each other, who leads and
// how the mode ends is src/PresTwoScreen.rgr; this is the browser's part:
// the window's address, its place on the other screen (the Window
// Management API, where there is one and it is allowed), and the channel.
// Kept apart from main.js so it can be tested under Node
// (web/test/twoscreen.test.mjs).
//
// The deck goes over the channel as web/version-view.js's packet (the
// text, the theme, the files as Blobs), the same the version frame is sent.

export const AUDIENCE_PARAM = "audience";

// A new pair's name: what both windows' channel is named for, so two
// presentations in two tabs do not hear each other.
export function newPair(random = Math.random) {
  let s = "";
  const abc = "abcdefghijklmnopqrstuvwxyz0123456789";
  for (let i = 0; i < 12; i++) s += abc[Math.floor(random() * abc.length) % abc.length];
  return s;
}

// The pair this page is the audience's window of ("" when it is not one):
// asked for in the address and opened by another window of the page.
export function audiencePair(search, opened) {
  if (!opened) return "";
  const v = new URLSearchParams(search || "").get(AUDIENCE_PARAM) || "";
  return /^[a-z0-9]{1,40}$/i.test(v) ? v : "";
}

// The audience's window's address, relative to the page's base.
export function audienceSrc(pair) {
  return "./?" + AUDIENCE_PARAM + "=" + encodeURIComponent(pair);
}

export function channelName(pair) {
  return "sliqtly-two:" + pair;
}

// The screen for the slides: one that is not this window's, the largest of
// them (a projector, a second monitor); null with one screen.
export function otherScreen(details) {
  const list = Array.isArray(details?.screens) ? details.screens : [];
  const here = details?.currentScreen;
  const others = list.filter((s) => s && s !== here && !(here && s.left === here.left && s.top === here.top && s.width === here.width && s.height === here.height));
  if (!others.length) return null;
  return others.reduce((a, b) => (b.width * b.height > a.width * a.height ? b : a));
}

// window.open's features: on that screen, filling it, or (none known) a
// window of its own beside this one, to be dragged there.
export function windowFeatures(screen, fallback = { width: 960, height: 540 }) {
  if (screen) {
    const left = screen.availLeft ?? screen.left ?? 0;
    const top = screen.availTop ?? screen.top ?? 0;
    const width = screen.availWidth ?? screen.width;
    const height = screen.availHeight ?? screen.height;
    return `popup,left=${Math.round(left)},top=${Math.round(top)},width=${Math.round(width)},height=${Math.round(height)}`;
  }
  return `popup,width=${Math.round(fallback.width)},height=${Math.round(fallback.height)}`;
}

// Whether the page may read the screens without asking ("granted"), may ask
// ("prompt"), or has no such API ("none") / was refused ("denied").
async function screensAllowed(win) {
  if (typeof win.getScreenDetails !== "function") return "none";
  try {
    const p = await win.navigator.permissions.query({ name: "window-management" });
    return p.state;
  } catch (_) {
    return "prompt";
  }
}

/**
 * Open the audience's window. Where the screens may be read already, it
 * opens on the other one; where the page must ask, it opens at once (the
 * press that asked for it still counts as one) and is moved there once the
 * answer comes. → { win, placed } (`win` null: the browser blocked it);
 * `placed` a promise of whether it went to another screen.
 */
export async function openAudience(pair, { win = globalThis.window, name = "sliqtly-audience-" + pair } = {}) {
  const src = new URL(audienceSrc(pair), win.document.baseURI).href;
  const allowed = await screensAllowed(win);
  let screen = null;
  if (allowed === "granted") {
    try { screen = otherScreen(await win.getScreenDetails()); } catch (_) { screen = null; }
  }
  const w = win.open(src, name, windowFeatures(screen));
  if (!w) return { win: null, placed: Promise.resolve(false) };
  if (screen || allowed !== "prompt") return { win: w, placed: Promise.resolve(!!screen) };
  const placed = (async () => {
    try {
      const s = otherScreen(await win.getScreenDetails());
      if (!s || w.closed) return false;
      w.moveTo(s.availLeft ?? s.left, s.availTop ?? s.top);
      w.resizeTo(s.availWidth ?? s.width, s.availHeight ?? s.height);
      return true;
    } catch (_) {
      return false;
    }
  })();
  return { win: w, placed };
}

// The channel, with what arrives handed on: a string is one of the model's
// lines, anything else the deck's packet.
export function openChannel(pair, { line, packet }, Channel = globalThis.BroadcastChannel) {
  if (typeof Channel !== "function") return null;
  const ch = new Channel(channelName(pair));
  ch.onmessage = (ev) => {
    if (typeof ev.data === "string") line?.(ev.data);
    else packet?.(ev.data);
  };
  return ch;
}
