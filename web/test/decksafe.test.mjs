// node --test: whether the open deck is kept anywhere (web/decksafe.js)
import test from "node:test";
import assert from "node:assert/strict";
import { deckSafety, safetyText } from "../decksafe.js";

// sliqtly.com's editor, a deck changed and saved to its share
const base = { editing: true, loading: false, kept: false, cloudOnly: true, persisted: true, signedIn: true, cloud: true, noRight: "", failed: false, sending: false, changed: false, halted: false };
const at = (o) => deckSafety({ ...base, ...o });

test("a deck in the cloud, or kept by the browser, is safe", () => {
  assert.deepEqual(at({}), { reason: "", warnLeave: false });
  assert.deepEqual(at({ kept: true, signedIn: false, cloud: false }), { reason: "", warnLeave: false });
  assert.deepEqual(at({ kept: true, noRight: "full" }), { reason: "", warnLeave: false });
});

test("on its way to the cloud: no pill, but leaving asks", () => {
  assert.deepEqual(at({ cloud: false }), { reason: "", warnLeave: true });
  assert.deepEqual(at({ sending: true }), { reason: "", warnLeave: true });
  assert.deepEqual(at({ changed: true }), { reason: "", warnLeave: true });
});

test("every way a deck is not saved anywhere", () => {
  assert.deepEqual(at({ signedIn: false, cloud: false }), { reason: "signed-out", warnLeave: true });
  assert.deepEqual(at({ cloud: false, noRight: "full" }), { reason: "license", warnLeave: true });
  assert.deepEqual(at({ noRight: "expired" }), { reason: "expired", warnLeave: true });
  assert.deepEqual(at({ noRight: "not-yours" }), { reason: "not-yours", warnLeave: true });
  assert.deepEqual(at({ failed: true }), { reason: "failing", warnLeave: true });
  // a refusal says more than the failure it also is
  assert.equal(at({ failed: true, noRight: "full" }).reason, "license");
  // signed out, nothing else matters
  assert.equal(at({ cloud: false, signedIn: false, noRight: "full" }).reason, "signed-out");
  // elsewhere, a browser that keeps nothing, and not signed in
  assert.deepEqual(at({ cloudOnly: false, signedIn: false, cloud: false }), { reason: "no-storage", warnLeave: true });
  // signed in there, the cloud keeps it as on sliqtly.com
  assert.deepEqual(at({ cloudOnly: false }), { reason: "", warnLeave: false });
});

test("nothing to lose: unchanged, opening, viewing, being deleted", () => {
  for (const o of [{ persisted: false, signedIn: false }, { loading: true, failed: true }, { editing: false, noRight: "full" }, { halted: true, failed: true }]) {
    assert.deepEqual(at(o), { reason: "", warnLeave: false }, JSON.stringify(o));
  }
});

test("the pill and what to do", () => {
  const t = (s) => s;
  assert.deepEqual(safetyText("", t), { pill: "", text: "" });
  const lic = safetyText("license", t, { max: 2 });
  assert.equal(lic.pill, "Not saved");
  assert.match(lic.text, /edit 2 presentations/);
  assert.match(lic.text, /File → Export → Markdown \(\.md\)/);
  assert.match(lic.text, /delete another presentation/);
  assert.match(safetyText("failing", t, { error: "unavailable" }).text, /^Saving to the cloud failed: unavailable\. /);
  assert.match(safetyText("signed-out", t).text, /sign in again/);
  assert.match(safetyText("no-storage", t).text, /does not allow storage.*sign in with PRO/);
  for (const r of ["signed-out", "no-storage", "license", "expired", "not-yours", "failing"]) assert.match(safetyText(r, t).text, /\.zip/);
});
