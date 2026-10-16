#!/usr/bin/env node
// The Spaces page in Node: every view at desktop and phone width, the
// summary's three sections, a move on the calendar written back to the space,
// a pasted sheet in the summary, and the documents the assistant writes.
//
//   node test/app-check.mjs <ranger checkout>   (scripts/check.mjs runs it)
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const APP = path.resolve(HERE, "..");
const ranger = process.argv[2];
const require = createRequire(import.meta.url);
const M = require(path.join(APP, "bin", "SpacesApp.cjs"));
const CSS = fs.readFileSync(path.join(ranger, "gallery/evgui/theme/eventcal.css"), "utf8") + "\n"
  + fs.readFileSync(path.join(APP, "web", "spaces.css"), "utf8");

let passed = 0;
let failed = 0;
const ok = (what, cond, detail = "") => {
  if (cond) passed++;
  else {
    failed++;
    console.log(`  FAIL ${what}${detail ? ": " + detail : ""}`);
  }
};
const eq = (what, got, want) => ok(what, got === want, `got ${JSON.stringify(got)} want ${JSON.stringify(want)}`);

// Wednesday 21 October 2026, 10:00
const fresh = (lang = "fi", pack = "demo") => {
  const a = new M.SpacesApp();
  a.lang = lang;
  a.pack = pack;
  a.init(CSS);
  return a;
};
const flat = (a) => {
  const out = [];
  const walk = (el) => {
    out.push(el);
    for (let i = 0; i < el.getChildCount(); i++) walk(el.getChild(i));
  };
  a.laidOut();
  walk(a.root);
  return out;
};
const byId = (a, id) => flat(a).find((e) => e.id === id);
const texts = (a) => JSON.parse(a.displayListJson()).cmds.filter((c) => c.text).map((c) => c.text);

console.log("every view lays out");
{
  const a = fresh();
  eq("no stylesheet errors", a.styleErrorCount(), 0);
  for (const w of [1280, 390]) {
    for (const t of ["home", "calendar", "sheets", "docs", "assistant"]) {
      a.setWidth(w, 700);
      a.press("sp-nav-" + t);
      if (t === "assistant") ["qToday", "qWeek", "qNext", "qLate", "qPlan"].forEach((q) => a.press("sp-q-" + q));
      const list = JSON.parse(a.displayListJson());
      ok(`${w} ${t}: draws`, list.cmds.length > 20);
      const probs = a.a11yProblems();
      ok(`${w} ${t}: the accessible tree lints clean`, probs.length === 0, probs.join(" | "));
      const main = byId(a, "sp-main");
      ok(`${w} ${t}: the main column fits the page`, main.calculatedX + main.calculatedWidth <= w + 0.5,
        `${main.calculatedX} + ${main.calculatedWidth} > ${w}`);
    }
  }
}

console.log("the summary");
{
  const a = fresh("fi", "family");
  a.setWidth(1280, 700);
  const t = texts(a);
  for (const h of ["Tänään", "Myöhemmin tällä viikolla", "Ensi viikolla"]) ok(`the section ${h}`, t.includes(h), t.slice(0, 30).join(","));
  ok("today's dentist", t.includes("Hammaslääkäri"));
  ok("a sheet row later this week", t.includes("Treenikyyti"));
  ok("an overdue deadline", t.some((s) => s.startsWith("Palauta kirjaston kirjat")));
  const today = byId(a, "sp-card-today");
  const week = byId(a, "sp-card-week");
  const next = byId(a, "sp-card-next");
  ok("three columns side by side at 1280", today.calculatedY === week.calculatedY && week.calculatedY === next.calculatedY
    && today.calculatedX < week.calculatedX && week.calculatedX < next.calculatedX);
  a.setWidth(390, 700);
  a.laidOut();
  const t2 = byId(a, "sp-card-today");
  const w2 = byId(a, "sp-card-week");
  ok("stacked at 390", w2.calculatedY > t2.calculatedY && Math.abs(w2.calculatedX - t2.calculatedX) < 0.5);
}

