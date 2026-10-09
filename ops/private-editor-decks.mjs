// One-off: the presentations made in the editor become private. Until the
// editor's own links (links/{id}, Share) existed, a deck's address in the
// cloud was also its link: anyone who had it could read the deck. From now
// on only the owner and the people they invite open it, and Share makes a
// link of its own. Decks an assistant made (source "mcp", or
// owner "mcp" without sign-in) stay as they are: their link is what the
// assistant hands out.
//
//   cd ops && npm ci && GOOGLE_APPLICATION_CREDENTIALS=sa.json node private-editor-decks.mjs [--write]
//
// Without --write it only reports. Actions → Private editor decks runs it.

import { initializeApp } from "firebase-admin/app";
import { getFirestore } from "firebase-admin/firestore";

initializeApp({ projectId: process.env.GCLOUD_PROJECT || "sliqtly" });
const db = getFirestore();
const write = process.argv.includes("--write");

const snap = await db.collection("shares").get();
let n = 0, kept = 0;
for (const d of snap.docs) {
  const v = d.data();
  if (v.owner === "mcp" || v.source === "mcp") { kept++; continue; }
  if (v.visibility === "private") continue;
  n++;
  console.log(`${d.id}  ${v.visibility || "(none)"} → private`);
  if (write) await d.ref.update({ visibility: "private" });
}
console.log(`${n} editor decks were readable by link${write ? "; now private" : " (dry run: add --write)"}. ${kept} made by an assistant stay as they are.`);
