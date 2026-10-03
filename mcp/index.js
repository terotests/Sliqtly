// Cloud Function `mcp` (2nd gen), served by Hosting at https://sliqtly.com/mcp
// (firebase.json rewrites /mcp, /oauth/** and the OAuth /.well-known
// documents to it). Firestore and Storage are the ones the editor's Share
// button writes to; sign-in is the editor's PRO account (Firebase Auth).

import { onRequest } from "firebase-functions/v2/https";
import { initializeApp } from "firebase-admin/app";
import { getAuth } from "firebase-admin/auth";
import { getFirestore, FieldValue } from "firebase-admin/firestore";
import { getStorage } from "firebase-admin/storage";
import { createApp, dailyQuota } from "./src/http.js";
import { createOAuth } from "./src/oauth.js";
import { FirebaseStore } from "./src/store.js";
import { createStats } from "./src/stats.js";

initializeApp();

const db = getFirestore();
const store = new FirebaseStore({
  db,
  bucket: getStorage().bucket(process.env.SLIQTLY_BUCKET || "sliqtly.firebasestorage.app"),
  FieldValue,
});
const oauth = createOAuth({ db, verifyIdToken: (t) => getAuth().verifyIdToken(t) });

export const mcp = onRequest(
  { region: "europe-west1", invoker: "public", memory: "512MiB", timeoutSeconds: 60, maxInstances: 10, concurrency: 40 },
  createApp({ store, oauth, quota: dailyQuota({ db }), stats: createStats({ db, FieldValue }), baseUrl: process.env.SLIQTLY_URL || "https://sliqtly.com" }),
);
