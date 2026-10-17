#!/usr/bin/env node
// The Spaces page in Node: every view at desktop and phone width, the
// summary's three sections, a move on the calendar written back to the space,
// a pasted sheet in the summary, the documents the assistant writes, and the
// same app on a server (Warden), its calls answered here.
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
    for (const t of ["home", "calendar", "lists", "budget", "sheets", "docs", "assistant"]) {
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
  let t = texts(a);
  for (const h of ["Tänään", "Tämä viikko", "Tulossa", "Muista"]) ok(`the section ${h}`, t.includes(h), t.slice(0, 30).join(","));
  ok("the clock", t.includes("10.00"));
  ok("the day and the week", t.includes("keskiviikko 21. lokakuuta") && t.includes("viikko 43 · Perhe"), t.slice(0, 30).join(","));
  ok("today's dentist", t.includes("Hammaslääkäri"));
  ok("the next plan shows who looks after it", t.includes("14.00–14.30 · Vie: Juho"), t.filter((s) => /·/.test(s)).join(","));
  ok("a sheet row this week", t.includes("Treenikyyti"));
  ok("an overdue deadline", t.includes("Palauta kirjaston kirjat") && t.some((s) => s.startsWith("myöhässä")));
  ok("the wards' colours", t.includes("Nappi") && t.includes("Koko perhe"));
  ok("the Add button", t.includes("+ Lisää meno"));
  ok("no weather until the host has it", !byId(a, "sp-wx"));
  a.setWeather("Tampere", 7, 9, 4, 20, 2);
  ok("the weather", texts(a).includes("7°") && texts(a).includes("Tampere · ylin 9° alin 4°"));
  const today = byId(a, "sp-card-today");
  const week = byId(a, "sp-card-week");
  const late = byId(a, "sp-card-late");
  ok("three columns side by side at 1280", today.calculatedY === week.calculatedY && week.calculatedY === late.calculatedY
    && today.calculatedX < week.calculatedX && week.calculatedX < late.calculatedX);
  // a ticked deadline is done
  const books = a.space.deadlines.find((d) => d.title === "Palauta kirjaston kirjat");
  a.press("sp-done-" + books.id);
  ok("ticked off", books.done && !texts(a).includes("Palauta kirjaston kirjat"));
  // the minute moves on
  a.setNow(11 * 60 + 5);
  ok("the clock follows", texts(a).includes("11.05"));
  // a day of the week opens in the calendar
  a.press("sp-day-" + (a.today + 1));
  ok("a day opens in the calendar", a.tab === "calendar" && a.board.view === "day" && a.board.cursor === a.today + 1);
  a.press("sp-nav-home");
  // the week in columns, and what is coming with days to go
  a.press("sp-htab-week");
  t = texts(a);
  ok("Viikko: the days as columns", !!byId(a, "sp-card-weekboard") && t.includes("ma 19.") && t.includes("su 25."), t.slice(0, 60).join(","));
  const mon = byId(a, "sp-day-" + (a.today - 2));
  const tue = byId(a, "sp-day-" + (a.today - 1));
  ok("side by side at 1280", mon.calculatedY === tue.calculatedY && mon.calculatedX < tue.calculatedX);
  ok("with times", t.includes("Hammaslääkäri") && t.includes("14.00"));
  a.press("sp-htab-ahead");
  t = texts(a);
  ok("Tulossa: days to go", t.includes("huomenna") && t.some((s) => / pv$/.test(s)), t.slice(30, 80).join(","));
  ok("a plan with no ward is the whole family's", t.includes("koko perhe"));
  for (const k of ["today", "week", "ahead"]) {
    a.press("sp-htab-" + k);
    for (const w of [1280, 390]) {
      a.setWidth(w, 700);
      const probs = a.a11yProblems();
      ok(`${w} ${k}: lints clean`, probs.length === 0, probs.join(" | "));
      const main = byId(a, "sp-main");
      ok(`${w} ${k}: fits`, main.calculatedX + main.calculatedWidth <= w + 0.5);
    }
  }
  a.press("sp-htab-today");
  a.setWidth(1280, 700);
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
  ok("sports words", t.includes("Whole club") && t.includes("+ Add session"), t.slice(0, 80).join(","));
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
  const lunch = a.space.events.find((e) => e.title === "Vet");
  const k = a.occIds.indexOf(lunch.id);
  ok("the event is on the card", k >= 0);
  const ce = a.board.model.events.find((e) => e.id === a.boardIds[k]);
  ce.startDay += 1;
  ce.startMin += 60;
  ce.endMin += 60;
  ok("a move is written back", a.syncBack());
  eq("to the space's event", lunch.day, a.today + 1);
  eq("and its time", lunch.startMin, 690 + 60);
  ok("and it is marked to save", lunch.dirty);
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

  a.board.model.addEvent("n9", "New one", a.today + 2, 600, 660, "otso", "blue");
  a.syncBack();
  ok("a new event on the card is the space's", a.space.events.some((e) => e.title === "New one" && e.resource === "otso"));
  eq("without a server nothing is sent", a.outCount(), 0);
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

console.log("a space on a server");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "en";
  a.init(CSS);
  a.setWidth(1280, 700);
  eq("an empty space until the server answers", a.space.events.length, 0);
  a.start();
  eq("the first call", a.outOp(), "list_spaces");
  const me = { uid: "u1", name: "Mia", email: "mia@example.com" };
  const spaceOf = (extra = {}) => ({
    space_id: "sp1", name: "Home crew", vocab: "family", lang: "en", role: "owner",
    members: [{ uid: "u1", name: "Mia", email: "mia@example.com", role: "owner" }, { uid: "u2", name: "Juho", email: "juho@example.com", role: "member" }],
    invites: [], wards: [{ id: "w1", name: "Aino", color: "rose" }], ...extra,
  });
  const iso = (d) => M.UiDate.isoOfDays(d);
  a.answer(JSON.stringify({ me, spaces: [{ space_id: "sp1", name: "Home crew", vocab: "family", lang: "en", role: "owner" }] }));
  eq("then the space", a.outOp(), "get_space");
  eq("by its id", JSON.parse(a.outBody()).space_id, "sp1");
  a.answer(JSON.stringify({
    space: spaceOf(),
    events: [
      { event_id: "ev1", title: "Dentist", date: iso(a.today), start: "14:00", end: "14:30", ward: "w1", who: [{ name: "Juho", role: "takes" }], important: true },
      { event_id: "ev2", title: "Swimming", date: iso(a.today - 14), start: "17:00", end: "17:45", ward: "w1", rrule: "FREQ=WEEKLY;BYDAY=" + ["SU", "MO", "TU", "WE", "TH", "FR", "SA"][M.UiDate.weekday(a.today + 1)] },
    ],
  }));
  eq("nothing left to send", a.outCount(), 0);
  eq("the space's words", a.pack, "family");
  let t = texts(a);
  ok("the summary shows it", t.includes("★ Dentist") && t.includes("Swimming"), t.slice(0, 40).join(","));
  ok("with the ward and who", t.includes("Aino") && t.includes("14:00–14:30 · Takes: Juho"), t.filter((s) => /·/.test(s)).join(","));
  ok("an important event is marked", t.includes("★ Dentist"));
  ok("the brand", t.includes("Warden"));

  // a move on the calendar is saved
  a.press("sp-nav-calendar");
  a.displayListJson();
  const k = a.occIds.indexOf(a.space.events[0].id);
  const ce = a.board.model.events.find((e) => e.id === a.boardIds[k]);
  ce.startMin += 60;
  ce.endMin += 60;
  a.syncBack();
  eq("a move is a save_event", a.outOp(), "save_event");
  let body = JSON.parse(a.outBody());
  ok("of that event, at its new time", body.event_id === "ev1" && body.start === "15:00" && body.space_id === "sp1", JSON.stringify(body));
  a.answer(JSON.stringify({ ...body, event_id: "ev1" }));
  ok("answered, it is saved", !a.space.events[0].dirty);

  // one day of the series: the series and a new event
  const occ = a.occIds.findIndex((id) => id.startsWith(a.space.events[1].id + "@"));
  const ce2 = a.board.model.events.find((e) => e.id === a.boardIds[occ]);
  ce2.startMin += 30;
  ce2.endMin += 30;
  a.syncBack();
  eq("two saves", a.outCount(), 2);
  body = JSON.parse(a.outBody());
  ok("the series with its exdate", body.event_id === "ev2" && body.exdates.length === 1, JSON.stringify(body));
  a.answer(JSON.stringify({ ...body }));
  body = JSON.parse(a.outBody());
  ok("and the day as a new event", body.event_id === "" && body.start === "17:30" && !body.rrule, JSON.stringify(body));
  const made = a.space.events[2];
  made.startMin += 15;
  made.touch();
  a.answer(JSON.stringify({ ...body, event_id: "ev3" }));
  eq("the new event takes the server's id", made.remoteId, "ev3");
  ok("changed while it was saved, it goes again", made.dirty && a.outOp() === "save_event" && JSON.parse(a.outBody()).event_id === "ev3");
  a.answer(JSON.stringify({ event_id: "ev3" }));
  eq("and then nothing is left", a.outCount(), 0);

  // a person's email: the tooltip on the budget's filter and the members
  // page, and under the filter once a person is picked
  a.press("sp-nav-budget");
  const juho = a.space.payers().indexOf("Juho");
  const centre = (id) => { const e = byId(a, id); return [e.calculatedX + e.calculatedWidth / 2, e.calculatedY + e.calculatedHeight / 2]; };
  eq("the filter's tooltip is the email", a.tipAt(...centre("sp-bud-payer-" + juho)), "juho@example.com");
  a.press("sp-bud-payer-" + juho);
  ok("the picked person's email is shown", texts(a).includes("Juho · juho@example.com"));
  a.press("sp-bud-all");
  ok("and not for everyone", !texts(a).some((s) => s.includes("juho@example.com")));
  while (a.outOp() === "budget") a.answer(JSON.stringify({ months: [] }));
  a.press("sp-nav-members");
  eq("a member's tooltip is the email", a.tipAt(...centre("sp-member-1")), "juho@example.com");

  // members: invite with the text field, typed key by key
  a.press("sp-nav-members");
  t = texts(a);
  ok("the members", t.includes("Mia") && t.includes("Juho") && t.includes("owner"));
  a.press("sp-invite-email");
  eq("the field has the focus", a.focusedField(), "sp-invite-email");
  for (const ch of "leena@example.com") a.keyWith(ch, false, false);
  a.keyWith("Enter", false, false);
  eq("Enter invites", a.outOp(), "invite_member");
  eq("that address", JSON.parse(a.outBody()).email, "leena@example.com");
  a.answer(JSON.stringify(spaceOf({ invites: ["leena@example.com"] })));
  ok("the invite is listed", texts(a).includes("leena@example.com"));
  eq("and the field is empty again", a.inviteField.value, "");
  a.press("sp-uninvite-0");
  eq("an invite is taken back", JSON.parse(a.outBody()).member, "leena@example.com");
  a.failed("not allowed");
  eq("an error is shown", a.status, "not allowed");
  a.press("sp-remove-1");
  eq("a member is removed by uid", JSON.parse(a.outBody()).member, "u2");
  a.answer(JSON.stringify(spaceOf({ members: [{ uid: "u1", name: "Mia", email: "mia@example.com", role: "owner" }] })));
  eq("one member left", a.space.members.length, 1);
  eq("and the events are kept", a.space.events.length, 3);
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    for (const v of ["members", "spaces"]) {
      a.press("sp-nav-" + v);
      const probs = a.a11yProblems();
      ok(`${w} ${v}: the accessible tree lints clean`, probs.length === 0, probs.join(" | "));
      const main = byId(a, "sp-main");
      ok(`${w} ${v}: fits`, main.calculatedX + main.calculatedWidth <= w + 0.5);
    }
  }
  a.setWidth(1280, 700);

  // the server's older colour names, and one the page has no class for
  eq("green reads as emerald", M.SpSpace.pageColor("green"), "emerald");
  eq("red as rose", M.SpSpace.pageColor("red"), "rose");
  eq("#rrggbb as none", M.SpSpace.pageColor("#123456"), "");

  // the places: none means Tampere; a town is looked up, picked for a
  // ward, saved, and the summary shows the weather of each
  eq("no places: Tampere's weather", JSON.parse(a.weatherPlacesJson()).map((p) => p.name).join(), "Tampere");
  a.press("sp-nav-members");
  ok("the places card", texts(a).includes("Places"));
  a.press("sp-placefor-0");
  a.press("sp-place-name");
  for (const ch of "kuopio") a.keyWith(ch, false, false);
  a.keyWith("Enter", false, false);
  eq("Enter looks the town up", a.takeAction(), "places");
  eq("that town", a.placeQuery, "kuopio");
  a.placesFound(JSON.stringify({ results: [{ name: "Kuopio", latitude: 62.89238, longitude: 27.67703, admin1: "Pohjois-Savo" }] }));
  ok("the towns found", texts(a).includes("Kuopio · Pohjois-Savo"));
  a.press("sp-placepick-0");
  eq("add_place", a.outOp(), "add_place");
  const pb = JSON.parse(a.outBody());
  ok("for Aino, with where it is", pb.place === "Kuopio" && pb.ward === "w1" && pb.lat === 62.89238 && pb.lon === 27.67703, JSON.stringify(pb));
  a.answer(JSON.stringify(spaceOf({ places: [{ name: "Espoo", lat: 60.2, lon: 24.66 }],
    wards: [{ id: "w1", name: "Aino", color: "cyan", places: [{ name: "Kuopio", lat: 62.8924, lon: 27.677 }] }] })));
  eq("the space's place first, then the ward's", JSON.parse(a.weatherPlacesJson()).map((p) => p.name).join(), "Espoo,Kuopio");
  ok("listed", texts(a).includes("Kuopio") && texts(a).includes("Espoo"));
  a.press("sp-nav-home");
  a.setWeather("Kuopio", 3, 5, 1, 40, 61);
  a.setWeather("Espoo", 8, 10, 6, 10, 1);
  ok("the space's weather first", texts(a).includes("Espoo · high 10° low 6°"));
  a.press("sp-wxplace-1");
  ok("switched to Aino's town", texts(a).includes("Kuopio (Aino) · high 5° low 1°"), texts(a).filter((s) => /high/.test(s)).join());
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    const probs = a.a11yProblems();
    ok(`${w} the summary with places lints clean`, probs.length === 0, probs.join(" | "));
  }
  a.setWidth(1280, 700);
  a.press("sp-nav-members");
  const kuo = a.space.places.findIndex((p) => p.name === "Kuopio");
  a.press("sp-unplace-" + kuo);
  const rb = JSON.parse(a.outBody());
  ok("remove_place, the ward's", a.outOp() === "remove_place" && rb.place === "Kuopio" && rb.ward === "w1", JSON.stringify(rb));
  a.answer(JSON.stringify(spaceOf({ places: [{ name: "Espoo", lat: 60.2, lon: 24.66 }], wards: [{ id: "w1", name: "Aino", color: "cyan" }] })));
  eq("one place left", a.space.places.length, 1);

  // a ward added by mistake: Remove asks first, then its events stay without it
  a.press("sp-nav-members");
  t = texts(a);
  ok("the wards are listed", t.includes("Aino") && t.includes("Dependants"));
  // a ward added on the Members page
  a.press("sp-ward-name");
  for (const ch of "Leevi") a.keyWith(ch, false, false);
  a.keyWith("Enter", false, false);
  eq("Enter adds it", a.outOp(), "add_ward");
  eq("by that name", JSON.parse(a.outBody()).name, "Leevi");
  eq("on the page at once", a.space.resources.length, 2);
  ok("with a colour", a.space.resources[1].color.length > 0);
  a.answer(JSON.stringify({ id: "w2", name: "Leevi", color: a.space.resources[1].color }));
  eq("it takes the server's id", a.space.resources[1].id, "w2");
  a.press("sp-ward-name");
  for (const ch of "aino") a.keyWith(ch, false, false);
  a.press("sp-addward");
  eq("a name it has already is not added", a.outCount(), 0);
  a.wardField.setValue("");
  a.press("sp-unward-1");
  a.press("sp-unward-yes");
  a.answer(JSON.stringify(spaceOf()));
  eq("and it can go again", a.space.resources.length, 1);

  a.press("sp-wardcolor-0");
  eq("Colour gives the next one", a.space.resources[0].color, "cyan");
  eq("and saves it", a.outOp(), "set_ward");
  eq("that colour", JSON.parse(a.outBody()).color, "cyan");
  a.answer(JSON.stringify({ id: "w1", name: "Aino", color: "cyan" }));

  // a child's clothing sizes and clothing list, on the ward
  const type = (s) => { for (const ch of s) a.keyWith(ch, false, false); };
  a.press("sp-wardclothes-0");
  a.press("sp-size-what");
  type("kengät");
  a.press("sp-size-now");
  type("30");
  a.press("sp-size-next");
  type("31");
  a.press("sp-size-by");
  type("2027-03");
  a.keyWith("Enter", false, false);
  eq("Enter saves the size", a.outOp(), "set_ward");
  let wb = JSON.parse(a.outBody());
  eq("the whole lists, for that ward", JSON.stringify([wb.ward, wb.sizes, wb.clothes]), JSON.stringify(["w1", [{ what: "kengät", size: "30", next_size: "31", next_by: "2027-03" }], []]));
  a.answer(JSON.stringify({ id: "w1" }));
  ok("the size is shown", texts(a).includes("kengät 30 → 31 (3/2027)"));
  a.press("sp-size-what");
  type("Kengät");
  a.press("sp-size-now");
  type("31");
  a.press("sp-sizeadd");
  eq("one of the same kind is replaced", JSON.stringify(JSON.parse(a.outBody()).sizes), JSON.stringify([{ what: "Kengät", size: "31" }]));
  a.answer(JSON.stringify({ id: "w1" }));
  a.press("sp-size-what");
  type("pipo");
  a.press("sp-size-by");
  type("maaliskuu");
  a.press("sp-sizeadd");
  eq("a bad month is not sent", a.outCount(), 0);
  a.sizeWhat.setValue("");
  a.sizeBy.setValue("");
  a.press("sp-cloth-item");
  type("sukat");
  a.press("sp-cloth-need");
  type("6");
  a.keyWith("Enter", false, false);
  wb = JSON.parse(a.outBody());
  eq("a clothing row", JSON.stringify(wb.clothes), JSON.stringify([{ item: "sukat", need: 6, have: 0 }]));
  a.answer(JSON.stringify({ id: "w1" }));
  ok("it shows what is missing", texts(a).includes("sukat  0/6 · missing 6"));
  a.press("sp-clothmore-0");
  a.answer(JSON.stringify({ id: "w1" }));
  a.press("sp-clothmore-0");
  eq("+ counts one more", JSON.parse(a.outBody()).clothes[0].have, 2);
  a.answer(JSON.stringify({ id: "w1" }));
  a.press("sp-clothless-0");
  eq("− one fewer", JSON.parse(a.outBody()).clothes[0].have, 1);
  a.answer(JSON.stringify({ id: "w1" }));
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    const probs = a.a11yProblems();
    ok(`${w} clothing open: the accessible tree lints clean`, probs.length === 0, probs.join(" | "));
    const main = byId(a, "sp-main");
    ok(`${w} clothing open: fits`, main.calculatedX + main.calculatedWidth <= w + 0.5);
  }
  a.setWidth(1280, 700);
  a.press("sp-wardclothes-0");
  t = texts(a);
  ok("closed, a line of sizes and what is missing", t.includes("Sizes: Kengät 31") && t.includes("Missing: 5 × sukat"));
  a.press("sp-wardclothes-0");
  a.press("sp-clothdel-0");
  a.answer(JSON.stringify({ id: "w1" }));
  a.press("sp-sizedel-0");
  eq("removed, the lists are empty", JSON.stringify([JSON.parse(a.outBody()).sizes, JSON.parse(a.outBody()).clothes]), JSON.stringify([[], []]));
  a.answer(JSON.stringify({ id: "w1" }));
  a.press("sp-wardclothes-0");
  // and read from the server
  const rw = new M.SpResource();
  M.SpRemote.readClothing(rw, M.SpRemote.parse(JSON.stringify({ id: "w1", name: "Aino",
    sizes: [{ what: "ulkovaatteet", size: "122", next_size: "128", next_by: "2027-09" }],
    clothes: [{ item: "talvihaalari", size: "128", need: 1, have: 0, missing: 1 }] })));
  eq("a ward's clothing is read with the space", JSON.stringify([rw.sizes[0].text(), rw.missingText()]), JSON.stringify(["ulkovaatteet 122 → 128 (9/2027)", "talvihaalari"]));
  a.press("sp-unward-0");
  eq("Remove alone sends nothing", a.outCount(), 0);
  ok("it asks", texts(a).some((s) => s.startsWith("Remove Aino?")));
  a.press("sp-unward-no");
  ok("Cancel keeps it", a.space.resources.length === 1 && !texts(a).some((s) => s.startsWith("Remove Aino?")));
  a.press("sp-unward-0");
  a.press("sp-unward-yes");
  eq("then remove_ward", a.outOp(), "remove_ward");
  eq("of that ward", JSON.parse(a.outBody()).ward, "w1");
  eq("gone from the page", a.space.resources.length, 0);
  ok("its events kept, without it", a.space.events.length === 3 && a.space.events.every((e) => e.resource === ""));
  a.answer(JSON.stringify(spaceOf({ wards: [], members: [{ uid: "u1", name: "Mia", email: "mia@example.com", role: "owner" }] })));
  eq("nothing left to send after it", a.outCount(), 0);

  // a pointer in a field puts the caret where it lands
  a.press("sp-nav-members");
  a.press("sp-invite-email");
  a.keyWith("a", false, false);
  a.keyWith("b", false, false);
  const fieldEl = byId(a, "sp-invite-email-text");
  a.pointerDown("sp-invite-email-text", fieldEl.calculatedX + 1, fieldEl.calculatedY + 4);
  a.pointerUp();
  eq("a press at the start puts the caret there", a.inviteField.caret, 0);
  ok("a drawn caret", byId(a, "sp-invite-email-caret").calculatedWidth > 0);

  // the text-input bridge
  a.press("sp-nav-spaces");
  a.press("sp-new-name");
  const st = JSON.parse(a.fieldStateJson("sp-new-name"));
  ok("the bridge reads the field", st.value === "" && st.box.w > 0, JSON.stringify(st));
  ok("and edits it", a.applyEdit("sp-new-name", "Team Vireo", 10, 10));
  a.press("sp-newpack-business");
  a.press("sp-create");
  eq("a new space", a.outOp(), "create_space");
  const cb = JSON.parse(a.outBody());
  ok("with its name and words", cb.name === "Team Vireo" && cb.vocab === "business" && cb.lang === "en", JSON.stringify(cb));
  a.answer(JSON.stringify({ space_id: "sp2", name: "Team Vireo", vocab: "business", lang: "en", role: "owner", members: [me], invites: [], wards: [] }));
  eq("opened", a.spaceId, "sp2");
  eq("in the list", a.account.spaces.length, 2);
  eq("on the summary", a.tab, "home");
  ok("an empty space offers the demo data", !!byId(a, "sp-seed"));

  // the demo data: wards first, then the events with the wards' new ids
  a.press("sp-seed");
  eq("three wards", a.outCount(), 3);
  for (let i = 0; a.outOp() === "add_ward"; i++) {
    const wb = JSON.parse(a.outBody());
    ok("a ward " + wb.name, wb.space_id === "sp2" && wb.name.length > 0);
    a.answer(JSON.stringify({ id: "srv" + i, name: wb.name, color: wb.color }));
  }
  eq("then the events", a.outOp(), "import_events");
  const ib = JSON.parse(a.outBody());
  ok("every event with a ward has a server's ward", ib.events.length > 5 && ib.events.every((e) => !e.ward || e.ward.startsWith("srv")), JSON.stringify(ib.events.map((e) => e.ward)));
  ok("the deadlines are important events", ib.events.some((e) => e.important && !e.start));
  a.answer(JSON.stringify({ created: ib.events.length, updated: 0, event_ids: [] }));
  eq("and the space is read again", a.outOp(), "get_space");

  // nobody's spaces yet
  const b = new M.SpacesApp();
  b.server = true;
  b.lang = "fi";
  b.init(CSS);
  b.setWidth(390, 700);
  b.start();
  b.answer(JSON.stringify({ me, spaces: [] }));
  eq("no spaces: nothing to open", b.outCount(), 0);
  ok("and the spaces view says so", texts(b).some((s) => s.startsWith("Sinulla ei ole vielä tiloja")));
  ok("with a field for a new one", !!byId(b, "sp-new-name"));
}

