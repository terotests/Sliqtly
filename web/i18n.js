// The interface's language.
//
// The words are written in English in the source (src/*.rgr through
// PresI18n.t, this page through t() and data-i18n attributes). For any other
// language, i18n/<code>.json maps each English string to its translation; a
// string it lacks stays English. Adding a language: a new json and a line in
// LANGS.
//
// The language is, in order: ?lang= in the address, the one chosen before
// (localStorage), the browser's first preference we have, English. Choosing
// another reloads the page, so everything is built in one language.

import { embeddedAsset } from "./player-file.js";

export const LANGS = [
  ["en", "English"],
  ["fi", "Suomi"],
];

function pick() {
  const ok = (c) => LANGS.some(([k]) => k === c);
  const q = new URLSearchParams(location.search).get("lang");
  if (q && ok(q)) return q;
  try {
    const kept = localStorage.getItem("sliqtly.lang");
    if (kept && ok(kept)) return kept;
  } catch (_) { /* no storage: the browser decides */ }
  for (const l of navigator.languages || [navigator.language || ""]) {
    const c = String(l).toLowerCase().split("-")[0];
    if (ok(c)) return c;
  }
  return "en";
}

export const lang = pick();
document.documentElement.lang = lang;

let table = {};
if (lang !== "en") {
  try {
    const url = `./i18n/${lang}.json?v=${document.querySelector('meta[name="build"]')?.content || ""}`;
    // a player file (web/player-file.js) carries its translations
    const res = (await embeddedAsset(url)) || (await fetch(url));
    if (res.ok) table = await res.json();
  } catch (e) {
    console.warn("no translation for " + lang, e);
  }
}

// What the interface calls a room: "room" or "project" (File → Settings,
// src/PresTerm.rgr). Changing it reloads the page, as the language does.
const TERM_KEY = "sliqtly.roomWord";
export const term = (() => {
  try { return localStorage.getItem(TERM_KEY) === "project" ? "project" : "room"; } catch (_) { return "room"; }
})();

export function chooseTerm(word) {
  try { localStorage.setItem(TERM_KEY, word === "project" ? "project" : "room"); } catch (_) { return false; }
  location.reload();
  return true;
}

// the app's PresI18n once it has the table (handOver): t() asks it, so the
// page's words get the same word for a room as the canvas's
let viaApp = null;

export function handOver(P) {
  P.use(lang, pairs());
  if (typeof P.useTerm === "function") {
    P.useTerm(term);
    viaApp = P;
  }
}

export function t(s) {
  if (viaApp) return viaApp.t(s);
  return Object.prototype.hasOwnProperty.call(table, s) ? table[s] : s;
}

// The table as PresI18n.use takes it: key \u0001 value, entries \u0002.
export function pairs() {
  return Object.entries(table).map(([k, v]) => k + "\u0001" + v).join("\u0002");
}

// The page's own words: data-i18n (the text), data-i18n-title (the title),
// data-i18n-aria (aria-label). The English is what index.html says.
export function translateDom(root = document) {
  if (lang === "en") return;
  for (const el of root.querySelectorAll("[data-i18n]")) el.textContent = t(el.textContent.trim());
  for (const el of root.querySelectorAll("[data-i18n-title]")) el.title = t(el.title);
  for (const el of root.querySelectorAll("[data-i18n-aria]")) el.setAttribute("aria-label", t(el.getAttribute("aria-label")));
  document.title = t(document.title);
}

export function chooseLang(code) {
  try { localStorage.setItem("sliqtly.lang", code); } catch (_) { /* this visit only */ }
  const url = new URL(location.href);
  url.searchParams.set("lang", code);
  location.replace(url);
}
