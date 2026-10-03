// Sliqtly's numbers for the last days, as Markdown tables: visitors and
// page loads (stats/<day>, src/stats.js), new and returning signed-in users
// (Firebase Auth), and shares made (shares.created). Counts only: no email,
// name or id is printed.
//
//   cd mcp && GOOGLE_APPLICATION_CREDENTIALS=sa.json node scripts/stats.mjs [days]
//
// The Stats workflow (Actions → Stats → Run workflow) runs it and shows the
// tables in the run's summary.

import { initializeApp } from "firebase-admin/app";
import { getAuth } from "firebase-admin/auth";
import { getFirestore, Timestamp } from "firebase-admin/firestore";
import { dayOf } from "../src/stats.js";

initializeApp({ projectId: process.env.GCLOUD_PROJECT || "sliqtly" });
const db = getFirestore();
const DAY = 24 * 60 * 60 * 1000;
const days = Math.max(1, Math.min(366, Number(process.argv[2]) || 30));
const today = Date.now();
const list = Array.from({ length: days }, (_, i) => dayOf(today - i * DAY));
const first = list[list.length - 1];
const out = [];
const top = (o = {}, n = 3) => Object.entries(o).sort((a, b) => b[1] - a[1]).slice(0, n).map(([k, v]) => `${k} ${v}`).join(", ");

async function section(title, fn) {
  out.push(`## ${title}`, "");
  try { await fn(); } catch (e) { out.push(`Not available: ${e.message}`); }
  out.push("");
}

await section("Visitors (no cookies; a visitor is one browser on one day)", async () => {
  const snaps = await db.getAll(...list.map((d) => db.collection("stats").doc(d)));
  const rows = snaps.filter((s) => s.exists);
  if (!rows.length) return out.push("No counts yet: the counter starts when the MCP function with /api/hit is deployed.");
  out.push("| Day | Visitors | Loads | Editor / view / edit | Mobile | From |", "| --- | ---: | ---: | --- | ---: | --- |");
  let v = 0, l = 0;
  for (const s of rows) {
    const d = s.data();
    v += d.visitors || 0;
    l += d.views || 0;
    const p = d.pages || {};
    out.push(`| ${s.id} | ${d.visitors || 0} | ${d.views || 0} | ${p.editor || 0} / ${p.view || 0} / ${p.edit || 0} | ${(d.devices || {}).mobile || 0} | ${top(d.refs)} |`);
  }
  out.push(`| **total** | **${v}** | **${l}** | | | |`);
});

await section("Signed-in users (Firebase Auth)", async () => {
  const made = {}, seen = {};
  let all = 0, token;
  do {
    const page = await getAuth().listUsers(1000, token);
    for (const u of page.users) {
      all++;
      const c = dayOf(Date.parse(u.metadata.creationTime));
      if (c >= first) made[c] = (made[c] || 0) + 1;
      const last = u.metadata.lastRefreshTime || u.metadata.lastSignInTime;
      if (last) { const s = dayOf(Date.parse(last)); if (s >= first) seen[s] = (seen[s] || 0) + 1; }
    }
    token = page.pageToken;
  } while (token);
  out.push(`${all} users in all.`, "", "| Day | New users | Last active |", "| --- | ---: | ---: |");
  for (const d of list) if (made[d] || seen[d]) out.push(`| ${d} | ${made[d] || 0} | ${seen[d] || 0} |`);
});

await section("Shares made", async () => {
  const snap = await db.collection("shares").where("created", ">=", Timestamp.fromMillis(Date.parse(first))).get();
  const byDay = {};
  for (const s of snap.docs) {
    const v = s.data();
    const d = dayOf(v.created.toMillis());
    const r = (byDay[d] ||= { n: 0, mcp: 0, owners: new Set() });
    r.n++;
    if (v.owner === "mcp") r.mcp++; else r.owners.add(v.owner);
  }
  const owners = new Set(snap.docs.map((s) => s.data().owner).filter((o) => o !== "mcp"));
  out.push(`${snap.size} shares by ${owners.size} signed-in people, ${snap.docs.filter((s) => s.data().owner === "mcp").length} from MCP without sign-in.`, "",
    "| Day | Shares | People | MCP anonymous |", "| --- | ---: | ---: | ---: |");
  for (const d of list) if (byDay[d]) out.push(`| ${d} | ${byDay[d].n} | ${byDay[d].owners.size} | ${byDay[d].mcp} |`);
});

console.log(`# Sliqtly, ${first} – ${list[0]}\n\n` + out.join("\n"));
