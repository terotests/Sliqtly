// The MP4 muxer (web/mp4mux.js) and the H.264 encoder around WebCodecs
// (web/mp4export.js): the file's boxes read back, the samples' times and
// sizes, a frame the same as the one before shown longer, not coded; and,
// where FFmpeg with libx264 is installed, a real H.264 stream muxed here and
// decoded by FFmpeg.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { muxMp4, readMp4, readBoxes, findBox, timescaleFor } from "../dist/mp4mux.js";
import { h264Codecs, h264Config, createH264Encoder } from "../dist/mp4export.js";

// an AVCDecoderConfigurationRecord: High 4.0, one SPS, one PPS
const SPS = [0x67, 0x64, 0x00, 0x28, 0xac, 0xd9, 0x40, 0x78];
const PPS = [0x68, 0xeb, 0xe3, 0xcb];
const AVCC = Uint8Array.from([1, 0x64, 0x00, 0x28, 0xff, 0xe1, 0, SPS.length, ...SPS, 1, 0, PPS.length, ...PPS]);
const sample = (n, fill) => new Uint8Array(n).fill(fill);

test("the boxes in order: ftyp, moov before mdat (fast start)", () => {
  const mp4 = muxMp4({ width: 1920, height: 1080, timescale: 90000, duration: 90000, avcC: AVCC, samples: [{ data: sample(10, 1), time: 0, key: true }] });
  const top = readBoxes(mp4).map((b) => b.type);
  assert.deepEqual(top, ["ftyp", "moov", "mdat"]);
  const ftyp = readBoxes(mp4)[0];
  assert.equal(String.fromCharCode(...ftyp.body.subarray(0, 4)), "isom");
  // the track's parts where a player looks for them
  for (const p of ["moov/mvhd", "moov/trak/tkhd", "moov/trak/mdia/mdhd", "moov/trak/mdia/hdlr", "moov/trak/mdia/minf/vmhd", "moov/trak/mdia/minf/dinf/dref", "moov/trak/mdia/minf/stbl/stsd/avc1/avcC", "moov/trak/mdia/minf/stbl/stsc"]) {
    assert.ok(findBox(readBoxes(mp4), p), p);
  }
  const hdlr = findBox(readBoxes(mp4), "moov/trak/mdia/hdlr");
  assert.equal(String.fromCharCode(...hdlr.body.subarray(8, 12)), "vide");
});

test("durations, the sample table and the track's size read back", () => {
  const fps = 30;
  const ts = timescaleFor(fps);
  const unit = ts / fps;
  // frames 0, 1, 2 coded; 3..9 the same as 2; 10 coded (a key); 11 frames
  const samples = [
    { data: sample(100, 1), time: 0, key: true },
    { data: sample(20, 2), time: 1 * unit, key: false },
    { data: sample(30, 3), time: 2 * unit, key: false },
    { data: sample(90, 4), time: 10 * unit, key: true },
  ];
  const mp4 = muxMp4({ width: 1280, height: 720, timescale: ts, duration: 11 * unit, avcC: AVCC, samples });
  const r = readMp4(mp4);
  assert.equal(r.timescale, 90000);
  assert.equal(r.duration, 11 * unit);
  assert.equal(r.movieTimescale, 1000);
  assert.equal(r.movieDuration, Math.round((11 / 30) * 1000));
  assert.deepEqual([r.width, r.height, r.sampleWidth, r.sampleHeight], [1280, 720, 1280, 720]);
  // the held frame shows 8 frames long, the last one to the end
  assert.deepEqual(r.durations, [unit, unit, 8 * unit, unit]);
  assert.equal(r.durations.reduce((a, b) => a + b, 0), r.duration);
  assert.deepEqual(r.sizes, [100, 20, 30, 90]);
  assert.deepEqual(r.keys, [1, 4]);
  assert.deepEqual([...r.avcC], [...AVCC]);
  // one chunk: its offset is the first sample's bytes, the rest follow
  assert.equal(r.offsets.length, 1);
  let at = r.offsets[0];
  for (const s of samples) {
    assert.deepEqual([...mp4.subarray(at, at + s.data.length)], [...s.data]);
    at += s.data.length;
  }
  assert.equal(at, mp4.length);
});

