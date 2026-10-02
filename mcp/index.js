// Cloud Function `mcp` (2nd gen), served by Hosting at https://sliqtly.web.app/mcp
// (firebase.json rewrites /mcp to it). Firestore and Storage are the ones
// the editor's Share button writes to.

import { onRequest } from "firebase-functions/v2/https";
import { initializeApp } from "firebase-admin/app";
import { getFirestore, FieldValue } from "firebase-admin/firestore";
import { getStorage } from "firebase-admin/storage";
import { mcpHandler } from "./src/http.js";
import { FirebaseStore } from "./src/store.js";

initializeApp();

const store = new FirebaseStore({
  db: getFirestore(),
  bucket: getStorage().bucket(process.env.SLIQTLY_BUCKET || "sliqtly.firebasestorage.app"),
  FieldValue,
});

export const mcp = onRequest(
  { region: "europe-west1", invoker: "public", memory: "512MiB", timeoutSeconds: 60, maxInstances: 10, concurrency: 40 },
  mcpHandler({ store, baseUrl: process.env.SLIQTLY_URL || "https://sliqtly.web.app" }),
);
