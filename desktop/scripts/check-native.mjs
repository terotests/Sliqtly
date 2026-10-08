#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// A smoke check of the native editor (after `npm run old:native`), headless, against
// test/mock-server.mjs. Each run is the real binary driven by a script of the
// app's own steps (EditorApp.runScript), with a settings folder of its own:
//
//   1. token     connect (found on localhost:8080-8090 when a port there is
//                free), list the decks, open one with the server's display
//                list in the preview, type (UTF-8: ä ö), save, an edit made
//                elsewhere → 409 → Overwrite, back to the list; screenshots
//   2. oauth     Sign in: PKCE through the loopback redirect the app answers
//                (the check plays the browser), then the deck list
//   3. https     a server with its own CA: the fingerprint dialog, pinning
//                /ca.crt, then the deck list over verified TLS (needs openssl)
//
// Screenshots go to native/build/shots/*.png. On Linux without a display it
// runs under xvfb-run.
//
//   node scripts/check-native.mjs [--only token|oauth|https]

import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { ROOT } from "./ranger.mjs";
import { pamToPng, readPam } from "./png.mjs";

// on macOS the bundle's own binary: it finds SDL2.framework inside the .app
const appBin = path.join(ROOT, "native", "build", "Sliqtly Editor.app", "Contents", "MacOS", "sliqtly-editor");
const bin = process.platform === "darwin" && fs.existsSync(appBin) ? appBin : path.join(ROOT, "native", "build", "sliqtly-editor");
if (!fs.existsSync(bin)) { console.error("build it first: npm run old:native"); process.exit(2); }
const SHOTS = path.join(ROOT, "native", "build", "shots");
fs.mkdirSync(SHOTS, { recursive: true });
const only = process.argv.includes("--only") ? process.argv[process.argv.indexOf("--only") + 1] : "";

const fails = [];
const expect = (what, ok, extra = "") => {
  if (!ok) fails.push(what);
  console.log(`  ${ok ? "ok  " : "FAIL"} ${what}${!ok && extra ? "  (" + extra + ")" : ""}`);
};

const portFree = (port) => new Promise((resolve) => {
  const s = net.createServer();
  s.once("error", () => resolve(false));
  s.listen(port, "127.0.0.1", () => s.close(() => resolve(true)));
});
const anyPort = () => new Promise((resolve) => {
  const s = net.createServer().listen(0, "127.0.0.1", () => {
    const p = s.address().port;
    s.close(() => resolve(p));
  });
});

const mocks = [];
function startMock(args) {
  return new Promise((resolve, reject) => {
    const p = spawn(process.execPath, [path.join(ROOT, "test", "mock-server.mjs"), "--quiet", ...args], { stdio: ["ignore", "pipe", "inherit"] });
    mocks.push(p);
    let out = "";
    p.stdout.on("data", (d) => {
      out += d;
      const m = /listening on (\S+)/.exec(out);
      if (m) resolve(m[1]);
    });
    p.on("exit", (code) => reject(new Error("the mock server exited " + code)));
  });
}
const stop = () => { for (const p of mocks) p.kill(); };
process.on("exit", stop);

// Run the editor with a script; `onLine` sees its stdout line by line.
function runEditor(name, script, onLine = () => {}) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "sliqtly-editor-check-"));
  const cfg = path.join(tmp, "config");
  const file = path.join(tmp, "script.txt");
  fs.writeFileSync(file, script.replace(/\{tmp\}/g, tmp));
  let cmd = bin, args = ["--script", file, "--size", "1280x800"];
  if (process.platform === "linux" && !process.env.DISPLAY) {
    args = ["-a", "-s", "-screen 0 1400x900x24 +extension GLX", cmd, ...args];
    cmd = "xvfb-run";
  }
  return new Promise((resolve) => {
    const p = spawn(cmd, args, { env: { ...process.env, SLIQTLY_EDITOR_CONFIG: cfg, SLIQTLY_NO_BROWSER: "1" }, stdio: ["ignore", "pipe", "pipe"] });
    let out = "", err = "";
    const timer = setTimeout(() => p.kill("SIGKILL"), 120000);
    let buf = "";
    p.stdout.on("data", (d) => {
      out += d;
      buf += d;
      let nl;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        onLine(line);
      }
    });
    p.stderr.on("data", (d) => { err += d; });
    p.on("exit", (code) => {
      clearTimeout(timer);
      resolve({ code, out, err, tmp, cfg });
    });
  });
}

