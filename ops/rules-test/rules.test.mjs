// SPDX-License-Identifier: AGPL-3.0-or-later
// Who reads a share (firestore.rules, storage.rules): a private one only its
// owner, a "link" one (or one without the field) anyone with its id; only the
// owner writes, and visibility is private or link. Runs against the
// emulators (needs Java):
//   cd ops/rules-test && npm install && npm test
import { initializeTestEnvironment, assertFails, assertSucceeds } from "@firebase/rules-unit-testing";
import { readFileSync } from "node:fs";
import { doc, getDoc, setDoc, updateDoc, serverTimestamp, Timestamp } from "firebase/firestore";
import { ref, getBytes, uploadBytes, listAll } from "firebase/storage";
const env = await initializeTestEnvironment({ projectId: "demo-sliqtly",
  firestore: { rules: readFileSync(new URL("../../firestore.rules", import.meta.url), "utf8"), host: "127.0.0.1", port: 8085 },
  storage: { rules: readFileSync(new URL("../../storage.rules", import.meta.url), "utf8"), host: "127.0.0.1", port: 9199 } });
const fsOf = new Map(), stOf = new Map();
const FS = (c) => { if (!fsOf.has(c)) fsOf.set(c, c.firestore()); return fsOf.get(c); };
const ST = (c) => { if (!stOf.has(c)) stOf.set(c, c.storage()); return stOf.get(c); };
await env.withSecurityRulesDisabled(async (c) => {
  const db = c.firestore();
  await setDoc(doc(db, "shares/priv"), { owner: "u1", md: "# P", visibility: "private" });
  await setDoc(doc(db, "shares/open"), { owner: "u1", md: "# O", visibility: "link" });
  await setDoc(doc(db, "shares/old"), { owner: "u1", md: "# Old" });
  // what u1's editor license covers (firestore.rules licensed())
  await setDoc(doc(db, "licenses/u1"), { plan: "admin", maxDocs: -1, docs: [] });
  const st = c.storage();
  for (const id of ["priv", "open", "old"]) await uploadBytes(ref(st, `shares/${id}/media/a.png`), new Uint8Array([1, 2]), { contentType: "image/png" });
});
const u1 = env.authenticatedContext("u1"), u2 = env.authenticatedContext("u2"), anon = env.unauthenticatedContext();
const g = (c, id) => getDoc(doc(FS(c), "shares/" + id));
const f = (c, id) => getBytes(ref(ST(c), `shares/${id}/media/a.png`));
const l = (c, id) => listAll(ref(ST(c), `shares/${id}`));
let n = 0; const ok = async (p, what) => { await assertSucceeds(p); n++; }, no = async (p) => { await assertFails(p); n++; };
await ok(g(u1, "priv")); await no(g(u2, "priv")); await no(g(anon, "priv"));
for (const id of ["open", "old"]) { await ok(g(u2, id)); await ok(g(anon, id)); await ok(f(anon, id)); await ok(l(anon, id)); }
await ok(g(anon, "missing"));
await ok(f(u1, "priv")); await no(f(u2, "priv")); await no(f(anon, "priv"));
await ok(l(u1, "priv")); await no(l(u2, "priv")); await no(l(anon, "priv"));
// writes
await ok(setDoc(doc(FS(u1), "shares/new1"), { owner: "u1", md: "", visibility: "private" }));
await no(setDoc(doc(FS(u1), "shares/new2"), { owner: "u1", md: "", visibility: "everyone" }));
await ok(updateDoc(doc(FS(u1), "shares/priv"), { visibility: "link" }));
await ok(g(u2, "priv"));
await no(updateDoc(doc(FS(u2), "shares/priv"), { visibility: "private" }));
await ok(updateDoc(doc(FS(u1), "shares/old"), { md: "# changed" }));
// viewing links (links/): a term of 1 to 30 days from the server's time
const link = (c, id, v) => setDoc(doc(FS(c), "links/" + id), { of: "priv", owner: "u1", created: serverTimestamp(), since: serverTimestamp(), ...v });
await ok(link(u1, "lnk7", { days: 7 }));
await ok(link(u1, "lnk30", { days: 30 }));
await no(link(u1, "lnk31", { days: 31 }));
await no(link(u1, "lnk0", { days: 0 }));
await no(setDoc(doc(FS(u1), "links/lnkno"), { of: "priv", owner: "u1", created: serverTimestamp() }));
await no(link(u1, "lnkpast", { days: 7, since: Timestamp.fromMillis(Date.now() - 864e5) }));
await no(link(u2, "lnku2", { days: 7, owner: "u2" }));
await ok(link(u1, "lnkrec", { days: 1, file: "recordings/web-0123456789abcdef01234567.webm" }));
await no(link(u1, "lnkbad", { days: 1, file: "media/a.png" }));
await ok(updateDoc(doc(FS(u1), "links/lnk7"), { since: serverTimestamp(), days: 30 }));
await no(updateDoc(doc(FS(u1), "links/lnk7"), { since: serverTimestamp(), days: 60 }));
await no(updateDoc(doc(FS(u1), "links/lnk7"), { since: serverTimestamp(), days: 7, of: "open" }));
await no(updateDoc(doc(FS(u2), "links/lnk7"), { since: serverTimestamp(), days: 7 }));
console.log("rules checks passed:", n);
await env.cleanup();
