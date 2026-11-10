// node --test: Export ▸ Sliqtly work hours' rows (web/workhours.js)
import test from "node:test";
import assert from "node:assert/strict";
import {
  monthShift, monthNow, hoursText, monthLabel, sittingsText, topicText, nextWhSort, whNote, workHoursListJson, dayDetail, workHoursCsv,
} from "../workhours.js";

const t = (s) => s;
const at = (s) => Date.parse(s + "Z");
const res = {
  git: true,
  gitOn: true,
  report: {
    month: "2026-11", zone: "UTC", minutes: 255, edits: 7, sessions: 3, gapMinutes: 30, leadMinutes: 10,
    days: [
      {
        date: "2026-11-03", minutes: 45, edits: 2,
        sessions: [{ start: at("2026-11-03T14:00:00"), end: at("2026-11-03T14:45:00") }],
        topics: [{ id: "", title: "", minutes: 45, edits: 2, decks: [{ id: "d9", name: "Loose" }] }],
      },
      {
        date: "2026-11-10", minutes: 210, edits: 5,
        sessions: [{ start: at("2026-11-10T08:50:00"), end: at("2026-11-10T11:00:00") }, { start: at("2026-11-10T13:00:00"), end: at("2026-11-10T14:20:00") }],
        topics: [
          { id: "r1", title: "Launch", minutes: 130, edits: 4, decks: [{ id: "d1", name: "Plan" }, { id: "d2", name: "Roadmap, v2" }] },
          { id: "r2", title: "Sales", minutes: 80, edits: 1, decks: [] },
          { id: "r3", title: "Made", minutes: 0, edits: 1, decks: [] },
        ],
      },
    ],
  },
};

test("months move over the year's end; this month in a zone", () => {
  assert.equal(monthShift("2026-11", 1), "2026-12");
  assert.equal(monthShift("2026-12", 1), "2027-01");
  assert.equal(monthShift("2026-01", -1), "2025-12");
  assert.equal(monthShift("bad", 1), "bad");
  assert.equal(monthNow(at("2026-11-30T23:30:00"), "UTC"), "2026-11");
  assert.equal(monthNow(at("2026-11-30T23:30:00"), "Europe/Helsinki"), "2026-12");
});

test("hours and months in words", () => {
  assert.equal(hoursText(210), "3 h 30 min");
  assert.equal(hoursText(45), "45 min");
  assert.equal(hoursText(120), "2 h");
  assert.equal(hoursText(0), "0 min");
  assert.equal(monthLabel("2026-11", "en"), "November 2026");
  assert.equal(monthLabel("2026-11", "fi"), "Marraskuu 2026");
});

test("a day's sittings and topics", () => {
  const d = res.report.days[1];
  assert.equal(sittingsText(d, "en", "UTC"), "08:50–11:00, 13:00–14:20");
  assert.equal(sittingsText(d, "en", "Europe/Helsinki"), "10:50–13:00, 15:00–16:20");
  assert.equal(topicText(d.topics[0], t), "Launch 2 h 10 min (Plan, Roadmap, v2)");
  assert.equal(topicText(res.report.days[0].topics[0], t), "No topic 45 min (Loose)");
});

test("the window's rows: a day each, topics under it, sorted as asked", () => {
  const j = JSON.parse(workHoursListJson(res, { month: "2026-11", t, lang: "en", zone: "UTC", now: at("2026-11-20T10:00:00") }));
  assert.equal(j.list, "wh");
  assert.equal(j.nav.label, "November 2026 · 4 h 15 min");
  assert.equal(j.nav.prev, true);
  assert.equal(j.nav.next, false, "no months after this one");
  assert.equal(j.download, "Download CSV");
  assert.deepEqual(j.rows.map((r) => r.id), ["2026-11-03", "2026-11-10"]);
  const r = j.rows[1];
  assert.equal(r.added, "3 h 30 min");
  assert.equal(r.modified, "08:50–14:20");
  assert.equal(r.where, "Launch 2 h 10 min (Plan, Roadmap, v2) · Sales 1 h 20 min");
  assert.equal(r.canDelete, false);
  assert.match(j.note, /^2 days, 3 sittings, 7 saves\. Estimated from/);
  const most = JSON.parse(workHoursListJson(res, { month: "2026-11", sort: nextWhSort(null, "minutes"), t, zone: "UTC", now: at("2026-12-20T10:00:00") }));
  assert.deepEqual(most.rows.map((x) => x.id), ["2026-11-10", "2026-11-03"]);
  assert.equal(most.nav.next, true);
  const early = JSON.parse(workHoursListJson(res, { month: "2026-11", sort: nextWhSort(null, "start"), t, zone: "UTC" }));
  assert.deepEqual(early.rows.map((x) => x.id), ["2026-11-10", "2026-11-03"], "08:50 before 14:00");
  assert.deepEqual(nextWhSort({ by: "minutes", dir: "desc" }, "minutes"), { by: "minutes", dir: "asc" });
  assert.deepEqual(nextWhSort({ by: "name", dir: "asc" }, "bogus"), { by: "name", dir: "asc" });
});

test("while asked: no rows (the window says Loading…); nothing to count says why", () => {
  const j = JSON.parse(workHoursListJson(null, { month: "2026-10", t, zone: "UTC" }));
  assert.equal(j.rows, undefined);
  assert.equal(j.nav.label, "October 2026");
  assert.equal(j.download, "");
  assert.match(whNote({ git: false, report: { days: [] } }, t), /keeps no Git versions/);
  assert.match(whNote({ git: true, gitOn: false, report: { days: [] } }, t), /Git is off on this server/);
  assert.equal(whNote({ error: "HTTP 500" }, t), "HTTP 500");
});

test("a row pressed tells the day in full; CSV a row per topic", () => {
  assert.equal(dayDetail(res, "2026-11-10", { t, zone: "UTC" }), "Tue, 11/10 3 h 30 min: 08:50–11:00, 13:00–14:20. Launch 2 h 10 min (Plan, Roadmap, v2) · Sales 1 h 20 min");
  assert.equal(dayDetail(res, "2026-11-11", { t }), "");
  const csv = workHoursCsv(res, { zone: "UTC" }).trim().split("\n");
  assert.equal(csv[0], "Date,Day hours,Sittings,Topic,Topic hours,Presentations,Saves");
  assert.equal(csv[1], "2026-11-03,0.75,14:00–14:45,,0.75,Loose,2");
  assert.equal(csv[2], '2026-11-10,3.50,"08:50–11:00, 13:00–14:20",Launch,2.17,"Plan; Roadmap, v2",4');
  assert.equal(csv.length, 4);
});
