#!/usr/bin/env node
/**
 * npm run check:editor: the editor as sliqtly.com serves it at /editor to a
 * signed-in user (mcp-go/editor.go), against stand-ins for the server's
 * gate and for Firebase kept here.
 *
 *   - the page and every file it loads come from under /editor/ (Hosting's
 *     own files have none of the editor), with no request failing
 *   - a deck saved to the cloud is first taken under the license
 *     (/editor/api/claim) and its address is /editor/s/{id}?edit
 *   - a reload there opens it from the cloud
 *   - with the Trial's two presentations taken, a new deck is not saved to
 *     the cloud and the Files tab says why
 *   - signed out in the page, it goes back through the sign-in page
 *
 * Run after `npm run build`. Chromium: $CHROMIUM_PATH, Playwright's
 * (/opt/pw-browsers), or the installed Chrome.
 */
import fs from "node:fs";
import path from "node:path";
import http from "node:http";
import { distDir, log } from "./lib.mjs";

const { chromium } = await import("playwright-core").catch(() => {
  log("playwright-core is not installed: npm install");
  process.exit(1);
});

function chromiumOpts() {
  if (process.env.CHROMIUM_PATH) return { executablePath: process.env.CHROMIUM_PATH };
  const direct = path.join(process.env.PLAYWRIGHT_BROWSERS_PATH || "/opt/pw-browsers", "chromium");
  if (fs.existsSync(direct) && fs.statSync(direct).isFile()) return { executablePath: direct };
  return { channel: "chrome" };
}

let failures = 0;
function check(name, ok, note = "") {
  if (!ok) failures += 1;
  log(`${ok ? "ok  " : "FAIL"} ${name}${note ? `  (${note})` : ""}`);
}

