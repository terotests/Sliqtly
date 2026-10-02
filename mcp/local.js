// A local run: node local.js → http://localhost:8790/mcp
//
// Without Firebase credentials the decks travel in the link (#md=…) and no
// pictures are kept. With GOOGLE_APPLICATION_CREDENTIALS set (a service
// account of the sliqtly project) it writes real shares and offers sign-in,
// as the function does.

import { createApp } from "./src/http.js";
import { createOAuth } from "./src/oauth.js";
import { FirebaseStore, LinkStore } from "./src/store.js";

const port = Number(process.env.PORT || 8790);
const baseUrl = process.env.SLIQTLY_URL || "https://sliqtly.com";

async function makeStore() {
  if (!process.env.GOOGLE_APPLICATION_CREDENTIALS) return { store: new LinkStore(), oauth: null };
  const { initializeApp } = await import("firebase-admin/app");
  const { getFirestore, FieldValue } = await import("firebase-admin/firestore");
  const { getStorage } = await import("firebase-admin/storage");
  const { getAuth } = await import("firebase-admin/auth");
  initializeApp({ projectId: process.env.GCLOUD_PROJECT || "sliqtly" });
  const db = getFirestore();
  const store = new FirebaseStore({
    db,
    bucket: getStorage().bucket(process.env.SLIQTLY_BUCKET || "sliqtly.firebasestorage.app"),
    FieldValue,
  });
  return { store, oauth: createOAuth({ db, verifyIdToken: (t) => getAuth().verifyIdToken(t) }) };
}

const { store, oauth } = await makeStore();
const app = createApp({ store, oauth, baseUrl, trustHost: true });
app.listen(port, () => console.log(`Sliqtly MCP (${store.kind}${oauth ? ", sign-in" : ""}) on http://localhost:${port}/mcp`));
