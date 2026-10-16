// SPDX-License-Identifier: AGPL-3.0-or-later
import test from "node:test";
import assert from "node:assert/strict";
import { sharedPanelJson, putShared, moveShared, makeSharedFolder, removeSharedFolder } from "../sharedfiles.js";

test("the Files panel gets each file's kind, folder and note, and the folders kept", () => {
  const res = {
    files: [
      { id: "app", name: "app.fig", kind: "figma", status: "ready", screens: 4, bytes: 2048, folder: "Designs" },
      { id: "q3", name: "Q3.pptx", kind: "pptx", status: "indexing" },
    ],
    folders: ["Empty"],
  };
  const out = JSON.parse(sharedPanelJson(res));
  assert.deepEqual(out.folders, ["Empty"]);
  assert.deepEqual(out.files[0], { id: "app", name: "app.fig", kind: "figma", status: "ready", folder: "Designs", note: "4 screens · 2 KB" });
  assert.equal(out.files[1].folder, "");
  assert.equal(out.files[1].note, "Indexing…");
  assert.deepEqual(JSON.parse(sharedPanelJson(null)), { files: [], folders: [] });
});

test("folder calls", async () => {
  const seen = [];
  const fetchFn = async (url, init = {}) => {
    seen.push([init.method || "GET", url, init.body && typeof init.body === "string" ? init.body : ""]);
    return new Response("{}", { status: 200 });
  };
  await putShared({ name: "a b.fig" }, fetchFn, null, "Designs/Mobile");
  await moveShared("app", "Old", fetchFn);
  await makeSharedFolder("New", fetchFn);
  await removeSharedFolder("New/x", fetchFn);
  assert.deepEqual(seen, [
    ["PUT", "/api/shared/a%20b.fig?folder=Designs%2FMobile", ""],
    ["POST", "/api/shared/app/move", '{"folder":"Old"}'],
    ["POST", "/api/shared/~folders", '{"path":"New"}'],
    ["DELETE", "/api/shared/~folders?path=New%2Fx", ""],
  ]);
});