// the gate as editor.go keeps it: one user, their license, signed in or not
const gate = { signedIn: true, license: { plan: "trial", maxDocs: 2, docs: [], canEdit: true }, claims: [], signouts: 0 };
const types = { ".html": "text/html", ".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".json": "application/json", ".csv": "text/csv", ".md": "text/markdown", ".svg": "image/svg+xml", ".png": "image/png", ".ttf": "font/ttf" };
const outside = []; // requests for the editor's files outside /editor/

const server = http.createServer(async (req, res) => {
  const u = new URL(req.url, "http://x");
  const p = decodeURIComponent(u.pathname);
  const json = (status, v) => { res.writeHead(status, { "content-type": "application/json" }); res.end(JSON.stringify(v)); };
  if (p.startsWith("/editor/api/")) {
    const op = p.slice("/editor/api/".length);
    let body = "";
    for await (const c of req) body += c;
    if (op === "signout") { gate.signedIn = false; gate.signouts += 1; return json(200, { ok: true }); }
    if (op === "session") return json(200, { uid: "u1", license: gate.license });
    if (!gate.signedIn) return json(401, { code: "signed-out" });
    if (op === "license") return json(200, { license: gate.license });
    if (op === "claim") {
      const { id } = JSON.parse(body || "{}");
      gate.claims.push(id);
      const l = gate.license;
      if (!l.docs.includes(id)) {
        if (l.docs.length >= l.maxDocs) return json(403, { code: "no-edit-right", why: "full", license: l });
        l.docs.push(id);
      }
      return json(200, { license: l });
    }
    return json(404, {});
  }
  if (p === "/editor/" || /^\/editor\/s\/[A-Za-z0-9]+\/?$/.test(p)) {
    res.writeHead(200, { "content-type": "text/html" });
    if (!gate.signedIn) return res.end("<!doctype html><title>Sliqtly editor</title><h1>Sign in to the editor</h1>");
    const info = JSON.stringify({ uid: "u1", email: "t@example.com", license: gate.license }).replace(/&/g, "&amp;").replace(/"/g, "&quot;");
    const html = fs.readFileSync(path.join(distDir, "index.html"), "utf8")
      .replace('<base href="/" />', '<base href="/editor/" />')
      .replace("</head>", `<meta name="sliqtly-editor" content="${info}" />\n</head>`);
    return res.end(html);
  }
  if (p.startsWith("/editor/")) {
    const file = path.join(distDir, p.slice("/editor/".length));
    if (!gate.signedIn || !file.startsWith(distDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
      res.writeHead(404);
      return res.end();
    }
    res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream" });
    return fs.createReadStream(file).pipe(res);
  }
  if (p === "/api/hit") { res.writeHead(204); return res.end(); }
  if (p !== "/favicon.svg") outside.push(p);
  res.writeHead(404);
  res.end();
});
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const base = `http://127.0.0.1:${server.address().port}`;

const browser = await chromium.launch({ ...chromiumOpts(), args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"] });
try {
  const fakeDb = new Map();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 820 } });
  await ctx.exposeFunction("__fakeFirebase", async (op, a) => {
    if (op === "get") return fakeDb.get(a.k) ?? null;
    if (op === "query") return [...fakeDb.entries()].filter(([k, d]) => k.startsWith(a.c + "/") && d[a.f] === a.v).map(([k, d]) => ({ id: k.slice(a.c.length + 1), d }));
    if (op === "set") fakeDb.set(a.k, a.merge ? { ...fakeDb.get(a.k), ...a.data } : a.data);
    else if (op === "update") fakeDb.set(a.k, { ...fakeDb.get(a.k), ...a.data });
    return null;
  });
  // Firebase as the page uses it, signed in as u1 until signOut()
  const fake = `(() => {
    let user = { uid: "u1", displayName: "Testi", email: "t@example.com", getIdToken: async () => "token" };
    const listeners = [];
    const call = (op, a) => window.__fakeFirebase(op, a);
    const ref = (c, id) => ({
      set: (data, o) => call("set", { k: c + "/" + id, data, merge: !!(o && o.merge) }),
      update: (data) => call("update", { k: c + "/" + id, data }),
      get: async () => { const d = await call("get", { k: c + "/" + id }); return { exists: d != null, data: () => d }; },
    });
    const query = (c, f, v) => ({ limit: () => query(c, f, v), get: async () => ({ docs: (await call("query", { c, f, v })).map((x) => ({ id: x.id, data: () => x.d })) }) });
    const db = { collection: (c) => ({ doc: (id) => ref(c, id), where: (f, op, v) => query(c, f, v) }), runTransaction: async (fn) => fn({ get: (r) => r.get(), update: (r, d) => r.update(d) }) };
    const firestore = () => db;
    firestore.FieldValue = { serverTimestamp: () => Date.now() };
    const storage = () => ({ ref: () => ({ put: async () => {}, getDownloadURL: async () => "", delete: async () => {}, listAll: async () => ({ items: [] }) }) });
    const auth = () => ({
      onAuthStateChanged(cb) { listeners.push(cb); setTimeout(() => cb(user), 0); return () => {}; },
      onIdTokenChanged(cb) { return () => {}; },
      get currentUser() { return user; },
      async signOut() { user = null; for (const cb of listeners) cb(null); },
    });
    window.firebase = { auth, firestore, storage };
  })();`;
  await ctx.route(/^https:\/\/www\.gstatic\.com\/firebasejs\//, (r) => r.fulfill({ contentType: "text/javascript", body: /app-compat/.test(r.request().url()) ? fake : "" }));
  await ctx.route(/\/__\/firebase\/init\.js/, (r) => r.fulfill({ contentType: "text/javascript", body: "" }));

  const pg = await ctx.newPage();
  const errors = [];
  const failed = [];
  pg.on("pageerror", (e) => errors.push(e.message));
  pg.on("response", (r) => { if (r.status() >= 400 && !/favicon/.test(r.url())) failed.push(r.status() + " " + r.url()); });
  const started = async () => {
    await pg.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pg.waitForTimeout(400);
  };

  await pg.goto(base + "/editor/");
  await started();
  check("the editor starts at /editor/, its files all from under /editor/", outside.length === 0 && failed.length === 0 && errors.length === 0,
    JSON.stringify({ outside: outside.slice(0, 5), failed: failed.slice(0, 5), errors: errors.slice(0, 3) }));

  // a change: taken under the license, saved, the address names it
  await pg.evaluate(() => window.__app.setSource("# Ensimmäinen\n\n## Dia\n"));
  await pg.waitForFunction(() => /^\/editor\/s\/[A-Za-z0-9]+$/.test(location.pathname), null, { timeout: 15000 }).catch(() => {});
  const shares = () => [...fakeDb.keys()].filter((k) => k.startsWith("shares/")).map((k) => k.slice(7));
  const first = { shares: shares(), claims: [...gate.claims], at: await pg.evaluate(() => location.pathname + location.search) };
  const id = first.shares[0] || "";
  check("a deck is taken under the license, then saved to the cloud at /editor/s/{id}?edit",
    first.shares.length === 1 && gate.license.docs.includes(id) && first.claims[0] === id && first.at === "/editor/s/" + id + "?edit", JSON.stringify(first));

  await pg.reload();
  await started();
  const back = await pg.evaluate(() => window.__app.source());
  check("a reload at /editor/s/{id}?edit opens it from the cloud", back.includes("# Ensimmäinen"), back.slice(0, 60));

  // the Trial full: a new deck stays in this browser, and the page says so
  gate.license.docs.push("someOther1");
  const before = shares().length;
  await pg.goto(base + "/editor/");
  await started();
  // a deck of its own, not the one in the cloud
  const made = await pg.evaluate(() => window.sliqtly.share({ deckId: "d-third", name: "Kolmas", md: "# Kolmas\n", theme: "", css: null, files: [] })
    .then(() => "saved", (e) => e.code + ": " + e.message));
  check("with the Trial's two taken, a third is not saved to the cloud, and why is said",
    shares().length === before && /^no-edit-right: .*2/.test(made), made);
  const note = await pg.evaluate(() => document.getElementById("pro")?.title || "");
  check("the PRO button tells the license", /Trial: 2 \/ 2/.test(note), note);

  // signed out in the page: back through the sign-in page
  await pg.evaluate(() => window.sliqtly.auth().then((a) => a.signOut()));
  await pg.waitForFunction(() => /Sign in to the editor/.test(document.body.textContent), null, { timeout: 10000 }).catch(() => {});
  const out = await pg.evaluate(() => document.body.textContent.slice(0, 80));
  check("signing out in the editor goes back to the sign-in page", gate.signouts === 1 && /Sign in to the editor/.test(out), JSON.stringify({ signouts: gate.signouts, out }));
} finally {
  await browser.close();
  server.close();
}
if (failures) {
  log(`${failures} check(s) failed`);
  process.exit(1);
}
log("check:editor ok");
