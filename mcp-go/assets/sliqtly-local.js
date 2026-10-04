// SPDX-License-Identifier: AGPL-3.0-or-later

// web/sliqtly.js for a server of one's own (mcp-go/local.go serves this file
// at /sliqtly.js in its place): the same window.sliqtly, kept by the server
// that served the page instead of Firebase. There is no sign-in: everyone
// who reaches the server is its one user (SLIQTLY_USER), so the page acts
// as PRO, signed in, and decks are shares in the server's folder.
//
//   GET    /api/me                         { uid, name }
//   GET    /api/shares                     the user's shares
//   POST   /api/shares                     a new share → { id }
//   GET    /api/shares/{id}                a share, or 404
//   PATCH  /api/shares/{id}                change fields; ifMd refuses a
//                                          share changed since (409)
//   DELETE /api/shares/{id}                the share and its files
//   POST   /api/shares/{id}/head           move the version head (pushHead)
//   PUT    /api/files/shares/{id}/{path}   a file → { path, type, size, url }
//   GET    /files/shares/{id}/{path}       read one
//   DELETE /api/files/shares/{id}/{path}

const pro = document.getElementById("pro");
let user = null;

async function api(method, path, body, type) {
  const init = { method, cache: "no-store", headers: {} };
  if (body !== undefined) {
    if (body instanceof Blob || body instanceof Uint8Array || body instanceof ArrayBuffer) {
      init.body = body;
      init.headers["Content-Type"] = type || "application/octet-stream";
    } else {
      init.body = JSON.stringify(body);
      init.headers["Content-Type"] = "application/json";
    }
  }
  const res = await fetch(path, init);
  if (res.status === 404) return null;
  if (!res.ok) {
    let msg = "HTTP " + res.status;
    let code = "";
    try {
      const e = await res.json();
      msg = e.error || msg;
      code = e.code || "";
    } catch (_) { /* not JSON */ }
    if (code === "maintenance") onStatus({ state: "migrating" });
    throw Object.assign(new Error(msg), { code });
  }
  return res.status === 204 ? null : res.json();
}

const enc = (path) => path.split("/").map(encodeURIComponent).join("/");

function show() {
  if (!pro) return;
  pro.textContent = user ? user.displayName : "Local";
  pro.title = user ? "This server keeps your presentations (" + location.host + ")" : "";
}

if (pro) {
  pro.addEventListener("click", () => {
    window.open("/decks", "_blank", "noopener");
  });
}

// what Firebase Auth's object offers main.js
const authObj = {
  onAuthStateChanged(cb) {
    cb(user);
    return () => {};
  },
  signOut: async () => {},
};

function auth() {
  return Promise.resolve(authObj);
}

function signedIn() {
  return Promise.resolve(user);
}

async function putFile(id, f) {
  const blob = f.data instanceof Blob ? f.data : new Blob([f.data ?? ""], { type: f.type || "text/plain" });
  return api("PUT", "/api/files/shares/" + id + "/" + enc(f.path), blob, f.type || blob.type || "application/octet-stream");
}

async function share(deck) {
  if (!user) throw new Error("not signed in");
  const { id } = await api("POST", "/api/shares", {
    name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null, deck: deck.deckId,
  });
  try {
    const kept = [];
    for (const f of deck.files || []) kept.push(await putFile(id, f));
    if (kept.length) await api("PATCH", "/api/shares/" + id, { files: kept });
  } catch (e) {
    throw Object.assign(e instanceof Error ? e : new Error(String(e)), { shareId: id });
  }
  return id;
}

async function loadShare(id) {
  return api("GET", "/api/shares/" + id);
}

async function listMine() {
  return (await api("GET", "/api/shares")) || [];
}

async function saveShare(id, deck, since) {
  if (!user) throw new Error("not signed in");
  const cur = await loadShare(id);
  if (!cur) throw Object.assign(new Error("share not found"), { code: "not-found" });
  if (since.md != null && cur.md !== since.md) throw Object.assign(new Error("changed elsewhere"), { code: "changed-elsewhere" });
  const had = new Map((cur.files || []).map((f) => [f.path, f]));
  const kept = [];
  for (const f of deck.files) {
    const prev = had.get(f.path);
    if (prev && since.stamps?.get(f.path) === f.stamp) {
      kept.push(prev);
      continue;
    }
    kept.push(await putFile(id, f));
  }
  for (const path of had.keys()) {
    if (!deck.files.some((f) => f.path === path)) api("DELETE", "/api/files/shares/" + id + "/" + enc(path)).catch(() => {});
  }
  await api("PATCH", "/api/shares/" + id, {
    name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null, files: kept,
    ifMd: since.md ?? null,
  });
  return kept;
}

async function deleteShare(id) {
  if (!user) throw new Error("not signed in");
  await api("DELETE", "/api/shares/" + id);
}

async function putObject(shareId, objId, bytes) {
  await api("PUT", "/api/files/shares/" + shareId + "/.versions/" + encodeURIComponent(objId), new Blob([bytes]), "application/octet-stream");
}

async function getObject(shareId, objId) {
  const res = await fetch("/files/shares/" + shareId + "/.versions/" + encodeURIComponent(objId), { cache: "no-store" });
  return res.ok ? new Uint8Array(await res.arrayBuffer()) : null;
}

