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

const SDK = "https://www.gstatic.com/firebasejs/10.14.1/";
const pro = document.getElementById("pro");
let user = null;
let ready = null;

// An AI assistant's preview (mcp/src/preview.html) may load scripts only
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
      window.dispatchEvent(new Event("sliqtly:user"));
    });
    authNow = a;
    return a;
  })();
  return ready;
}

function show() {
  const first = (user?.displayName || user?.email || "").split(/[\s@]/)[0];
  pro.textContent = user ? `PRO · ${first}` : "PRO";
  pro.title = user ? t("Signed in as ") + (user.displayName || user.email) : t("Sign in with Google");
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

async function signInByRedirect() {
  try { sessionStorage.setItem(REDIRECT_FLAG, "1"); } catch (_) { /* the result is still read below */ }
  const a = await redirectAuth();
  await a.signInWithRedirect(new globalThis.firebase.auth.GoogleAuthProvider());
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

function signInFailed(e) {
  if (e?.code === "auth/popup-closed-by-user" || e?.code === "auth/cancelled-popup-request") return;
  if (e?.code === "auth/popup-blocked" && canRedirect()) {
    signInByRedirect().catch(signInFailed);
    return;
  }
  console.error(e);
  alert(t("Sign-in failed: ") + (e?.message || e));
}

function signIn(a) {
  if (standalone && canRedirect()) return signInByRedirect().catch(signInFailed);
  // no await before this: the window opens within the press
  return a.signInWithPopup(new globalThis.firebase.auth.GoogleAuthProvider()).catch(signInFailed);
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

// Keeps the deck as its owner's and makes a copy anyone with the link can
// read: { deckId, name, md, theme, css, files: [{ path, type, data }] } →
// the share's id.
async function share(deck) {
  if (!user) throw new Error("not signed in");
  const { db, files } = await store();
  const fb = globalThis.firebase;
  const now = fb.firestore.FieldValue.serverTimestamp();
  const body = { name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null };
  await db.collection("decks").doc(deck.deckId).set({ ...body, owner: user.uid, updated: now }, { merge: true });
  const id = shortId();
  const doc = db.collection("shares").doc(id);
  // the copy first: Storage lets only the owner it names write its files
  await doc.set({ ...body, owner: user.uid, deck: deck.deckId, files: [], created: now });
  const kept = [];
  for (const f of deck.files || []) {
    const blob = f.data instanceof Blob ? f.data : new Blob([f.data ?? ""], { type: f.type || "text/plain" });
    const ref = files.ref(`shares/${id}/${f.path}`);
    await ref.put(blob, { contentType: f.type || blob.type || "application/octet-stream" });
    kept.push({ path: f.path, type: f.type || blob.type || "", size: blob.size, url: await ref.getDownloadURL() });
  }
  if (kept.length) await doc.update({ files: kept });
  return id;
}

// A shared copy: { name, md, theme, css, files: [{ path, type, size, url }] },
// or null when there is none by that id.
async function loadShare(id) {
  const { db } = await store();
  const snap = await db.collection("shares").doc(id).get();
  return snap.exists ? snap.data() : null;
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
// shares/{id}, files to Storage, so /s/{id} and an assistant (mcp/) see it.
// since: { md, stamps } as this page last wrote or read it. The share's text
// having moved on from since.md means someone else (an assistant) changed it:
// that is refused with code "changed-elsewhere" rather than written over.
// A file whose stamp is unchanged is not sent again. → the share's files
async function saveShare(id, deck, since) {
  if (!user) throw new Error("not signed in");
  const { db, files } = await store();
  const ref = db.collection("shares").doc(id);
  const snap = await ref.get();
  if (!snap.exists) throw Object.assign(new Error("share not found"), { code: "not-found" });
  const cur = snap.data();
  if (cur.owner !== user.uid) throw Object.assign(new Error("not the owner"), { code: "permission-denied" });
  if (since.md != null && cur.md !== since.md) throw Object.assign(new Error("changed elsewhere"), { code: "changed-elsewhere" });
  const had = new Map((cur.files || []).map((f) => [f.path, f]));
  const kept = [];
  for (const f of deck.files) {
    const prev = had.get(f.path);
    if (prev && since.stamps?.get(f.path) === f.stamp) {
      kept.push(prev);
      continue;
    }
    const blob = f.data instanceof Blob ? f.data : new Blob([f.data ?? ""], { type: f.type || "text/plain" });
    const obj = files.ref(`shares/${id}/${f.path}`);
    await obj.put(blob, { contentType: f.type || blob.type || "application/octet-stream" });
    kept.push({ path: f.path, type: f.type || blob.type || "", size: blob.size, url: await obj.getDownloadURL() });
  }
  for (const path of had.keys()) {
    if (!deck.files.some((f) => f.path === path)) files.ref(`shares/${id}/${path}`).delete().catch(() => {});
  }
  await ref.update({
    name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null, files: kept,
    updated: globalThis.firebase.firestore.FieldValue.serverTimestamp(),
  });
  return kept;
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
  let r = await get(api + "?fields=sheets.properties(sheetId,title)");
  if ((r.status === 403 || r.status === 404) && ask) {
    // not one of the files drive.file covers yet: the user picks it
    const picked = await pickSheet(id, token);
    if (picked !== id) throw Object.assign(new Error("not picked"), { code: "access" });
    r = await get(api + "?fields=sheets.properties(sheetId,title)");
  }
  if (r.status === 401) {
    sheetToken = null;
    throw Object.assign(new Error("token expired"), { code: "auth" });
  }
  if (!r.ok) throw Object.assign(new Error("HTTP " + r.status), { code: "access" });
  const tabs = ((await r.json()).sheets || []).map((x) => x.properties);
  if (!tab) {
    const gid = q.get("gid");
    tab = (tabs.find((x) => String(x.sheetId) === gid) || tabs[0] || {}).title || "";
  }
  const range = (tab ? "'" + tab.replace(/'/g, "''") + "'!" : "") + (q.get("range") || "A:ZZ");
  const v = await get(api + "/values/" + encodeURIComponent(range) + "?valueRenderOption=UNFORMATTED_VALUE&dateTimeRenderOption=FORMATTED_STRING");
  if (!v.ok) throw Object.assign(new Error("HTTP " + v.status), { code: "access" });
  const rows = (await v.json()).values || [];
  const width = rows.reduce((n, row) => Math.max(n, row.length), 0);
  return rows.map((row) => Array.from({ length: width }, (_, i) => csvCell(row[i])).join(",")).join("\n") + "\n";
}

window.sliqtly = { auth, user: () => user, signedIn, share, saveShare, loadShare, readSheet, sheetsToken: () => tokenValid(), askSheets: () => sheetsToken(true) };
window.dispatchEvent(new Event("sliqtly:ready"));
