// node --test: the AI assistant's calls (web/aiassist.js)
import test from "node:test";
import assert from "node:assert/strict";
import { aiCall, aiOnPage } from "../aiassist.js";

const answer = (status, body) => async () => ({ status, text: async () => body });

test("an answer and a refusal with a reason both come back as JSON text", async () => {
  const seen = [];
  const fetchFn = async (url, opt) => { seen.push([url, opt.method || "GET", opt.body]); return { status: 200, text: async () => '{"reply":"Hi"}' }; };
  assert.equal(await aiCall("ask", '{"message":"x"}', fetchFn), '{"reply":"Hi"}');
  assert.deepEqual(seen[0], ["/editor/api/ai/ask", "POST", '{"message":"x"}']);
  await aiCall("credits", undefined, fetchFn);
  assert.deepEqual(seen[1].slice(0, 2), ["/editor/api/ai/credits", "GET"]);
  assert.equal(await aiCall("ask", "{}", answer(402, '{"error":"used up","code":"no-credits"}')), '{"error":"used up","code":"no-credits"}');
});

test("no answer at all throws", async () => {
  await assert.rejects(aiCall("ask", "{}", answer(502, "<html>Bad gateway</html>")), /HTTP 502/);
  await assert.rejects(aiCall("ask", "{}", async () => { throw new TypeError("offline"); }), /offline/);
});

test("only the editor's page on the server has the assistant", () => {
  assert.equal(aiOnPage({ querySelector: (q) => (q.includes("sliqtly-editor") ? {} : null) }), true);
  assert.equal(aiOnPage({ querySelector: () => null }), false);
  assert.equal(aiOnPage(undefined), false);
});
