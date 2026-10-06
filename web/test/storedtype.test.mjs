// node --test: the editor keeps files only under the types storage.rules
// takes (web/storedtype.js), or a share's save is refused part way.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { STORED_TYPES, storedType } from "../storedtype.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

test("storage.rules allows the same types as storedtype.js", () => {
  const rules = fs.readFileSync(path.join(root, "storage.rules"), "utf8");
  const m = /contentType\.matches\(\s*'([^']*)'\)/.exec(rules);
  assert.ok(m, "storage.rules has a contentType rule");
  assert.equal(m[1], STORED_TYPES);
});

test("pictures, sound, data and the editor's files keep their type", () => {
  for (const t of [
    "image/png", "image/jpeg", "image/svg+xml", "audio/webm;codecs=opus", "video/mp4",
    "text/csv", "text/plain", "text/markdown", "application/json", "application/pdf",
    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "application/vnd.sliqtly.ink+json", "application/octet-stream",
  ]) assert.equal(storedType(t), t, t);
});

test("pages and scripts are kept as bytes", () => {
  for (const t of ["text/html", "text/html; charset=utf-8", "application/xhtml+xml", "text/javascript",
    "application/javascript", "text/xml", "application/xml", "image/svg+xml-x", "", undefined]) {
    assert.equal(storedType(t), "application/octet-stream", String(t));
  }
});
