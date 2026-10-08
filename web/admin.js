// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The owner's dashboard, /main/admin (web/admin.html). The numbers come from
// GET /main/admin/api/stats (mcp-go/admin.go), which answers only a Google
// account on the server's list; this page only signs in and draws. Sign-in
// is the viewer's (viewauth.js).

import { currentUser, signIn, authHeaders } from "./viewauth.js";

const API = "/main/admin/api/";
const RANGES = [7, 30, 90];

// ------------------------------------------------------------- the model --

/** Rounded "nice" top for an axis over values (at least 1). */
export function niceMax(max) {
  if (!(max > 0)) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(max)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= max) return m * p;
  return 10 * p;
}

/**
 * Bars for rows: one column per row, each series stacked (stack true) or
 * side by side. Returns {max, cols: [{x, w, bars: [{key, x, y, w, h}]}]}
 * in a width × height box, y down, the baseline at height.
 */
export function columns(rows, keys, { width, height, stack = true, gap = 2 }) {
  const n = rows.length || 1;
  const sums = rows.map((r) => (stack ? keys.reduce((s, k) => s + (r[k] || 0), 0) : Math.max(0, ...keys.map((k) => r[k] || 0))));
  const max = niceMax(Math.max(0, ...sums));
  const slot = width / n;
  const colW = Math.max(1, slot - Math.min(gap * 2, slot * 0.25));
  const cols = rows.map((r, i) => {
    const x = i * slot + (slot - colW) / 2;
    const bars = [];
    let base = height;
    const each = stack ? colW : Math.max(1, (colW - gap * (keys.length - 1)) / keys.length);
    keys.forEach((k, j) => {
      const v = r[k] || 0;
      const h = (v / max) * height;
      if (stack) {
        // the 2px surface gap between stacked fills
        const hh = Math.max(0, h - (base < height && h > 0 ? gap : 0));
        bars.push({ key: k, x, y: base - h, w: colW, h: hh, v });
        base -= h;
      } else {
        bars.push({ key: k, x: x + j * (each + gap), y: height - h, w: each, h, v });
      }
    });
    return { x: i * slot, w: slot, bars };
  });
  return { max, cols };
}

/** Money in the report's currency, cents shown. */
export function money(v, currency) {
  try {
    return new Intl.NumberFormat("en", { style: "currency", currency: currency || "EUR", minimumFractionDigits: 2 }).format(v || 0);
  } catch {
    return (v || 0).toFixed(2) + " " + (currency || "");
  }
}

const num = (v) => new Intl.NumberFormat("en").format(v || 0);
const shortDay = (d) => { const [, m, day] = d.split("-"); return `${+day}.${+m}.`; };

// -------------------------------------------------------------- the page --

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
let user = null;
let days = 30;
try { days = RANGES.includes(+localStorage.getItem("sliqtly-admin-days")) ? +localStorage.getItem("sliqtly-admin-days") : 30; } catch {}

export function show(state, text = "") {
  for (const id of ["signin", "denied", "board"]) $(id).hidden = id !== state;
  $("signout").hidden = !user;
  $("status").textContent = text;
}

