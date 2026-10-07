#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// npm test: the app's models (src/*.rgr compiled to JavaScript, the same
// source the C++ build compiles) checked under Node:
//
//   JVal                  the JSON reader and writer
//   TextBuffer            the Markdown buffer: caret, selection, UTF-8, undo
//   Settings, ApiClient   the settings file, addresses, URL encoding
//   Session               against test/mock-server.mjs, with Node's fetch as
//                         the transport: connect, list, open, save, 409,
//                         create, delete, OAuth sign-in (PKCE) and refresh on
//                         401, finding servers on localhost:8080-8090
//   EditorApp             every screen builds a display list; a scripted run
//
//   node test/run-tests.mjs [--no-build]

import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(HERE, "..");
const require = createRequire(import.meta.url);

if (!process.argv.includes("--no-build")) {
  const { buildJs } = await import("../scripts/build-web.mjs");
  buildJs();
}
const M = require(path.join(ROOT, "build", "EditorApp.cjs"));
const { JVal, TextBuffer, Settings, ApiClient, Session, EditorApp } = M;

let passed = 0, failed = 0;
const results = [];
async function test(name, fn) {
  try {
    await fn();
    passed++;
    results.push(`  ok    ${name}`);
    console.log(`  ok    ${name}`);
  } catch (e) {
    failed++;
    console.log(`  FAIL  ${name}\n        ${(e && e.stack || String(e)).split("\n").slice(0, 4).join("\n        ")}`);
  }
}

// --- the mock server --------------------------------------------------------------
const mocks = [];
function startMock(args = []) {
  return new Promise((resolve, reject) => {
    const p = spawn(process.execPath, [path.join(HERE, "mock-server.mjs"), "--quiet", ...args], { stdio: ["ignore", "pipe", "inherit"] });
    mocks.push(p);
    let out = "";
    p.stdout.on("data", (d) => {
      out += d;
      const m = /listening on (\S+)/.exec(out);
      if (m) resolve({ url: m[1].replace("localhost", "127.0.0.1"), proc: p });
    });
    p.on("exit", (code) => reject(new Error("mock exited " + code + ": " + out)));
  });
}
const freePort = () => new Promise((resolve) => {
  const s = net.createServer().listen(0, "127.0.0.1", () => {
    const p = s.address().port;
    s.close(() => resolve(p));
  });
});
const portFree = (port) => new Promise((resolve) => {
  const s = net.createServer();
  s.once("error", () => resolve(false));
  s.listen(port, "127.0.0.1", () => s.close(() => resolve(true)));
});

// --- the transport: what a host does, with fetch ---------------------------------------
async function performAll(session) {
  const jobs = [];
  for (let r = session.api.take(); r; r = session.api.take()) {
    const req = r;
    const headers = {};
    for (const h of req.headers) {
      const at = h.indexOf(":");
      headers[h.slice(0, at).trim()] = h.slice(at + 1).trim();
    }
    jobs.push(fetch(req.url, {
      method: req.method, headers, redirect: "manual",
      body: req.method === "GET" ? undefined : req.body,
      signal: AbortSignal.timeout(req.timeoutMs),
    }).then(async (res) => session.deliver(req.id, res.status, await res.text(), ""))
      .catch((e) => session.deliver(req.id, 0, "", String(e.cause?.code || e.message))));
  }
  await Promise.all(jobs);
  return jobs.length;
}
async function settle(session, max = 50) {
  for (let i = 0; i < max; i++) {
    const n = await performAll(session);
    if (!n && !session.api.busy()) return;
  }
  throw new Error("the session did not settle: " + session.debugState());
}
function commands(session) {
  const out = [];
  while (session.cmdNames.length) {
    out.push([session.cmdNames.shift(), session.cmdArgs.shift(), session.cmdArgs2.shift()]);
  }
  return out;
}
const mockState = async (url) => (await fetch(url + "/__test/state")).json();

