// SPDX-License-Identifier: AGPL-3.0-or-later
// Who changes a share (firestore.rules, storage.rules): its owner under a
// license of their own (licenses/{uid}), the people the owner invited by
// address (`editors`), nobody else; and the owner's viewing links
// (links/{id}). Runs with rules.test.mjs: npm test.
import { initializeTestEnvironment, assertFails, assertSucceeds } from "@firebase/rules-unit-testing";
import { readFileSync } from "node:fs";
import { doc, setDoc, updateDoc, getDoc, deleteDoc, Timestamp, getDocs, query, collection, where } from "firebase/firestore";
import { ref, uploadBytes, deleteObject } from "firebase/storage";

const env = await initializeTestEnvironment({ projectId: "demo-sliqtly",
  firestore: { rules: readFileSync(new URL("../../firestore.rules", import.meta.url), "utf8"), host: "127.0.0.1", port: 8085 },
  storage: { rules: readFileSync(new URL("../../storage.rules", import.meta.url), "utf8"), host: "127.0.0.1", port: 9199 } });

const ok = [], bad = [];
async function check(name, p, expect) {
  try { await (expect ? assertSucceeds(p) : assertFails(p)); ok.push(name); } catch (e) { bad.push(name + ": " + e.message); }
}
await env.withSecurityRulesDisabled(async (c) => {
  const db = c.firestore();
  await uploadBytes(ref(c.storage(), "shares/shareI/media/a.png"), new Uint8Array([1]), { contentType: "image/png" });
  await setDoc(doc(db, "licenses/anna"), { plan: "trial", maxDocs: 2, docs: ["shareA"] });
  await setDoc(doc(db, "licenses/old"), { plan: "pro", maxDocs: 5, docs: ["shareO"], editUntil: Timestamp.fromMillis(Date.now() - 1000) });
  await setDoc(doc(db, "licenses/tero"), { plan: "admin", maxDocs: -1, docs: [] });
  await setDoc(doc(db, "shares/shareO"), { owner: "old", md: "x", visibility: "link" });
  await setDoc(doc(db, "shares/shareN"), { owner: "nolic", md: "x", visibility: "link" });
  await setDoc(doc(db, "shares/shareI"), { owner: "anna", md: "x", visibility: "private", editors: ["bob@x.fi"] });
  await setDoc(doc(db, "licenses/bob"), { plan: "trial", maxDocs: 2, docs: [] });
  await setDoc(doc(db, "licenses/bobold"), { plan: "trial", maxDocs: 2, docs: [], editUntil: Timestamp.fromMillis(Date.now() - 1000) });
});
const anna = env.authenticatedContext("anna").firestore();
const old = env.authenticatedContext("old").firestore();
const tero = env.authenticatedContext("tero").firestore();
const nolic = env.authenticatedContext("nolic").firestore();
const bob = env.authenticatedContext("bob", { email: "Bob@X.fi", email_verified: true }).firestore();
const bobUnverified = env.authenticatedContext("bob", { email: "bob@x.fi", email_verified: false }).firestore();
const bobOld = env.authenticatedContext("bobold", { email: "bob@x.fi", email_verified: true }).firestore();
const carl = env.authenticatedContext("carl", { email: "carl@x.fi", email_verified: true }).firestore();
const body = (o) => ({ owner: o, md: "# hi", visibility: "link", files: [] });

