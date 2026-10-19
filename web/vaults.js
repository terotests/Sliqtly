// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vaults: a set of presentations chosen for one use, such as the ones fit to
// show in a demo. While a vault is on, the editor lists only its decks: the
// Rooms panel (a room with none of them is not shown), a room's Stories,
// File → Presentations… and Recent, the search, Onboarding's samples and the
// deck tabs. A vault is no room and no folder: a deck has one home room, but
// it can be in many vaults, so a vault is a view over the rooms, not a place.
//
// state.vaults: { vaultId: { title, decks: [deckId] } }, kept with the rooms
// of this browser (web/rooms.js, the fact "vault/<id>"), the account's on
// sliqtly.com (web/layoutsync.js). Deck ids are the rooms' own: "cloud:<share
// id>", this browser's deck id or "sample:<key>". The Demo Vault is always
// there; until it is changed it holds the welcome sample. Which vault is on
// is this browser's (VAULT_KEY), or the address's ?vault=<id>.

export const DEMO = "demo";
export const VAULT_KEY = "sliqtly.vault";
// the vaults there are before any is made or changed
export const BUILT_IN_VAULTS = { [DEMO]: { title: "Demo Vault", decks: ["sample:welcome"] } };
// a vault is one fact of the account's layout, at most 16 KiB (mcp-go/layout.go)
export const MAX_VAULT_DECKS = 300;

export const vaultTitle = (title) => String(title || "").replace(/\s+/g, " ").trim().slice(0, 100);

// A vault as kept, made sound: a title, decks as unique strings.
export function vaultOf(v) {
  const decks = Array.isArray(v?.decks) ? [...new Set(v.decks.filter((d) => typeof d === "string" && d))].slice(0, MAX_VAULT_DECKS) : [];
  return { title: vaultTitle(v?.title), decks };
}

const isBuiltInVault = (id) => Object.hasOwn(BUILT_IN_VAULTS, id);

// The vault `id` ({ id, title, decks }), or null when there is none.
export function findVault(state, id) {
  if (!id) return null;
  const kept = state?.vaults?.[id];
  if (kept) return { id, ...vaultOf(kept), title: vaultTitle(kept.title) || BUILT_IN_VAULTS[id]?.title || id };
  if (isBuiltInVault(id)) return { id, ...vaultOf(BUILT_IN_VAULTS[id]) };
  return null;
}

// Every vault: the built-in ones first, then the made ones by title.
export function listVaults(state) {
  const made = Object.keys(state?.vaults || {}).filter((id) => !isBuiltInVault(id));
  const rows = made.map((id) => findVault(state, id)).filter(Boolean);
  rows.sort((a, b) => a.title.localeCompare(b.title));
  return [...Object.keys(BUILT_IN_VAULTS).map((id) => findVault(state, id)), ...rows];
}

function withVault(state, id, v) {
  return { ...state, vaults: { ...(state.vaults || {}), [id]: vaultOf(v) } };
}

// A deck in the vault, or out of it. A vault that is not there takes none.
export function setInVault(state, vaultId, deckId, on) {
  const v = findVault(state, vaultId);
  if (!v || !deckId) return state;
  const has = v.decks.includes(deckId);
  if (has === !!on) return state;
  return withVault(state, vaultId, { title: v.title, decks: on ? [...v.decks, deckId] : v.decks.filter((d) => d !== deckId) });
}

// A deck gone (deleted) is in no vault any more.
export function forgetInVaults(state, deckId) {
  let next = state;
  for (const v of listVaults(state)) if (v.decks.includes(deckId)) next = setInVault(next, v.id, deckId, false);
  return next;
}

// A vault of one's own, empty. → { state, id } (id "" for no title)
export function createVault(state, title, idOf) {
  const t = vaultTitle(title);
  if (!t) return { state, id: "" };
  const id = "v-" + idOf();
  return { state: withVault(state, id, { title: t, decks: [] }), id };
}

export function renameVault(state, id, title) {
  const v = findVault(state, id);
  const t = vaultTitle(title);
  if (!v || !t || t === v.title) return state;
  return withVault(state, id, { title: t, decks: v.decks });
}

// A made vault goes; a built-in one is put back as it was.
export function deleteVault(state, id) {
  if (!state?.vaults?.[id]) return state;
  const vaults = { ...state.vaults };
  delete vaults[id];
  return { ...state, vaults };
}

// Is the deck in the vault? A row may be known by more than one id: this
// browser's deck kept in the cloud too is "cloud:<its share>" there.
export function vaultHas(vault, id, alias = "") {
  if (!vault) return true;
  return vault.decks.includes(id) || (!!alias && vault.decks.includes(alias));
}

// The alias of a deck row (web/decklist.js deckRows: its share, if any).
export const rowAlias = (row) => (row?.cloud && !String(row.id).startsWith("cloud:") ? "cloud:" + row.cloud : "");

// The rows in the vault (all of them when no vault is on).
export function vaultRows(rows, vault) {
  if (!vault) return rows;
  return (rows || []).filter((r) => vaultHas(vault, r.id, rowAlias(r)));
}

// The samples in the vault: [{ key, name }] as "sample:<key>".
export function vaultSamples(samples, vault) {
  if (!vault) return samples;
  return (samples || []).filter((s) => vault.decks.includes("sample:" + s.key));
}

// The rooms as the panel lists them, with a vault on: each counts only the
// vault's decks (`count`: { roomId: n }), and one with none is left out.
export function vaultRooms(rooms, vault, count) {
  if (!vault) return rooms;
  return (rooms || [])
    .map((r) => ({ ...r, presentations: count[r.room_id] || 0 }))
    .filter((r) => r.presentations > 0);
}

// The vault on: the address's ?vault= when it has one ("" or "off" leaves
// any), else the one kept; one that is not there (any more) is none.
// → { id, keep } keep: what to keep from now on (null: leave it as it is)
export function vaultChoice(state, asked, kept) {
  if (asked !== null && asked !== undefined) {
    const a = String(asked).trim();
    const id = a === "off" ? "" : a;
    return { id: findVault(state, id) ? id : "", keep: findVault(state, id) ? id : "" };
  }
  return { id: findVault(state, kept) ? kept : "", keep: null };
}

// The toolbar's lines for the vaults: "<id>\t<title>\t<deck>\t<deck>…", the
// first line the vault on ("" none).
export function vaultLines(state, on) {
  return [on || "", ...listVaults(state).map((v) => [v.id, v.title.replace(/[\t\n\r]+/g, " "), ...v.decks].join("\t"))].join("\n");
}
