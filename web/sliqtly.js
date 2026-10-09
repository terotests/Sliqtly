// Sliqtly's PRO: Google sign-in through Firebase Auth, and sharing through
// Firestore and Cloud Storage.
//
// The PRO button sits in the editor's bar (data-canvas makes the canvas bar
// draw it). Signed out, a press opens Google's sign-in; signed in, the
// button carries the user's first name and a press offers to sign out.
//
// Sharing (window.sliqtly.share / loadShare, used by main.js):
//   decks/{deckId}     the presentation, its owner's alone (firestore.rules)
//   shares/{shareId}   a copy made when it is shared, under a short random
//                      id: anyone with the id reads it, nobody can list the
//                      ids, only the owner changes or deletes it
//   shares/{shareId}/… in Storage: the copy's pictures and data files, read
//                      like the copy, written only by its owner
//   users/{uid}/decks/{deckId}/… in Storage: a signed-in user's pictures as
//                      they are added (putFile), the user's alone
// The link is /s/{shareId}.
//
// Firebase comes from Google's CDN; the project's config from Hosting's
// reserved /__/firebase/init.js, so nothing about the project is in this file.
// Served anywhere but Firebase Hosting, sign-in is simply not available.

import { t } from "./i18n.js";
import { storedType } from "./storedtype.js";

const SDK = "https://www.gstatic.com/firebasejs/10.14.1/";
const pro = document.getElementById("pro");
let user = null;
let ready = null;

// --- sliqtly.com/editor --------------------------------------------------------------
// The server sent this page to a signed-in user (mcp-go/editor.go), and says
// who and under which license in <meta name="sliqtly-editor">; elsewhere
// (a server of one's own, the checks) there is no such tag and none of this
// applies. The sign-in the server checks is a cookie holding the user's ID
// token, an hour long: this page sends a fresh one whenever Firebase renews
// it. Signed out here, or another account here, the page goes back through
// the sign-in page.
const gate = (() => {
  try { return JSON.parse(document.querySelector('meta[name="sliqtly-editor"]')?.content || "null"); } catch (_) { return null; }
})();
let license = gate?.license || null;
let licenseAt = Date.now();
// the sign-in page's guard against a loop: in, so cleared
try { sessionStorage.removeItem("sliqtly:editor-entered"); } catch (_) { /* none */ }

function gateCall(op, body) {
  return fetch("/editor/api/" + op, body === undefined
    ? { cache: "no-store" }
    : { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
}

function keepLicense(out) {
  if (out?.license) {
    license = out.license;
    licenseAt = Date.now();
    show();
  }
}

let renewedAt = 0;
async function renewSession(u) {
  const res = await gateCall("session", { idToken: await u.getIdToken() });
  renewedAt = Date.now();
  if (res.ok) keepLicense(await res.json().catch(() => null));
}

let leaving = false;
function gateUser(u) {
  if (!gate || leaving) return;
  if (!u) {
    leaving = true;
    gateCall("signout", {}).finally(() => location.reload());
  } else if (u.uid !== gate.uid) {
    leaving = true;
    renewSession(u).finally(() => location.reload());
  }
}

function gateTokens(a) {
  if (!gate) return;
  a.onIdTokenChanged((u) => {
    if (u && u.uid === gate.uid && !leaving) renewSession(u).catch((e) => console.warn("sign-in not renewed", e));
  });
  // a renewal before the hour is up, also after the tab slept
  const renew = () => {
    if (a.currentUser && Date.now() - renewedAt > 40 * 60000) a.currentUser.getIdToken(true).catch(() => {});
  };
  setInterval(renew, 5 * 60000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) renew(); });
}

function noRight(why) {
  const text = why === "expired"
    ? t("Your license to edit has ended. Your presentations stay yours: you can open, present and export them. Changes are kept in this browser only.")
    : t("Your license lets you edit {n} presentations in the cloud. Changes to this one are kept in this browser only.").replace("{n}", String(license?.maxDocs ?? 2));
  return Object.assign(new Error(text), { code: "no-edit-right" });
}