/** A bar chart in el: series [{key, label, color}], hover tells the day. */
function chart(el, rows, series, { stack = true, fmt = num } = {}) {
  const W = Math.max(280, Math.round(el.clientWidth || 720)), H = 170, L = 40, B = 22, T = 8;
  const { max, cols } = columns(rows, series.map((s) => s.key), { width: W - L, height: H - B - T, stack });
  const color = Object.fromEntries(series.map((s) => [s.key, s.color]));
  const ticks = [0, max / 2, max];
  const every = Math.ceil(rows.length / Math.max(2, Math.floor((W - L) / 56)));
  let svg = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(el.dataset.title || "")}">`;
  for (const t of ticks) {
    const y = T + (H - B - T) * (1 - t / max);
    svg += `<line class="grid" x1="${L}" x2="${W}" y1="${y}" y2="${y}"/><text class="tick" x="${L - 6}" y="${y + 4}" text-anchor="end">${esc(fmt(t, true))}</text>`;
  }
  cols.forEach((c, i) => {
    for (const b of c.bars) {
      if (b.h <= 0) continue;
      // 4px rounded data end, square at the baseline
      const r = Math.min(4, b.w / 2, b.h);
      const x = L + b.x, y = T + b.y, w = b.w, h = b.h;
      svg += `<path fill="${color[b.key]}" d="M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z"/>`;
    }
    if ((cols.length - 1 - i) % every === 0) {
      svg += `<text class="tick" x="${L + c.x + c.w / 2}" y="${H - 6}" text-anchor="middle">${shortDay(rows[i].day)}</text>`;
    }
    svg += `<rect class="hit" data-i="${i}" x="${L + c.x}" y="0" width="${c.w}" height="${H - B}"/>`;
  });
  svg += `</svg>`;
  const legend = series.length > 1
    ? `<div class="legend">${series.map((s) => `<span><i style="background:${s.color}"></i>${esc(s.label)}</span>`).join("")}</div>` : "";
  const table = `<details><summary>Table</summary><div class="tablewrap"><table><thead><tr><th>Day</th>${series.map((s) => `<th>${esc(s.label)}</th>`).join("")}</tr></thead><tbody>${
    rows.slice().reverse().map((r) => `<tr><td>${r.day}</td>${series.map((s) => `<td>${esc(fmt(r[s.key] || 0))}</td>`).join("")}</tr>`).join("")}</tbody></table></div></details>`;
  el.innerHTML = legend + `<div class="plot">${svg}<div class="tip" hidden></div></div>` + table;
  const tip = el.querySelector(".tip");
  const plot = el.querySelector(".plot");
  plot.addEventListener("pointermove", (e) => {
    const hit = e.target.closest?.(".hit");
    if (!hit) { tip.hidden = true; return; }
    const r = rows[+hit.dataset.i];
    tip.innerHTML = `<b>${r.day}</b>` + series.map((s) => `<div><i style="background:${s.color}"></i>${esc(s.label)} <b>${esc(fmt(r[s.key] || 0))}</b></div>`).join("") + (r.extra ? `<div class="dim">${esc(r.extra)}</div>` : "");
    tip.hidden = false;
    const box = plot.getBoundingClientRect();
    const x = e.clientX - box.left;
    tip.style.left = Math.min(Math.max(0, x + 12), box.width - tip.offsetWidth) + "px";
    tip.style.top = "8px";
  });
  plot.addEventListener("pointerleave", () => { tip.hidden = true; });
}

const GREEN = "#74a015", BLUE = "#3987e5";

function tile(label, value, note = "") {
  return `<div class="tile"><div class="label">${esc(label)}</div><div class="value">${esc(value)}</div><div class="note">${esc(note)}</div></div>`;
}

let shown = null;

