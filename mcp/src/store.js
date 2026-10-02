// Where the MCP server keeps a deck.
//
// FirebaseStore writes the same share the editor's Share button makes
// (web/sliqtly.js): shares/{id} in Firestore, its pictures in Storage under
// shares/{id}/media/…, so /s/{id} opens it with no change to the page. The
// share is world-readable by id, so the edit key's hash lives apart, in
// mcp_keys/{id}, which only the Admin SDK reaches (firestore.rules has no
// match for it).
//
// LinkStore keeps nothing: the deck travels compressed in the link
// (#md=…, the editor's own text-only share). It is what a local run uses
// without Firebase credentials. Pictures cannot travel that way.

import crypto from "node:crypto";
import zlib from "node:zlib";

const ABC = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";

// 10 characters of a-z, A-Z, 0-9, as the editor's shortId()
export function shortId(n = 10) {
  return [...crypto.randomBytes(n)].map((b) => ABC[b % ABC.length]).join("");
}

export function hashKey(key) {
  return crypto.createHash("sha256").update(key).digest("hex");
}

// the editor's packText(): deflate-raw, then base64url
export function packText(text) {
  return zlib.deflateRawSync(Buffer.from(text, "utf8")).toString("base64url");
}

export function unpackText(code) {
  return zlib.inflateRawSync(Buffer.from(code, "base64url")).toString("utf8");
}

export class FirebaseStore {
  constructor({ db, bucket, FieldValue }) {
    this.db = db;
    this.bucket = bucket;
    this.FieldValue = FieldValue;
    this.kind = "cloud";
  }

  async create({ name, md, theme, css, images }) {
    const id = shortId();
    const key = shortId(24);
    const now = this.FieldValue.serverTimestamp();
    const doc = this.db.collection("shares").doc(id);
    await this.db.collection("mcp_keys").doc(id).set({ hash: hashKey(key), created: now });
    const files = await this.#upload(id, images);
    await doc.set({ name, md, theme, css, owner: "mcp", deck: "mcp", source: "mcp", files, created: now });
    return { id, key };
  }

  // null when the id or the key does not match
  async update(id, key, { name, md, theme, css, images }) {
    const keyDoc = await this.db.collection("mcp_keys").doc(id).get();
    if (!keyDoc.exists || keyDoc.data().hash !== hashKey(key)) return null;
    const doc = this.db.collection("shares").doc(id);
    const snap = await doc.get();
    if (!snap.exists) return null;
    const cur = snap.data();
    const added = await this.#upload(id, images);
    const files = [...(cur.files || []).filter((f) => !added.some((a) => a.path === f.path)), ...added];
    const patch = { files, updated: this.FieldValue.serverTimestamp() };
    if (name != null) patch.name = name;
    if (md != null) patch.md = md;
    if (theme != null) patch.theme = theme;
    if (css !== undefined) patch.css = css;
    await doc.update(patch);
    return { ...cur, ...patch };
  }

  async get(id) {
    const snap = await this.db.collection("shares").doc(id).get();
    return snap.exists ? snap.data() : null;
  }

  async #upload(id, images) {
    const out = [];
    for (const img of images || []) {
      const path = `media/${img.name}`;
      const file = this.bucket.file(`shares/${id}/${path}`);
      // the token is what getDownloadURL() hands the editor for its own uploads
      const token = crypto.randomUUID();
      await file.save(img.data, { contentType: img.type, resumable: false, metadata: { metadata: { firebaseStorageDownloadTokens: token } } });
      const url = `https://firebasestorage.googleapis.com/v0/b/${this.bucket.name}/o/${encodeURIComponent(file.name)}?alt=media&token=${token}`;
      out.push({ path, type: img.type, size: img.data.length, url });
    }
    return out;
  }
}

export class LinkStore {
  constructor() {
    this.kind = "link";
  }
}
