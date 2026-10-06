#!/usr/bin/env node
/**
 * npm run serve [-- --port=8080] [-- --data=mcp-go/data] [-- --check] [-- --no-build]
 *
 * The whole local server from the sources, in one go: the web app (npm run
 * build), the Go sources made from it (go generate in mcp-go: the Ranger
 * compiled to Go, fonts, themes and the web app copied in), the server
 * compiled (mcp-go/dist/sliqtly-server), and then started on the folder.
 * The server carries the web app inside it, so a page change shows only
 * after all three steps; this is that.
 *
 *   --check     before starting: npm run check and the Go tests
 *   --no-build  start the server as last compiled
 *   anything after `--` that is not one of these goes to the server
 *   (e.g. --listen=network)
 */
import fs from "node:fs";
import path from "node:path";
import { spawnSync, spawn } from "node:child_process";
import { root, log, ensureRanger } from "./lib.mjs";
import { build } from "./build.mjs";

const own = ["port", "data", "check", "no-build"];
const arg = (name, fallback) => {
  const hit = process.argv.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : fallback;
};
const has = (name) => process.argv.includes(`--${name}`);
const port = arg("port", process.env.PORT || "8080");
const mcp = path.join(root, "mcp-go");
const data = path.resolve(root, arg("data", path.join("mcp-go", "data")));
const bin = path.join(mcp, "dist", process.platform === "win32" ? "sliqtly-server.exe" : "sliqtly-server");
const extra = process.argv.slice(2).filter((a) => !own.some((o) => a === `--${o}` || a.startsWith(`--${o}=`)));

function run(what, cmd, args, opts = {}) {
  log(`${what.padEnd(6)} ${[cmd, ...args].join(" ")}`);
  const r = spawnSync(cmd, args, { stdio: "inherit", ...opts });
  if (r.error) throw new Error(`${cmd}: ${r.error.message}`);
  if (r.status !== 0) throw new Error(`${what} failed (${cmd} exited ${r.status})`);
}

try {
  if (!has("no-build")) {
    build({ ranger: ensureRanger() });
    run("gen", "go", ["generate"], { cwd: mcp });
    fs.mkdirSync(path.dirname(bin), { recursive: true });
    run("go", "go", ["build", "-o", bin, "."], { cwd: mcp, env: { ...process.env, CGO_ENABLED: "0" } });
  }
  if (has("check")) {
    run("check", process.execPath, [path.join(root, "scripts", "check.mjs")], { cwd: root });
    run("test", "go", ["test", "./..."], { cwd: mcp });
  }
  if (!fs.existsSync(bin)) throw new Error(`no server at ${path.relative(root, bin)} yet: run without --no-build`);
} catch (e) {
  log(e.message);
  process.exit(1);
}

fs.mkdirSync(data, { recursive: true });
log(`serve  http://localhost:${port}/  (folder ${path.relative(root, data) || "."})`);
const server = spawn(bin, ["-data", data, "-port", port, ...extra], { stdio: "inherit" });
// Ctrl+C reaches the server too; it stops, and so does this
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => server.kill(sig));
server.on("exit", (code, sig) => process.exit(sig ? 0 : code ?? 0));
