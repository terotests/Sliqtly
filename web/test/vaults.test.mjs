// node --test: vaults, the decks chosen for one use (web/vaults.js)
import test from "node:test";
import assert from "node:assert/strict";
import { DEMO, findVault, listVaults, setInVault, forgetInVaults, createVault, renameVault, deleteVault, vaultHas, vaultRows, vaultSamples, vaultRooms, vaultChoice, vaultLines } from "../vaults.js";
import { emptyRooms, factsOf, readKept, changeKept, listRooms, roomDecks, GENERAL, ONBOARDING } from "../rooms.js";

function memStore() {
  const m = new Map();
  return {
    get length() { return m.size; },
    key: (i) => [...m.keys()][i] ?? null,
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
  };
}

test("the Demo Vault is there from the start, with the welcome sample", () => {
  const s = emptyRooms();
  assert.deepEqual(findVault(s, DEMO), { id: DEMO, title: "Demo Vault", decks: ["sample:welcome"] });
  assert.deepEqual(listVaults(s).map((v) => v.id), [DEMO]);
  assert.equal(findVault(s, "nope"), null);
  assert.equal(findVault(s, ""), null);
  // nothing kept until it changes
  assert.deepEqual([...factsOf(s).keys()], []);
});

test("a deck goes into a vault and out of it; a vault not there takes none", () => {
  let s = setInVault(emptyRooms(), DEMO, "cloud:A", true);
  assert.deepEqual(findVault(s, DEMO).decks, ["sample:welcome", "cloud:A"]);
  assert.equal(setInVault(s, DEMO, "cloud:A", true), s, "already in");
  s = setInVault(s, DEMO, "sample:welcome", false);
  assert.deepEqual(findVault(s, DEMO).decks, ["cloud:A"]);
  assert.equal(findVault(s, DEMO).title, "Demo Vault");
  const same = setInVault(s, "nope", "cloud:B", true);
  assert.equal(same, s);
  s = forgetInVaults(s, "cloud:A");
  assert.deepEqual(findVault(s, DEMO).decks, []);
});

test("vaults of one's own: made, renamed, listed after the built-in ones by title, deleted", () => {
  let n = 0;
  const idOf = () => "x" + ++n;
  let { state, id } = createVault(emptyRooms(), "  Sales   pitch ", idOf);
  assert.equal(id, "v-x1");
  ({ state } = createVault(state, "Board", idOf));
  assert.equal(createVault(state, "   ", idOf).id, "");
  assert.deepEqual(listVaults(state).map((v) => v.title), ["Demo Vault", "Board", "Sales pitch"]);
  state = renameVault(state, id, "Customers");
  assert.deepEqual(listVaults(state).map((v) => v.title), ["Demo Vault", "Board", "Customers"]);
  state = deleteVault(state, id);
  assert.equal(findVault(state, id), null);
  // the Demo Vault changed and deleted is as it was
  state = deleteVault(setInVault(state, DEMO, "cloud:A", true), DEMO);
  assert.deepEqual(findVault(state, DEMO).decks, ["sample:welcome"]);
});

test("a vault is kept as one fact with the rooms and read back", () => {
  const store = memStore();
  const facts = [];
  changeKept(store, "sliqtly.rooms", (s) => setInVault(s, DEMO, "cloud:A", true), (k, v) => facts.push([k, v]));
  assert.deepEqual(facts, [["vault/demo", JSON.stringify({ title: "Demo Vault", decks: ["sample:welcome", "cloud:A"] })]]);
  assert.deepEqual(findVault(readKept(store, "sliqtly.rooms"), DEMO).decks, ["sample:welcome", "cloud:A"]);
  // a bad fact is left out, the built-in vault is there as it was
  store.setItem("sliqtly.rooms/vault/demo", "{not json");
  assert.deepEqual(findVault(readKept(store, "sliqtly.rooms"), DEMO).decks, ["sample:welcome"]);
  // decks that are no strings or twice are dropped
  store.setItem("sliqtly.rooms/vault/demo", JSON.stringify({ title: "Demo", decks: ["a", "a", 3, null, "b"] }));
  assert.deepEqual(findVault(readKept(store, "sliqtly.rooms"), DEMO), { id: DEMO, title: "Demo", decks: ["a", "b"] });
});

test("with a vault on, only its decks are listed; a deck kept here and in the cloud is found by either id", () => {
  let s = setInVault(emptyRooms(), DEMO, "cloud:S1", true);
  s = setInVault(s, DEMO, "c", true);
  const v = findVault(s, DEMO);
  const rows = [{ id: "a" }, { id: "b", cloud: "S1" }, { id: "c" }, { id: "cloud:M1" }];
  assert.deepEqual(vaultRows(rows, v).map((r) => r.id), ["b", "c"]);
  assert.equal(vaultRows(rows, null), rows, "no vault: every row");
  assert.equal(vaultHas(null, "a"), true);
  assert.equal(vaultHas(v, "cloud:S1"), true);
  assert.equal(vaultHas(v, "b", "cloud:S1"), true);
  const samples = [{ key: "welcome", name: "Welcome" }, { key: "talous", name: "Finance" }];
  assert.deepEqual(vaultSamples(samples, v).map((x) => x.key), ["welcome"]);
  assert.deepEqual(vaultSamples(samples, findVault(setInVault(s, DEMO, "sample:welcome", false), DEMO)), []);
});

test("the Rooms panel in a vault: rooms count the vault's decks, an empty room is not shown", () => {
  let s = setInVault(emptyRooms(), DEMO, "a", true);
  const v = findVault(s, DEMO);
  const decks = [{ id: "a", name: "A" }, { id: "b", name: "B" }];
  const samples = [{ key: "welcome", name: "Welcome" }, { key: "talous", name: "Finance" }];
  const inV = vaultRows(decks, v);
  const smp = vaultSamples(samples, v);
  const count = {};
  for (const r of listRooms(s, inV, smp)) count[r.room_id] = r.presentations;
  const rooms = vaultRooms(listRooms(s, decks, samples), v, count);
  assert.deepEqual(rooms.map((r) => [r.room_id, r.presentations]), [[GENERAL, 1], [ONBOARDING, 1]]);
  assert.deepEqual(roomDecks(s, GENERAL, inV, smp).map((d) => d.id), ["a"]);
  assert.deepEqual(roomDecks(s, ONBOARDING, inV, smp).map((d) => d.id), ["sample:welcome"]);
});

test("which vault is on: the address wins and is kept, off leaves it, an unknown one is none", () => {
  const s = emptyRooms();
  assert.deepEqual(vaultChoice(s, null, ""), { id: "", keep: null });
  assert.deepEqual(vaultChoice(s, null, DEMO), { id: DEMO, keep: null });
  assert.deepEqual(vaultChoice(s, null, "gone"), { id: "", keep: null });
  assert.deepEqual(vaultChoice(s, "demo", ""), { id: DEMO, keep: DEMO });
  assert.deepEqual(vaultChoice(s, "", DEMO), { id: "", keep: "" });
  assert.deepEqual(vaultChoice(s, "off", DEMO), { id: "", keep: "" });
  assert.deepEqual(vaultChoice(s, "gone", DEMO), { id: "", keep: "" });
});

test("the toolbar's lines: the vault on, then each vault with its decks", () => {
  const s = setInVault(emptyRooms(), DEMO, "cloud:A", true);
  assert.equal(vaultLines(s, DEMO), "demo\ndemo\tDemo Vault\tsample:welcome\tcloud:A");
  assert.equal(vaultLines(emptyRooms(), ""), "\ndemo\tDemo Vault\tsample:welcome");
});
