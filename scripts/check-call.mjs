#!/usr/bin/env node
/**
 * node scripts/check-call.mjs [--no-build] [--shots=dir]
 *
 * A call on a deck, end to end: the folder server built from the sources
 * (as npm run serve builds it) and started on an empty folder, two browsers
 * with fake microphones opening the same deck in Edit mode. One starts the
 * call from the bar, the other joins it; each hears the other through the
 * server, the host mutes the other, one leaves, and the deck's room chat
 * was told. Then the same port over https:// with the server's own
 * certificate (Chromium told to accept it: installing a CA is the person's
 * step, not a test's).
 *
 *   --no-build  use mcp-go/dist/sliqtly-server as last built
 *   --shots=dir screenshots of both pages in the call
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import net from "node:net";
import { spawn, spawnSync } from "node:child_process";
import { root, log } from "./lib.mjs";
import { build } from "./build.mjs";

const has = (name) => process.argv.includes(`--${name}`);
const shotsArg = process.argv.find((a) => a.startsWith("--shots="));
const shots = shotsArg ? path.resolve(shotsArg.split("=")[1]) : "";
const mcp = path.join(root, "mcp-go");
const bin = path.join(mcp, "dist", "sliqtly-server");

function run(what, cmd, args, opts = {}) {
  log(`${what.padEnd(6)} ${[cmd, ...args].join(" ")}`);
  const r = spawnSync(cmd, args, { stdio: "inherit", ...opts });
  if (r.status !== 0) throw new Error(`${what} failed`);
}

function chromiumPath() {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const direct = path.join(process.env.PLAYWRIGHT_BROWSERS_PATH || "/opt/pw-browsers", "chromium");
  return fs.existsSync(direct) && fs.statSync(direct).isFile() ? direct : undefined;
}

function freePort() {
  return new Promise((resolve) => {
    const s = net.createServer();
    s.listen(0, "127.0.0.1", () => {
      const p = s.address().port;
      s.close(() => resolve(p));
    });
  });
}

// a mono 16-bit WAV of a sine tone, `secs` long (the fake capture loops it)
function toneWav(hz, secs, rate = 48000) {
  const n = rate * secs;
  const b = Buffer.alloc(44 + n * 2);
  b.write("RIFF", 0);
  b.writeUInt32LE(36 + n * 2, 4);
  b.write("WAVEfmt ", 8);
  b.writeUInt32LE(16, 16);
  b.writeUInt16LE(1, 20);
  b.writeUInt16LE(1, 22);
  b.writeUInt32LE(rate, 24);
  b.writeUInt32LE(rate * 2, 28);
  b.writeUInt16LE(2, 32);
  b.writeUInt16LE(16, 34);
  b.write("data", 36);
  b.writeUInt32LE(n * 2, 40);
  for (let i = 0; i < n; i += 1) b.writeInt16LE(Math.round(Math.sin((2 * Math.PI * hz * i) / rate) * 12000), 44 + i * 2);
  return b;
}

let failures = 0;
function check(name, ok, note = "") {
  if (!ok) failures += 1;
  log(`${ok ? "ok  " : "FAIL"} ${name}${note ? `  (${note})` : ""}`);
}

async function waitFor(what, fn, ms = 20000) {
  const end = Date.now() + ms;
  for (;;) {
    try {
      const v = await fn();
      if (v) return v;
    } catch (_) { /* not yet */ }
    if (Date.now() > end) throw new Error(`no ${what}`);
    await new Promise((r) => setTimeout(r, 150));
  }
}

const { chromium } = await import("playwright-core").catch(() => {
  log("playwright-core is not installed: npm install");
  process.exit(1);
});

if (!has("no-build")) {
  build();
  run("gen", "go", ["generate"], { cwd: mcp });
  fs.mkdirSync(path.dirname(bin), { recursive: true });
  run("go", "go", ["build", "-o", bin, "."], { cwd: mcp, env: { ...process.env, CGO_ENABLED: "0" } });
}

