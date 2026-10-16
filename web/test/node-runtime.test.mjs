// node --test: Node.js's fs, path, process and Buffer as a program run on a
// presentation's files sees them (web/node-runtime.js, run_script): the
// answers are compared with Node's own where Node has them.
import test from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import nodePath from "node:path";
import { NODE_RUNTIME } from "../node-runtime.js";

function sandbox(files = []) {
  const mods = {};
  const out = [];
  const ctx = vm.createContext({
    JSON, Math, Date, Promise, queueMicrotask, BigInt,
    console: { log: (...a) => out.push(a.join(" ")) },
    defineModule: (name, exports) => { mods[name] = exports; return exports; },
  });
  vm.runInContext(NODE_RUNTIME, ctx);
  ctx.__nodeMount(JSON.stringify({ files, argv: ["a"] }));
  return { ctx, mods, out, changes: () => JSON.parse(ctx.__nodeChanges()) };
}

test("path answers as Node's posix path does", () => {
  const { mods } = sandbox();
  const p = mods.path;
  const cases = [
    ["join", ["a", "..", "b/", "c"]], ["join", ["/a", "./b", "../c"]], ["join", [""]],
    ["normalize", ["a//b/../c/"]], ["normalize", ["/../x"]], ["dirname", ["/a/b/c.txt"]], ["dirname", ["c.txt"]],
    ["basename", ["/a/b.svg", ".svg"]], ["extname", ["x/y.tar.gz"]], ["extname", [".hidden"]],
    ["relative", ["/data/x", "/media/y.svg"]], ["isAbsolute", ["/a"]], ["parse", ["/home/u/f.txt"]],
  ];
  for (const [fn, args] of cases) assert.deepEqual(JSON.parse(JSON.stringify(p[fn](...args))), nodePath.posix[fn](...args), fn + " " + args);
  assert.equal(p.resolve("data", "x.csv"), "/data/x.csv");
});

test("files read, write and fail as Node's do; what changed comes back", () => {
  const s = sandbox([
    { path: "data/s.csv", text: "a,b\n1,2\n" },
    { path: "media/p.png", size: 10, binary: true },
    { path: "data/b-S.csv", text: "x\n", readonly: true, why: "a sheet" },
  ]);
  const fs = s.mods.fs;
  assert.equal(fs.readFileSync("data/s.csv", "utf8"), "a,b\n1,2\n");
  assert.equal(fs.readFileSync("/data/s.csv").toString(), "a,b\n1,2\n");
  assert.deepEqual([...fs.readdirSync("/")], ["apps", "code", "data", "media", "tmp"]);
  assert.equal(fs.statSync("media/p.png").size, 10);
  assert.throws(() => fs.readFileSync("media/p.png"), (e) => e.code === "ENOTSUP");
  assert.throws(() => fs.readFileSync("nope"), (e) => e.code === "ENOENT" && e.message === "ENOENT: no such file or directory, open '/nope'");
  assert.throws(() => fs.writeFileSync("data/b-S.csv", "y"), (e) => e.code === "EACCES");
  assert.throws(() => fs.writeFileSync("no/dir/x.txt", "y"), (e) => e.code === "ENOENT");
  assert.throws(() => fs.readFileSync("data"), (e) => e.code === "EISDIR");
  fs.mkdirSync("/tmp/a/b", { recursive: true });
  assert.throws(() => fs.rmdirSync("/tmp/a"), (e) => e.code === "ENOTEMPTY");
  fs.writeFileSync("data/s.csv", "a,b\n1,2\n");
  fs.writeFileSync("media/x.svg", "<svg/>");
  fs.appendFileSync("media/x.svg", "\n");
  fs.writeFileSync("media/y.png", s.ctx.Buffer.from("aGVp", "base64"));
  fs.unlinkSync("data/b-S.csv".replace("b-S", "s"));
  const c = s.changes();
  assert.deepEqual(c.written.map((w) => w.path), ["media/x.svg", "media/y.png"]);
  assert.equal(c.written[0].text, "<svg/>\n");
  assert.equal(c.written[1].base64, "aGVp");
  assert.deepEqual(c.removed, ["data/s.csv"]);
});

test("Buffer, process and require", () => {
  const { ctx, mods } = sandbox();
  assert.equal(ctx.Buffer.from("hei ää").toString("base64"), Buffer.from("hei ää").toString("base64"));
  assert.equal(ctx.Buffer.from("68c3a4", "hex").toString(), "hä");
  assert.equal(ctx.Buffer.byteLength("ää"), 4);
  assert.deepEqual([...ctx.process.argv], ["node", "/script.js", "a"]);
  assert.equal(ctx.require("node:fs"), mods.fs);
  assert.equal(mods["node:path"].default, mods.path);
  assert.throws(() => ctx.require("lodash"), /no npm packages/);
  assert.throws(() => ctx.process.exit(3), (e) => e.name === "ProcessExit" && e.code === 3);
});

test("callbacks and fs.promises answer after the current step", async () => {
  const { mods } = sandbox([{ path: "data/s.csv", text: "a" }]);
  const order = [];
  mods.fs.readFile("data/s.csv", "utf8", (e, t) => order.push("cb " + t));
  order.push("sync");
  order.push("p " + (await mods.fs.promises.readFile("data/s.csv", "utf8")));
  await mods["fs/promises"].readFile("none").catch((e) => order.push(e.code));
  assert.deepEqual(order, ["sync", "cb a", "p a", "ENOENT"]);
});
