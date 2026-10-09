import test from "node:test";
import assert from "node:assert/strict";
import { toBase64, fromBase64, fileBytes, fileState, plainChord } from "../slideclip.js";

test("base64 both ways, big files in chunks", () => {
  const big = new Uint8Array(100000).map((_, i) => (i * 7) % 256);
  const b = toBase64(big);
  assert.equal(b, Buffer.from(big).toString("base64"));
  assert.deepEqual(new Uint8Array(fromBase64(b.replace(/(.{76})/g, "$1\n"))), big);
});

test("a file's bytes from a string, a Blob or an array", async () => {
  assert.equal(new TextDecoder().decode(await fileBytes({ data: "a,b\n1,2" })), "a,b\n1,2");
  assert.deepEqual(new Uint8Array(await fileBytes({ data: new Blob([new Uint8Array([1, 2])]) })), new Uint8Array([1, 2]));
  assert.deepEqual(new Uint8Array(await fileBytes({ data: new Uint8Array([3, 4]) })), new Uint8Array([3, 4]));
  assert.equal((await fileBytes({})).byteLength, 0);
});

test("a pasted file against the deck's: new, same or differs", async () => {
  assert.equal(await fileState(null, "AA=="), "new");
  assert.equal(await fileState({ data: "x" }, "eA=="), "same");
  assert.equal(await fileState({ data: "x" }, "eQ=="), "differs");
});

test("Ctrl/⌘+Shift+V is Paste without formatting", () => {
  assert.equal(plainChord({ ctrlKey: true, shiftKey: true, key: "V" }), true);
  assert.equal(plainChord({ metaKey: true, shiftKey: true, key: "v" }), true);
  assert.equal(plainChord({ ctrlKey: true, key: "v" }), false);
});
