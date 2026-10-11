#!/usr/bin/env node
/**
 * npm run check:vp8 — Sliqtly's own video encoder (src/PresVp8.rs) checked.
 *
 * src/PresVp8Check.rs codes a small test video. It is built twice, by Ranger
 * (JavaScript, as the page and `npm run video --codec=vp8` use the encoder)
 * and by rustc when it is on the PATH, and both must print the same bytes.
 * FFmpeg, when it is on the PATH, then decodes the IVF: every frame must be
 * exactly the picture the encoder kept as its reference (a VP8 encoder that
 * drifts from its decoder breaks every frame after), and the WebM must hold
 * every frame and the video's whole length.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { ensureRanger, compile, root, srcDir, log } from "./lib.mjs";

const W = 150;
const H = 100;
const PW = Math.ceil(W / 16) * 16;
const PH = Math.ceil(H / 16) * 16;

function has(cmd) {
  return spawnSync(cmd, ["-version"], { encoding: "utf8" }).status === 0 || spawnSync(cmd, ["--version"], { encoding: "utf8" }).status === 0;
}

function fail(msg) {
  log(`vp8: FAILED ${msg}`);
  process.exit(1);
}

try {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "vp8-check-"));
  const js = path.join(tmp, "vp8_check.js");
  compile(ensureRanger(), "PresVp8Check.rs", js, "-nodecli");
  const r = spawnSync(process.execPath, [js], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  if (r.status !== 0) fail(`the JavaScript build stopped:\n${r.stderr}`);
  const text = r.stdout;

  if (has("rustc")) {
    const bin = path.join(tmp, "vp8_check");
    const b = spawnSync("rustc", ["--edition", "2021", "-O", "-o", bin, path.join(srcDir, "PresVp8Check.rs")], { encoding: "utf8" });
    if (b.status !== 0) fail(`rustc rejected src/PresVp8Check.rs:\n${b.stderr}`);
    const n = spawnSync(bin, [], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
    if (n.stdout !== text) fail("rustc's build and Ranger's JavaScript wrote different bytes");
    log("vp8: rustc and Ranger's JavaScript write the same bytes");
  } else log("vp8: no rustc, the native build is not compared");

  const lines = text.trim().split("\n");
  const recons = lines.filter((l) => l.startsWith("R ")).map((l) => Buffer.from(l.slice(2), "hex"));
  const field = (k) => Buffer.from(lines.find((l) => l.startsWith(k + " ")).slice(k.length + 1), "hex");
  const ivf = field("IVF");
  const webm = field("WEBM");
  log(`vp8: ${recons.length} frames coded, ${lines.find((l) => l.startsWith("KEYS ")).slice(5)} key frames, IVF ${ivf.length} bytes, WebM ${webm.length} bytes`);

  if (!has("ffmpeg")) {
    log("vp8: no ffmpeg, the decode is not checked");
    process.exit(0);
  }
  fs.writeFileSync(path.join(tmp, "a.ivf"), ivf);
  fs.writeFileSync(path.join(tmp, "a.webm"), webm);
  const d = spawnSync("ffmpeg", ["-v", "error", "-i", path.join(tmp, "a.ivf"), "-f", "rawvideo", "-pix_fmt", "yuv420p", "-fps_mode", "passthrough", "-"], { maxBuffer: 256 * 1024 * 1024 });
  if (d.status !== 0) fail(`ffmpeg could not decode the IVF:\n${d.stderr}`);
  const cw = (W + 1) >> 1;
  const ch = (H + 1) >> 1;
  const size = W * H + 2 * cw * ch;
  const frames = d.stdout.length / size;
  if (frames !== recons.length) fail(`ffmpeg decoded ${frames} frames, the encoder coded ${recons.length}`);
  for (let i = 0; i < frames; i++) {
    const f = d.stdout.subarray(i * size, (i + 1) * size);
    const rec = recons[i];
    let bad = 0;
    for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) if (f[y * W + x] !== rec[y * PW + x]) bad++;
    for (let k = 0; k < 2; k++) {
      const fo = W * H + k * cw * ch;
      const ro = PW * PH + (k * PW * PH) / 4;
      for (let y = 0; y < ch; y++) for (let x = 0; x < cw; x++) if (f[fo + y * cw + x] !== rec[ro + y * (PW / 2) + x]) bad++;
    }
    if (bad) fail(`frame ${i}: ${bad} samples differ from the encoder's picture`);
  }
  log(`vp8: ffmpeg decodes all ${frames} frames to the encoder's own pictures`);
  const p = spawnSync("ffprobe", ["-v", "error", "-count_frames", "-show_entries", "stream=nb_read_frames:format=duration", "-of", "default=nw=1", path.join(tmp, "a.webm")], { encoding: "utf8" });
  const read = Number((/nb_read_frames=(\d+)/.exec(p.stdout) || [])[1]);
  const dur = Number((/duration=([\d.]+)/.exec(p.stdout) || [])[1]);
  if (read !== frames || Math.abs(dur - 2) > 0.001) fail(`the WebM: ${read} frames, ${dur} s (want ${frames}, 2 s)\n${p.stderr}`);
  log(`vp8: the WebM holds ${read} frames, ${dur} s`);
  fs.rmSync(tmp, { recursive: true, force: true });
} catch (e) {
  fail(e.message);
}