console.log("the words switch");
{
  const a = fresh("en", "demo");
  a.setWidth(1280, 700);
  a.press("sp-pack-sports");
  eq("the pack", a.pack, "sports");
  const t = texts(a);
  ok("sports words", t.some((s) => s.startsWith("Teams")), t.filter((s) => /·/.test(s)).join(","));
  ok("sports data", t.includes("Coaches' meeting"));
  a.press("sp-lang-fi");
  ok("in Finnish", texts(a).includes("Valmentajapalaveri"));
  ok("calendar in Finnish", a.board.lang === "fi" && a.board.weekStart === 1);
}

console.log("the calendar writes back");
{
  const a = fresh("en", "demo");
  a.setWidth(1280, 700);
  a.press("sp-nav-calendar");
  a.displayListJson();
  const lunch = a.space.events.find((e) => e.title === "Lunch with Kim");
  const k = a.occIds.indexOf(lunch.id);
  ok("the event is on the card", k >= 0);
  const ce = a.board.model.events.find((e) => e.id === a.boardIds[k]);
  ce.startDay += 1;
  ce.startMin += 60;
  ce.endMin += 60;
  ok("a move is written back", a.syncBack());
  eq("to the space's event", lunch.day, a.today + 1);
  eq("and its time", lunch.startMin, 690 + 60);
  ok("the card is filled again", a.board.model.events.length === a.occIds.length);

  const wk = a.space.events.find((e) => e.rule.freq === "weekly");
  const occ = a.occIds.findIndex((id) => id.startsWith(wk.id + "@"));
  const ce2 = a.board.model.events.find((e) => e.id === a.boardIds[occ]);
  const before = a.space.events.length;
  ce2.startMin += 30;
  ce2.endMin += 30;
  a.syncBack();
  eq("one day of a repeating event becomes its own event", a.space.events.length, before + 1);
  ok("and is skipped in the series", wk.exdates.length === 1);

  const row = a.occIds.findIndex((id) => id.startsWith("s1-r"));
  const ce3 = a.board.model.events.find((e) => e.id === a.boardIds[row]);
  ce3.startDay += 2;
  a.syncBack();
  ok("a sheet row is not moved", a.status.includes("sheet"), a.status);
  const back = a.board.model.events.find((e) => e.id === a.boardIds[row]);
  eq("and goes back", back.startDay, a.occDay[row]);

  a.board.model.addEvent("n9", "New one", a.today + 2, 600, 660, "b", "blue");
  a.syncBack();
  ok("a new event on the card is the space's", a.space.events.some((e) => e.title === "New one" && e.resource === "b"));
}

console.log("sheets");
{
  const a = fresh("fi", "business");
  a.setWidth(1280, 700);
  a.press("sp-import");
  eq("import asks the host", a.takeAction(), "import");
  const mon = a.today - 2;
  const iso = (d) => { const p = [0, 0, 0]; M.UiDate.fromDays(d, p); return `${p[2]}.${p[1]}.${p[0]}`; };
  ok("pasted cells", a.importSheet("Asiakaskäynnit", `Päivä\tAika\tMitä\tKuka\n${iso(a.today)}\t15-16\tKäynti asiakkaalla\tMyynti\n`));
  eq("the sheets view opens on it", a.tab, "sheets");
  a.press("sp-nav-home");
  ok("its row is in today's summary", texts(a).includes("Käynti asiakkaalla"));
  const id = a.space.sheets[a.space.sheets.length - 1].id;
  a.press("sp-export-" + id);
  eq("export asks the host", a.takeAction(), "export:" + id);
  ok("as CSV", a.sheetCsv(id).startsWith("Päivä,Aika,Mitä,Kuka\n"));
  ok("an empty paste is refused", !a.importSheet("x", ""));
}

console.log("documents and the assistant");
{
  const a = fresh("fi", "family");
  a.setWidth(1280, 700);
  const n = a.space.docs.length;
  a.press("sp-write-plan");
  eq("a plan is written", a.space.docs.length, n + 1);
  eq("and shown", a.tab, "docs");
  ok("its title", texts(a).some((s) => s.startsWith("Ensi viikon suunnitelma")));
  a.press("sp-nav-assistant");
  a.press("sp-q-qLate");
  ok("the assistant answers from the data", texts(a).some((s) => s.includes("Palauta kirjaston kirjat")));
}

console.log(`passed=${passed} failed=${failed}`);
if (failed > 0) process.exit(1);
console.log("ALL PASS");
