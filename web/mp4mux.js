// SPDX-License-Identifier: AGPL-3.0-or-later
//
// web/mp4mux.js — H.264 samples into an MP4 file, and an MP4's boxes read back.
//
// File → Export → Video as MP4 (web/mp4export.js): the browser's own H.264
// encoder (WebCodecs) codes the frames, this puts them in the file every
// player opens (QuickTime, Finder, PowerPoint, LinkedIn). One video track,
// the samples in one chunk, the index first (ftyp, moov, mdat: "fast start",
// so a player starts before it has the whole file):
//
//   ftyp                      isom, avc1
//   moov  mvhd                the movie's length (ms)
//         trak tkhd           its size
//              mdia mdhd      its timescale and length
//                   hdlr      vide
//                   minf vmhd, dinf/dref
//                        stbl stsd/avc1/avcC   the encoder's decoderConfig
//                             stts             how long each sample shows
//                             stss             the key frames (none: all)
//                             stsc, stsz, stco one chunk, each sample's size
//   mdat                      the samples, length-prefixed NAL units
//
// A frame the same as the one before is not coded (web/videoexport.js
// `same`): the sample before it just shows longer, so a slide held 10 s is
// one sample. Samples come in decode order with no reordering (WebCodecs'
// H.264 encoders make no B-frames), so there is no ctts.

const enc = new TextEncoder();

function u32(v) {
  return [(v >>> 24) & 255, (v >>> 16) & 255, (v >>> 8) & 255, v & 255];
}
function u16(v) {
  return [(v >>> 8) & 255, v & 255];
}
function fourcc(s) {
  return [...enc.encode(s)];
}
// A box: size, type, then its parts (byte arrays or Uint8Arrays).
function box(type, ...parts) {
  let n = 8;
  for (const p of parts) n += p.length;
  const out = new Uint8Array(n);
  out.set([...u32(n), ...fourcc(type)], 0);
  let at = 8;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}
// A full box: version and flags first.
function full(type, version, flags, ...parts) {
  return box(type, [version, (flags >>> 16) & 255, (flags >>> 8) & 255, flags & 255], ...parts);
}
const MATRIX = [0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000].flatMap(u32);

/** The track's units per second for `fps`: 90 kHz when a frame is a whole number of it. */
export function timescaleFor(fps) {
  return 90000 % fps === 0 ? 90000 : fps * 1000;
}

/**
 * The MP4 of an H.264 track.
 * @param {object} o
 * @param {number} o.width, o.height  the picture's size
 * @param {number} o.timescale        units per second of `time` and `duration`
 * @param {number} o.duration         the video's length in those units
 * @param {Uint8Array} o.avcC         AVCDecoderConfigurationRecord (decoderConfig.description)
 * @param {{data: Uint8Array, time: number, key: boolean}[]} o.samples  in order, the first at 0
 * @returns {Uint8Array}
 */
export function muxMp4({ width, height, timescale, duration, avcC, samples }) {
  if (!samples.length) throw new Error("mp4: no samples");
  if (samples[0].time !== 0) throw new Error("mp4: the first sample is not at 0");
  if (!samples[0].key) throw new Error("mp4: the first sample is not a key frame");
  // how long each shows: to the next one, the last to the end
  const durations = samples.map((s, i) => (i + 1 < samples.length ? samples[i + 1].time : duration) - s.time);
  if (durations.some((d) => !(d > 0) || !Number.isInteger(d))) throw new Error("mp4: sample times must rise in whole units, within the duration");
  let total = 0;
  for (const s of samples) total += s.data.length;
  const movieScale = 1000;
  const movieDuration = Math.round((duration * movieScale) / timescale);

  const stts = [];
  for (const d of durations) {
    const last = stts[stts.length - 1];
    if (last && last[1] === d) last[0] += 1;
    else stts.push([1, d]);
  }
  const keys = [];
  samples.forEach((s, i) => { if (s.key) keys.push(i + 1); });

  const moovWith = (dataAt) => {
    const avc1 = box("avc1",
      [0, 0, 0, 0, 0, 0, ...u16(1)], // reserved, data reference 1
      new Uint8Array(16), // pre_defined, reserved
      [...u16(width), ...u16(height), ...u32(0x00480000), ...u32(0x00480000), ...u32(0), ...u16(1)],
      new Uint8Array(32), // compressor name
      [...u16(0x0018), 0xff, 0xff], // depth, pre_defined -1
      box("avcC", avcC),
      box("pasp", [...u32(1), ...u32(1)]));
    const stbl = box("stbl",
      full("stsd", 0, 0, u32(1), avc1),
      full("stts", 0, 0, u32(stts.length), stts.flatMap(([n, d]) => [...u32(n), ...u32(d)])),
      ...(keys.length === samples.length ? [] : [full("stss", 0, 0, u32(keys.length), keys.flatMap(u32))]),
      full("stsc", 0, 0, u32(1), [...u32(1), ...u32(samples.length), ...u32(1)]),
      full("stsz", 0, 0, u32(0), u32(samples.length), samples.flatMap((s) => u32(s.data.length))),
      full("stco", 0, 0, u32(1), u32(dataAt)));
    const minf = box("minf",
      full("vmhd", 0, 1, [0, 0, 0, 0, 0, 0, 0, 0]),
      box("dinf", full("dref", 0, 0, u32(1), full("url ", 0, 1))),
      stbl);
    const mdia = box("mdia",
      full("mdhd", 0, 0, u32(0), u32(0), u32(timescale), u32(duration), [...u16(0x55c4), 0, 0]), // "und"
      full("hdlr", 0, 0, u32(0), fourcc("vide"), new Uint8Array(12), [...enc.encode("VideoHandler"), 0]),
      minf);
    const tkhd = full("tkhd", 0, 3,
      u32(0), u32(0), u32(1), u32(0), u32(movieDuration), new Uint8Array(8),
      [0, 0, 0, 0, 0, 0, 0, 0], // layer, alternate group, volume, reserved
      MATRIX, u32(width * 65536), u32(height * 65536));
    const mvhd = full("mvhd", 0, 0,
      u32(0), u32(0), u32(movieScale), u32(movieDuration),
      u32(0x00010000), [...u16(0x0100), 0, 0], new Uint8Array(8),
      MATRIX, new Uint8Array(24), u32(2));
    return box("moov", mvhd, box("trak", tkhd, mdia));
  };
  const ftyp = box("ftyp", fourcc("isom"), u32(0x200), fourcc("isom"), fourcc("iso2"), fourcc("avc1"), fourcc("mp41"));
  // the index's size does not depend on where the data starts
  const dataAt = ftyp.length + moovWith(0).length + 8;
  if (dataAt + total > 0xffffffff) throw new Error("mp4: larger than 4 GB");
  const moov = moovWith(dataAt);
  const out = new Uint8Array(dataAt + total);
  out.set(ftyp, 0);
  out.set(moov, ftyp.length);
  out.set([...u32(total + 8), ...fourcc("mdat")], ftyp.length + moov.length);
  let at = dataAt;
  for (const s of samples) {
    out.set(s.data, at);
    at += s.data.length;
  }
  return out;
}