export function render(r) {
  shown = r;
  const b = r.billing;
  const cur = b.currency || "EUR";
  $("tiles").innerHTML = [
    tile("Visitors", num(r.visitors.visitors), `${num(r.visitors.views)} page loads`),
    tile("Presentations made", num(r.decks.total), `${num(r.decks.allTime)} kept in all`),
    tile("Signed-in people", num(r.users.total), `${num(r.users.new)} new, ${num(r.users.active)} active`),
    tile("Cloud cost this month", b.off ? "–" : money(b.month, cur), b.off ? "billing export not connected" : `last month ${money(b.lastMonth, cur)}`),
  ].join("");

  const vRows = r.visitors.rows.map((x) => ({ ...x, extra: `${x.views} loads · view ${x.view} · edit ${x.edit} · mobile ${x.mobile}` }));
  chart($("cVisitors"), vRows, [{ key: "visitors", label: "Visitors", color: GREEN }]);
  $("eVisitors").textContent = r.visitors.error ? "Not available: " + r.visitors.error : "";
  $("refs").innerHTML = r.visitors.refs.length
    ? "Came from: " + r.visitors.refs.map((x) => `${esc(x.name)} <b>${num(x.value)}</b>`).join(" · ") : "";

  chart($("cDecks"), r.decks.rows, [
    { key: "signedIn", label: "Signed in", color: GREEN },
    { key: "anonymous", label: "Assistant without sign-in", color: BLUE },
  ]);
  $("eDecks").textContent = r.decks.error ? "Not available: " + r.decks.error : "";

  chart($("cUsers"), r.users.rows, [
    { key: "new", label: "New", color: GREEN },
    { key: "active", label: "Last active", color: BLUE },
  ], { stack: false });
  $("eUsers").textContent = r.users.error ? "Not available: " + r.users.error : "";

  const moneyFmt = (v, axis) => (axis ? (v >= 10 ? Math.round(v) : v.toFixed(v ? 2 : 0)) : money(v, cur));
  if (b.off) {
    $("cBilling").innerHTML = "";
    $("nBilling").textContent = "";
    $("eBilling").innerHTML = `Not connected. Export Cloud Billing to BigQuery and set <code>SLIQTLY_BILLING_TABLE</code> on the service (mcp-go/README.md, “Owner's dashboard”).`;
    $("services").innerHTML = "";
  } else {
    chart($("cBilling"), b.rows.map((x) => ({ ...x })), [{ key: "cost", label: "Cost", color: GREEN }], { fmt: moneyFmt });
    $("eBilling").textContent = b.error ? "Not available: " + b.error : "";
    $("nBilling").textContent = b.error ? "" : (b.latest ? `Costs reach ${b.latest}; the export runs a day or so behind.` : "No costs exported yet.");
    $("services").innerHTML = b.services.length
      ? `<table><thead><tr><th>Service, this month</th><th>Cost</th></tr></thead><tbody>${b.services.map((s) => `<tr><td>${esc(s.name)}</td><td>${esc(money(s.value, cur))}</td></tr>`).join("")}</tbody></table>` : "";
  }
  $("generated").textContent = "Updated " + new Date(r.generated).toLocaleString();
}

async function load(fresh = false) {
  if (!user) return show("signin");
  $("status").textContent = "Loading…";
  let res;
  try {
    res = await fetch(`${API}stats?days=${days}${fresh ? "&fresh=1" : ""}`, { headers: await authHeaders(user) });
  } catch (e) {
    return ($("status").textContent = "Could not reach the server: " + e.message);
  }
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) return show("signin", body.error || "");
  if (res.status === 403) {
    $("who").textContent = user.email || "";
    return show("denied");
  }
  if (!res.ok) return ($("status").textContent = body.error || `The server answered ${res.status}.`);
  show("board");
  $("me").textContent = user.email || "";
  render(body);
}

function ranges() {
  $("ranges").innerHTML = RANGES.map((n) => `<button type="button" data-days="${n}" aria-pressed="${n === days}">${n} days</button>`).join("");
}

async function start() {
  ranges();
  $("ranges").addEventListener("click", (e) => {
    const n = +e.target.dataset?.days;
    if (!n) return;
    days = n;
    try { localStorage.setItem("sliqtly-admin-days", String(n)); } catch {}
    ranges();
    load();
  });
  $("refresh").addEventListener("click", () => load(true));
  // the charts are drawn to their width: again when it changes
  let width = innerWidth;
  addEventListener("resize", () => {
    if (shown && innerWidth !== width) render(shown);
    width = innerWidth;
  });
  const go = (pick) => async () => {
    try {
      user = await signIn(pick);
      load();
    } catch (e) {
      $("status").textContent = "Sign-in failed: " + (e.code || e.message);
    }
  };
  $("signinBtn").addEventListener("click", go(false));
  $("switchBtn").addEventListener("click", go(true));
  $("signout").addEventListener("click", async () => {
    await window.firebase?.auth().signOut();
    user = null;
    show("signin");
  });
  user = await currentUser();
  load();
}

if (typeof document !== "undefined" && document.getElementById("board")) start();