// =====================================================================================
console.log("JVal");
await test("parses and writes objects, arrays, numbers, escapes", () => {
  const v = JVal.parse('{"a": [1, 2.5, -3, true, false, null], "s": "x\\"y\\\\z\\n\\u00e4\\ud83d\\ude00", "t": 1790000000000, "e": {}}');
  assert.equal(v.valid(), true);
  assert.equal(v.get("a").len(), 6);
  assert.equal(v.get("a").at(1).num, 2.5);
  assert.equal(v.get("s").str, 'x"y\\z\nä\u{1F600}');
  assert.equal(v.numOr("t", 0), 1790000000000);
  const back = JSON.parse(v.toJson());
  assert.deepEqual(back, { a: [1, 2.5, -3, true, false, null], s: 'x"y\\z\nä\u{1F600}', t: 1790000000000, e: {} });
});
await test("keeps UTF-8 text as it is and refuses broken JSON", () => {
  const v = JVal.parse('{"name":"Päivitys – ok 😀"}');
  assert.equal(v.strOr("name", ""), "Päivitys – ok 😀");
  assert.equal(JVal.parse("{\"a\":").valid(), false);
  assert.equal(JVal.parse("[1,]").valid(), false);
  assert.equal(JVal.parse("tru").valid(), false);
  assert.equal(JVal.numText(1790000000123), "1790000000123");
  assert.equal(JVal.numText(-42), "-42");
  assert.equal(JVal.quote("a\tb\u0001"), '"a\\tb\\u0001"');
});

// =====================================================================================
console.log("TextBuffer");
await test("typing, Enter keeps the indentation, Backspace joins lines", () => {
  const b = new TextBuffer();
  b.setText("# Title\n\n  - item");
  b.moveTo(2, 8, false);
  b.newline();
  assert.equal(b.getText(), "# Title\n\n  - item\n  ");
  b.type("next");
  assert.equal(b.lineAt(3), "  next");
  b.moveTo(3, 0, false);
  b.backspace();
  assert.equal(b.getText(), "# Title\n\n  - item  next");
  assert.equal(b.caretLine, 2);
  assert.equal(b.caretCol, 8);
});
await test("columns are characters: ä and emoji are one column each", () => {
  const b = new TextBuffer();
  b.setText("äö😀x");
  assert.equal(b.lineLength(0), 4);
  b.moveTo(0, 3, false);
  b.type("Å");
  assert.equal(b.getText(), "äö😀Åx");
  b.backspace();
  b.backspace();
  assert.equal(b.getText(), "äöx");
  b.right(true);
  assert.equal(b.selectedText(), "x");
  b.left(false);
  b.left(true);
  assert.equal(b.selectedText(), "ö");
});
await test("selection across lines, cut, paste, delete forward", () => {
  const b = new TextBuffer();
  b.setText("one\ntwo\nthree");
  b.moveTo(0, 1, false);
  b.moveTo(2, 2, true);
  assert.equal(b.selectedText(), "ne\ntwo\nth");
  const cut = b.cut();
  assert.equal(cut, "ne\ntwo\nth");
  assert.equal(b.getText(), "oree");
  b.paste("NE\nTH");
  assert.equal(b.getText(), "oNE\nTHree");
  b.docStart(false);
  b.deleteForward();
  assert.equal(b.getText(), "NE\nTHree");
  b.end(false);
  b.deleteForward();
  assert.equal(b.getText(), "NETHree");
});
await test("up / down keep the column; home goes to the indentation first", () => {
  const b = new TextBuffer();
  b.setText("abcdefgh\nab\n    abcdefgh");
  b.moveTo(0, 6, false);
  b.down(false);
  assert.equal(b.caretCol, 2);
  b.down(false);
  assert.equal(b.caretCol, 6);
  b.home(false);
  assert.equal(b.caretCol, 4);
  b.home(false);
  assert.equal(b.caretCol, 0);
  b.end(false);
  b.wordLeft(false);
  assert.equal(b.caretCol, 4);
  b.vertical(-10, false);
  assert.equal(b.caretLine, 0);
  b.vertical(10, true);
  assert.equal(b.caretLine, 2);
  assert.equal(b.hasSelection(), true);
});
await test("undo and redo: a run of typing is one step", () => {
  const b = new TextBuffer();
  b.setText("x");
  b.docEnd(false);
  b.type("a");
  b.type("b");
  b.type("c");
  b.newline();
  b.type("d");
  assert.equal(b.getText(), "xabc\nd");
  b.undo();
  assert.equal(b.getText(), "xabc\n");
  b.undo();
  assert.equal(b.getText(), "xabc");
  b.undo();
  assert.equal(b.getText(), "x");
  b.redo();
  assert.equal(b.getText(), "xabc");
  b.selectAll();
  b.type("z");
  assert.equal(b.getText(), "z");
  b.undo();
  assert.equal(b.getText(), "xabc");
});

