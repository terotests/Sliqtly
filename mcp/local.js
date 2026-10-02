// A local run: node local.js → http://localhost:8790/mcp
//
// Without Firebase credentials the decks travel in the link (#md=…) and no
// pictures are kept. With GOOGLE_APPLICATION_CREDENTIALS set (a service
// account of the sliqtly project) it writes real shares, as the function does.

import express from "express";
import { mcpHandler } from "./src/http.js";
import { FirebaseStore, LinkStore } from "./src/store.js";

const port = Number(process.env.PORT || 8790);
const baseUrl = process.env.SLIQTLY_URL || "https://sliqtly.com";

async function makeStore() {
  if (!process.env.GOOGLE_APPLICATION_CREDENTIALS) return new LinkStore();
  const { initializeApp } = await import("firebase-admin/app");
  const { getFirestore, FieldValue } = await import("firebase-admin/firestore");
  const { getStorage } = await import("firebase-admin/storage");
  initializeApp({ projectId: process.env.GCLOUD_PROJECT || "sliqtly" });
  return new FirebaseStore({
    db: getFirestore(),
    bucket: getStorage().bucket(process.env.SLIQTLY_BUCKET || "sliqtly.firebasestorage.app"),
    FieldValue,
  });
}

const store = await makeStore();
const app = express();
app.use(express.json({ limit: "40mb" }));
app.all("/mcp", mcpHandler({ store, baseUrl }));
app.listen(port, () => console.log(`Sliqtly MCP (${store.kind}) on http://localhost:${port}/mcp`));