console.log("entries: the form, the Add menu, deleting and Undo");
{
  const a = fresh("en", "family");
  a.setWidth(1280, 700);
  // a summary row opens its event in the form
  const vet = a.space.events.find((e) => e.title === "Dentist");
  texts(a);
  const row = a.itemOccs.indexOf(vet.id);
  ok("the dentist is in the summary", row >= 0);
  a.press("sp-item-" + row);
  ok("a click opens the form", a.editing && a.editId === vet.id);
  let t = texts(a);
  ok("with its title and who", t.includes("Dentist") && t.includes("Takes: Juho"), t.slice(0, 60).join(","));
  const field = (key) => a.formCtls[a.formKeys.indexOf(key)];
  field("title").setValue("Dentist, check-up");
  field("place").setValue("Health centre");
  field("start").setValue("15.00");
  a.press("sp-tog-" + a.pickKeys.indexOf("important"));
  a.press("sp-ed-save");
  ok("saved", !a.editing && vet.title === "Dentist, check-up" && vet.place === "Health centre" && vet.startMin === 900 && vet.important, a.formErr);
  eq("the end follows a start after it", vet.endMin, 960);

  // a bad date is named, nothing is changed
  a.press("sp-item-" + a.itemOccs.indexOf(vet.id));
  field("date").setValue("31.2.");
  a.press("sp-ed-save");
  ok("a date that does not read is named", a.editing && a.formErr.includes("Date"), a.formErr);
  a.keyWith("Escape", false, false);
  ok("Escape closes the form", !a.editing);

  // every view of the form lays out and lints, at both widths, for every type
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    for (const ty of a.types.types) {
      a.openNew(ty.id);
      a.rebuild();
      const probs = a.a11yProblems();
      ok(`${w} the form of ${ty.id} lints clean`, probs.length === 0, probs.join(" | "));
      const main = byId(a, "sp-main");
      ok(`${w} the form of ${ty.id} fits`, main.calculatedX + main.calculatedWidth <= w + 0.5);
      a.press("sp-ed-cancel");
    }
  }
  a.setWidth(1280, 700);

  // a date is never typed: every date key of every type is EVGUI's calendar
  // behind a button, and nothing a date goes into is a text field
  const OWN_DATES = ["date", "end_date", "until", "cost:first", "get:from", "get:by"];
  let dates = 0;
  for (const ty of a.types.types) {
    a.openNew(ty.id);
    a.formCost = true;
    a.rebuild();
    const typed = ty.fields.filter((f) => f.type === "date").map((f) => "f:" + f.key);
    a.formKeys.forEach((key, i) => {
      if (!OWN_DATES.includes(key) && !typed.includes(key)) return;
      dates++;
      const btn = byId(a, "sp-date-" + i);
      ok(`${ty.id} ${key} is a calendar`, !!btn && btn.role === "button" && !byId(a, "sp-ed-" + i));
    });
    for (const f of typed) ok(`${ty.id} ${f} is on the form`, a.formKeys.includes(f));
    const boxes = flat(a).filter((e) => e.role === "textbox" && /^sp-ed-/.test(e.id));
    for (const b of boxes) {
      const key = a.formKeys[Number(b.id.slice(6))];
      ok(`${ty.id} ${key} is text and not a day`, !a.isDateKey(key));
    }
    a.press("sp-ed-cancel");
  }
  ok("the forms have dates", dates > 50, String(dates));

  // choosing a day: open, pick, it is written as ISO; Today; Clear; Escape
  {
    a.openNew("birthday_invitation");
    const i = a.formKeys.indexOf("date");
    const iso = (d) => M.UiDate.isoOfDays(d);
    a.press("sp-date-" + i);
    ok("the button opens the calendar", a.dateOpen === i && !!byId(a, "sp-dp-grid"));
    eq("on the field's day", a.datePick.selectedDay, M.SpTypes.dayOf(field("date").value, 2026));
    const probs = a.a11yProblems();
    ok("an open calendar lints clean", probs.length === 0, probs.join(" | "));
    for (const w of [1280, 390]) {
      a.setWidth(w, 700);
      const main = byId(a, "sp-main");
      ok(`${w} an open calendar fits`, main.calculatedX + main.calculatedWidth <= w + 0.5);
    }
    a.setWidth(1280, 700);
    a.press("sp-dp-" + iso(a.today + 3));
    ok("a day is chosen and the calendar closes", field("date").value === iso(a.today + 3) && a.dateOpen === -1, field("date").value);
    a.press("sp-date-" + i);
    a.press("sp-dp-next");
    ok("the next month", byId(a, "sp-dp-month") && a.dateOpen === i);
    a.press("sp-dp-today");
    eq("Today", field("date").value, iso(a.today));
    a.press("sp-date-" + i);
    a.keyWith("ArrowRight", false, false);
    a.keyWith("Enter", false, false);
    eq("the keyboard picks a day", field("date").value, iso(a.today + 1));
    a.press("sp-date-" + i);
    a.keyWith("Escape", false, false);
    ok("Escape closes only the calendar", a.dateOpen === -1 && a.editing);
    const end = a.formKeys.indexOf("end_date");
    a.press("sp-date-" + end);
    a.press("sp-dp-clear");
    eq("Clear", field("end_date").value, "");
    a.press("sp-date-" + i);
    a.press("sp-ed-title");
    ok("a press elsewhere closes it", a.dateOpen === -1);
    a.press("sp-ed-cancel");
  }

  // the Add menu: without a server, a search over the pack's types and its
  // groups to browse, not the whole long list
  a.press("sp-nav-calendar");
  a.press("sp-add");
  t = texts(a);
  ok("the Add menu searches the types", byId(a, "sp-addsearch") && a.focused === "sp-addsearch" && t.includes("Health") && !t.includes("Doctor's visit"), t.slice(-40).join(","));
  a.press("sp-addgrp-health");
  t = texts(a);
  ok("a group shows its types", t.includes("Doctor's visit") && !t.includes("Birthday invitation"));
  a.press("sp-addgrp-health");
  a.typeSearch.setValue("invit");
  a.rebuild();
  t = texts(a);
  ok("the search finds the family types", t.includes("Birthday invitation") && !t.includes("Party invitation") && !t.includes("Doctor's visit"), t.slice(-40).join(","));
  a.typeSearch.setValue("kutsu");
  a.rebuild();
  ok("in Finnish too", texts(a).includes("Birthday invitation"));
  a.typeSearch.setValue("zzz");
  a.rebuild();
  ok("nothing found is said", texts(a).includes("Nothing found"));
  a.typeSearch.setValue("thing");
  a.rebuild();
  ok("a thing", texts(a).includes("Thing"));
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    const probs = a.a11yProblems();
    ok(`${w} the Add menu lints clean`, probs.length === 0, probs.join(" | "));
  }
  a.setWidth(1280, 700);
  a.typeSearch.setValue("");
  a.board.cursor = a.today + 2;
  a.press("sp-addtype-birthday_invitation");
  ok("a new invitation", a.editing && a.editId === "" && a.editType === "birthday_invitation");
  eq("on the calendar's day", field("date").value, M.UiDate.isoOfDays(a.today + 2));
  field("title").setValue("Eero's birthday");
  field("f:host").setValue("Eero");
  const rsvp = a.pickKeys.indexOf("f:rsvp");
  a.press(`sp-pick-${rsvp}-1`);
  field("cost:amount").setValue("20,50");
  field("cost:payer").setValue("Mia");
  const n = a.space.events.length;
  a.press("sp-ed-save");
  eq("it is in the space", a.space.events.length, n + 1);
  const inv = a.space.events[n];
  ok("with its type, fields and cost", inv.typeId === "birthday_invitation" && inv.fieldText("rsvp") === "going" && inv.fieldText("host") === "Eero"
    && inv.hasCost && inv.cost.amount === 20.5 && inv.cost.scope === "shared" && inv.cost.payer === "Mia", JSON.stringify(inv.values));
  ok("and on the calendar", a.occIds.includes(inv.id));

  // a record without a date
  a.press("sp-nav-lists");
  a.press("sp-addtype-item");
  eq("a thing has no date to start with", field("date").value, "");
  field("title").setValue("Skates");
  a.press(`sp-pick-${a.pickKeys.indexOf("f:state")}-2`);
  a.press("sp-ed-save");
  const skates = a.space.events.find((e) => e.title === "Skates");
  ok("saved without a date", !!skates && skates.day === -1 && skates.fieldText("state") === "repair", a.formErr);
  a.press("sp-list-item");
  ok("listed under Things", texts(a).includes("Skates"));
  ok("not on the calendar", !a.occIds.includes(skates.id));

  // a missing thing with a day: the two weeks to get it in are on the
  // calendar (a switch hides them); its estimate and what it cost are kept
  const iso = (d) => M.UiDate.isoOfDays(d);
  a.press("sp-addtype-item");
  field("title").setValue("Kids' beds");
  field("date").setValue(iso(a.today + 9));
  a.press(`sp-pick-${a.pickKeys.indexOf("f:state")}-1`);
  field("budget:min").setValue("200");
  field("budget:max").setValue("400");
  a.press("sp-ed-save");
  const beds = a.space.events.find((e) => e.title === "Kids' beds");
  ok("saved with its estimate", !!beds && Number(beds.fieldText("estimated_cost_min")) === 200 && Number(beds.fieldText("estimated_cost_max")) === 400 && beds.fieldText("budget_scope") === "", a.formErr);
  a.press("sp-nav-calendar");
  ok("the calendar leaves windows and payment days out unless switched on", !a.showGets && !a.showPays && !a.occIds.includes(beds.id + "#get"));
  a.press("sp-cal-gets");
  const getAt = a.occIds.indexOf(beds.id + "#get");
  ok("the window to get it is on the calendar", getAt >= 0 && a.occDay[getAt] === a.today - 5);
  const getOcc = a.space.occurrences(a.today, a.today).find((o) => o.id === beds.id + "#get");
  ok("titled, and important while open", !!getOcc && getOcc.title === "Kids' beds" && getOcc.important && getOcc.endDay === a.today + 9);
  a.press("sp-nav-home");
  ok("on the summary once, under Remember, not as a Today card", texts(a).filter((t) => t === "Kids' beds").length === 1, texts(a).filter((t) => t.includes("beds")).join("|"));
  a.press("sp-nav-calendar");
  ok("the switch is on the calendar", texts(a).some((t) => t.includes("Things to get")));
  a.press("sp-cal-gets");
  ok("switched off, it is not", !a.occIds.includes(beds.id + "#get"));
  ok("the summary has it either way", a.space.occurrences(a.today, a.today).some((o) => o.id === beds.id + "#get"));
  a.press("sp-cal-gets");
  ok("and back", a.occIds.includes(beds.id + "#get"));
  a.press("sp-cal-pays");
  ok("payment days switched on", a.showPays);
  a.press("sp-cal-pays");
  ok("a moved window is not moved", !a.space.moveOccurrence(beds.id + "#get", a.today, -1, -1) && beds.day === a.today + 9);
  ok("it opens the thing", a.space.eventIdOf(beds.id + "#get") === beds.id);
  // a window backwards is named
  a.press("sp-entry-" + beds.id);
  field("get:from").setValue(iso(a.today + 9));
  field("get:by").setValue(iso(a.today + 2));
  a.press("sp-ed-save");
  ok("get from after get by is named", a.editing && a.formErr.includes("Get from"), a.formErr);
  field("get:from").setValue(iso(a.today));
  a.press("sp-ed-save");
  ok("its own window", !a.editing && beds.fieldText("get_from") === iso(a.today) && beds.fieldText("get_by") === iso(a.today + 2), a.formErr);

  // a cost in twelve parts: its payment days, reminded three days before; an
  // own budget names its owner
  a.press("sp-nav-lists");
  a.press("sp-addtype-item");
  field("title").setValue("Electricity");
  field("cost:amount").setValue("600");
  field("cost:instalments").setValue("12");
  field("cost:first").setValue(iso(a.today - 28));
  a.press(`sp-pick-${a.pickKeys.indexOf("budget:scope")}-1`);
  a.press("sp-ed-save");
  ok("an own budget without its owner is named", a.editing && a.formErr.includes("Whose own"), a.formErr);
  field("budget:owner").setValue("Tero");
  a.press("sp-ed-save");
  const el = a.space.events.find((e) => e.title === "Electricity");
  ok("Tero's own, paid by him", !!el && el.cost.scope === "personal" && el.cost.payer === "Tero" && el.fieldText("budget_scope") === "personal", a.formErr);
  const pays = a.space.occurrences(a.today, a.today + 40).filter((o) => o.eventId === el.id);
  eq("two payment days in forty days", pays.length, 2);
  ok("the next one is reminded of", pays[0].title === "Payment: Electricity 50 €" && pays[0].important && !pays[1].important, JSON.stringify(pays.map((o) => [o.title, o.day - a.today, o.important])));
  a.space.showPays = false;
  eq("hidden with the switch", a.space.occurrences(a.today, a.today + 40).filter((o) => o.eventId === el.id).length, 0);
  a.space.showPays = true;

  // Backspace deletes the selected event; Undo brings it back
  a.press("sp-nav-calendar");
  a.displayListJson();
  const k = a.occIds.indexOf(inv.id);
  a.press("spc-ev-" + a.boardIds[k]);
  eq("a click selects it", a.selectedOcc(), inv.id);
  ok("Backspace deletes it", a.keyWith("Backspace", false, false));
  ok("gone", !a.space.events.includes(inv) && !a.occIds.includes(inv.id));
  ok("with Undo offered", a.status.startsWith("Deleted") && !!byId(a, "sp-undo"));
  a.press("sp-undo");
  ok("Undo brings it back", a.space.events.includes(inv) && a.occIds.includes(inv.id));
  // in a text field, Backspace is the field's
  a.press("sp-entry-" + skates.id);
  a.press("sp-ed-0");
  eq("the title field has the keyboard", a.focusedField(), "sp-ed-0");
  a.keyWith("End", false, false);
  a.keyWith("Backspace", false, false);
  ok("Backspace in a field edits the text", field("title").value === "Skate" && a.space.events.includes(skates), field("title").value);
  a.keyWith("Escape", false, false);
  a.keyWith("Escape", false, false);
  ok("and Escape leaves the form", !a.editing);

  // one day of a series
  a.press("sp-nav-calendar");
  a.displayListJson();
  const wk = a.space.events.find((e) => e.rule.freq === "weekly");
  const occ = a.occIds.findIndex((id) => id.startsWith(wk.id + "@"));
  a.press("spc-ev-" + a.boardIds[occ]);
  a.keyWith("Delete", false, false);
  eq("Delete on one day of a series skips that day", wk.exdates.length, 1);
  a.keyWith("z", false, true);
  eq("Ctrl+Z puts it back", wk.exdates.length, 0);
  // Enter on a selected event opens it
  a.displayListJson();
  a.press("spc-ev-" + a.boardIds[occ]);
  a.keyWith("Enter", false, false);
  ok("Enter opens the selected event", a.editing && a.editId === wk.id && a.editOcc >= 0);
  ok("with Delete this day only", !!byId(a, "sp-ed-delday"));
  a.press("sp-ed-cancel");
  // a sheet row is not deleted
  const sr = a.occIds.findIndex((id) => id.startsWith("s1-r"));
  a.press("spc-ev-" + a.boardIds[sr]);
  const before = a.space.events.length;
  a.keyWith("Backspace", false, false);
  ok("a sheet row stays", a.space.events.length === before && a.status.includes("sheet"), a.status);

  // the budget
  a.press("sp-nav-budget");
  t = texts(a);
  ok("the budget shows the cost", t.includes("Eero's birthday") && t.includes("€20.50"), t.filter((x) => /€/.test(x)).join(","));
  const mia = a.space.payers().indexOf("Mia");
  a.press("sp-bud-payer-" + mia);
  ok("one person's budget: their own and the family's", texts(a).includes("Eero's birthday"));
  eq("without a server nothing is sent", a.outCount(), 0);
}