async function pushHead(shareId, expect, head, entries) {
  return api("POST", "/api/shares/" + shareId + "/head", { expect: expect || null, head, entries });
}

async function readHead(shareId) {
  const s = await loadShare(shareId);
  return s ? { ...s, head: s.head || null, log: s.log || [] } : null;
}

// private Google Sheets need Google: not here
async function readSheet() {
  throw Object.assign(new Error("Google Sheets are not available on this server"), { code: "auth" });
}

try {
  const me = await api("GET", "/api/me");
  if (me) user = { uid: me.uid, displayName: me.name, email: "" };
} catch (e) {
  console.warn("this server's API:", e.message);
}
show();

window.sliqtly = {
  auth, user: () => user, signedIn, share, saveShare, deleteShare, loadShare, listMine, readSheet,
  putObject, getObject, pushHead, readHead,
  sheetsToken: () => null, askSheets: async () => null, sheetName: () => null,
};
window.dispatchEvent(new Event("sliqtly:ready"));
window.dispatchEvent(new Event("sliqtly:user"));

// A line at the top of the page about the server itself: being updated,
// restarting, out of reach, or updated to a new version. Edits are kept in
// this browser meanwhile (the editor saves every deck here first), and go
// to the server when it is back.
let banner = null;
function say(text, reload) {
  if (!text) {
    banner?.remove();
    banner = null;
    return;
  }
  if (!banner) {
    banner = document.createElement("div");
    banner.id = "server-status";
    banner.setAttribute("role", "status");
    banner.style.cssText = "position:fixed;top:0;left:50%;transform:translateX(-50%);z-index:2147483000;" +
      "max-width:calc(100vw - 32px);margin-top:8px;padding:8px 14px;border-radius:8px;" +
      "font:14px/1.4 system-ui,sans-serif;background:#2b2b28;color:#f4f4f0;box-shadow:0 4px 16px rgba(0,0,0,.25);" +
      "display:flex;gap:12px;align-items:center";
    document.body.append(banner);
  }
  banner.textContent = text;
  if (reload) {
    const b = document.createElement("button");
    b.textContent = "Reload";
    b.style.cssText = "font:inherit;padding:2px 10px;border-radius:6px;border:0;cursor:pointer";
    b.onclick = () => location.reload();
    banner.append(b);
  }
}

// The server's state as /api/events tells it (mcp-go/localstatus.go).
let firstVersion = "";
let down = false;
function onStatus(st) {
  if (st.state === "migrating") {
    down = true;
    say("The server is being updated. Your changes are kept in this browser and saved when it is back.");
    return;
  }
  if (st.state === "failed") {
    down = true;
    say("The server could not finish its update. Your changes are kept in this browser; ask the server's administrator.");
    return;
  }
  if (st.state === "stopping") {
    down = true;
    say("The server is restarting. Your changes are kept in this browser and saved when it is back.");
    return;
  }
  if (st.state !== "ready") return;
  if (!firstVersion) firstVersion = st.version;
  const wasDown = down;
  down = false;
  if (st.version !== firstVersion) {
    say("The server was updated to " + st.version + ". Reload to use the new version.", true);
  } else {
    say("");
  }
  // what could not be saved while it was away goes now
  if (wasDown) {
    window.__cloudSoon?.();
    window.__checkElsewhere?.();
  }
}

// A deck changed on the server (an assistant's update_presentation, another
// tab's save): the editor compares with it at once, as it otherwise does on
// focus and every minute, and takes it when nothing was changed here (or
// merges); the player opened at /s/{id} reloads on the slide it shows (the
// address keeps it), without the intro. Not in an assistant's preview,
// which is not served from here.
function listen() {
  if (typeof EventSource !== "function" || !/^https?:$/.test(location.protocol)) return;
  const viewing = /^\/s\/([A-Za-z0-9]{6,32})\/?$/.exec(location.pathname);
  const player = viewing && !new URLSearchParams(location.search).has("edit") ? viewing[1] : null;
  let timer = 0;
  const changed = new Set();
  const es = new EventSource("/api/events");
  es.addEventListener("status", (ev) => {
    try { onStatus(JSON.parse(ev.data)); } catch (_) { /* not one */ }
  });
  // out of reach: said after a moment, so a quick restart passes quietly;
  // the browser keeps trying, and "ready" clears it
  let lost = 0;
  es.onerror = () => {
    // the browser tries again every few seconds, each try another error:
    // the first one starts the clock
    if (lost) return;
    lost = setTimeout(() => {
      if (es.readyState === EventSource.OPEN) return;
      down = true;
      say("Offline: the server cannot be reached. Your changes are kept in this browser and saved when it is back.");
    }, 4000);
  };
  es.onopen = () => {
    clearTimeout(lost);
    lost = 0;
  };
  es.onmessage = (ev) => {
    let id = "";
    try { id = JSON.parse(ev.data).id || ""; } catch (_) { return; }
    changed.add(id);
    // one update writes the deck more than once: the last one counts
    clearTimeout(timer);
    timer = setTimeout(() => {
      const ids = [...changed];
      changed.clear();
      if (player) {
        if (!ids.includes(player)) return;
        try { sessionStorage.setItem("sliqtly:quiet-reload", "1"); } catch (_) { /* the intro plays */ }
        location.reload();
        return;
      }
      window.__checkElsewhere?.();
    }, 400);
  };
}
listen();