// --- reading back ----------------------------------------------------------
// Enough of an MP4 to check one: the box tree, and the video track's
// numbers (the tests, and check-web's exported file).

const CONTAINERS = new Set(["moov", "trak", "mdia", "minf", "stbl", "dinf", "edts"]);

/** The boxes of `bytes` from `start` to `end`: { type, at, size, body, kids }. */
export function readBoxes(bytes, start = 0, end = bytes.length) {
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const out = [];
  let at = start;
  while (at + 8 <= end) {
    let size = dv.getUint32(at);
    const type = String.fromCharCode(bytes[at + 4], bytes[at + 5], bytes[at + 6], bytes[at + 7]);
    let head = 8;
    if (size === 1) {
      size = Number(dv.getBigUint64(at + 8));
      head = 16;
    } else if (size === 0) size = end - at;
    if (size < head || at + size > end) throw new Error("mp4: box " + type + " runs past its parent");
    const b = { type, at, size, body: bytes.subarray(at + head, at + size) };
    if (CONTAINERS.has(type)) b.kids = readBoxes(bytes, at + head, at + size);
    else if (type === "stsd") b.kids = readBoxes(bytes, at + head + 8, at + size);
    else if (type === "avc1") b.kids = readBoxes(bytes, at + head + 78, at + size);
    out.push(b);
    at += size;
  }
  return out;
}

/** The first box of `path` ("moov/trak/tkhd") under `boxes`. */
export function findBox(boxes, path) {
  let list = boxes;
  let hit = null;
  for (const type of path.split("/")) {
    hit = (list || []).find((b) => b.type === type) || null;
    if (!hit) return null;
    list = hit.kids;
  }
  return hit;
}

/** The video track's numbers: size, timescale, durations, samples. */
export function readMp4(bytes) {
  const boxes = readBoxes(bytes);
  const view = (b) => new DataView(b.body.buffer, b.body.byteOffset, b.body.byteLength);
  const need = (p) => {
    const b = findBox(boxes, p);
    if (!b) throw new Error("mp4: no " + p);
    return view(b);
  };
  const mvhd = need("moov/mvhd");
  const tkhd = need("moov/trak/tkhd");
  const mdhd = need("moov/trak/mdia/mdhd");
  const stbl = "moov/trak/mdia/minf/stbl/";
  const stts = need(stbl + "stts");
  const stsz = need(stbl + "stsz");
  const stco = need(stbl + "stco");
  const stssBox = findBox(boxes, stbl + "stss");
  const avcC = findBox(boxes, stbl + "stsd/avc1/avcC");
  const avc1 = findBox(boxes, stbl + "stsd/avc1");
  const durations = [];
  for (let i = 0, n = stts.getUint32(4); i < n; i++) {
    for (let k = 0; k < stts.getUint32(8 + i * 8); k++) durations.push(stts.getUint32(12 + i * 8));
  }
  const sizes = [];
  for (let i = 0, n = stsz.getUint32(8); i < n; i++) sizes.push(stsz.getUint32(4) || stsz.getUint32(12 + i * 4));
  const offsets = [];
  for (let i = 0, n = stco.getUint32(4); i < n; i++) offsets.push(stco.getUint32(8 + i * 4));
  let keys = null;
  if (stssBox) {
    const s = view(stssBox);
    keys = [];
    for (let i = 0, n = s.getUint32(4); i < n; i++) keys.push(s.getUint32(8 + i * 4));
  }
  const a1 = avc1 ? view(avc1) : null;
  return {
    boxes: boxes.map((b) => b.type),
    movieTimescale: mvhd.getUint32(12),
    movieDuration: mvhd.getUint32(16),
    width: tkhd.getUint32(76) / 65536,
    height: tkhd.getUint32(80) / 65536,
    sampleWidth: a1 ? a1.getUint16(24) : 0,
    sampleHeight: a1 ? a1.getUint16(26) : 0,
    timescale: mdhd.getUint32(12),
    duration: mdhd.getUint32(16),
    durations,
    sizes,
    offsets,
    keys,
    avcC: avcC ? avcC.body : null,
  };
}