// The license lets this page change share `id` (taking it under the
// license when there is room), or throws with code "no-edit-right". The
// rules check the same (firestore.rules); this only says so first.
async function editRight(id, again = true) {
  if (!gate) return;
  const fresh = Date.now() - licenseAt < 60000;
  if (license?.canEdit && (license.maxDocs < 0 || license.docs.includes(id))) return;
  if (fresh && license && !license.canEdit) throw noRight("expired");
  if (fresh && license && license.maxDocs >= 0 && license.docs.length >= license.maxDocs) throw noRight("full");
  const res = await gateCall("claim", { id });
  const out = await res.json().catch(() => ({}));
  keepLicense(out);
  if (res.status === 401 && again && user) {
    await renewSession(user);
    return editRight(id, false);
  }
  if (!res.ok) throw out.code === "no-edit-right" ? noRight(out.why) : new Error(out.error || "HTTP " + res.status);
}

// An AI assistant's preview (mcp-go/assets/preview.html) may load scripts only
// from blob: URLs, and gives the page its own loader for that.
function load(src) {
  if (globalThis.__sliqtlyLoadScript) return globalThis.__sliqtlyLoadScript(src);
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = src;
    s.onload = resolve;
    s.onerror = () => reject(new Error("could not load " + src));
    document.head.appendChild(s);
  });
}

let core = null;
let data = null;

// the app and the project's config, once
function firebaseApp() {
  core ??= (async () => {
    await load(SDK + "firebase-app-compat.js");
    await load("/__/firebase/init.js");
    return globalThis.firebase;
  })();
  return core;
}

// Firestore and Storage, loaded only when a share is made or opened
function store() {
  data ??= (async () => {
    const fb = await firebaseApp();
    await Promise.all([load(SDK + "firebase-firestore-compat.js"), load(SDK + "firebase-storage-compat.js")]);
    return { db: fb.firestore(), files: fb.storage() };
  })();
  return data;
}

let authNow = null; // the Auth once it is ready, so a press can use it at once

function auth() {
  ready ??= (async () => {
    const fb = await firebaseApp();
    await load(SDK + "firebase-auth-compat.js");
    const a = fb.auth();
    a.onAuthStateChanged((u) => {
      user = u;
      show();
      gateUser(u);
      window.dispatchEvent(new Event("sliqtly:user"));
    });
    gateTokens(a);
    authNow = a;
    return a;
  })();
  return ready;
}

function show() {
  const first = (user?.displayName || user?.email || "").split(/[\s@]/)[0];
  // signed out it asks to sign in: a bare "PRO" read as being signed in
  pro.textContent = user ? `PRO · ${first}` : t("Sign in");
  pro.title = user ? t("Signed in as ") + (user.displayName || user.email) + licenseNote() : t("Sign in with Google");
}

// the license, after who is signed in: " · Trial: 1 / 2 presentations"
function licenseNote() {
  if (!license) return "";
  const plan = license.plan ? license.plan[0].toUpperCase() + license.plan.slice(1) : "";
  if (!license.canEdit) return " · " + plan + ": " + t("editing has ended");
  if (license.maxDocs < 0) return " · " + plan;
  return " · " + plan + ": " + t("{n} / {max} presentations").replace("{n}", String(license.docs.length)).replace("{max}", String(license.maxDocs));
}

// --- sign-in on phones -------------------------------------------------------------
// Safari (iPhone, iPad) opens Google's window only when it is asked for in
// the press itself: the popup is asked for before anything else is waited on.
// When the browser still blocks it, or the page runs from the home screen
// (where a popup cannot come back), the sign-in goes by redirect instead.
// A redirect comes back through this site's own /__/auth/handler (Firebase
// Hosting serves it on every domain): Safari keeps no storage for
// sliqtly.firebaseapp.com inside sliqtly.com, so a redirect through the
// project's default auth domain would come back signed out. That handler is
// a second, same-site Firebase app whose Google credential then signs in the
// page's own app. Needs https://sliqtly.com/__/auth/handler (and
// sliqtly.web.app's) among the OAuth client's redirect URIs.
const REDIRECT_HOSTS = ["sliqtly.com", "www.sliqtly.com", "sliqtly.web.app", "sliqtly.firebaseapp.com"];
const REDIRECT_FLAG = "sliqtly:redirect";
const standalone = navigator.standalone === true || globalThis.matchMedia?.("(display-mode: standalone)").matches;

