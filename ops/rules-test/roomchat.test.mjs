// SPDX-License-Identifier: AGPL-3.0-or-later
// Who reads a cloud room's chat (firestore.rules room_chat): its members,
// by user id or by a verified address; nobody writes it but the server, and
// nobody reads the rooms, memberships or people from a page.
// Runs with the others: npm test.
import { initializeTestEnvironment, assertFails, assertSucceeds } from "@firebase/rules-unit-testing";
import { readFileSync } from "node:fs";
import { doc, setDoc, getDoc, getDocs, query, collection, where, orderBy, limit } from "firebase/firestore";

const env = await initializeTestEnvironment({ projectId: "demo-sliqtly",
  firestore: { rules: readFileSync(new URL("../../firestore.rules", import.meta.url), "utf8"), host: "127.0.0.1", port: 8085 } });

const ok = [], bad = [];
async function check(name, p, expect) {
  try { await (expect ? assertSucceeds(p) : assertFails(p)); ok.push(name); } catch (e) { bad.push(name + ": " + e.message); }
}
await env.withSecurityRulesDisabled(async (c) => {
  const db = c.firestore();
  await setDoc(doc(db, "rooms/R1"), { tenant: "cloud", title: "Launch" });
  await setDoc(doc(db, "room_members/R1~user-anna"), { tenant: "cloud", room: "R1", member: "user:anna", role: "owner" });
  await setDoc(doc(db, "room_members/R1~email-bob@x.fi"), { tenant: "cloud", room: "R1", member: "email:bob@x.fi", role: "editor" });
  await setDoc(doc(db, "room_chat/cloud~R1"), { tenant: "cloud", room: "R1", seq: 1 });
  await setDoc(doc(db, "room_chat/cloud~R1/msgs/m1"), { seq: 1, thread: "", at: 1, touched: 1, msg: "{}" });
  await setDoc(doc(db, "room_chat/cloud~R2/msgs/m1"), { seq: 1, thread: "", at: 1, touched: 1, msg: "{}" });
  await setDoc(doc(db, "room_chat/local~R1/msgs/m1"), { seq: 1, thread: "", at: 1, touched: 1, msg: "{}" });
  await setDoc(doc(db, "chat_people/cloud~p-anna"), { tenant: "cloud", in: ["R1"] });
});
const anna = env.authenticatedContext("anna").firestore();
const bob = env.authenticatedContext("bob", { email: "Bob@X.fi", email_verified: true }).firestore();
const bobUnverified = env.authenticatedContext("bob2", { email: "bob@x.fi", email_verified: false }).firestore();
const carl = env.authenticatedContext("carl", { email: "carl@x.fi", email_verified: true }).firestore();
const nobody = env.unauthenticatedContext().firestore();
const since = (db, room) => getDocs(query(collection(db, "room_chat/" + room + "/msgs"), where("touched", ">", 0), orderBy("touched"), limit(50)));

await check("member by id reads a message", getDoc(doc(anna, "room_chat/cloud~R1/msgs/m1")), true);
await check("member by id listens to the new ones", since(anna, "cloud~R1"), true);
await check("member reads the room's seq", getDoc(doc(anna, "room_chat/cloud~R1")), true);
await check("member by verified address reads", since(bob, "cloud~R1"), true);
await check("unverified address reads", since(bobUnverified, "cloud~R1"), false);
await check("not a member reads", since(carl, "cloud~R1"), false);
await check("signed out reads", since(nobody, "cloud~R1"), false);
await check("member reads another room", since(anna, "cloud~R2"), false);
await check("another tenant's key", getDoc(doc(anna, "room_chat/local~R1/msgs/m1")), false);
await check("member writes a message", setDoc(doc(anna, "room_chat/cloud~R1/msgs/m2"), { seq: 2, thread: "", at: 2, touched: 2, msg: "{}" }), false);
await check("member changes a message", setDoc(doc(anna, "room_chat/cloud~R1/msgs/m1"), { seq: 1, thread: "", at: 1, touched: 3, msg: "{\"text\":\"x\"}" }), false);
await check("member raises the seq", setDoc(doc(anna, "room_chat/cloud~R1"), { seq: 9 }), false);
await check("rooms read from a page", getDoc(doc(anna, "rooms/R1")), false);
await check("memberships read from a page", getDoc(doc(anna, "room_members/R1~user-anna")), false);
await check("membership made from a page", setDoc(doc(carl, "room_members/R1~user-carl"), { room: "R1", member: "user:carl", role: "owner" }), false);
await check("people read from a page", getDoc(doc(anna, "chat_people/cloud~p-anna")), false);

await env.cleanup();
console.log(`room chat rules: ${ok.length} ok, ${bad.length} failed`);
for (const b of bad) console.log("FAIL " + b);
if (bad.length) process.exit(1);
