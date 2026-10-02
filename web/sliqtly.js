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
    return a;
  })();
  return ready;
}

function show() {
  const first = (user?.displayName || user?.email || "").split(/[\s@]/)[0];
  pro.textContent = user ? `PRO · ${first}` : "PRO";
  pro.title = user ? t("Signed in as ") + (user.displayName || user.email) : t("Sign in with Google");
}

pro.addEventListener("click", async () => {
  try {
    const a = await auth();
    if (!user) {
      await a.signInWithPopup(new globalThis.firebase.auth.GoogleAuthProvider());
    } else if (confirm(t("Signed in as ") + (user.displayName || user.email) + "\n\n" + t("Sign out?"))) {
      await a.signOut();
    }
  } catch (e) {
    if (e?.code === "auth/popup-closed-by-user" || e?.code === "auth/cancelled-popup-request") return;
    console.error(e);
    alert(t("Sign-in failed: ") + (e?.message || e));
  }
});

// a session from an earlier visit comes back without a press
auth().catch((e) => console.warn("sign-in not available:", e.message));
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

window.sliqtly = { auth, user: () => user, signedIn, share, saveShare, loadShare };
window.dispatchEvent(new Event("sliqtly:ready"));