// =====================================================================================
console.log("Settings, ApiClient");
await test("settings round trip", () => {
  const s = new Settings();
  const e = s.upsert("https://slides.example.com:8443");
  e.token = "t";
  e.refresh = "r";
  e.ca = "ab12";
  s.upsert("http://localhost:8080");
  s.select("http://localhost:8080");
  const t = new Settings();
  assert.equal(t.load(s.toJson()), true);
  assert.equal(t.count(), 2);
  assert.equal(t.current, 1);
  assert.equal(t.at(0).name, "slides.example.com:8443");
  assert.equal(t.at(0).signedInWithOAuth(), true);
  assert.equal(t.at(0).ca, "ab12");
  assert.equal(t.load("not json"), false);
  assert.equal(t.count(), 0);
});
await test("addresses and URL encoding", () => {
  assert.equal(ApiClient.normalizeBase(" localhost:8080/ "), "http://localhost:8080");
  assert.equal(ApiClient.normalizeBase("https://x.example/api/v1/"), "https://x.example");
  assert.equal(ApiClient.enc("a b/ä~"), "a%20b%2F%C3%A4~");
  assert.equal(ApiClient.enc("😀"), "%F0%9F%98%80");
  assert.equal(Session.fingerprintText("abcdef12"), "AB:CD:EF:12");
  assert.equal(Session.dateText(Date.UTC(2026, 9, 7)), "2026-10-07");
  assert.equal(Session.ago(1000000000, 1000000000 - 5 * 60000), "5 min ago");
});

