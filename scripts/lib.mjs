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
 *
 * The UI controls (UiHost, MenuCtl, CropCtl…) come from EVGUI, linked into
 * the same checkout as gallery/evgui, where its own ranger.json finds lib/evg.
 * EVGUI is cloned into .deps/EVGUI unless EVGUI_DIR points at a checkout.
 *
 * The diagram library, the Markdown engine and the PowerPoint module moved
 * out of Ranger into the private RangerFlow, RangerMarkdown and RangerPPTX.
 * Each is cloned INTO the Ranger checkout at the path it had there,
 * gallery/rangerflow, gallery/markdown and gallery/pptx (Ranger's .gitignore
 * leaves them out; the Markdown engine's deck export imports ../../pptx/…):
 * their imports reach the rest of
 * Ranger by relative paths (../../../rangerdb/…), which a link would resolve
 * from where the clone really is. A Ranger checkout from before the move
 * still tracks them and is used as is.
 * Cloning a private repository needs git credentials for github.com; in CI,
 * DEPS_TOKEN (scripts/ci-git-auth.sh).
 *
 * The version history (web/versions.js) uses RangerDiff's built module,
 * dist/rangerdiff.mjs, cloned into .deps/RangerDiff unless RANGERDIFF_DIR
 * points at a checkout.
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
export const EVGUI_LINK = "gallery/evgui";

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
  const marker = path.join(depsDir, "ranger-ref");
  if (given && !fs.existsSync(dir)) throw new Error(`RANGER_DIR=${given} does not exist`);
  if (!given) syncClone("Ranger", config.ranger.url, ref, dir, marker, update);
  if (!fs.existsSync(path.join(dir, "dist", "rgrc.js"))) throw new Error(`${dir} is not a Ranger checkout (no dist/rgrc.js)`);
  // RangerFlow, RangerMarkdown and RangerPPTX, cloned into the checkout
  // where it no longer tracks them
  for (const [key, at] of [
    ["rangerflow", "gallery/rangerflow"],
    ["rangermarkdown", "gallery/markdown"],
    ["rangerpptx", "gallery/pptx"],
  ]) {
    const place = path.join(dir, at);
    let st = null;
    try {
      st = fs.lstatSync(place);
    } catch { /* not there */ }
    if (st && st.isSymbolicLink()) fs.unlinkSync(place);
    else if (st && !fs.existsSync(path.join(place, ".git"))) continue; // tracked by this Ranger
    ensureCheckout(key, { update, into: place });
  }
  // SmartArt (src/PresSmartArt.rgr) is drawn by RangerPPTX's engine. A
  // Ranger ref that still tracks its own older gallery/pptx has none, and the
  // compile would fail far from the reason.
  if (!fs.existsSync(path.join(dir, "gallery/pptx/smartart/SaEngine.rgr"))) {
    throw new Error(
      `${dir} has no gallery/pptx/smartart: its gallery/pptx is not RangerPPTX.\n` +
        `The Ranger ref (${ref}) must be one that leaves gallery/pptx to RangerPPTX, which setup then clones there.`,
    );
  }
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
  link(ensureEvgui({ update }), path.join(dir, EVGUI_LINK));
  link(srcDir, path.join(dir, LINK));
  return dir;
}

/** EVGUI (the UI controls): EVGUI_DIR, or a clone in .deps at config.evgui.ref. */
export function ensureEvgui({ update = false } = {}) {
  const given = process.env.EVGUI_DIR;
  const dir = given ? path.resolve(given) : path.join(depsDir, "EVGUI");
  const ref = process.env.EVGUI_REF || config.evgui.ref;
  if (given && !fs.existsSync(dir)) throw new Error(`EVGUI_DIR=${given} does not exist`);
  if (!given) syncClone("EVGUI", config.evgui.url, ref, dir, path.join(depsDir, "evgui-ref"), update);
  if (!fs.existsSync(path.join(dir, "src", "UiHost.rgr"))) throw new Error(`${dir} is not an EVGUI checkout (no src/UiHost.rgr)`);
  return dir;
}

// what a checkout of each must hold
const CHECKOUT_HAS = {
  rangerflow: "layout/FlowWrap.rgr",
  rangermarkdown: "src/MdLayout.rgr",
  rangerpptx: "src/PptxModel.rgr",
};

/**
 * A dependency cloned like EVGUI: <KEY>_DIR, or a clone in .deps at
 * config[key].ref (or <KEY>_REF).
 */
export function ensureCheckout(key, { update = false, into = null } = {}) {
  const c = config[key];
  if (!c) throw new Error(`presentation.config.json has no "${key}"`);
  const name = c.url.replace(/\/+$/, "").split("/").pop();
  const env = key.toUpperCase();
  const given = into ? null : process.env[`${env}_DIR`];
  const dir = into || (given ? path.resolve(given) : path.join(depsDir, name));
  const ref = process.env[`${env}_REF`] || c.ref;
  // the ref the clone was taken at, kept beside it
  const marker = into ? path.join(dir, ".git", "sliqtly-ref") : path.join(depsDir, `${key}-ref`);
  if (given && !fs.existsSync(dir)) throw new Error(`${env}_DIR=${given} does not exist`);
  if (!given) syncClone(name, c.url, ref, dir, marker, update);
  const must = CHECKOUT_HAS[key];
  if (must && !fs.existsSync(path.join(dir, must))) throw new Error(`${dir} is not a ${name} checkout (no ${must})`);
  return dir;
}

