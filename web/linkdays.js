// SPDX-License-Identifier: AGPL-3.0-or-later
//
// How long a viewing link works (Share, Export ▸ Cloud HTML, Export ▸ Cloud
// video). No DOM: main.js asks with these choices, web/sliqtly.js writes
// the link as { since: the server's time, days }, firestore.rules allows
// 1 to MAX_DAYS, and the server (mcp-go/rgr/Store.rgr Store.shown) shows
// nothing through a link past since + days. A link written before links
// had a term works for MAX_DAYS from its `created`.

export const MAX_DAYS = 30;
export const DAY_MS = 24 * 60 * 60 * 1000;
// the choices offered, the one picked first
export const CHOICES = [1, 7, 30];
export const DEFAULT_DAYS = 7;

// a number of days the cloud takes: a whole number from 1 to MAX_DAYS
// (anything else: DEFAULT_DAYS)
export function linkDays(v) {
  const n = Math.round(Number(v));
  if (!Number.isFinite(n) || n < 1) return DEFAULT_DAYS;
  return Math.min(n, MAX_DAYS);
}

// When the link stops working, ms: { since, days, created } as listed
// (ms; since 0 when not written yet); 0 when not known.
export function linkEnds(l) {
  if (!l) return 0;
  if (l.days > 0 && l.since > 0) return l.since + Math.min(l.days, MAX_DAYS) * DAY_MS;
  if (l.created > 0) return l.created + MAX_DAYS * DAY_MS;
  return 0;
}

export function linkExpired(l, now = Date.now()) {
  const end = linkEnds(l);
  return end > 0 && now >= end;
}

// The window's chips: "key=label" lines for PresApp.openPick. t translates.
// i18n: "1 day" "7 days" "30 days"
export function choiceLines(t) {
  return CHOICES.map((d) => d + "=" + t(d === 1 ? "1 day" : d + " days")).join("\n");
}

// "Works until 14.10.2026." (as the app writes dates, web/decklist.js);
// "" for no time
export function untilText(ms, t) {
  if (!ms) return "";
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "";
  return t("Works until {date}.").replace("{date}", `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()}`);
}
