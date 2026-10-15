// SPDX-License-Identifier: AGPL-3.0-or-later
// Who reads who is working on a Story (firestore.rules presence): its owner
// and the editors it invited, by a verified address; nobody writes it but
// the server. Runs with the others: npm test.
import { initializeTestEnvironment, assertFails, assertSucceeds } from "@firebase/rules-unit-testing";
import { readFileSync } from "node:fs";
import { doc, setDoc, getDoc, getDocs, collection, deleteDoc } from "firebase/firestore";

const env = await initializeTestEnvironment({ projectId: "demo-sliqtly",
  firestore: { rules: readFileSync(new URL("../../firestore.rules", import.meta.url), "utf8"), host: "127.0.0.1", port: 8085 } });

const ok = [], bad = [];
async function check(name, p, expect) {
  try { await (expect ? assertSucceeds(p) : assertFails(p)); ok.push(name); } catch (e) { bad.push(name + ": " + e.message); }
}
await env.withSecurityRulesDisabled(async (c) => {
  const db = c.firestore();
  await setDoc(doc(db, "shares/S1"), { owner: "anna", visibility: "link", editors: ["bob@x.fi"], md: "# a" });
  await setDoc(doc(db, "presence/S1/agents/a1"), { id: "a1", name: "Writer", first: 1, last: 2 });
  await setDoc(doc(db, "presence/S2/agents/a1"), { id: "a1", name: "Writer", first: 1, last: 2 });
});
const anna = env.authenticatedContext("anna").firestore();
const bob = env.authenticatedContext("bob", { email: "Bob@X.fi", email_verified: true }).firestore();
const bobUnverified = env.authenticatedContext("bob2", { email: "bob@x.fi", email_verified: false }).firestore();
const carl = env.authenticatedContext("carl", { email: "carl@x.fi", email_verified: true }).firestore();
const nobody = env.unauthenticatedContext().firestore();
const all = (db, deck) => getDocs(collection(db, "presence/" + deck + "/agents"));

await check("owner listens", all(anna, "S1"), true);
await check("owner reads one", getDoc(doc(anna, "presence/S1/agents/a1")), true);
await check("invited editor listens", all(bob, "S1"), true);
await check("unverified address listens", all(bobUnverified, "S1"), false);
await check("a link holder listens", all(carl, "S1"), false);
await check("signed out listens", all(nobody, "S1"), false);
await check("no such share", all(anna, "S2"), false);
await check("owner writes", setDoc(doc(anna, "presence/S1/agents/a2"), { id: "a2", name: "Me", first: 1, last: 2 }), false);
await check("owner removes", deleteDoc(doc(anna, "presence/S1/agents/a1")), false);
await check("the deck's presence doc", getDoc(doc(anna, "presence/S1")), false);

await env.cleanup();
console.log(`presence rules: ${ok.length} ok, ${bad.length} failed`);
for (const b of bad) console.log("FAIL " + b);
if (bad.length) process.exit(1);