/** RangerDiff (deltas and versions): RANGERDIFF_DIR, or a clone in .deps at config.rangerdiff.ref. */
export function ensureRangerDiff({ update = false } = {}) {
  const given = process.env.RANGERDIFF_DIR;
  const dir = given ? path.resolve(given) : path.join(depsDir, "RangerDiff");
  const ref = process.env.RANGERDIFF_REF || config.rangerdiff.ref;
  if (given && !fs.existsSync(dir)) throw new Error(`RANGERDIFF_DIR=${given} does not exist`);
  if (!given) syncClone("RangerDiff", config.rangerdiff.url, ref, dir, path.join(depsDir, "rangerdiff-ref"), update);
  if (!fs.existsSync(path.join(dir, "dist", "rangerdiff.mjs"))) throw new Error(`${dir} is not a RangerDiff checkout (no dist/rangerdiff.mjs)`);
  return dir;
}

/**
 * A clone kept in step with `ref` (a branch, as in presentation.config.json):
 * cloned when missing, fetched again when the config names another ref than
 * the one in `marker`, and also when the branch has moved on its remote
 * since. A ref that names a branch is a promise to build on its head, so a
 * clone that only refreshed on --update went on compiling against whatever
 * the branch held the day it was cloned (Sliqtly then imported controls
 * that clone did not have yet). Offline, the clone is used as it is.
 */
function syncClone(name, url, ref, dir, marker, update) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(path.dirname(dir), { recursive: true });
    log(`clone  ${url} (${ref}) → ${path.relative(root, dir)}`);
    git(["clone", "--depth", "1", ...(ref ? ["--branch", ref] : []), url, dir], path.dirname(dir));
    fs.writeFileSync(marker, ref + "\n");
    return;
  }
  const had = fs.existsSync(marker) ? fs.readFileSync(marker, "utf8").trim() : "";
  let why = "";
  if (update || had !== ref) why = `${had || "unknown"} → ${ref}`;
  else {
    let remote = "";
    try {
      remote = (git(["ls-remote", "origin", ref || "HEAD"], dir).split("\n")[0] || "").split(/\s+/)[0];
    } catch {
      log(`${name}: origin not reachable, building on the clone as it is`);
      return;
    }
    // no such branch or tag on the remote (e.g. ref is a commit): nothing to compare
    if (!remote) return;
    const head = git(["rev-parse", "HEAD"], dir);
    if (remote === head) return;
    why = `${ref} ${head.slice(0, 7)} → ${remote.slice(0, 7)}`;
  }
  // A clone with edits of its own (work on the engine before its PR is up)
  // is not checked out over: `checkout -f` would throw the edits away.
  let dirty = "";
  try {
    dirty = git(["status", "--porcelain", "--untracked-files=no"], dir);
  } catch {
    dirty = "";
  }
  if (dirty.trim()) {
    log(`${name}: has local changes, building on the clone as it is (not ${why})`);
    return;
  }
  log(`update ${name} (${why})`);
  git(["fetch", "--depth", "1", "origin", ref || "HEAD"], dir);
  git(["checkout", "-q", "-f", "--detach", "FETCH_HEAD"], dir);
  fs.writeFileSync(marker, ref + "\n");
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

/**
 * Heap for the Ranger compiler, in MB. PresApp pulls in some 16 MB of Ranger
 * source and its compile holds 4-5 GB at its peak; Node's default limit
 * depends on the machine (about 4 GB on many Macs, more on others), so the
 * same build passed on one machine and ran out of memory on another. The
 * limit is a ceiling, not memory taken up front. NODE_OPTIONS with its own
 * --max-old-space-size wins.
 */
const COMPILER_HEAP_MB = 8192;

/** Compile one Ranger file of src/ to `out` (absolute). */
export function compile(ranger, file, out, flag = "") {
  fs.mkdirSync(path.dirname(out), { recursive: true });
  const stage = path.join(ranger, "gallery", "presentation-build");
  fs.mkdirSync(stage, { recursive: true });
  const name = path.basename(out);
  const staged = path.join(stage, name);
  fs.rmSync(staged, { force: true });
  const heap = /--max-old-space-size/.test(process.env.NODE_OPTIONS || "") ? [] : [`--max-old-space-size=${COMPILER_HEAP_MB}`];
  const args = [...heap, "dist/rgrc.js", "-es6", ...(flag ? [flag] : []), `./${LINK}/${file}`, `-d=./gallery/presentation-build`, `-o=${name}`];
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
    // A crash of Node itself ends in its native stack, which says nothing;
    // the reason is the FATAL ERROR line above it.
    const fatal = lines.findIndex((l) => /FATAL ERROR|RangeError|out of memory/.test(l));
    const show = at >= 0 ? lines.slice(Math.max(0, at - 6), at + 8) : fatal >= 0 ? lines.slice(Math.max(0, fatal - 2), fatal + 3) : lines.slice(-30);
    if (/out of memory/.test(text)) show.push(`The compiler ran out of heap; raise it with NODE_OPTIONS=--max-old-space-size=<MB> (default here ${COMPILER_HEAP_MB}).`);
    throw new Error(`compile ${file} failed:\n${show.join("\n")}`);
  }
  fs.copyFileSync(staged, out);
  fs.rmSync(staged, { force: true });
  log(`build  ${path.relative(root, out)} (${((Date.now() - t0) / 1000).toFixed(1)}s)`);
}
