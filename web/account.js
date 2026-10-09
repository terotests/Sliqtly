// SPDX-License-Identifier: AGPL-3.0-or-later

// What this browser keeps is kept per Google account on sliqtly.com/editor.
//
// The browser's store (web/vfs.js, IndexedDB) and localStorage belong to the
// site, not to whoever is signed in: two accounts in one browser saw the same
// presentations, rooms and open tabs. On the editor each account has a store
// of its own (storeName) and its own keys (accountStorage); a server of one's
// own and the pages without an editor keep the one store they always had.
//
// What was kept before this (one store for everyone) is not handed to
// whoever signs in next: legacyChoice says whether to ask this account, once,
// and takeLegacy moves it in when the answer is yes.

// the localStorage keys holding one account's work; the rest (language,
// skin, sort order, zoom) is the browser's and stays shared
export const ACCOUNT_KEYS = ["sliqtly.rooms", "sliqtly.openFolders", "evgp.doc", "sliqtly.chatRead", "sliqtly.chatMe", "sliqtly.collab.me", "sliqtly.fileClip"];
// the sessionStorage keys holding the tab's open presentations
export const ACCOUNT_SESSION_KEYS = ["sliqtly.deckTabs", "sliqtly.tabDoc"];
// "taken" once an account moved the old store in
export const LEGACY_KEY = "sliqtly.legacyStore";
// the accounts that said the old store is not theirs, one id a line
export const DECLINED_KEY = "sliqtly.legacyDeclined";

const prefixOf = (scope) => "sliqtly@" + scope + "/";

// The IndexedDB database of an account ("" : the one store of old).
export function storeName(base, scope) {
  return scope ? base + "@" + scope : base;
}

// A Storage whose keys are the account's: the same calls (getItem, setItem,
// removeItem, key, length) over keys only it sees. scope "" is the storage
// itself. own(rawKey) is the key as this account names it, or null when it
// is not one of its own (a storage event's key).
export function accountStorage(storage, scope) {
  if (!scope) return {
    getItem: (k) => storage.getItem(k),
    setItem: (k, v) => storage.setItem(k, v),
    removeItem: (k) => storage.removeItem(k),
    key: (i) => storage.key(i),
    get length() { return storage.length; },
    own: (raw) => raw,
  };
  const prefix = prefixOf(scope);
  const keys = () => {
    const out = [];
    for (let i = 0; i < storage.length; i++) {
      const k = storage.key(i);
      if (k !== null && k.startsWith(prefix)) out.push(k.slice(prefix.length));
    }
    return out;
  };
  return {
    getItem: (k) => storage.getItem(prefix + k),
    setItem: (k, v) => storage.setItem(prefix + k, v),
    removeItem: (k) => storage.removeItem(prefix + k),
    key: (i) => keys()[i] ?? null,
    get length() { return keys().length; },
    own: (raw) => (raw !== null && raw.startsWith(prefix) ? raw.slice(prefix.length) : null),
  };
}

// Whether to ask the account `scope` if the old store is theirs: "ask" when
// it holds presentations (`kept` of them), nobody took it and this account
// has not said no; else "no".
export function legacyChoice(storage, scope, kept) {
  if (!scope || !kept) return "no";
  if (storage.getItem(LEGACY_KEY) === "taken") return "no";
  const declined = (storage.getItem(DECLINED_KEY) || "").split("\n");
  return declined.includes(scope) ? "no" : "ask";
}

// The account said the old store is not theirs: not asked again.
export function declineLegacy(storage, scope) {
  const had = (storage.getItem(DECLINED_KEY) || "").split("\n").filter(Boolean);
  if (!had.includes(scope)) storage.setItem(DECLINED_KEY, [...had, scope].join("\n"));
}

// The old keys of `keys` (and their "key/…" parts, as web/rooms.js keeps a
// room's facts) moved under the account. Nothing the account already has is
// written over.
export function moveKeys(storage, scope, keys) {
  const mine = accountStorage(storage, scope);
  const raw = [];
  for (let i = 0; i < storage.length; i++) raw.push(storage.key(i));
  for (const k of raw) {
    if (k === null || !keys.some((x) => k === x || k.startsWith(x + "/"))) continue;
    if (mine.getItem(k) === null) mine.setItem(k, storage.getItem(k));
    storage.removeItem(k);
  }
}

// The old store's presentations, files and history copied into the
// account's (`from`, `to`: web/vfs.js stores); the caller deletes the old
// one after. → how many presentations
export async function copyStore(from, to) {
  const docs = await from.listDocs();
  for (const d of docs) {
    for (const f of await from.listFiles(d.id)) await to.putFile(f);
    for (const o of await from.listObjects(d.id)) await to.putObject(o.doc, o.id, o.data);
    await to.putDoc(d);
  }
  return docs.length;
}