function canRedirect() {
  return REDIRECT_HOSTS.includes(location.hostname);
}

// the same-site app the redirect goes through
async function redirectAuth() {
  const fb = await firebaseApp();
  await auth();
  const name = "sliqtly-redirect";
  const app = fb.apps.find((x) => x.name === name) || fb.initializeApp({ ...fb.app().options, authDomain: location.host }, name);
  const a = app.auth();
  await a.setPersistence(fb.auth.Auth.Persistence.NONE);
  return a;
}

async function signInByRedirect(pick) {
  try { sessionStorage.setItem(REDIRECT_FLAG, "1"); } catch (_) { /* the result is still read below */ }
  const a = await redirectAuth();
  await a.signInWithRedirect(google(pick));
}

// Google's sign-in; `pick` asks which account even when one is signed in
function google(pick) {
  const p = new globalThis.firebase.auth.GoogleAuthProvider();
  if (pick) p.setCustomParameters({ prompt: "select_account" });
  return p;
}

// back from Google's page: its credential signs in the page's own app
async function finishRedirect() {
  let flagged = false;
  try { flagged = sessionStorage.getItem(REDIRECT_FLAG) === "1"; sessionStorage.removeItem(REDIRECT_FLAG); } catch (_) { /* none */ }
  if (!flagged || !canRedirect()) return;
  try {
    const r = await (await redirectAuth()).getRedirectResult();
    if (r?.credential) await (await auth()).signInWithCredential(r.credential);
  } catch (e) {
    console.error(e);
    alert(t("Sign-in failed: ") + (e?.message || e));
  }
}

function signInFailed(e, pick) {
  if (e?.code === "auth/popup-closed-by-user" || e?.code === "auth/cancelled-popup-request") return;
  if (e?.code === "auth/popup-blocked" && canRedirect()) {
    signInByRedirect(pick).catch(signInFailed);
    return;
  }
  console.error(e);
  alert(t("Sign-in failed: ") + (e?.message || e));
}

function signIn(a, pick) {
  if (standalone && canRedirect()) return signInByRedirect(pick).catch((e) => signInFailed(e, pick));
  // no await before this: the window opens within the press
  return a.signInWithPopup(google(pick)).catch((e) => signInFailed(e, pick));
}

// Another Google account in place of this one (the owner of a deck opened
// here, signed in under a different account): Google asks which.
function switchAccount() {
  return authNow ? signIn(authNow, true) : auth().then((a) => signIn(a, true));
}

pro.addEventListener("click", () => {
  if (authNow && !user) {
    signIn(authNow);
    return;
  }
  auth().then((a) => {
    if (!user) return signIn(a);
    if (confirm(t("Signed in as ") + (user.displayName || user.email) + "\n\n" + t("Sign out?"))) return a.signOut();
  }).catch(signInFailed);
});

// a session from an earlier visit comes back without a press
auth().catch((e) => console.warn("sign-in not available:", e.message));
finishRedirect();
show();

// a share's id: 10 characters of a-z, A-Z, 0-9 (about 59 bits)
function shortId() {
  const abc = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  const bytes = crypto.getRandomValues(new Uint8Array(10));
  return [...bytes].map((b) => abc[b % abc.length]).join("");
}