const data = fs.mkdtempSync(path.join(os.tmpdir(), "sliqtly-call-"));
const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const server = spawn(bin, ["-data", data, "-port", String(port), "-listen", "local"], { stdio: ["ignore", "inherit", "inherit"] });
let browser = null;
try {
  await waitFor("the server", async () => (await fetch(base + "/api/status")).ok);
  const made = await (await fetch(base + "/api/shares", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "Call check", md: "# Weekly\n\n- One\n- Two\n" }),
  })).json();
  const id = made.id;
  check("a deck on the server", !!id, id);

  // the fake microphones say a steady tone (Chromium's own beeps are too
  // short to be caught between level readings)
  const wav = path.join(data, "tone.wav");
  fs.writeFileSync(wav, toneWav(440, 3));
  browser = await chromium.launch({
    executablePath: chromiumPath(),
    args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist",
      "--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", `--use-file-for-fake-audio-capture=${wav}`, "--autoplay-policy=no-user-gesture-required"],
  });
  // two people: their own browsers (names, colours and ids kept apart)
  const open = async (who) => {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, permissions: ["microphone"] });
    const page = await ctx.newPage();
    page.on("pageerror", (e) => log(`${who}: ${e.message}`));
    await page.goto(`${base}/s/${id}?edit`);
    await waitFor(`${who}'s Call button`, () => page.evaluate(() => {
      const b = document.getElementById("collabCall");
      return !!b && !b.hidden;
    }));
    return page;
  };
  const a = await open("Ada");
  const b = await open("Bo");
  const label = (p) => p.evaluate(() => document.getElementById("collabCall").textContent);
  const state = (p) => p.evaluate(() => window.__app.meetState());
  const client = (p) => p.evaluate(() => window.__meet.client());
  const press = (p) => p.evaluate(() => document.getElementById("collabCall").click());
  const act = (p, target) => p.evaluate((t) => { window.__app.meetAct(t); window.__handleRequests(); }, target);
  const ca = await client(a);
  const cb = await client(b);

  check("no call yet: the bar says Call", (await label(a)).includes("Call") && !(await label(a)).includes("·"), await label(a));
  await press(a);
  await waitFor("Ada in the call", async () => (await state(a)) === "joined");
  check("Ada started the call", true);
  await waitFor("Bo told of it", async () => (await label(b)).includes("Join call · 1"));
  check("Bo's bar offers to join", true, await label(b));
  await press(b);
  await waitFor("Bo in the call", async () => (await state(b)) === "joined");
  check("both in: In call · 2", (await label(a)).includes("· 2") && (await label(b)).includes("· 2"), `${await label(a)} / ${await label(b)}`);

  // each hears the other (the fake microphone beeps), never itself
  await waitFor("Bo hearing Ada", () => b.evaluate((c) => window.__meet.heard().includes(c), ca));
  await waitFor("Ada hearing Bo", () => a.evaluate((c) => window.__meet.heard().includes(c), cb));
  check("each hears the other", true);
  check("nobody hears themselves", !(await a.evaluate((c) => window.__meet.heard().includes(c), ca)));
  const loud = await waitFor("Bo's voice loud on Ada's page", () => a.evaluate((c) => {
    const l = window.__meet.loudest.get(c) || 0;
    return l > 0.08 ? l : 0;
  }, cb));
  check("Bo's voice reaches Ada's speakers", loud > 0.08, loud.toFixed(2));
  if (shots) {
    fs.mkdirSync(shots, { recursive: true });
    await a.screenshot({ path: path.join(shots, "call-ada.png") });
    await b.screenshot({ path: path.join(shots, "call-bo.png") });
  }
  const panel = await a.evaluate(() => window.__app.panels.callOpen);
  check("the people show on joining", panel === true);

  // the host mutes Bo; Bo's own microphone stops too
  await act(a, "pn-call-p-1-mute");
  await waitFor("Bo muted", () => b.evaluate(() => window.__meet.mutedHere));
  const muted = await a.evaluate((c) => window.__meet.members.find((m) => m.client === c)?.muted, cb);
  check("the host muted Bo", muted === true);
  // Bo unmutes himself
  await act(b, "pn-call-mic");
  await waitFor("Bo unmuted", () => a.evaluate((c) => window.__meet.members.find((m) => m.client === c)?.muted === false, cb));
  check("only Bo unmutes Bo", true);

  // Ada leaves: Bo is the host, the call goes on
  await act(a, "pn-call-leave");
  await waitFor("Ada out", async () => (await state(a)) === "idle");
  await waitFor("Ada's bar offers to join again", async () => (await label(a)).includes("Join call · 1"));
  const host = await b.evaluate((c) => window.__meet.members.find((m) => m.client === c)?.host, cb);
  check("Ada left, Bo is the host", host === true);
  check("Ada hears nobody now", (await a.evaluate(() => window.__meet.heard().length)) === 0);

  // the room's chat was told
  const chat = await (await fetch(base + "/api/rooms/read_room_chat", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ room_id: "general" }),
  })).json();
  const said = JSON.stringify(chat.messages || []);
  check("the deck's room chat says a call started", said.includes("started a call on [[slides:" + id + "]]"), said.slice(0, 160));
  await act(b, "pn-call-leave");
  await waitFor("the call over", async () => (await label(a)) === "📞 Call");
  const after = JSON.stringify((await (await fetch(base + "/api/rooms/read_room_chat", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ room_id: "general" }),
  })).json()).messages || []);
  check("and that it ended, with who took part", /Call on \[\[slides:[A-Za-z0-9]+\]\] ended/.test(after), after.slice(0, 200));

  // the same port over https://, the page a secure context (the microphone allowed)
  const tls = await browser.newContext({ ignoreHTTPSErrors: true });
  const tp = await tls.newPage();
  await tp.goto(`https://127.0.0.1:${port}/ca`);
  const secure = await tp.evaluate(() => window.isSecureContext && location.protocol === "https:");
  check("https:// on the server's own port", secure);
  check("/ca shows the fingerprint", /SHA-256 ([0-9A-F]{2}:){31}[0-9A-F]{2}/.test(await tp.content()));
} catch (e) {
  failures += 1;
  log(`FAIL ${e.message}`);
} finally {
  if (browser) await browser.close();
  server.kill();
  fs.rmSync(data, { recursive: true, force: true });
}
log(failures ? `${failures} failed` : "all passed");
process.exit(failures ? 1 : 0);
