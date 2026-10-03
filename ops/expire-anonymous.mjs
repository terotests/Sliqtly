// One-off: gives the MCP decks made without sign-in before `expires`
// existed the same end as new ones, 30 days after their last change, so
// Firestore's TTL policy deletes them too. Their pictures and data files in
// Storage stay (TTL deletes documents only); the script lists those decks.
//
//   cd ops && npm ci && GOOGLE_APPLICATION_CREDENTIALS=sa.json node expire-anonymous.mjs [--write]
//
// Without --write it only reports.

import { initializeApp } from "firebase-admin/app";
import { getFirestore, Timestamp } from "firebase-admin/firestore";

// as the MCP server keeps a deck made without sign-in (mcp-go/rgr/Store.rgr)
const ANON_DAYS = 30;

initializeApp({ projectId: process.env.GCLOUD_PROJECT || "sliqtly" });
const db = getFirestore();
const write = process.argv.includes("--write");
const DAY = 24 * 60 * 60 * 1000;
const ms = (t) => (t && typeof t.toMillis === "function" ? t.toMillis() : typeof t === "number" ? t : Date.now());

const snap = await db.collection("shares").where("owner", "==", "mcp").get();
let n = 0;
for (const d of snap.docs) {
  const v = d.data();
  if (v.expires) continue;
  const expires = Timestamp.fromMillis(ms(v.updated || v.created) + ANON_DAYS * DAY);
  n++;
  console.log(`${d.id}  ${expires.toDate().toISOString().slice(0, 10)}  ${(v.files || []).length} files  ${JSON.stringify(v.name || "")}`);
  if (write) {
    await d.ref.update({ expires });
    const key = db.collection("mcp_keys").doc(d.id);
    if ((await key.get()).exists) await key.update({ expires });
  }
}
console.log(`${n} decks without sign-in had no expiry${write ? "; set" : " (dry run: add --write)"}.`);