// Keeps the deck as its owner's and makes its copy in the cloud, private to
// the owner until Share opens it by link (setVisibility; firestore.rules):
// { deckId, name, md, theme, css, files: [{ path, type, data }] } → the
// share's id. made(id), when given, is told the id as soon as the share
// exists, before its files go.
async function share(deck, made) {
  if (!user) throw new Error("not signed in");
  const { db, files } = await store();
  const fb = globalThis.firebase;
  const now = fb.firestore.FieldValue.serverTimestamp();
  const body = { name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null };
  const id = shortId();
  await editRight(id);
  await db.collection("decks").doc(deck.deckId).set({ ...body, owner: user.uid, updated: now }, { merge: true });
  const doc = db.collection("shares").doc(id);
  // the copy first: Storage lets only the owner it names write its files
  await doc.set({ ...body, owner: user.uid, visibility: "link", deck: deck.deckId, files: [], created: now });
  // the share exists from here: a file that fails names it (e.shareId), so
  // the deck keeps it and the next save sends the files again instead of
  // making another share
  try {
    if (made) await made(id);
    const kept = [];
    for (const f of deck.files || []) {
      const blob = f.data instanceof Blob ? f.data : new Blob([f.data ?? ""], { type: f.type || "text/plain" });
      const ref = files.ref(`shares/${id}/${f.path}`);
      await ref.put(blob, { contentType: storedType(f.type || blob.type) });
      kept.push({ path: f.path, type: f.type || blob.type || "", size: blob.size, url: await ref.getDownloadURL() });
    }
    if (kept.length) await doc.update({ files: kept });
  } catch (e) {
    throw Object.assign(e instanceof Error ? e : new Error(String(e)), { shareId: id });
  }
  return id;
}

// A shared copy: { name, md, theme, css, visibility, files: [{ path, type,
// size, url }] }, or null when there is none by that id. A private one is
// read only by its owner: the sign-in from an earlier visit is waited for
// (a few seconds at most), and anyone else gets an error with code
// "private".
async function loadShare(id) {
  const { db } = await store();
  await Promise.race([signedIn(), new Promise((ok) => setTimeout(ok, 5000))]);
  let snap;
  try {
    snap = await db.collection("shares").doc(id).get();
  } catch (e) {
    if (e?.code === "permission-denied") throw Object.assign(new Error("private"), { code: "private" });
    throw e;
  }
  return snap.exists ? snap.data() : null;
}

// Who reads the owner's share: "private" (the owner only) or "link" (anyone
// with its id).
async function setVisibility(id, visibility) {
  if (!user) throw new Error("not signed in");
  const { db } = await store();
  await db.collection("shares").doc(id).update({ visibility });
}

// The signed-in user's own shares, newest first: [{ id, name, updated }]
// (updated in ms). The rules let an owner list only a query on owner.
async function listMine() {
  if (!user) return [];
  const { db } = await store();
  const snap = await db.collection("shares").where("owner", "==", user.uid).limit(200).get();
  const ms = (v) => (v && typeof v.toMillis === "function" ? v.toMillis() : 0);
  return snap.docs
    .map((d) => ({ id: d.id, name: d.data().name || "", created: ms(d.data().created), updated: ms(d.data().updated) || ms(d.data().created) }))
    .sort((a, b) => b.updated - a.updated);
}

// The signed-in user once the session from an earlier visit is known (null
// when nobody is, or sign-in is not available here).
let known = null;
function signedIn() {
  known ??= auth().then((a) => new Promise((ok) => {
    const off = a.onAuthStateChanged((u) => { off(); ok(u); });
  })).catch(() => null);
  return known;
}

