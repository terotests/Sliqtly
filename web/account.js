// SPDX-License-Identifier: AGPL-3.0-or-later

// What this browser keeps is kept per Google account on sliqtly.com/editor.
//
// localStorage and sessionStorage belong to the site, not to whoever is
// signed in: two accounts in one browser saw the same rooms and open tabs.
// On the editor each account has its own keys (accountStorage); a server of
// one's own and the pages without an editor keep the keys they always had.
// The editor keeps no presentations in the browser at all (web/main.js
// CLOUD_ONLY): they live in the cloud.

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
  const prefix = "sliqtly@" + scope + "/";
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