// The PAM shots of a run, as PNGs in native/build/shots/<run>-<name>.png.
function keepShots(run, tmp) {
  const made = [];
  for (const f of fs.readdirSync(tmp).filter((f) => f.endsWith(".pam"))) {
    const to = path.join(SHOTS, `${run}-${f.replace(/\.pam$/, ".png")}`);
    pamToPng(path.join(tmp, f), to);
    made.push(to);
  }
  return made;
}

// Is a region of a shot more than one flat colour? (`x, y, w, h` in points of a 1280 px wide window)
function painted(file, x, y, w, h) {
  const { w: W, data } = readPam(file);
  const k = W / 1280;
  const colours = new Set();
  for (let yy = y; yy < y + h; yy += 3) {
    for (let xx = x; xx < x + w; xx += 3) {
      const o = (Math.floor(yy * k) * W + Math.floor(xx * k)) * 4;
      colours.add((data[o] >> 3) * 1024 + (data[o + 1] >> 3) * 32 + (data[o + 2] >> 3));
    }
  }
  return colours.size;
}

const state = async (base) => (await fetch(base.replace("localhost", "127.0.0.1") + "/__test/state")).json();

// --- 1. a token, the whole round ---------------------------------------------------
if (!only || only === "token") {
  console.log("run 1: token");
  let port = 0;
  for (let p = 8090; p >= 8080; p--) if (await portFree(p)) { port = p; break; }
  const found = port > 0;
  if (!found) port = await anyPort();
  const base = await startMock(["--port", String(port), "--auth", "token", "--token", "secret"]);
  const script = `
${found ? "wait probe>=1 10" : "sleep 1"}
shot {tmp}/1-connect.pam
field url ${base}
field token secret
press connect
wait screen=decks
wait idle
shot {tmp}/2-decks.pam
select-deck Sliqtly Editor sample
key Enter
wait view
key Down
key Down
key Down
key Down
key Down
key Down
key Down
key Down
key Down
key Down
key Down
key End
sleep 0.4
shot {tmp}/3-editor.pam
key End mod
type \\n## Uusi dia\\n\\n- Hyvää päivää: äö ÄÖ å\\n- Second point
expect dirty
key s mod
wait saved
wait view
sleep 0.3
shot {tmp}/4-saved.pam
http POST ${base}/__test/bump/sample
wait idle
type \\n- lisää
key s mod
wait dialog=conflict
shot {tmp}/5-conflict.pam
press c-over
wait saved
wait view
expect text~lisää
press back-decks
wait screen=decks
quit
`;
  const r = await runEditor("token", script);
  const shots = keepShots("token", r.tmp);
  expect("the editor ran the script to its end (exit 0)", r.code === 0, `exit ${r.code}: ${r.err.slice(-600)}`);
  const st = await state(base);
  const sample = st.decks.find((d) => d.id === "sample");
  expect("the typed text reached the server (UTF-8 intact)", /- Hyvää päivää: äö ÄÖ å\n- Second point/.test(sample.markdown), JSON.stringify(sample.markdown.slice(-120)));
  expect("409 happened and Overwrite won (the local text replaced the other edit)", st.stats.conflicts >= 1 && /- lisää\s*$/.test(sample.markdown) && !/Edited elsewhere/.test(sample.markdown), JSON.stringify(st.stats) + " " + JSON.stringify(sample.markdown.slice(-80)));
  expect("saves went through", st.stats.saves >= 2, JSON.stringify(st.stats));
  const settings = JSON.parse(fs.readFileSync(path.join(r.cfg, "settings.json"), "utf8"));
  expect("settings.json keeps the server and its token", settings.servers.some((s) => s.url === base && s.token === "secret"));
  const mode = fs.statSync(path.join(r.cfg, "settings.json")).mode & 0o777;
  expect("settings.json is readable by the user only (0600)", mode === 0o600, mode.toString(8));
  if (found) expect(`the probe found the server on localhost:${port}`, /script: wait probe>=1/.test(r.out) && r.code === 0);
  const editor = path.join(r.tmp, "3-editor.pam");
  if (fs.existsSync(editor)) {
    expect("the preview is painted (the server's display list)", painted(editor, 700, 120, 520, 280) > 4);
    expect("the text area is painted", painted(editor, 60, 60, 500, 300) > 4);
  } else expect("the editor screenshot was written", false);
  for (const s of shots) console.log("       " + path.relative(ROOT, s));
}

