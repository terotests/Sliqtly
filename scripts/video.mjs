#!/usr/bin/env node
/**
 * npm run video — a presentation as a video file, through FFmpeg.
 *
 * Milestone 1 of the video export (README, "Video"): the page draws
 * each frame at time n / fps with its own painter (web/videoexport.js,
 * src/PresVideo.rgr) in headless Chromium, and FFmpeg encodes the RGBA
 * frames to VP9 (or AV1) in a WebM. No screen is filmed and no clock is
 * read, so the same deck gives the same frames every time.
 *
 *   npm run video -- --sample=esittely --out=esittely.webm
 *   npm run video -- --deck=talk.md --recording=take.json --audio=take.webm
 *
 *   --sample=NAME      a sample deck (samples/NAME.md)
 *   --deck=FILE.md     a Markdown file (its text only: no media folder yet)
 *   --recording=FILE   a take.json: the video follows the recording
 *   --audio=FILE       the recording's sound (take.webm), cuts left out
 *   --out=FILE         default video.webm
 *   --size=WxH         default 1920x1080
 *   --fps=N            default 30
 *   --codec=vp9|av1    default vp9
 *   --quality=0..63    the encoder's crf, default 32
 *   --pix=yuv420p|yuv444p
 *   --frames=N         only the first N frames
 *   --raw=FILE         also write the raw RGBA stream (rawvideo)
 *   --timeline=FILE    also write the compiled timeline (JSON)
 *
 * Prints how many frames were drawn and how many repeated the one before,
 * and the SHA-256 of the raw frames: two runs of one deck give the same.
 */
import fs from "node:fs";
import path from "node:path";
import http from "node:http";
import crypto from "node:crypto";
import { spawn } from "node:child_process";
import { distDir, root, log } from "./lib.mjs";
import { build } from "./build.mjs";

const args = Object.fromEntries(process.argv.slice(2).map((a) => {
  const m = /^--([^=]+)(?:=(.*))?$/.exec(a);
  return m ? [m[1], m[2] === undefined ? "1" : m[2]] : [a, "1"];
}));

const [W, H] = (args.size || "1920x1080").split("x").map(Number);
const options = {
  width: W,
  height: H,
  fps: Number(args.fps || 30),
  codec: args.codec || "vp9",
  quality: Number(args.quality ?? 32),
  pixelFormat: args.pix || "yuv420p",
};
const out = path.resolve(args.out || "video.webm");

function chromiumPath() {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const base = process.env.PLAYWRIGHT_BROWSERS_PATH || "/opt/pw-browsers";
  const direct = path.join(base, "chromium");
  if (fs.existsSync(direct) && fs.statSync(direct).isFile()) return direct;
  return undefined;
}

/** FFmpeg's arguments for the timeline (the page's PresVideoTimeline). */
export function ffmpegArgs(tl, { out, audio, codec, quality, pixelFormat }) {
  const a = ["-hide_banner", "-loglevel", "error", "-y",
    "-f", "rawvideo", "-pixel_format", "rgba", "-video_size", `${tl.width}x${tl.height}`,
    "-framerate", String(tl.fps), "-i", "pipe:0"];
  const keep = (tl.audio && tl.audio.keep) || [];
  if (audio) {
    a.push("-i", audio);
    // the sound as played: the parts between the recording's cuts
    const parts = [];
    for (let i = 0; i + 1 < keep.length; i += 2) parts.push(`[1:a]atrim=${keep[i]}:${keep[i + 1]},asetpts=PTS-STARTPTS[a${i / 2}]`);
    const n = parts.length;
    const graph = n > 1
      ? `${parts.join(";")};${parts.map((_, i) => `[a${i}]`).join("")}concat=n=${n}:v=0:a=1[aout]`
      : n === 1 ? parts[0].replace("[a0]", "[aout]") : "[1:a]anull[aout]";
    a.push("-filter_complex", graph, "-map", "0:v", "-map", "[aout]", "-c:a", "libopus", "-b:a", "96k");
  }
  if (codec === "av1") a.push("-c:v", "libaom-av1", "-crf", String(quality), "-b:v", "0", "-cpu-used", "6", "-row-mt", "1");
  else a.push("-c:v", "libvpx-vp9", "-crf", String(quality), "-b:v", "0", "-deadline", "good", "-cpu-used", "4", "-row-mt", "1");
  a.push("-pix_fmt", pixelFormat === "yuv444p" ? "yuv444p" : "yuv420p", "-shortest", out);
  if (!audio) a.splice(a.indexOf("-shortest"), 1);
  return a;
}

