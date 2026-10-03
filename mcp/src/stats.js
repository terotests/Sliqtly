// Visitor counts without cookies: the page posts one beacon per load to
// /api/hit (web/main.js countVisit) and this counts it in stats/<UTC day>:
//
//   views     page loads
//   visitors  distinct visitors that day: SHA-256 of a salt of the day, the
//             address and the browser. The salt is random, kept in
//             stats_salt/<day> and deleted by the TTL policy two days later,
//             so a hash can no longer be tied to an address after that; the
//             same person on two days is two visitors.
//   pages     loads by page: editor (/), view (/s/{id}), edit (/s/{id}?edit)
//   devices   visitors by mobile / desktop
//   refs      visitors by the site they came from (host only)
//
// stats_seen/<hash> marks a visitor counted today; it goes with the salt.
// Nothing else about a visitor is kept. No client rule reaches these.

import crypto from "node:crypto";

const DAY = 24 * 60 * 60 * 1000;
export const PAGES = ["editor", "view", "edit"];
const BOT = /bot|crawl|spider|slurp|headless|lighthouse|preview|facebookexternalhit|curl|wget|python|node-fetch|go-http/i;
const MOBILE = /mobi|android|iphone|ipad/i;
const HOST = /^[a-z0-9.-]{1,64}$/;

export const dayOf = (t) => new Date(t).toISOString().slice(0, 10);

// What one beacon counts, or null when it counts nothing (a bot, a page
// that is not one of ours, a referrer that is our own site).
export function visitOf({ body, ua, own = ["sliqtly.com", "sliqtly.web.app"] }) {
  if (!ua || BOT.test(ua)) return null;
  const page = body && typeof body.p === "string" ? body.p : "";
  if (!PAGES.includes(page)) return null;
  let ref = typeof body.r === "string" ? body.r.toLowerCase().replace(/^www\./, "") : "";
  if (!HOST.test(ref) || own.includes(ref)) ref = "";
  return { page, device: MOBILE.test(ua) ? "mobile" : "desktop", ref };
}

// hit({ ip, ua, body }) → true when counted. db: Firestore (Admin SDK).
export function createStats({ db, FieldValue, now = () => Date.now(), random = () => crypto.randomBytes(32).toString("hex") }) {
  const salts = new Map();

  async function saltOf(day, t) {
    if (salts.has(day)) return salts.get(day);
    const ref = db.collection("stats_salt").doc(day);
    const salt = await db.runTransaction(async (tx) => {
      const snap = await tx.get(ref);
      if (snap.exists) return snap.data().salt;
      const s = random();
      tx.set(ref, { salt: s, expires: new Date(t + 2 * DAY) });
      return s;
    });
    salts.clear();
    salts.set(day, salt);
    return salt;
  }

  return async function hit({ ip, ua, body }) {
    const v = visitOf({ body, ua });
    if (!v) return false;
    const t = now();
    const day = dayOf(t);
    const id = crypto.createHash("sha256").update(`${await saltOf(day, t)}|${ip}|${ua}`).digest("hex");
    const seen = db.collection("stats_seen").doc(id);
    const stats = db.collection("stats").doc(day);
    const inc = FieldValue.increment(1);
    await db.runTransaction(async (tx) => {
      const fresh = !(await tx.get(seen)).exists;
      const add = { views: inc, pages: { [v.page]: inc } };
      if (fresh) {
        tx.set(seen, { expires: new Date(t + 2 * DAY) });
        Object.assign(add, { visitors: inc, devices: { [v.device]: inc } });
        if (v.ref) add.refs = { [v.ref]: inc };
      }
      tx.set(stats, add, { merge: true });
    });
    return true;
  };
}
