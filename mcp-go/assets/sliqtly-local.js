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
//   GET    /api/collab/{id}                editing together (mcp-go/collab.go):
//   POST   /api/collab/{id}/op|presence|chat   the deck's room, web/collab.js
//   GET    /api/socket                     the page's one stream: the server's
//                                          state, decks changed, the room
//                                          (web/eventline.js)

import { EventLine } from "./eventline.js";

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

// made(id), when given, is told the id as soon as the share exists, before
// its files go (web/sliqtly.js share)
async function share(deck, made) {
  if (!user) throw new Error("not signed in");
  const { id } = await api("POST", "/api/shares", {
    name: deck.name, md: deck.md, theme: deck.theme || "", css: deck.css ?? null, deck: deck.deckId,
  });
  try {
    if (made) await made(id);
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

// `since.collab`: the deck's room writes the Markdown (web/collab.js), so
// it is neither sent nor compared here
async function saveShare(id, deck, since) {
  if (!user) throw new Error("not signed in");
  const cur = await loadShare(id);
  if (!cur) throw Object.assign(new Error("share not found"), { code: "not-found" });
  if (!since.collab && since.md != null && cur.md !== since.md) throw Object.assign(new Error("changed elsewhere"), { code: "changed-elsewhere" });
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
  const patch = { name: deck.name, theme: deck.theme || "", css: deck.css ?? null, files: kept };
  if (!since.collab) {
    patch.md = deck.md;
    patch.ifMd = since.md ?? null;
  }
  await api("PATCH", "/api/shares/" + id, patch);
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

// Editing a deck together: its room on this server (mcp-go/collab.go), for
// web/collab.js's CollabSession. The room's events come on the page's one
// stream (line, below), which comes back by itself after a drop, naming the
// last edit it had; the server sends the edits it missed.
let line = null;
const collab = {
  snapshot: (id) => api("GET", "/api/collab/" + id),
  send: (id, body) => api("POST", "/api/collab/" + id + "/op", body),
  presence: (id, body) => api("POST", "/api/collab/" + id + "/presence", body),
  chat: (id, body) => api("POST", "/api/collab/" + id + "/chat", body),
  // the first open is the join itself, a later one a reconnect
  stream(id, q, onEvent, onOpen) {
    if (!line) throw new Error("this page has no stream to the server");
    return line.join(id, q, onEvent, onOpen);
  },
};

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

// this server's version, for Help → About
async function serverVersion() {
  if (firstVersion) return firstVersion;
  const res = await fetch("/api/status", { cache: "no-store" });
  return res.ok ? (await res.json()).version || "" : "";
}

window.sliqtly = {
  auth, user: () => user, signedIn, share, saveShare, deleteShare, loadShare, listMine, readSheet,
  putObject, getObject, pushHead, readHead, collab, serverVersion,
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
// merges); the player opened at /s/{id} takes the changes in place, on the
// slide it shows (web/main.js followShare). Not in an assistant's preview,
// which is not served from here. One stream per page, for this and the
// room alike (web/eventline.js).
function listen() {
  if ((typeof WebSocket !== "function" && typeof EventSource !== "function") || !/^https?:$/.test(location.protocol)) return;
  const viewing = /^\/s\/([A-Za-z0-9]{6,32})\/?$/.exec(location.pathname);
  const player = viewing && !new URLSearchParams(location.search).has("edit") ? viewing[1] : null;
  let timer = 0;
  const changed = new Set();
  // out of reach: said after a moment, so a quick restart passes quietly;
  // the stream keeps trying, and "ready" clears it
  let lost = 0;
  let open = false;
  line = new EventLine({
    WebSocket: typeof WebSocket === "function" ? WebSocket : undefined,
    EventSource: typeof EventSource === "function" ? EventSource : undefined,
    location,
    setTimeout: (f, ms) => setTimeout(f, ms),
    clearTimeout: (t) => clearTimeout(t),
  }, {
    status: onStatus,
    open() {
      open = true;
      clearTimeout(lost);
      lost = 0;
      // a room's chat open on the page asks for what it missed meanwhile
      window.dispatchEvent(new CustomEvent("sliqtly:chat", { detail: { t: "reopen" } }));
    },
    // rooms' chats (web/roomchat.js): a message posted or changed, who is here
    chat(v) {
      window.dispatchEvent(new CustomEvent("sliqtly:chat", { detail: v }));
    },
    lost() {
      open = false;
      // each try that fails is another loss: the first one starts the clock
      if (lost) return;
      lost = setTimeout(() => {
        lost = 0;
        if (open) return;
        down = true;
        say("Offline: the server cannot be reached. Your changes are kept in this browser and saved when it is back.");
      }, 4000);
    },
    changed(id) {
      changed.add(id);
      // one update writes the deck more than once: the last one counts
      clearTimeout(timer);
      timer = setTimeout(() => {
        const ids = [...changed];
        changed.clear();
        if (player) {
          if (ids.includes(player)) window.__followShare?.(player);
          return;
        }
        window.__checkElsewhere?.();
      }, 400);
    },
  });
  line.start();
}
listen();