/** Frames posted by the page, in order, one request each. */
function frameServer(onFrame) {
  const types = { ".html": "text/html", ".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".md": "text/markdown", ".ttf": "font/ttf", ".wasm": "application/wasm", ".json": "application/json" };
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, "http://x");
    if (req.method === "POST" && url.pathname === "/__video/frame") {
      const chunks = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        Promise.resolve(onFrame(Buffer.concat(chunks))).then(() => {
          res.writeHead(204);
          res.end();
        }, (e) => {
          res.writeHead(500);
          res.end(String(e));
        });
      });
      return;
    }
    let rel = decodeURIComponent(url.pathname);
    if (rel.endsWith("/")) rel += "index.html";
    const file = path.join(distDir, rel);
    if (!file.startsWith(distDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
      res.writeHead(404);
      res.end();
      return;
    }
    res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream" });
    fs.createReadStream(file).pipe(res);
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server)));
}

async function main() {
  const { chromium } = await import("playwright-core").catch(() => {
    log("playwright-core is not installed: npm install");
    process.exit(1);
  });
  build();

  let ff = null;
  let prev = null;
  let drawn = 0;
  let repeated = 0;
  const hash = crypto.createHash("sha256");
  const raw = args.raw ? fs.createWriteStream(path.resolve(args.raw)) : null;
  const write = (stream, buf) => new Promise((resolve) => {
    if (stream.write(buf)) resolve();
    else stream.once("drain", resolve);
  });
  const server = await frameServer(async (body) => {
    // an empty body: the same picture as the frame before
    if (body.length === 0) repeated += 1;
    else {
      prev = body;
      drawn += 1;
    }
    hash.update(prev);
    if (raw) await write(raw, prev);
    await write(ff.stdin, prev);
  });
  const base = `http://127.0.0.1:${server.address().port}/`;
  const browser = await chromium.launch({
    executablePath: chromiumPath(),
    args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"],
  });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
    page.on("pageerror", (e) => log(`page: ${e.message}`));
    const sample = args.sample || (args.deck ? "" : "esittely");
    await page.goto(base + (sample ? `?sample=${encodeURIComponent(sample)}` : ""));
    await page.waitForFunction(() => window.__pageStarted === true, null, { timeout: 120000 });
    if (args.deck) {
      const text = fs.readFileSync(path.resolve(args.deck), "utf8");
      await page.evaluate((t) => window.__app.setSource(t), text);
    }
    if (args.recording) {
      const take = fs.readFileSync(path.resolve(args.recording), "utf8");
      await page.evaluate((t) => window.__app.loadRecording(t), take);
    }
    // the deck's faces and pictures, as the page loads them for the stage
    await page.waitForTimeout(1500);
    const tl = await page.evaluate(([o, rec]) => window.__video.begin(o, rec), [options, !!args.recording]);
    if (args.timeline) fs.writeFileSync(path.resolve(args.timeline), JSON.stringify(tl, null, 2));
    const frames = Math.min(tl.frames, Number(args.frames || tl.frames));
    const audio = args.recording && args.audio ? path.resolve(args.audio) : "";
    ff = spawn(process.env.FFMPEG || "ffmpeg", ffmpegArgs(tl, { out, audio, ...options }), { stdio: ["pipe", "inherit", "inherit"] });
    const done = new Promise((resolve, reject) => {
      ff.on("error", reject);
      ff.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`ffmpeg exited ${code}`))));
    });
    log(`video  ${tl.source}, ${tl.slides} slides, ${tl.duration} s, ${frames} frames at ${tl.width}x${tl.height}/${tl.fps} → ${path.relative(root, out) || out}`);
    const started = Date.now();
    await page.evaluate(async (n) => {
      for (let i = 0; i < n; i++) {
        const f = window.__video.frame(i);
        const r = await fetch("/__video/frame", { method: "POST", body: f.same ? new Uint8Array(0) : f.rgba });
        if (!r.ok) throw new Error(`frame ${i}: ${await r.text()}`);
      }
      window.__video.close();
    }, frames);
    ff.stdin.end();
    if (raw) raw.end();
    await done;
    const secs = (Date.now() - started) / 1000;
    const size = fs.statSync(out).size;
    log(`frames ${drawn} drawn, ${repeated} repeated the one before (${(frames / secs).toFixed(1)} frames/s)`);
    log(`sha256 ${hash.digest("hex")} (raw RGBA)`);
    log(`wrote  ${out} (${(size / 1024).toFixed(1)} KiB, ${((size * 8) / 1000 / (frames / tl.fps)).toFixed(1)} kbit/s)`);
  } finally {
    await browser.close();
    server.close();
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(new URL(import.meta.url).pathname)) {
  main().catch((e) => {
    log(e.stack || e.message);
    process.exit(1);
  });
}
