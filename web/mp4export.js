// SPDX-License-Identifier: AGPL-3.0-or-later
//
// web/mp4export.js — a video's frames coded to H.264 by the browser and kept
// as an MP4 (web/mp4mux.js).
//
// File → Export → Video offers MP4 where the browser can code H.264
// (WebCodecs VideoEncoder, `h264Config`); elsewhere the video is a WebM made
// by Sliqtly's own VP8 encoder (web/video-worker.js). The encoder here
// takes the same frames as that worker: RGBA top down, or null for "the
// same picture as frame n - 1", which is not coded at all: the sample before
// it shows longer.

import { muxMp4, timescaleFor } from "./mp4mux.js";

/**
 * The H.264 codec strings to try for a size and rate, best first: High,
 * then Main, then Constrained Baseline, each at the lowest level that holds
 * that many macroblocks a second (and a frame of that many).
 */
export function h264Codecs(width, height, fps) {
  const mbs = Math.ceil(width / 16) * Math.ceil(height / 16);
  // [level_idc, max macroblocks a second, max frame macroblocks] (H.264 Table A-1)
  const LEVELS = [[30, 40500, 1620], [31, 108000, 3600], [32, 216000, 5120], [40, 245760, 8192], [42, 522240, 8704], [50, 589824, 22080], [51, 983040, 36864], [52, 2073600, 36864]];
  const lv = LEVELS.find(([, rate, frame]) => mbs * fps <= rate && mbs <= frame) || LEVELS[LEVELS.length - 1];
  const hex = lv[0].toString(16).toUpperCase().padStart(2, "0");
  return ["avc1.6400" + hex, "avc1.4D00" + hex, "avc1.42E0" + hex];
}

/** The bits a second asked of the encoder: slides are mostly still, 6 Mb/s at 1080p30. */
export function h264Bitrate(width, height, fps) {
  return Math.round((6e6 * (width * height)) / (1920 * 1080) * Math.sqrt(fps / 30));
}

/**
 * A VideoEncoder configuration this browser codes, or null (no WebCodecs,
 * or no H.264 encoder: Chromium builds without proprietary codecs, some
 * Linux browsers). `VE` is the VideoEncoder class (the page's by default).
 */
export async function h264Config(width, height, fps, VE = globalThis.VideoEncoder) {
  if (typeof VE !== "function" || typeof VE.isConfigSupported !== "function") return null;
  for (const codec of h264Codecs(width, height, fps)) {
    const config = { codec, width, height, framerate: fps, bitrate: h264Bitrate(width, height, fps), bitrateMode: "variable", latencyMode: "quality", avc: { format: "avc" } };
    try {
      const r = await VE.isConfigSupported(config);
      if (r && r.supported) return r.config || config;
    } catch (_) {
      // a string this browser does not parse: the next one
    }
  }
  return null;
}

/**
 * An encoder for frames 0..frames-1 at `fps`: frame(n, rgba|null) for each
 * frame in order, then end(frames) → the MP4's bytes. A key frame at the
 * start and at least every two seconds of video, so a player can seek.
 * `VE`, `VF`: VideoEncoder and VideoFrame (the page's by default).
 */
export function createH264Encoder({ width, height, fps, config, VE = globalThis.VideoEncoder, VF = globalThis.VideoFrame }) {
  const timescale = timescaleFor(fps);
  const unit = timescale / fps;
  const samples = [];
  let avcC = null;
  let failed = null;
  let lastKey = -Infinity;
  let waiting = null;
  const encoder = new VE({
    output(chunk, meta) {
      const data = new Uint8Array(chunk.byteLength);
      chunk.copyTo(data);
      if (meta && meta.decoderConfig && meta.decoderConfig.description) {
        const d = meta.decoderConfig.description;
        avcC = new Uint8Array(ArrayBuffer.isView(d) ? d.buffer.slice(d.byteOffset, d.byteOffset + d.byteLength) : d.slice(0));
      }
      // µs back to its frame, then to the track's units
      samples.push({ data, time: Math.round((chunk.timestamp * fps) / 1e6) * unit, key: chunk.type === "key" });
    },
    error(e) {
      failed = e;
      if (waiting) waiting();
    },
  });
  encoder.configure(config);
  if (typeof encoder.addEventListener === "function") encoder.addEventListener("dequeue", () => { if (waiting) waiting(); });
  const check = () => { if (failed) throw failed; };

  async function frame(n, rgba) {
    check();
    if (!rgba) return;
    const f = new VF(rgba, { format: "RGBA", codedWidth: width, codedHeight: height, timestamp: Math.round((n * 1e6) / fps), duration: Math.round(1e6 / fps) });
    const keyFrame = n - lastKey >= 2 * fps;
    if (keyFrame) lastKey = n;
    try {
      encoder.encode(f, { keyFrame });
    } finally {
      f.close();
    }
    // a few frames in the encoder at most: the page holds no more pictures
    while (encoder.encodeQueueSize > 2 && !failed) await new Promise((r) => { waiting = r; setTimeout(r, 50); });
    waiting = null;
    check();
  }

  async function end(frames) {
    check();
    await encoder.flush();
    check();
    encoder.close();
    if (!avcC) throw new Error("mp4: the encoder gave no decoder configuration");
    return muxMp4({ width, height, timescale, duration: frames * unit, avcC, samples });
  }

  function close() {
    try { if (encoder.state !== "closed") encoder.close(); } catch (_) { /* already closed */ }
  }

  return { frame, end, close, samples: () => samples.length, keys: () => samples.filter((s) => s.key).length };
}
