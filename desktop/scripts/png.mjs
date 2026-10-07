// SPDX-License-Identifier: MIT
//
// PAM (what the native host writes: RGB_ALPHA, top row first) to PNG, with
// Node's zlib and nothing else. From Carnivore's scripts/make-icon.mjs
// (github.com/terotests/CarnivoreMusicPlayer, MIT).
//
//   node scripts/png.mjs in.pam out.png

import fs from "node:fs";
import zlib from "node:zlib";

export function readPam(file) {
  const buf = fs.readFileSync(file);
  const end = buf.indexOf("ENDHDR\n") + 7;
  const head = buf.subarray(0, end).toString("latin1");
  const w = Number(/WIDTH (\d+)/.exec(head)[1]), h = Number(/HEIGHT (\d+)/.exec(head)[1]);
  return { w, h, data: buf.subarray(end) };
}

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});
const crc = (b) => { let c = 0xffffffff; for (const x of b) c = crcTable[(c ^ x) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
const chunk = (type, data) => {
  const t = Buffer.concat([Buffer.from(type, "latin1"), data]);
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
  const c = Buffer.alloc(4); c.writeUInt32BE(crc(t));
  return Buffer.concat([len, t, c]);
};

export function pngOf({ w, h, data }) {
  const raw = Buffer.alloc((w * 4 + 1) * h);
  for (let y = 0; y < h; y++) data.copy(raw, y * (w * 4 + 1) + 1, y * w * 4, (y + 1) * w * 4);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 6;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw, { level: 9 })), chunk("IEND", Buffer.alloc(0)),
  ]);
}

export function pamToPng(from, to) {
  fs.writeFileSync(to, pngOf(readPam(from)));
}

if (import.meta.url === `file://${process.argv[1]}`) pamToPng(process.argv[2], process.argv[3]);