await check("licensed create", setDoc(doc(anna, "shares/shareA"), body("anna")), true);
await check("licensed update", updateDoc(doc(anna, "shares/shareA"), { md: "# 2" }), true);
await check("unlicensed create", setDoc(doc(anna, "shares/shareB"), body("anna")), false);
await check("admin create any", setDoc(doc(tero, "shares/anyX"), body("tero")), true);
await check("no license doc create", setDoc(doc(nolic, "shares/shareZ"), body("nolic")), false);
await check("no license doc update", updateDoc(doc(nolic, "shares/shareN"), { md: "y" }), false);
await check("no license visibility only", updateDoc(doc(nolic, "shares/shareN"), { visibility: "private" }), true);
await check("expired update", updateDoc(doc(old, "shares/shareO"), { md: "y" }), false);
await check("expired visibility", updateDoc(doc(old, "shares/shareO"), { visibility: "private" }), true);
await check("expired read own", getDoc(doc(old, "shares/shareO")), true);
await check("expired delete own", deleteDoc(doc(old, "shares/shareO")), true);
await check("read own license", getDoc(doc(anna, "licenses/anna")), true);
await check("read other license", getDoc(doc(anna, "licenses/tero")), false);
await check("write own license", updateDoc(doc(anna, "licenses/anna"), { maxDocs: -1 }), false);
await check("make own license", setDoc(doc(nolic, "licenses/nolic"), { maxDocs: -1 }), false);
await check("invited reads private", getDoc(doc(bob, "shares/shareI")), true);
await check("not invited reads private", getDoc(doc(carl, "shares/shareI")), false);
await check("invited edits md", updateDoc(doc(bob, "shares/shareI"), { md: "# bob" }), true);
await check("invited edits head/log", updateDoc(doc(bob, "shares/shareI"), { head: "h", log: [] }), true);
await check("invited cannot take owner", updateDoc(doc(bob, "shares/shareI"), { owner: "bob" }), false);
await check("invited cannot change editors", updateDoc(doc(bob, "shares/shareI"), { editors: ["bob@x.fi", "carl@x.fi"] }), false);
await check("invited cannot change visibility", updateDoc(doc(bob, "shares/shareI"), { visibility: "link" }), false);
await check("invited cannot delete", deleteDoc(doc(bob, "shares/shareI")), false);
await check("unverified email not invited", updateDoc(doc(bobUnverified, "shares/shareI"), { md: "u" }), false);
await check("invited with ended license", updateDoc(doc(bobOld, "shares/shareI"), { md: "o" }), false);
await check("not invited edits", updateDoc(doc(carl, "shares/shareI"), { md: "c" }), false);
await check("owner sets editors without license slot", updateDoc(doc(nolic, "shares/shareN"), { editors: ["carl@x.fi"] }), true);
await check("owner editors not a list", updateDoc(doc(anna, "shares/shareA"), { editors: "x" }), false);
await check("invited lists by editors", getDocs(query(collection(bob, "shares"), where("editors", "array-contains", "bob@x.fi"))), true);
await check("lists others by editors", getDocs(query(collection(carl, "shares"), where("editors", "array-contains", "bob@x.fi"))), false);
await check("owner lists own", getDocs(query(collection(anna, "shares"), where("owner", "==", "anna"))), true);
await check("owner links own", setDoc(doc(anna, "links/L1"), { of: "shareA", owner: "anna", created: 1 }), true);
await check("link to other's deck", setDoc(doc(carl, "links/L2"), { of: "shareA", owner: "carl", created: 1 }), false);
await check("link naming another owner", setDoc(doc(anna, "links/L3"), { of: "shareA", owner: "carl", created: 1 }), false);
await check("link with extra fields", setDoc(doc(anna, "links/L4"), { of: "shareA", owner: "anna", created: 1, visibility: "x" }), false);
await check("link over a share id", setDoc(doc(anna, "links/shareA"), { of: "shareA", owner: "anna", created: 1 }), false);
await check("owner reads own link", getDoc(doc(anna, "links/L1")), true);
await check("other reads link", getDoc(doc(carl, "links/L1")), false);
await check("link not changed", updateDoc(doc(anna, "links/L1"), { of: "shareX" }), false);
await check("owner lists links", getDocs(query(collection(anna, "links"), where("owner", "==", "anna"))), true);
await check("share over a link id", setDoc(doc(tero, "shares/L1"), body("tero")), false);
await check("other deletes link", deleteDoc(doc(carl, "links/L1")), false);
await check("owner deletes link", deleteDoc(doc(anna, "links/L1")), true);
const bobSt = env.authenticatedContext("bob", { email: "bob@x.fi", email_verified: true }).storage();
const carlSt = env.authenticatedContext("carl", { email: "carl@x.fi", email_verified: true }).storage();
const png = new Uint8Array([1, 2]);
await check("invited uploads a picture", uploadBytes(ref(bobSt, "shares/shareI/media/b.png"), png, { contentType: "image/png" }), true);
await check("not invited uploads", uploadBytes(ref(carlSt, "shares/shareI/media/c.png"), png, { contentType: "image/png" }), false);
await check("invited removes a picture", deleteObject(ref(bobSt, "shares/shareI/media/a.png")), true);
console.log("ok:", ok.length, ok.join(" | "));
console.log("bad:", bad.length ? "\n" + bad.join("\n") : 0);
await env.cleanup();
if (bad.length) process.exit(1);
console.log("editor rules checks passed:", ok.length);