test("all key frames: no stss; 60 fps keeps 90 kHz, 24 fps too, 7 fps its own", () => {
  const mp4 = muxMp4({ width: 16, height: 16, timescale: 90000, duration: 3000, avcC: AVCC, samples: [{ data: sample(4, 9), time: 0, key: true }, { data: sample(4, 8), time: 1500, key: true }] });
  assert.equal(readMp4(mp4).keys, null);
  assert.equal(timescaleFor(60), 90000);
  assert.equal(timescaleFor(24), 90000);
  assert.equal(timescaleFor(7), 7000);
});

test("samples out of order, past the end or not starting on a key are refused", () => {
  const base = { width: 16, height: 16, timescale: 30, duration: 3, avcC: AVCC };
  assert.throws(() => muxMp4({ ...base, samples: [] }), /no samples/);
  assert.throws(() => muxMp4({ ...base, samples: [{ data: sample(1, 0), time: 0, key: false }] }), /key/);
  assert.throws(() => muxMp4({ ...base, samples: [{ data: sample(1, 0), time: 1, key: true }] }), /at 0/);
  assert.throws(() => muxMp4({ ...base, samples: [{ data: sample(1, 0), time: 0, key: true }, { data: sample(1, 0), time: 0, key: false }] }), /rise/);
  assert.throws(() => muxMp4({ ...base, samples: [{ data: sample(1, 0), time: 0, key: true }, { data: sample(1, 0), time: 3, key: false }] }), /rise/);
});

test("H.264's level for the size and rate", () => {
  assert.deepEqual(h264Codecs(1920, 1080, 30), ["avc1.640028", "avc1.4D0028", "avc1.42E028"]);
  assert.equal(h264Codecs(1920, 1080, 60)[0], "avc1.64002A");
  assert.equal(h264Codecs(1080, 1920, 30)[0], "avc1.640028");
  assert.equal(h264Codecs(1280, 720, 30)[0], "avc1.64001F");
  assert.equal(h264Codecs(1280, 720, 60)[0], "avc1.640020");
});

test("no WebCodecs or no H.264 encoder: no configuration (the video is WebM)", async () => {
  assert.equal(await h264Config(1920, 1080, 30, undefined), null);
  const none = { isConfigSupported: async () => ({ supported: false }) };
  assert.equal(await h264Config(1920, 1080, 30, Object.assign(function () {}, none)), null);
  const asked = [];
  const main = Object.assign(function () {}, { isConfigSupported: async (c) => { asked.push(c.codec); return { supported: c.codec.startsWith("avc1.4D"), config: c }; } });
  const c = await h264Config(1920, 1080, 30, main);
  assert.equal(c.codec, "avc1.4D0028");
  assert.equal(c.avc.format, "avc");
  assert.deepEqual(asked, ["avc1.640028", "avc1.4D0028"]);
});

// a VideoEncoder that codes each frame as a few bytes naming it
function fakeCodec() {
  const made = [];
  class VF {
    constructor(data, o) { this.o = o; this.closed = false; made.push(this); }
    get timestamp() { return this.o.timestamp; }
    close() { this.closed = true; }
  }
  class VE {
    constructor({ output }) { this.output = output; this.encodeQueueSize = 0; this.state = "unconfigured"; this.queue = []; }
    configure(c) { this.config = c; this.state = "configured"; }
    encode(f, { keyFrame }) {
      const ts = f.timestamp;
      const first = !this.described;
      this.described = true;
      const bytes = Uint8Array.from([0, 0, 0, 1, keyFrame ? 0x65 : 0x41]);
      this.queue.push(() => this.output({ byteLength: bytes.length, copyTo: (d) => d.set(bytes), timestamp: ts, type: keyFrame ? "key" : "delta" }, first ? { decoderConfig: { description: AVCC.buffer.slice(0) } } : undefined));
    }
    async flush() { for (const q of this.queue.splice(0)) q(); }
    close() { this.state = "closed"; }
  }
  return { VE, VF, made };
}

