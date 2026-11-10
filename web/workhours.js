// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Export ▸ Sliqtly work hours (a server of one's own, turned on in
// Settings): a month's work hours, estimated by the server from when the
// presentations were saved (mcp-go/workhours.go), one row a day with the
// topics worked on. No DOM: main.js asks the server (window.sliqtly
// .workHours), this makes the rows, PresPanels draws them in the
// presentations window (list "wh") with ‹ month › above them.

export const WH_SORTS = ["name", "minutes", "start"];

// "2026-11" moved by n months
export function monthShift(month, n) {
  const m = /^(\d{4})-(\d{2})$/.exec(month || "");
  if (!m) return month;
  const i = Number(m[1]) * 12 + Number(m[2]) - 1 + n;
  return `${Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, "0")}`;
}

// this month where the user is ("2026-11")
export function monthNow(now = Date.now(), zone) {
  const parts = new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", timeZone: zone }).formatToParts(new Date(now));
  const get = (k) => parts.find((p) => p.type === k)?.value || "";
  return `${get("year")}-${get("month")}`;
}

// 210 → "3 h 30 min", 45 → "45 min", 120 → "2 h"
export function hoursText(minutes) {
  const m = Math.max(0, Math.round(minutes || 0));
  const h = Math.floor(m / 60);
  const r = m % 60;
  if (!h) return `${r} min`;
  return r ? `${h} h ${r} min` : `${h} h`;
}

// "November 2026" in the user's language
export function monthLabel(month, lang = "en") {
  const m = /^(\d{4})-(\d{2})$/.exec(month || "");
  if (!m) return month || "";
  const s = new Intl.DateTimeFormat(lang, { month: "long", year: "numeric", timeZone: "UTC" }).format(Date.UTC(Number(m[1]), Number(m[2]) - 1, 15));
  return s.charAt(0).toUpperCase() + s.slice(1);
}

const clock = (ms, lang, zone) => new Intl.DateTimeFormat(lang, { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: zone }).format(new Date(ms));

// "Tue 10/11" in the user's language, for a day "2026-11-10"
function dayText(date, lang) {
  const [y, mo, d] = date.split("-").map(Number);
  return new Intl.DateTimeFormat(lang, { weekday: "short", day: "numeric", month: "numeric", timeZone: "UTC" }).format(Date.UTC(y, mo - 1, d, 12));
}

// a day's sittings as "09:10–12:40, 13:30–15:00"
export function sittingsText(day, lang = "en", zone = "UTC") {
  return (day.sessions || []).map((s) => clock(s.start, lang, zone) + "–" + clock(s.end, lang, zone)).join(", ");
}

// a topic as "Launch 2 h 10 min (Plan, Roadmap)"; a deck in no topic is
// under "No topic"
// i18n: "No topic"
export function topicText(tp, t) {
  const decks = (tp.decks || []).map((d) => d.name || d.id).filter(Boolean);
  return `${tp.title || t("No topic")} ${hoursText(tp.minutes)}` + (decks.length ? ` (${decks.join(", ")})` : "");
}

// a day's topics worth naming: a topic only touched in passing (made, or
// its folders moved, with no time and no presentation) is left out
const shownTopics = (d) => (d.topics || []).filter((tp) => tp.minutes > 0 || tp.decks?.length);