console.log("entries on a server");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "fi";
  a.init(CSS);
  a.setWidth(1280, 700);
  a.start();
  const iso = (d) => M.UiDate.isoOfDays(d);
  a.answer(JSON.stringify({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [{ space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner" }] }));
  a.answer(JSON.stringify({
    space: { space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner", members: [{ uid: "u1", name: "Mia", email: "mia@example.com", role: "owner" }], invites: [], wards: [{ id: "w1", name: "Aino", color: "rose" }] },
    events: [
      { event_id: "ev1", title: "Synttärit", date: iso(a.today + 1), start: "13:00", end: "15:00", ward: "w1", type: "invitation", fields: { rsvp: "open", gift_bought: false }, cost: { amount: 20, payer: "Mia", scope: "personal", instalments: 1, every_months: 1 } },
      { event_id: "ev2", title: "Passi", date: iso(a.today + 10), type: "renewal", remind_from: iso(a.today - 20) },
      { event_id: "ev3", title: "Luistimet", date: "", type: "item", fields: { state: "repair" } },
    ],
  }));
  eq("three entries", a.space.events.length, 3);
  let t = texts(a);
  ok("a due renewal is on the summary", t.some((x) => x === "Passi"), t.slice(0, 50).join(","));
  const party = a.space.events[0];
  a.press("sp-item-" + a.itemOccs.indexOf(party.id));
  const field = (key) => a.formCtls[a.formKeys.indexOf(key)];
  ok("the form shows the type's fields", texts(a).includes("Mennäänkö?") && texts(a).includes("Ei vastattu"));
  a.press(`sp-pick-${a.pickKeys.indexOf("f:rsvp")}-0`);
  field("f:gift").setValue("Kirja");
  field("cost:amount").setValue("");
  a.press("sp-ed-save");
  eq("saving is a save_event", a.outOp(), "save_event");
  let body = JSON.parse(a.outBody());
  ok("with the fields", body.event_id === "ev1" && body.type === "invitation" && body.fields.gift === "Kirja" && body.fields.rsvp === "open", JSON.stringify(body));
  ok("and the cost removed", body.cost === null, JSON.stringify(body));
  a.answer(JSON.stringify({ ...body, cost: undefined }));
  ok("answered, nothing is left to save", !party.dirty && !party.costDropped);

  // delete: a delete_event; Undo before it is sent keeps it
  a.press("sp-nav-lists");
  a.press("sp-list-item");
  a.press("sp-entry-" + a.space.events[2].id);
  a.press("sp-ed-cancel");
  a.setFocus("sp-entry-" + a.space.events[2].id);
  a.keyWith("Backspace", false, false);
  eq("Backspace on a list row deletes it", a.outOp(), "delete_event");
  eq("by the server's id", JSON.parse(a.outBody()).event_id, "ev3");
  a.answer(JSON.stringify({ deleted: true }));
  eq("gone", a.space.events.length, 2);
  a.press("sp-undo");
  eq("Undo after the delete was sent saves it again", a.outOp(), "save_event");
  body = JSON.parse(a.outBody());
  ok("as a new entry, its fields kept", body.event_id === "" && body.type === "item" && body.date === "" && body.fields.state === "repair", JSON.stringify(body));
  a.answer(JSON.stringify({ ...body, event_id: "ev9" }));
  eq("with a new id", a.space.events[2].remoteId, "ev9");

  // a failed delete puts it back
  a.setFocus("sp-entry-" + a.space.events[2].id);
  a.keyWith("Delete", false, false);
  a.failed("not allowed");
  eq("a delete the server refuses is undone", a.space.events.length, 3);

  // the budget comes from the server
  a.press("sp-nav-budget");
  eq("the budget is asked for", a.outOp(), "budget");
  const bb = JSON.parse(a.outBody());
  ok("for six months", bb.space_id === "sp1" && bb.from.length === 7 && bb.to.length === 7 && !bb.payer, JSON.stringify(bb));
  a.answer(JSON.stringify({ from: bb.from, to: bb.to, total: 120, shared: 100, by_payer: { Mia: 100, Juho: 20 },
    months: [{ month: bb.from, total: 120, shared: 100, by_payer: { Mia: 100, Juho: 20 }, lines: [
      { event_id: "ev5", title: "Uintimaksu", type: "hobby", date: bb.from + "-15", amount: 100, payer: "Mia", scope: "shared", n: 1, of: 2 },
      { event_id: "ev6", title: "Lahja", type: "wish", date: bb.from + "-20", amount: 20, payer: "Juho", scope: "personal", n: 1, of: 1 }] }] }));
  t = texts(a);
  ok("the server's budget is shown", t.includes("Uintimaksu") && t.includes("120 €") && t.includes("100 €"), t.filter((x) => /€/.test(x)).join(","));
  a.press("sp-bud-payer-0");
  eq("a person's budget asks again", JSON.parse(a.outBody()).person, "Mia");
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    for (const v of ["lists", "budget"]) {
      a.press("sp-nav-" + v);
      const probs = a.a11yProblems();
      ok(`${w} ${v} on a server lints clean`, probs.length === 0, probs.join(" | "));
    }
  }
}

console.log("a reminder ticked off on the summary");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "fi";
  a.init(CSS);
  a.setWidth(1280, 700);
  a.start();
  const iso = (d) => M.UiDate.isoOfDays(d);
  a.answer(JSON.stringify({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [{ space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner" }] }));
  a.answer(JSON.stringify({
    space: { space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner", members: [], invites: [], wards: [{ id: "w1", name: "Aino", color: "rose" }] },
    events: [
      { event_id: "ev1", title: "Retkimaksu 15 €", date: iso(a.today + 4), type: "reminder", ward: "w1" },
      { event_id: "ev2", title: "Ainon passi", date: iso(a.today + 15), type: "renewal", remind_from: iso(a.today - 5), fields: { status: "done" } },
    ],
  }));
  let t = texts(a);
  const [fee, passport] = a.space.events;
  ok("a reminder under Muista", t.includes("Retkimaksu 15 €") && !!byId(a, "sp-tick-" + fee.id), t.slice(0, 60).join(","));
  ok("not one already done", !byId(a, "sp-tick-" + passport.id));
  // each box can be copied, as the canvas has no selection
  a.press("sp-copy-r-0");
  const cp = a.takeAction() + "|" + a.copyText;
  ok("a Muista box to the clipboard", cp.startsWith("copy|Retkimaksu 15 €, ") && cp.endsWith(" · Aino"), cp);
  for (const w of [1280, 390]) {
    a.setWidth(w, 800);
    const probs = a.a11yProblems();
    ok(`${w} Muista with its copy chips lints clean`, probs.length === 0, probs.join(" | "));
  }
  a.press("sp-tick-" + fee.id);
  eq("ticking saves it", a.outOp(), "save_event");
  const body = JSON.parse(a.outBody());
  ok("as done", body.event_id === "ev1" && body.fields && body.fields.status === "done", JSON.stringify(body));
  ok("and it leaves the list", !byId(a, "sp-tick-" + fee.id));
}

console.log("an entry's offer watch");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "fi";
  a.init(CSS);
  a.setWidth(1280, 700);
  a.start();
  a.answer(JSON.stringify({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [{ space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner" }] }));
  a.answer(JSON.stringify({
    space: { space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner", members: [], invites: [], wards: [] },
    events: [
      { event_id: "ev1", title: "Lasten sängyt", date: "", type: "item", fields: { state: "missing" },
        watch: { query: "lasten sänky 90x200", active: true, sources: ["tori"], every_days: 1, finds: [
          { title: "Lasten sänky", url: "https://www.tori.fi/recommerce/forsale/item/111", price: 80, location: "Tampere", source: "Tori", found: "2026-10-07" },
          { title: "Huono", url: "javascript:alert(1)", source: "Tori", found: "2026-10-07" },
        ] } },
    ],
  }));
  const bed = a.space.events[0];
  eq("one safe find read", bed.findUrls.length, 1);
  a.press("sp-nav-lists");
  a.press("sp-list-item");
  a.press("sp-entry-" + bed.id);
  const t = texts(a);
  ok("the form shows the watch and its find", t.includes("Tarjousvahti") && t.includes("Lasten sänky · 80 € · Tampere · Tori · 7.10."), t.join(","));
  a.press("sp-find-0");
  eq("a find opens in a new tab", a.takeAction(), "open:https://www.tori.fi/recommerce/forsale/item/111");
  // its settings, sent as set_watch
  a.press("sp-wsrc-web");
  eq("a source is a set_watch", a.outOp(), "set_watch");
  let wb = JSON.parse(a.outBody());
  ok("with the sources", wb.event_id === "ev1" && wb.active === true && wb.sources.join() === "tori,web" && wb.every_days === 1, JSON.stringify(wb));
  a.answer(JSON.stringify({ event_id: "ev1", watch: { query: "lasten sänky 90x200", active: true, sources: ["tori", "web"], every_days: 1, finds: [] } }));
  a.press("sp-wint-7");
  wb = JSON.parse(a.outBody());
  eq("weekly", wb.every_days, 7);
  a.answer(JSON.stringify({ event_id: "ev1", watch: { query: "lasten sänky 90x200", active: true, sources: ["tori", "web"], every_days: 7, finds: [] } }));
  a.press("sp-watch-off");
  wb = JSON.parse(a.outBody());
  eq("stopped", wb.active, false);
  a.answer(JSON.stringify({ event_id: "ev1", watch: { query: "lasten sänky 90x200", active: false, sources: ["tori", "web"], every_days: 7, finds: [] } }));
  ok("and it can be turned on again", !!byId(a, "sp-watch-on"));
  const probs = a.a11yProblems();
  ok("the watch lints clean", probs.length === 0, probs.join(" | "));
}

console.log("a web address in today's card");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "fi";
  a.init(CSS);
  a.setWidth(1280, 700);
  a.start();
  const iso = (d) => M.UiDate.isoOfDays(d);
  const url = "https://ilmaistapahtumat.fi/tapahtuma/tampere:6a9fd2f180aad500079c9608";
  a.answer(JSON.stringify({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [{ space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner" }] }));
  a.answer(JSON.stringify({
    space: { space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner", members: [], invites: [], wards: [] },
    events: [{ event_id: "ev1", title: "Lasten galleria", date: iso(a.today), start: "23:00", end: "23:30",
      notes: "Hinta: ilmainen. Näyttely keskustassa. " + url + " Koodi:abcdefghijklmnopqrstuvwxyzabcdefghijklmnop" }],
  }));
  const t = texts(a);
  ok("the address is not written out", !t.some((s) => s.includes("https://")), t.filter((s) => s.includes("http")).join(","));
  ok("a link with its site instead", !!byId(a, "sp-turl-0") && t.includes("ilmaistapahtumat.fi ↗"));
  ok("a word too long to wrap is cut", t.some((s) => s.includes("Koodi:abcdefghijklmnopqrstuvwxyzab…")), t.filter((s) => s.includes("Koodi")).join(","));
  a.press("sp-turl-0");
  eq("a press opens it", a.takeAction(), "open:" + url);
  for (const w of [1280, 390]) {
    a.setWidth(w, 800);
    const probs = a.a11yProblems();
    ok(`${w} the card with its link lints clean`, probs.length === 0, probs.join(" | "));
    const card = byId(a, "sp-item-0");
    const inside = [];
    const walk = (el) => { inside.push(el); for (let i = 0; i < el.getChildCount(); i++) walk(el.getChild(i)); };
    walk(card);
    inside.push(byId(a, "sp-turl-0"));
    const over = inside.filter((e) => e.calculatedX + e.calculatedWidth > card.calculatedX + card.calculatedWidth + 0.5);
    ok(`${w} everything stays inside the card`, over.length === 0, over.map((e) => `${e.id || e.className} ${e.calculatedX}+${e.calculatedWidth}`).join(", ") + ` card ${card.calculatedX}+${card.calculatedWidth}`);
  }
}

console.log("the assistant on a server");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "fi";
  a.init(CSS);
  a.setWidth(1280, 700);
  a.start();
  const iso = (d) => M.UiDate.isoOfDays(d);
  a.answer(JSON.stringify({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [{ space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner" }] }));
  const space = { space_id: "sp1", name: "Kotiväki", vocab: "family", lang: "fi", role: "owner", members: [], invites: [], wards: [{ id: "w1", name: "Leo", color: "sky" }] };
  a.answer(JSON.stringify({ space, events: [] }));
  a.press("sp-nav-assistant");
  ok("a field to write in", texts(a).includes("Lisää kuva") && texts(a).includes("Lähetä"));
  a.press("sp-assist-send");
  eq("nothing written, nothing sent", a.outCount(), 0);
  a.press("sp-assist-image");
  eq("a picture is the host's to read", a.takeAction(), "image");
  a.addImage("kirje.jpg", "image/jpeg", "QUJD");
  ok("the picture is shown", texts(a).includes("× kirje.jpg"));
  a.assistField.setValue("Leon uinti tiistaisin 17-18 toukokuun loppuun");
  a.keyWith("Enter", false, false);
  a.focused = "sp-assist-text";
  a.press("sp-assist-send");
  eq("sent to the assistant", a.outOp(), "assist");
  const body = JSON.parse(a.outBody());
  ok("with the text, the picture and the day", body.space_id === "sp1" && body.text.startsWith("Leon uinti") && body.images[0].data === "QUJD"
    && body.images[0].mimeType === "image/jpeg" && body.today === iso(a.today) && body.history.length === 0, JSON.stringify(body));
  ok("the question is in the feed, the field emptied", texts(a).some((x) => x.startsWith("Leon uinti")) && a.assistField.value === "" && a.pics.length === 0);
  ok("while it reads", texts(a).includes("Luetaan…"));
  a.answer(JSON.stringify({ space_id: "sp1", text: "Lisäsin Leon uinnin tiistaisin.", changed: true, event_ids: ["e1"], calls: [{ tool: "import_events", ok: true }] }));
  ok("its answer", texts(a).includes("Lisäsin Leon uinnin tiistaisin.") && !a.assistBusy);
  eq("what it made is read again", a.outOp(), "get_space");
  a.answer(JSON.stringify({ space, events: [
    { event_id: "e1", title: "Uinti", date: iso(a.today + 1), start: "17:00", end: "18:00", ward: "w1", type: "activity", rrule: "FREQ=WEEKLY", fields: { hobby: "Uinti", gear: ["uimalasit", "pyyhe"] } }] }));
  eq("still on the assistant", a.tab, "assistant");
  const e1 = a.space.events[0];
  eq("a list field reads as one text", e1.fieldText("gear"), "uimalasit; pyyhe");
  // the next question carries the conversation; a refusal is said
  a.assistField.setValue("Peru huominen");
  a.press("sp-assist-send");
  const b2 = JSON.parse(a.outBody());
  ok("with the history", b2.history.length === 2 && b2.history[1].text === "Lisäsin Leon uinnin tiistaisin.", JSON.stringify(b2.history));
  a.failed("Too many questions in a short time; try again in a few minutes.");
  ok("the error in its place", texts(a).some((x) => x.startsWith("Se ei mennyt läpi")) && !a.assistBusy);
  eq("what was written is back to send again", a.assistField.value, "Peru huominen");
  // the canvas has no selection: a press copies an answer or a question
  a.press("sp-copy-a-0");
  eq("an answer to the clipboard", a.takeAction() + "|" + a.copyText, "copy|Lisäsin Leon uinnin tiistaisin.");
  a.press("sp-copy-q-1");
  eq("a question to the clipboard", a.takeAction() + "|" + a.copyText, "copy|Peru huominen");
  a.copied(true);
  eq("said so", a.status, "Kopioitu");
  // a server older than the page: said in words, not as "No such call."
  a.press("sp-assist-send");
  a.failed("No such call.");
  ok("an older server is said plainly", texts(a).some((x) => x.includes("avustaja ei ole vielä palvelimella")) && a.assistField.value === "Peru huominen");
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    const probs = a.a11yProblems();
    ok(`${w} the assistant lints clean`, probs.length === 0, probs.join(" | "));
  }
  a.setWidth(1280, 700);
  // on a server Add opens the assistant first; the types by hand are folded
  a.press("sp-nav-calendar");
  a.press("sp-add");
  ok("Add opens the assistant", byId(a, "sp-assist-send") && a.focused === "sp-assist-text" && !byId(a, "sp-addsearch"));
  a.addImage("kirje.jpg", "image/jpeg", "QUJD");
  eq("a picture stays in the Add menu", a.tab, "calendar");
  a.assistField.setValue("Eelin hammaslääkäri ensi tiistaina klo 9");
  a.press("sp-assist-send");
  eq("sent from the Add menu", a.outOp(), "assist");
  a.answer(JSON.stringify({ space_id: "sp1", text: "Lisäsin hammaslääkärin.", changed: true, event_ids: ["e2"], calls: [] }));
  ok("its answer in the menu", texts(a).includes("Lisäsin hammaslääkärin.") && a.tab === "calendar" && a.addOpen);
  a.press("sp-add-manual");
  ok("by hand: the search", byId(a, "sp-addsearch") && a.focused === "sp-addsearch");
  a.typeSearch.setValue("hammas");
  a.keyWith("Enter", false, false);
  ok("Enter opens the first found", a.editing && a.editType !== "" && !a.addOpen, a.editType);
  a.press("sp-ed-cancel");
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    a.press("sp-add");
    const probs = a.a11yProblems();
    ok(`${w} the Add menu with the assistant lints clean`, probs.length === 0, probs.join(" | "));
    a.press("sp-add");
  }
}

console.log("changes made elsewhere, and the tab in the address");
{
  const a = new M.SpacesApp();
  a.server = true;
  a.lang = "en";
  a.init(CSS);
  a.setWidth(1280, 700);
  const iso = (d) => M.UiDate.isoOfDays(d);
  const space = { space_id: "sp1", name: "Home", vocab: "family", lang: "en", role: "owner", members: [], invites: [], wards: [{ id: "w1", name: "Aino", color: "blue" }] };
  const ev = (id, title) => ({ event_id: id, title, date: iso(a.today), start: "12:00", end: "13:00", ward: "w1" });
  eq("nothing to ask before a space is open", a.poll(), false);
  a.start();
  a.answer(JSON.stringify({ me: { uid: "u1" }, spaces: [{ space_id: "sp1", name: "Home" }] }));
  a.answer(JSON.stringify({ space, events: [ev("e1", "Dentist")], changed: "100" }));
  eq("the space's stamp", a.changed, "100");
  a.importSheet("Shopping", "milk\tbread");
  a.press("sp-nav-calendar");
  ok("a poll asks", a.poll() && a.outOp() === "changes" && JSON.parse(a.outBody()).space_id === "sp1");
  eq("one at a time", a.poll(), false);
  a.answer(JSON.stringify({ changed: "100", mine: false }));
  eq("unchanged: nothing more", a.outCount(), 0);
  a.poll();
  a.answer(JSON.stringify({ changed: "150", mine: true }));
  ok("the page's own change is no news", a.outCount() === 0 && a.changed === "150");
  a.poll();
  a.answer(JSON.stringify({ changed: "200", mine: false }));
  ok("one made elsewhere reads the space again", a.outOp() === "get_space" && a.outbox[0].quiet);
  a.answer(JSON.stringify({ space, events: [ev("e1", "Dentist"), ev("e2", "Swimming")], changed: "200" }));
  eq("the new event is there", a.space.events.length, 2);
  eq("the tab stays", a.tab, "calendar");
  eq("and the page's own sheet", a.space.sheets.length, 1);

  // a form open: the change waits until it is closed
  a.poll();
  a.answer(JSON.stringify({ changed: "300", mine: false }));
  a.editing = true;
  a.answer(JSON.stringify({ space, events: [ev("e1", "Dentist")], changed: "300" }));
  ok("not while a form is open", a.space.events.length === 2 && a.stale);
  eq("nor asked", a.poll(), true);
  eq("(the poll asks only whether it changed)", a.outOp(), "changes");
  a.answer(JSON.stringify({ changed: "300", mine: false }));
  a.editing = false;
  ok("closed, it is read again", a.poll() && a.outOp() === "get_space");
  a.answer(JSON.stringify({ space, events: [ev("e1", "Dentist")], changed: "300" }));
  ok("and shown", a.space.events.length === 1 && !a.stale);
  a.poll();
  a.failed("The space could not be reached");
  eq("a failed poll says nothing", a.status, "");

  // the address names the tab
  ok("a tab by name", a.goTab("budget") && a.tab === "budget");
  ok("not a tab", !a.goTab("nowhere") && a.tab === "budget");
}

console.log(`passed=${passed} failed=${failed}`);
if (failed > 0) process.exit(1);
console.log("ALL PASS");