test("the encoder: same frames not coded, a key frame every two seconds, the MP4 as long as the video", async () => {
  const { VE, VF, made } = fakeCodec();
  const fps = 30;
  const enc = createH264Encoder({ width: 320, height: 180, fps, config: { codec: "avc1.640028" }, VE, VF });
  const frames = 150; // 5 s
  const rgba = new Uint8Array(320 * 180 * 4);
  for (let n = 0; n < frames; n++) {
    // frames 10..99 the same picture as frame 9
    await enc.frame(n, n >= 10 && n < 100 ? null : rgba);
  }
  const mp4 = await enc.end(frames);
  assert.ok(made.every((f) => f.closed), "every VideoFrame closed");
  assert.equal(made.length, 10 + 50);
  const r = readMp4(mp4);
  const unit = 90000 / fps;
  assert.equal(r.duration, frames * unit);
  assert.equal(r.durations.length, 60);
  // frame 9 shows to frame 100
  assert.equal(r.durations[9], 91 * unit);
  assert.equal(r.durations.reduce((a, b) => a + b, 0), r.duration);
  // keys at frames 0, 100 (two seconds after 0 among the coded ones), 160 > end
  assert.deepEqual(r.keys, [1, 11]);
  assert.deepEqual([...r.avcC], [...AVCC]);
});

// FFmpeg's own H.264 (libx264, Annex B) split into samples, muxed here and
// decoded by FFmpeg: the file is one a real decoder reads, as long as asked.
const ff = spawnSync("ffmpeg", ["-hide_banner", "-encoders"], { encoding: "utf8" });
const hasX264 = ff.status === 0 && /libx264/.test(ff.stdout);
test("a real H.264 stream muxed here plays in FFmpeg", { skip: hasX264 ? false : "no ffmpeg with libx264" }, () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "mp4mux-"));
  try {
    const raw = path.join(dir, "a.h264");
    const frames = 24;
    const enc = spawnSync("ffmpeg", ["-v", "error", "-f", "lavfi", "-i", `testsrc=size=320x240:rate=12:duration=${frames / 12}`, "-c:v", "libx264", "-bf", "0", "-g", "12", "-pix_fmt", "yuv420p", "-x264-params", "slices=1", "-f", "h264", raw]);
    assert.equal(enc.status, 0, String(enc.stderr));
    const bytes = fs.readFileSync(raw);
    // NAL units between start codes
    const nals = [];
    let start = -1;
    for (let i = 0; i + 3 <= bytes.length; i++) {
      if (bytes[i] === 0 && bytes[i + 1] === 0 && bytes[i + 2] === 1) {
        if (start >= 0) nals.push(bytes.subarray(start, i > 0 && bytes[i - 1] === 0 ? i - 1 : i));
        start = i + 3;
        i += 2;
      }
    }
    nals.push(bytes.subarray(start));
    const sps = nals.find((n) => (n[0] & 31) === 7);
    const pps = nals.find((n) => (n[0] & 31) === 8);
    const avcC = Uint8Array.from([1, sps[1], sps[2], sps[3], 0xff, 0xe1, sps.length >> 8, sps.length & 255, ...sps, 1, pps.length >> 8, pps.length & 255, ...pps]);
    const vcl = nals.filter((n) => (n[0] & 31) === 1 || (n[0] & 31) === 5);
    assert.equal(vcl.length, frames);
    // each coded frame held two frames of a 24 fps video: 2 s
    const fps = 24;
    const unit = timescaleFor(fps) / fps;
    const samples = vcl.map((n, i) => {
      const data = new Uint8Array(4 + n.length);
      new DataView(data.buffer).setUint32(0, n.length);
      data.set(n, 4);
      return { data, time: i * 2 * unit, key: (n[0] & 31) === 5 };
    });
    const out = path.join(dir, "a.mp4");
    fs.writeFileSync(out, muxMp4({ width: 320, height: 240, timescale: timescaleFor(fps), duration: frames * 2 * unit, avcC, samples }));
    const probe = spawnSync("ffprobe", ["-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,nb_read_frames,duration:format=duration", "-of", "default=nw=1", out], { encoding: "utf8" });
    assert.equal(probe.status, 0, probe.stderr);
    const got = Object.fromEntries(probe.stdout.trim().split("\n").map((l) => l.split("=")));
    assert.equal(got.codec_name, "h264");
    assert.equal(got.width, "320");
    assert.equal(got.height, "240");
    assert.equal(got.nb_read_frames, String(frames));
    assert.ok(Math.abs(Number(got.duration) - 2) < 0.01, probe.stdout);
    const decode = spawnSync("ffmpeg", ["-v", "error", "-i", out, "-f", "null", "-"], { encoding: "utf8" });
    assert.equal(decode.status, 0);
    assert.equal(decode.stderr.trim(), "");
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