export function nextWhSort(cur, by) {
  if (!WH_SORTS.includes(by)) return cur;
  if (cur && cur.by === by) return { by, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { by, dir: by === "minutes" ? "desc" : "asc" };
}

function sortDays(days, by, dir, zone) {
  const sign = dir === "desc" ? -1 : 1;
  // the time of day the day's work started, where the user is
  const start = (d) => (d.sessions?.length ? clock(d.sessions[0].start, "en-GB", zone) : "");
  const key = by === "minutes" ? (a, b) => a.minutes - b.minutes : by === "start" ? (a, b) => start(a).localeCompare(start(b)) : (a, b) => a.date.localeCompare(b.date);
  return days.slice().sort((a, b) => sign * key(a, b) || a.date.localeCompare(b.date));
}

// The note under the rows: the month's sum and how it is reckoned, or why
// there is nothing to count.
// i18n: "{n} days, {s} sittings, {e} saves." "Estimated from the server's Git versions: a pause over {gap} min starts a new sitting, and a sitting counts {lead} min before its first save. Every save on this server counts, whoever made it."
// i18n: "This server keeps no Git versions of its presentations, so there is nothing to count. Turn on Git in the server's settings (/settings), and from then on each pause in editing is kept with its time."
// i18n: "Git is off on this server: saves after it was turned off are not counted."
export function whNote(res, t) {
  if (!res) return "";
  if (res.error) return res.error;
  if (!res.git) return t("This server keeps no Git versions of its presentations, so there is nothing to count. Turn on Git in the server's settings (/settings), and from then on each pause in editing is kept with its time.");
  const r = res.report || {};
  const parts = [];
  if ((r.days || []).length) {
    parts.push(t("{n} days, {s} sittings, {e} saves.").replace("{n}", String(r.days.length)).replace("{s}", String(r.sessions || 0)).replace("{e}", String(r.edits || 0)));
  }
  parts.push(t("Estimated from the server's Git versions: a pause over {gap} min starts a new sitting, and a sitting counts {lead} min before its first save. Every save on this server counts, whoever made it.").replace("{gap}", String(r.gapMinutes || 30)).replace("{lead}", String(r.leadMinutes || 10)));
  if (!res.gitOn) parts.push(t("Git is off on this server: saves after it was turned off are not counted."));
  return parts.join(" ");
}

// What the presentations window is sent (PresPanels decksDialog, list
// "wh"): a day a row, its topics under the day, its hours and its first to
// last minute in the columns; "nav" the month with ‹ › beside it, and the
// month's sum. res is the server's answer ({git, gitOn, report}) or null
// while it is asked; month what is asked.
// i18n: "Sliqtly work hours" "Hours" "Time" "Day" "Download CSV" "No saves in this month." "Loading…"
export function workHoursListJson(res, { month, sort = { by: "name", dir: "asc" }, t, lang = "en", zone = "UTC", now = Date.now() } = {}) {
  const r = res?.report || null;
  const key = WH_SORTS.includes(sort?.by) ? sort.by : "name";
  const dir = sort?.dir === "desc" ? "desc" : "asc";
  const sum = r ? " · " + hoursText(r.minutes) : "";
  const out = {
    list: "wh",
    title: t("Sliqtly work hours"),
    nameLabel: t("Day"),
    head1: "minutes",
    head1Label: t("Hours"),
    head2: "start",
    head2Label: t("Time"),
    empty: t("No saves in this month."),
    nav: { label: monthLabel(month, lang) + sum, prev: true, next: month < monthNow(now, zone) },
    download: r && r.days?.length ? t("Download CSV") : "",
    sort: key,
    dir,
  };
  if (res) {
    out.note = whNote(res, t);
    out.rows = sortDays(r?.days || [], key, dir, zone).map((d) => {
      const first = d.sessions?.[0];
      const last = d.sessions?.[d.sessions.length - 1];
      return {
        id: d.date,
        name: dayText(d.date, lang),
        where: shownTopics(d).map((tp) => topicText(tp, t)).join(" · "),
        added: hoursText(d.minutes),
        modified: first ? clock(first.start, lang, zone) + "–" + clock(last.end, lang, zone) : "",
        canDelete: false,
      };
    });
  }
  return JSON.stringify(out);
}

// A row pressed: the day in full, for a toast
export function dayDetail(res, date, { t, lang = "en", zone = "UTC" } = {}) {
  const d = (res?.report?.days || []).find((x) => x.date === date);
  if (!d) return "";
  return `${dayText(d.date, lang)} ${hoursText(d.minutes)}: ${sittingsText(d, lang, zone)}. ` + shownTopics(d).map((tp) => topicText(tp, t)).join(" · ");
}

const csvCell = (v) => {
  const s = String(v ?? "");
  return /[",\n;]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};

// The month as CSV, a row for each topic of each day, hours as decimals
// (for a timesheet)
export function workHoursCsv(res, { lang = "en", zone = "UTC" } = {}) {
  const head = ["Date", "Day hours", "Sittings", "Topic", "Topic hours", "Presentations", "Saves"];
  const rows = [head];
  const h = (m) => (Math.round((m / 60) * 100) / 100).toFixed(2);
  for (const d of res?.report?.days || []) {
    const sit = sittingsText(d, lang, zone);
    const topics = shownTopics(d).length ? shownTopics(d) : [{ title: "", minutes: d.minutes, decks: [], edits: d.edits }];
    for (const tp of topics) {
      rows.push([d.date, h(d.minutes), sit, tp.title || "", h(tp.minutes), (tp.decks || []).map((x) => x.name || x.id).join("; "), tp.edits || 0]);
    }
  }
  return rows.map((r) => r.map(csvCell).join(",")).join("\n") + "\n";
}
