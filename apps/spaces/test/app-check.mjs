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
  const t = texts(a);
  for (const h of ["Tänään", "Myöhemmin tällä viikolla", "Ensi viikolla"]) ok(`the section ${h}`, t.includes(h), t.slice(0, 30).join(","));
  ok("today's dentist", t.includes("Hammaslääkäri"));
  ok("a sheet row later this week", t.includes("Treenikyyti"));
  ok("an overdue deadline", t.some((s) => s.startsWith("Palauta kirjaston kirjat")));
  ok("a row names its ward and who looks after it", t.includes("Aino · Vie: Juho"), t.filter((s) => /·/.test(s)).join(","));
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
  ok("sports words", t.some((s) => s.startsWith("Players and kit")), t.filter((s) => /·/.test(s)).join(","));
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
  ok("the summary shows it", t.includes("Dentist") && t.includes("Swimming"), t.slice(0, 40).join(","));
  ok("with the ward and who", t.includes("Aino · Takes: Juho"));
  ok("an important event is marked", t.includes("★"));
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

  // the Add menu offers the pack's types
  a.press("sp-nav-calendar");
  a.press("sp-add");
  t = texts(a);
  ok("the Add menu lists the family types", t.includes("Birthday invitation") && t.includes("Childcare shift") && t.includes("Thing") && !t.includes("Party invitation"), t.slice(-40).join(","));
  ok("by group", t.includes("Health") && t.includes("Doctor's visit") && t.includes("Change to the usual"));
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
  a.press(`sp-pick-${a.pickKeys.indexOf("cost:scope")}-1`);
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
  ok("one payer's budget", texts(a).includes("Eero's birthday"));
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
  eq("a payer asks again", JSON.parse(a.outBody()).payer, "Mia");
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    for (const v of ["lists", "budget"]) {
      a.press("sp-nav-" + v);
      const probs = a.a11yProblems();
      ok(`${w} ${v} on a server lints clean`, probs.length === 0, probs.join(" | "));
    }
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
  for (const w of [1280, 390]) {
    a.setWidth(w, 700);
    const probs = a.a11yProblems();
    ok(`${w} the assistant lints clean`, probs.length === 0, probs.join(" | "));
  }
}

console.log(`passed=${passed} failed=${failed}`);
if (failed > 0) process.exit(1);
console.log("ALL PASS");