// =====================================================================================
console.log("Session against the mock server (token)");
const tok = await startMock(["--port", String(await freePort()), "--auth", "token", "--token", "secret"]);
await test("a wrong token is refused, the right one connects and lists the decks", async () => {
  const s = new Session();
  s.connectTo(tok.url, "wrong", false);
  await settle(s);
  assert.equal(s.screen, "connect");
  assert.match(s.message, /not accepted/);
  s.connectTo(tok.url, "secret", false);
  await settle(s);
  assert.equal(s.screen, "decks");
  assert.equal(s.userName, "Tero Tester");
  assert.equal(s.deckCount(), 3);
  assert.equal(s.deckAt(0).name, "Sliqtly Editor sample");
  assert.equal(s.deckAt(0).slides, 5);
  const saved = commands(s).filter(([n]) => n === "save-settings");
  assert.ok(saved.length >= 1);
  const t = new Settings();
  t.load(s.settings.toJson());
  assert.equal(t.currentServer().token, "secret");
});
await test("no token at all: the server says it needs one", async () => {
  const s = new Session();
  s.connectTo(tok.url, "", false);
  await settle(s);
  assert.equal(s.screen, "connect");
  assert.match(s.message, /needs a token/);
});
let shared;
await test("open a deck: its Markdown and the server's display lists", async () => {
  const s = new Session();
  shared = s;
  s.connectTo(tok.url, "secret", false);
  await settle(s);
  s.openDeck("sample");
  await settle(s);
  assert.equal(s.screen, "editor");
  assert.match(s.buffer.getText(), /# Sliqtly Editor/);
  assert.equal(s.viewSlides, 5);
  assert.ok(Math.abs(s.viewW - 960) < 1);
  assert.equal(JSON.parse(s.viewBody).lists.length, 5);
  assert.equal(s.isDirty(), false);
});
await test("the preview follows the caret from slide to slide", () => {
  const s = shared;
  const lines = s.buffer.getText().split("\n");
  const at = (re) => lines.findIndex((l) => re.test(l));
  assert.equal(s.slideOfLine(0), 0);
  assert.equal(s.slideOfLine(at(/^# Sliqtly Editor/)), 0);
  assert.equal(s.slideOfLine(at(/^## Why/)), 1);
  assert.equal(s.slideOfLine(at(/^## Päivitys/) + 2), 2);
  assert.equal(s.slideOfLine(lines.length - 1), 4);
  s.buffer.moveTo(at(/^## Numbers/), 0, false);
  s.followCaret();
  assert.equal(s.slide, 3);
});
await test("edit and save (ifVersion); the preview is fetched again", async () => {
  const s = shared;
  const before = s.deckVersion;
  s.buffer.docEnd(false);
  s.buffer.type("\n## Uusi dia\n\nHyvää päivää äö 😀");
  assert.equal(s.isDirty(), true);
  const views = (await mockState(tok.url)).stats.views;
  s.save();
  await settle(s);
  assert.equal(s.isDirty(), false);
  assert.notEqual(s.deckVersion, before);
  const st = await mockState(tok.url);
  const d = st.decks.find((x) => x.id === "sample");
  assert.match(d.markdown, /Hyvää päivää äö 😀/);
  assert.equal(st.stats.views, views + 1);
  assert.equal(s.viewSlides, 6);
});
await test("409: changed on the server; Reload takes theirs", async () => {
  const s = shared;
  await fetch(tok.url + "/__test/bump/sample", { method: "POST" });
  s.buffer.docEnd(false);
  s.buffer.type("\nmine");
  s.save();
  await settle(s);
  assert.equal(s.dialog, "conflict");
  assert.equal(s.conflictCount, 1);
  assert.equal(s.isDirty(), true);
  s.conflictReload();
  await settle(s);
  assert.equal(s.dialog, "");
  assert.equal(s.isDirty(), false);
  assert.match(s.buffer.getText(), /Edited elsewhere/);
  assert.doesNotMatch(s.buffer.getText(), /\nmine$/);
});
await test("409 again; Overwrite saves ours over theirs", async () => {
  const s = shared;
  await fetch(tok.url + "/__test/bump/sample", { method: "POST" });
  s.buffer.docEnd(false);
  s.buffer.type("\nmine wins");
  s.save();
  await settle(s);
  assert.equal(s.dialog, "conflict");
  s.conflictOverwrite();
  await settle(s);
  assert.equal(s.dialog, "");
  assert.equal(s.isDirty(), false);
  const d = (await mockState(tok.url)).decks.find((x) => x.id === "sample");
  assert.match(d.markdown, /mine wins$/);
  assert.equal((d.markdown.match(/Edited elsewhere/g) || []).length, 1);
});
await test("leaving with unsaved changes asks; New and Delete", async () => {
  const s = shared;
  s.buffer.type("x");
  s.backToDecks();
  assert.equal(s.dialog, "discard");
  s.discardAndLeave();
  await settle(s);
  assert.equal(s.screen, "decks");
  s.newDeck("Uusi esitys äö");
  await settle(s);
  assert.equal(s.screen, "editor");
  assert.equal(s.deckName, "Uusi esitys äö");
  assert.equal(s.deckCount(), 4);
  const id = s.deckId;
  s.backToDecks();
  await settle(s);
  s.askDelete(id);
  assert.equal(s.dialog, "delete");
  s.confirmDelete();
  await settle(s);
  assert.equal(s.deckCount(), 3);
  assert.equal(s.indexOfDeck(id), -1);
});
await test("a server that is not there", async () => {
  const s = new Session();
  s.connectTo("http://127.0.0.1:" + (await freePort()), "", false);
  await settle(s);
  assert.equal(s.screen, "connect");
  assert.match(s.message, /Cannot reach/);
});

// =====================================================================================
console.log("Session against the mock server (OAuth)");
const oa = await startMock(["--port", String(await freePort()), "--auth", "oauth", "--token", "secret"]);
await test("sign in: PKCE S256 through /oauth/authorize and /oauth/token", async () => {
  const s = new Session();
  s.platform = "mac";
  s.connectTo(oa.url, "", false);
  await settle(s);
  assert.equal(s.needsSignIn, true);
  assert.equal(s.oauth, true);
  assert.equal(s.provider, "Mock ID");
  s.signIn();
  await settle(s);
  const cmd = commands(s).find(([n]) => n === "oauth");
  assert.ok(cmd, "the host is asked to run the browser part");
  // what the native host does: PKCE, a loopback redirect, the browser
  const verifier = crypto.randomBytes(32).toString("base64url");
  const challenge = crypto.createHash("sha256").update(verifier).digest("base64url");
  const redirect = "http://127.0.0.1:" + (await freePort()) + "/callback";
  const url = s.oauthAuthorizeUrl(challenge, "st4te", redirect);
  assert.match(url, /client_id=sliqtly-desktop/);
  assert.match(url, /code_challenge_method=S256/);
  const res = await fetch(url, { redirect: "manual" });
  assert.equal(res.status, 302);
  const back = new URL(res.headers.get("location"));
  assert.equal(back.searchParams.get("state"), "st4te");
  s.oauthCode(back.searchParams.get("code"), verifier, redirect);
  await settle(s);
  assert.equal(s.screen, "decks");
  assert.equal(s.deckCount(), 3);
  const e = s.settings.currentServer();
  assert.ok(e.refresh.startsWith("rt-"));
  assert.ok(e.token.startsWith("at-"));
  shared = s;
});
await test("a wrong verifier is refused", async () => {
  const s = new Session();
  s.connectTo(oa.url, "", false);
  await settle(s);
  s.signIn();
  await settle(s);
  const challenge = crypto.createHash("sha256").update("right").digest("base64url");
  const redirect = "http://127.0.0.1:9/callback";
  const res = await fetch(s.oauthAuthorizeUrl(challenge, "x", redirect), { redirect: "manual" });
  const code = new URL(res.headers.get("location")).searchParams.get("code");
  s.oauthCode(code, "wrong", redirect);
  await settle(s);
  assert.equal(s.screen, "connect");
  assert.match(s.message, /refused/);
});
await test("an expired access token: 401, one refresh (rotated), the request again", async () => {
  const s = shared;
  const old = s.settings.currentServer().refresh;
  await fetch(oa.url + "/__test/expire", { method: "POST" });
  s.loadDecks();
  s.openDeck("quarterly");
  await settle(s);
  assert.equal(s.screen, "editor");
  assert.equal(s.deckName, "Quarterly review");
  assert.equal((await mockState(oa.url)).stats.refreshes, 1);
  const e = s.settings.currentServer();
  assert.notEqual(e.refresh, old);
  assert.ok(commands(s).some(([n]) => n === "save-settings"));
});
await test("a refused refresh ends the session: sign in again", async () => {
  const s = shared;
  s.api.refreshToken = "rt-revoked";
  await fetch(oa.url + "/__test/expire", { method: "POST" });
  s.loadDecks();
  await settle(s);
  assert.equal(s.screen, "connect");
  assert.equal(s.needsSignIn, true);
  assert.equal(s.api.signedOut, true);
});
await test("the web client id and its redirect", async () => {
  const s = new Session();
  s.platform = "web";
  s.connectTo(oa.url, "", false);
  await settle(s);
  s.signIn();
  await settle(s);
  const url = s.oauthAuthorizeUrl("c".repeat(43), "s", "http://127.0.0.1:8140/callback.html");
  assert.match(url, /client_id=sliqtly-web/);
  const res = await fetch(url, { redirect: "manual" });
  assert.equal(res.status, 302);
});

// =====================================================================================
console.log("finding servers on this computer");
let probePort = 0;
for (let p = 8090; p >= 8080; p--) if (await portFree(p)) { probePort = p; break; }
if (probePort) {
  await startMock(["--port", String(probePort), "--auth", "none"]);
  await test(`localhost:8080-8090 is searched (a mock on ${probePort})`, async () => {
    const s = new Session();
    s.probe();
    assert.equal(s.probesOut, 11);
    await settle(s);
    assert.equal(s.probed, true);
    const i = s.probeUrls.indexOf("http://localhost:" + probePort);
    assert.ok(i >= 0, "found " + JSON.stringify(s.probeUrls));
    assert.match(s.probeLabels[i], /Sliqtly mock-1/);
  });
} else {
  console.log("  skip  probe: no free port in 8080-8090");
}

// =====================================================================================
console.log("EditorApp");
await test("every screen and dialog builds a display list", async () => {
  const app = new EditorApp();
  app.init(fs.readFileSync(path.join(ROOT, "assets", "editor.css"), "utf8"));
  assert.equal(app.styleErrorCount(), 0, app.styleErrorCount() ? app.styleErrorAt(0) : "");
  app.setViewport(1280, 800);
  app.setNow(Date.now());
  app.start();
  const kinds = (json) => {
    const l = JSON.parse(json);
    assert.ok(Array.isArray(l.cmds) && l.cmds.length > 10);
    return l;
  };
  kinds(app.displayListJson());
  const s = app.s;
  s.connectTo(tok.url, "secret", false);
  await settle(s);
  assert.equal(s.screen, "decks");
  const decks = kinds(app.displayListJson());
  assert.ok(decks.cmds.some((c) => c.text === "Sliqtly Editor sample"));
  app.press("open:0");
  await settle(s);
  assert.equal(s.screen, "editor");
  const ed = kinds(app.displayListJson());
  assert.ok(ed.cmds.some((c) => c.k === 3 && c.text === "# Sliqtly Editor"));
  assert.ok(app.previewVisible());
  assert.ok(app.previewW() > 100 && app.previewH() > 50);
  for (const d of ["trust", "delete", "conflict", "newdeck", "discard"]) {
    s.dialog = d;
    kinds(app.displayListJson());
    assert.equal(app.previewVisible(), false);
  }
  s.dialog = "";
  s.screen = "settings";
  kinds(app.displayListJson());
  const icon = JSON.parse(app.iconDisplayListJson(256));
  assert.ok(icon.cmds.filter((c) => c.k === 6).length >= 4, "the mark is four paths");
});
await test("keys, a click and the clipboard in the editor", async () => {
  const app = new EditorApp();
  app.init(fs.readFileSync(path.join(ROOT, "assets", "editor.css"), "utf8"));
  app.setViewport(1280, 800);
  app.setPlatform("linux");
  const s = app.s;
  s.connectTo(tok.url, "secret", false);
  await settle(s);
  app.press("open:1");
  await settle(s);
  app.displayListJson();
  // a click on the third line, fifth column
  app.pointerDown(app.textX + 4.6 * app.cw, app.textY + 2.5 * app.lineH, false);
  app.pointerUp(0, 0);
  assert.equal(s.buffer.caretLine, 2);
  assert.equal(s.buffer.caretCol, 5);
  app.key("End", true, false, false);
  app.key("c", false, true, false);
  const copy = commands(s).find(([n]) => n === "copy");
  assert.equal(copy[1], "rs and next steps");
  app.key("Home", false, true, false);
  app.typeText("ä");
  assert.equal(s.buffer.lineAt(0), "ä# Quarterly review");
  app.key("z", false, true, false);
  assert.equal(s.buffer.lineAt(0), "# Quarterly review");
  app.key("v", false, true, false);
  assert.ok(commands(s).some(([n]) => n === "paste"));
  app.paste("X\nY");
  assert.equal(s.buffer.lineAt(1), "Y# Quarterly review");
  app.key("s", false, true, false);
  await settle(s);
  assert.equal(s.isDirty(), false);
});
await test("a scripted run", async () => {
  const app = new EditorApp();
  app.init(fs.readFileSync(path.join(ROOT, "assets", "editor.css"), "utf8"));
  app.setViewport(1200, 760);
  app.runScript(`field url ${tok.url}\nfield token secret\npress connect\nwait screen=decks\nselect-deck Workshop notes\nkey Enter\nwait view\nkey End mod\ntype \\n- Lisää äö\nkey s mod\nwait saved\nexpect text~Lisää\nquit`);
  const log = [];
  for (let i = 0; i < 200 && app.scriptRunning(); i++) {
    app.setNow(Date.now());
    app.scriptTick();
    for (const c of commands(app.s)) log.push(c);
    await performAll(app.s);
    await new Promise((r) => setTimeout(r, 5));
  }
  assert.ok(!log.some(([n]) => n === "fail"), JSON.stringify(log.filter(([n]) => n === "fail")));
  assert.ok(log.some(([n]) => n === "quit"));
  const d = (await mockState(tok.url)).decks.find((x) => x.id === "workshop");
  assert.match(d.markdown, /- Lisää äö$/);
});

for (const p of mocks) p.kill();
console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed ? 1 : 0);