// A PRO deck lives in its share: every change in the editor is written to
// shares/{id}, files to Storage, so /s/{id} and an assistant (mcp-go/) see it.
// since: { md, stamps } as this page last wrote or read it. The share's text
// having moved on from since.md means someone else (an assistant) changed it:
// that is refused with code "changed-elsewhere" rather than written over.
// A file whose stamp is unchanged is not sent again. A file the share got
// elsewhere (not in since.stamps) is kept: this page takes it in on its next
// look at the cloud. Changed files go to Storage first; the share is then
// read again and written in one transaction, and a file this page removed
// leaves Storage only once that has gone through. → the share's files
const elsewhere = (msg) => Object.assign(new Error(msg), { code: "changed-elsewhere" });
async function saveShare(id, deck, since) {
  if (!user) throw new Error("not signed in");
  await editRight(id);
  const { db, files } = await store();
  const ref = db.collection("shares").doc(id);
  const check = (snap) => {
    if (!snap.exists) throw Object.assign(new Error("share not found"), { code: "not-found" });
    const cur = snap.data();
    if (cur.owner !== user.uid) throw Object.assign(new Error("not the owner"), { code: "permission-denied" });
    if (since.md != null && cur.md !== since.md) throw elsewhere("changed elsewhere");
    return cur;
  };
  const known = (path) => !since.stamps || since.stamps.has(path);
  const had = new Map((check(await ref.get()).files || []).map((f) => [f.path, f]));
  const sent = new Map();
  for (const f of deck.files) {
    if (had.has(f.path) && since.stamps?.get(f.path) === f.stamp) continue;
    const blob = f.data instanceof Blob ? f.data : new Blob([f.data ?? ""], { type: f.type || "text/plain" });
    const obj = files.ref(`shares/${id}/${f.path}`);
    await obj.put(blob, { contentType: storedType(f.type || blob.type) });
    sent.set(f.path, { path: f.path, type: f.type || blob.type || "", size: blob.size, url: await obj.getDownloadURL() });
  }
  let gone = [];
  const kept = await db.runTransaction(async (tx) => {
    const cur = check(await tx.get(ref));
    const now = new Map((cur.files || []).map((f) => [f.path, f]));
    const out = deck.files.map((f) => {
      const e = sent.get(f.path) || now.get(f.path);
      // removed there since it was read here: this page looks again
      if (!e) throw elsewhere("a file changed elsewhere");
      return e;
    });
    const mine = new Set(deck.files.map((f) => f.path));
    for (const [path, f] of now) if (!mine.has(path) && !known(path)) out.push(f);
    gone = [...now.keys()].filter((path) => !mine.has(path) && known(path));
    tx.update(ref, {
      name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null, files: out,
      updated: globalThis.firebase.firestore.FieldValue.serverTimestamp(),
    });
    return out;
  });
  for (const path of gone) files.ref(`shares/${id}/${path}`).delete().catch(() => {});
  return kept;
}

// A PRO deck deleted: its share's files in Storage, the share and the
// owner's deck record. Files go first, while the share still names its
// owner (storage.rules).
async function deleteShare(id) {
  if (!user) throw new Error("not signed in");
  const { db, files } = await store();
  const ref = db.collection("shares").doc(id);
  const snap = await ref.get();
  if (snap.exists) {
    const cur = snap.data();
    if (cur.owner !== user.uid) throw Object.assign(new Error("not the owner"), { code: "permission-denied" });
    await Promise.all((cur.files || []).map((f) => files.ref(`shares/${id}/${f.path}`).delete().catch(() => {})));
    // the version history's objects (putObject)
    const kept = await files.ref(`shares/${id}/.versions`).listAll().catch(() => null);
    if (kept) await Promise.all(kept.items.map((r) => r.delete().catch(() => {})));
    await ref.delete();
    if (cur.deck) await db.collection("decks").doc(cur.deck).delete().catch(() => {});
  }
}

// --- version history (web/versions.js) ---------------------------------------------
// A PRO deck's versions are objects in Storage, shares/{id}/.versions/{object
// id}; each is written once and never changed. The share keeps `head` (the
// newest version) and `log` (the versions, newest last, for the history
// list on another device).
async function putObject(shareId, objId, bytes) {
  if (!user) throw new Error("not signed in");
  await editRight(shareId);
  const { files } = await store();
  await files.ref(`shares/${shareId}/.versions/${objId}`).put(new Blob([bytes]), { contentType: "application/octet-stream" });
}

// → Uint8Array, or null when there is no such object
async function getObject(shareId, objId) {
  const { files } = await store();
  let url;
  try {
    url = await files.ref(`shares/${shareId}/.versions/${objId}`).getDownloadURL();
  } catch (_) {
    return null;
  }
  const res = await fetch(url);
  return res.ok ? new Uint8Array(await res.arrayBuffer()) : null;
}

