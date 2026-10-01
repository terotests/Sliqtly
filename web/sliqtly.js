// Sliqtly's PRO: Google sign-in through Firebase Auth.
//
// The PRO button sits in the editor's bar (scripts/brand.mjs puts it there,
// data-canvas makes the canvas bar draw it). Signed out, a press opens
// Google's sign-in; signed in, the button carries the user's first name and
// a press offers to sign out. What PRO unlocks (the user's files kept in the
// cloud) builds on window.sliqtly.
//
// Firebase comes from Google's CDN; the project's config from Hosting's
// reserved /__/firebase/init.js, so nothing about the project is in this file.
// Served anywhere but Firebase Hosting, sign-in is simply not available.

const SDK = "https://www.gstatic.com/firebasejs/10.14.1/";
const pro = document.getElementById("pro");
let user = null;
let ready = null;

function load(src) {
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = src;
    s.onload = resolve;
    s.onerror = () => reject(new Error("could not load " + src));
    document.head.appendChild(s);
  });
}

function auth() {
  ready ??= (async () => {
    await load(SDK + "firebase-app-compat.js");
    await load(SDK + "firebase-auth-compat.js");
    await load("/__/firebase/init.js");
    const a = globalThis.firebase.auth();
    a.onAuthStateChanged((u) => {
      user = u;
      show();
    });
    return a;
  })();
  return ready;
}

function show() {
  const first = (user?.displayName || user?.email || "").split(/[\s@]/)[0];
  pro.textContent = user ? `PRO · ${first}` : "PRO";
  pro.title = user ? `Kirjautuneena: ${user.displayName || user.email}` : "Kirjaudu Google-tilillä";
}

pro.addEventListener("click", async () => {
  try {
    const a = await auth();
    if (!user) {
      await a.signInWithPopup(new globalThis.firebase.auth.GoogleAuthProvider());
    } else if (confirm(`Kirjautuneena: ${user.displayName || user.email}\n\nKirjaudutaanko ulos?`)) {
      await a.signOut();
    }
  } catch (e) {
    if (e?.code === "auth/popup-closed-by-user" || e?.code === "auth/cancelled-popup-request") return;
    console.error(e);
    alert("Kirjautuminen ei onnistunut: " + (e?.message || e));
  }
});

// a session from an earlier visit comes back without a press
auth().catch((e) => console.warn("sign-in not available:", e.message));
show();

window.sliqtly = { auth, user: () => user };
