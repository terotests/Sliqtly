// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Google sign-in on the public viewer, for one thing only: a private
// presentation is shown to its owner (/api/view and /api/export take the
// Firebase ID token as "Authorization: Bearer …", mcp-go/rgr/App.rgr
// pageUser). Loaded when a presentation was not found, never before, so a
// shared link costs nothing extra. Firebase Auth as oauth.html uses it: the
// compat SDK and Hosting's /__/firebase/init.js.

const SDK = "https://www.gstatic.com/firebasejs/10.14.1/";
let ready = null;

function load(src) {
  return new Promise((ok, no) => {
    const s = document.createElement("script");
    s.src = src;
    s.onload = ok;
    s.onerror = () => no(new Error("could not load " + src));
    document.head.append(s);
  });
}

/** Firebase Auth, loaded once; rejects where it cannot load. */
function auth() {
  ready ??= (async () => {
    await load(SDK + "firebase-app-compat.js");
    await load("/__/firebase/init.js");
    await load(SDK + "firebase-auth-compat.js");
    return window.firebase.auth();
  })();
  return ready;
}

/** The user signed in on this site before (persisted), or null; null too where sign-in is not available. */
export async function currentUser(ms = 5000) {
  try {
    const a = await auth();
    return await new Promise((done) => {
      const timer = setTimeout(() => done(a.currentUser), ms);
      const stop = a.onAuthStateChanged((u) => {
        clearTimeout(timer);
        stop();
        done(u);
      });
    });
  } catch (e) {
    console.warn("viewer sign-in not available", e);
    return null;
  }
}

/** Google's sign-in window; pick asks which account. */
export async function signIn(pick = false) {
  const a = await auth();
  const p = new window.firebase.auth.GoogleAuthProvider();
  if (pick) p.setCustomParameters({ prompt: "select_account" });
  return (await a.signInWithPopup(p)).user;
}

/** Request headers that carry user's ID token (none for no user). */
export async function authHeaders(user) {
  if (!user) return {};
  return { Authorization: "Bearer " + (await user.getIdToken()) };
}