// The share's head moved from `expect` to `head`, with `entries` added to
// its log, only if the head is still `expect`. → { ok, head (the share's),
// log }
const LOG_MAX = 300;
async function pushHead(shareId, expect, head, entries) {
  if (!user) throw new Error("not signed in");
  await editRight(shareId);
  const { db } = await store();
  const ref = db.collection("shares").doc(shareId);
  return db.runTransaction(async (tx) => {
    const snap = await tx.get(ref);
    if (!snap.exists) throw Object.assign(new Error("share not found"), { code: "not-found" });
    const cur = snap.data();
    const now = cur.head || null;
    if (now !== (expect || null) && now !== head) return { ok: false, head: now, log: cur.log || [] };
    const seen = new Set((cur.log || []).map((e) => e.id));
    const log = (cur.log || []).concat(entries.filter((e) => !seen.has(e.id))).slice(-LOG_MAX);
    tx.update(ref, { head, log });
    return { ok: true, head, log };
  });
}

// The share's head and log as they are now: { head, log, md, css, theme,
// name, files } (null when it is gone).
async function readHead(shareId) {
  const s = await loadShare(shareId);
  return s ? { ...s, head: s.head || null, log: s.log || [] } : null;
}

// --- private Google Sheets ---------------------------------------------------------
// A sheet that is not shared by link is read through the Sheets API as the
// signed-in user, with the drive.file scope: Sliqtly may read only the files
// the user picks in Google's Picker, nothing else on their Drive. The token
// comes from a second Google popup on the same account (Firebase keeps no
// Google token of its own) and lasts an hour; it is kept for this tab only.
const DRIVE_FILE = "https://www.googleapis.com/auth/drive.file";
let sheetToken = null;
try { sheetToken = JSON.parse(sessionStorage.getItem("sliqtly:sheets") || "null"); } catch (_) { /* none */ }

function tokenValid() {
  return sheetToken && sheetToken.exp > Date.now() + 60000 ? sheetToken.token : null;
}

// The access token; a popup when there is none and `ask` (a press or a
// paste, so the browser lets the popup open), else null.
async function sheetsToken(ask) {
  const have = tokenValid();
  if (have || !ask || !user) return have;
  const p = new globalThis.firebase.auth.GoogleAuthProvider();
  p.addScope(DRIVE_FILE);
  if (user.email) p.setCustomParameters({ login_hint: user.email });
  const res = await user.reauthenticateWithPopup(p);
  const token = res?.credential?.accessToken;
  if (!token) return null;
  sheetToken = { token, exp: Date.now() + 55 * 60000 };
  try { sessionStorage.setItem("sliqtly:sheets", JSON.stringify(sheetToken)); } catch (_) { /* this page only */ }
  return token;
}

// Google's Picker, open on `fileId` when given: the file picked is the one
// drive.file lets Sliqtly read. → its id, or null when cancelled.
let pickerLoaded = null;
async function pickSheet(fileId, token) {
  const fb = await firebaseApp();
  const opts = fb.app().options || {};
  pickerLoaded ??= (async () => {
    await load("https://apis.google.com/js/api.js");
    await new Promise((ok) => globalThis.gapi.load("picker", ok));
  })();
  await pickerLoaded;
  const g = globalThis.google.picker;
  const key = document.querySelector('meta[name="google-picker-key"]')?.content || opts.apiKey;
  const view = new g.DocsView(g.ViewId.SPREADSHEETS).setMode(g.DocsViewMode.LIST);
  if (fileId && typeof view.setFileIds === "function") view.setFileIds(fileId);
  return new Promise((ok) => {
    new g.PickerBuilder()
      .addView(view)
      .setOAuthToken(token)
      .setDeveloperKey(key)
      .setAppId(opts.messagingSenderId)
      .setTitle(t("Pick the sheet Sliqtly may read"))
      .setCallback((d) => {
        if (d.action === g.Action.PICKED) ok(d.docs?.[0]?.id || null);
        else if (d.action === g.Action.CANCEL) ok(null);
        else if (d.action === "error") ok(false); // e.g. the developer key does not allow the Picker API
      })
      .build()
      .setVisible(true);
  });
}

