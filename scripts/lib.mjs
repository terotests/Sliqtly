/**
 * Shared by the scripts.
 *
 * The presentation's Ranger sources import the markdown module, the code
 * editor and lib/evg by paths relative to a Ranger checkout, and are compiled
 * by Ranger's own compiler. So `src/` is LINKED into the checkout as
 * gallery/presentation — the same arrangement EvgHarness uses — and compiled
 * from there.
 *
 * Ranger is cloned into .deps/Ranger unless RANGER_DIR points at a checkout.
 */
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const srcDir = path.join(root, "src");
export const webDir = path.join(root, "web");
export const distDir = path.join(webDir, "dist");
export const depsDir = path.join(root, ".deps");
export const LINK = "gallery/presentation";

const config = JSON.parse(fs.readFileSync(path.join(root, "presentation.config.json"), "utf8"));

export function log(line) {
  process.stderr.write(`${line}\n`);
}

function git(args, cwd) {
  const r = spawnSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  if (r.status !== 0) throw new Error(`git ${args.join(" ")} failed:\n${(r.stderr || r.stdout || "").trim()}`);
  return (r.stdout || "").trim();
}

export function ensureRanger({ update = false } = {}) {
  const given = process.env.RANGER_DIR;
  const dir = given ? path.resolve(given) : path.join(depsDir, "Ranger");
  const ref = process.env.RANGER_REF || config.ranger.ref;
  // Which ref the clone in .deps was last taken from. A clone made before
  // the config named another branch is fetched again rather than silently
  // compiled against the old one.
  const marker = path.join(depsDir, "ranger-ref");
  const had = fs.existsSync(marker) ? fs.readFileSync(marker, "utf8").trim() : "";
  if (!fs.existsSync(dir)) {
    if (given) throw new Error(`RANGER_DIR=${given} does not exist`);
    fs.mkdirSync(depsDir, { recursive: true });
    log(`clone  ${config.ranger.url} (${ref}) → ${path.relative(root, dir)}`);
    git(["clone", "--depth", "1", ...(ref ? ["--branch", ref] : []), config.ranger.url, dir], root);
    fs.writeFileSync(marker, ref + "\n");
  } else if (!given && (update || had !== ref)) {
    log(`update Ranger (${had || "unknown"} → ${ref})`);
    git(["fetch", "--depth", "1", "origin", ref || "HEAD"], dir);
    git(["checkout", "-q", "-f", "--detach", "FETCH_HEAD"], dir);
    fs.writeFileSync(marker, ref + "\n");
  }
  if (!fs.existsSync(path.join(dir, "dist", "rgrc.js"))) throw new Error(`${dir} is not a Ranger checkout (no dist/rgrc.js)`);
  // What this checkout has to have. A checkout of your own (RANGER_DIR) is
  // never switched for you, so say which branch it needs.
  if (!fs.existsSync(path.join(dir, "gallery/rangerflow/layout/FlowWrap.rgr"))) {
    throw new Error(
      `${dir} has no gallery/rangerflow/layout/FlowWrap.rgr.\n` +
        (given
          ? `Check out ${ref} there (git fetch origin ${ref} && git checkout ${ref}), or unset RANGER_DIR to use .deps/Ranger.`
          : `Run npm run setup -- --update.`),
    );
  }
  // lib/evg is not in Ranger's git: its `npm run deps` fetches it from the
  // commit its ranger.json pins (terotests/evg), and fetches nothing when it
  // is already in place. An older checkout still tracks lib/evg and has no
  // scripts/deps.mjs.
  if (fs.existsSync(path.join(dir, "scripts", "deps.mjs"))) {
    const r = spawnSync(process.execPath, [path.join(dir, "scripts", "deps.mjs")], { cwd: dir, stdio: "inherit" });
    if (r.status !== 0) throw new Error(`npm run deps failed in ${dir}`);
  }
  link(srcDir, path.join(dir, LINK));
  return dir;
}

function link(target, at) {
  let st = null;
  try {
    st = fs.lstatSync(at);
  } catch {
    st = null;
  }
  if (st && st.isSymbolicLink()) {
    if (path.resolve(path.dirname(at), fs.readlinkSync(at)) === path.resolve(target)) return;
    fs.unlinkSync(at);
  } else if (st) {
    throw new Error(`${at} exists and is not a link; move it away so ${target} can be linked there`);
  }
  fs.symlinkSync(target, at, process.platform === "win32" ? "junction" : "dir");
  log(`link   ${at} → ${target}`);
}

/** Compile one Ranger file of src/ to `out` (absolute). */
export function compile(ranger, file, out, flag = "") {
  fs.mkdirSync(path.dirname(out), { recursive: true });
  const stage = path.join(ranger, "gallery", "presentation-build");
  fs.mkdirSync(stage, { recursive: true });
  const name = path.basename(out);
  const staged = path.join(stage, name);
  fs.rmSync(staged, { force: true });
  const args = ["dist/rgrc.js", "-es6", ...(flag ? [flag] : []), `./${LINK}/${file}`, `-d=./gallery/presentation-build`, `-o=${name}`];
  const t0 = Date.now();
  const r = spawnSync(process.execPath, args, {
    cwd: ranger,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    env: { ...process.env, RANGER_LIB: "./compiler/Lang.rgr:./lib/stdops.rgr" },
  });
  const text = `${r.stdout || ""}${r.stderr || ""}`;
  if (r.status !== 0 || /Compilation FAILED/.test(text) || !fs.existsSync(staged)) {
    const lines = text.split("\n");
    const at = lines.findIndex((l) => /\[FAIL\]/.test(l));
    const show = at >= 0 ? lines.slice(Math.max(0, at - 6), at + 8) : lines.slice(-30);
    throw new Error(`compile ${file} failed:\n${show.join("\n")}`);
  }
  fs.copyFileSync(staged, out);
  fs.rmSync(staged, { force: true });
  log(`build  ${path.relative(root, out)} (${((Date.now() - t0) / 1000).toFixed(1)}s)`);
}