// --- 2. OAuth sign-in through the loopback ---------------------------------------
if (!only || only === "oauth") {
  console.log("run 2: oauth");
  const base = await startMock(["--port", String(await anyPort()), "--auth", "oauth"]);
  let browser = null;
  const script = `
field url ${base}
press connect
wait msg~sign
shot {tmp}/1-signin.pam
press signin
wait screen=decks 30
wait idle
shot {tmp}/2-decks.pam
quit
`;
  const r = await runEditor("oauth", script, (line) => {
    const m = /^oauth: open (\S+)/.exec(line);
    if (m) {
      // The browser: follow /oauth/authorize to the app's loopback /callback.
      browser = fetch(m[1].replace("localhost", "127.0.0.1"), { redirect: "follow" }).then((res) => res.text()).catch((e) => "error " + e.message);
    }
  });
  const page = browser ? await browser : "";
  keepShots("oauth", r.tmp);
  expect("the app opened the sign-in page (PKCE S256, client sliqtly-desktop)", /code_challenge_method=S256/.test(r.out) && /client_id=sliqtly-desktop/.test(r.out));
  expect("its loopback answered the browser", /Signed in/.test(page), page.slice(0, 200));
  expect("signed in: the deck list (exit 0)", r.code === 0, `exit ${r.code}: ${r.err.slice(-400)}`);
  const settings = JSON.parse(fs.readFileSync(path.join(r.cfg, "settings.json"), "utf8"));
  expect("a refresh token is kept", settings.servers.some((s) => /^rt-/.test(s.refresh)));
}

// --- 3. https with the server's own CA ----------------------------------------------
if ((!only || only === "https") && spawnSync("sh", ["-c", "command -v openssl"]).status === 0) {
  console.log("run 3: https, own CA");
  const base = await startMock(["--port", String(await anyPort()), "--auth", "token", "--token", "secret", "--tls"]);
  const script = `
field url ${base}
field token secret
press connect
wait dialog=trust
shot {tmp}/1-trust.pam
press trust-ok
wait screen=decks 20
quit
`;
  const r = await runEditor("https", script);
  keepShots("https", r.tmp);
  expect("trusted the CA and connected over TLS (exit 0)", r.code === 0, `exit ${r.code}: ${r.err.slice(-400)}`);
  const settings = JSON.parse(fs.readFileSync(path.join(r.cfg, "settings.json"), "utf8"));
  const entry = settings.servers.find((s) => s.url === base);
  expect("the CA fingerprint is kept for that server", !!entry && /^[0-9a-f]{64}$/.test(entry.ca));
  expect("the CA certificate is pinned in the settings folder", !!entry && fs.existsSync(path.join(r.cfg, "ca", entry.ca + ".pem")));
} else if (!only || only === "https") {
  console.log("run 3: skipped (no openssl)");
}

stop();
console.log(fails.length ? `\n${fails.length} check(s) failed` : "\nall checks passed");
process.exit(fails.length ? 1 : 0);