function csvCell(v) {
  const s = v == null ? "" : String(v);
  return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}

// A sheet's CSV as the signed-in user: `gviz` is the address the chart reads
// (MdVegaRender.sheetCsv: /d/<id>/gviz/tq?…&sheet=…&gid=…&range=…). `ask`
// allows the popup and the Picker. Throws { code: "auth" } when there is no
// token, { code: "access" } when the user may not or did not pick it.
// the spreadsheet's and the tab's names, by the address read (Files names
// the kept copy after them)
const sheetNames = new Map();
async function readSheet(gviz, ask) {
  const m = /\/spreadsheets\/d\/([^/?#]+)/.exec(gviz);
  if (!m || !user) throw Object.assign(new Error("not signed in"), { code: "auth" });
  const id = m[1];
  const q = new URL(gviz).searchParams;
  let token = await sheetsToken(ask);
  if (!token) throw Object.assign(new Error("no token"), { code: "auth" });
  const api = "https://sheets.googleapis.com/v4/spreadsheets/" + encodeURIComponent(id);
  const get = (u) => fetch(u, { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
  let tab = q.get("sheet") || "";
  // a tab named by its gid: its title from the spreadsheet's tabs
  const fields = "?fields=properties.title,sheets.properties(sheetId,title)";
  let r = await get(api + fields);
  if ((r.status === 403 || r.status === 404) && ask) {
    // not one of the files drive.file covers yet: the user picks it
    const picked = await pickSheet(id, token);
    if (picked === false) throw Object.assign(new Error("picker failed"), { code: "picker" });
    if (picked !== id) throw Object.assign(new Error("not picked"), { code: "access" });
    r = await get(api + fields);
  }
  if (r.status === 401) {
    sheetToken = null;
    throw Object.assign(new Error("token expired"), { code: "auth" });
  }
  if (!r.ok) throw Object.assign(new Error("HTTP " + r.status), { code: "access" });
  const meta = await r.json();
  const tabs = (meta.sheets || []).map((x) => x.properties);
  if (!tab) {
    const gid = q.get("gid");
    tab = (tabs.find((x) => String(x.sheetId) === gid) || tabs[0] || {}).title || "";
  }
  sheetNames.set(gviz, { title: meta.properties?.title || "", tab });
  const range = (tab ? "'" + tab.replace(/'/g, "''") + "'!" : "") + (q.get("range") || "A:ZZ");
  const v = await get(api + "/values/" + encodeURIComponent(range) + "?valueRenderOption=UNFORMATTED_VALUE&dateTimeRenderOption=FORMATTED_STRING");
  if (!v.ok) throw Object.assign(new Error("HTTP " + v.status), { code: "access" });
  const rows = (await v.json()).values || [];
  const width = rows.reduce((n, row) => Math.max(n, row.length), 0);
  return rows.map((row) => Array.from({ length: width }, (_, i) => csvCell(row[i])).join(",")).join("\n") + "\n";
}

window.sliqtly = { auth, user: () => user, license: () => license, signedIn, switchAccount, share, saveShare, deleteShare, loadShare, setVisibility, listMine, readSheet, putObject, getObject, pushHead, readHead, sheetsToken: () => tokenValid(), askSheets: () => sheetsToken(true), sheetName: (gviz) => sheetNames.get(gviz) || null };
// sliqtly.com/editor's Rooms search: the signed-in user's own presentations
// whose words hold q, searched by the server (mcp-go/searchapi.go)
// → [{ deck_id, name, snippet, updated }]
if (gate) {
  window.sliqtly.searchDecks = async (q) => {
    const res = await fetch("/editor/api/search?q=" + encodeURIComponent(q), { cache: "no-store" });
    const out = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(out.error || String(res.status));
    return out.presentations || [];
  };
}
window.dispatchEvent(new Event("sliqtly:ready"));
