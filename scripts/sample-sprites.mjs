// The robot of the hahmot sample (samples/hahmot/sprites/robot.png), drawn
// here rather than taken from anywhere:
//   node scripts/sample-sprites.mjs
// One row of 20×20 pixel frames, each pixel drawn 8×8 so the slides' smooth
// scaling keeps the edges sharp: idle, idle with the eye shut, four walking
// steps, jumping up, falling. The robot faces right.
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const OUT = path.join(root, "samples", "hahmot", "sprites", "robot.png");
const PX = 8;
const W = 20;
const H = 20;

const COLORS = {
  K: [29, 36, 51, 255], // outline
  B: [79, 179, 217, 255], // body
  L: [166, 227, 245, 255], // light edge
  E: [255, 255, 255, 255], // eye
  P: [29, 36, 51, 255], // pupil
  A: [255, 90, 95, 255], // antenna tip
  O: [255, 179, 71, 255], // chest light
  G: [107, 122, 144, 255], // legs
};

const HEAD = [
  "..........A.........",
  "..........K.........",
  "......KKKKKKKKK.....",
  ".....KLLLLLLLLLK....",
  ".....KLBBBBBBBBK....",
  ".....KLBBBBEEEBK....",
  ".....KLBBBBEPEBK....",
  ".....KLBBBBEEEBK....",
  ".....KBBBBBBBBBK....",
  "......KKKKKKKKK.....",
  "......KBBBBBBBK.....",
  ".....KKBBOOBBBKK....",
  ".....KKBBOOBBBKK....",
  "......KBBBBBBBK.....",
  "......KKKKKKKKK.....",
];
const BLINK = HEAD.map((r, i) => (i >= 5 && i <= 7 ? (i === 6 ? ".....KLBBBBKKKBK...." : ".....KLBBBBBBBBK....") : r));

const LEGS = {
  stand: [
    ".......KG...KG......",
    ".......KG...KG......",
    ".......KG...KG......",
    "......KKKK.KKKK.....",
    "....................",
  ],
  stride: [
    "......KG.....KG.....",
    ".....KG.......KG....",
    ".....KG.......KG....",
    "....KKKK.....KKKK...",
    "....................",
  ],
  tuck: [
    "......KGG...KGG.....",
    "......KKKK.KKKK.....",
    "....................",
    "....................",
    "....................",
  ],
  reach: [
    ".......KG...KG......",
    ".......KG...KG......",
    ".......KG...KG......",
    ".......KG...KG......",
    "......KKKK.KKKK.....",
  ],
};

// a frame: the head over the legs; `bob` sets the head a pixel lower on
// legs a pixel shorter, the step between two strides
function frame(head, legs, bob = false) {
  const rows = bob ? [".".repeat(W), ...head, ...LEGS[legs].slice(1)] : [...head, ...LEGS[legs]];
  return rows.slice(0, H).map((r) => (r + ".".repeat(W)).slice(0, W));
}

const FRAMES = [
  frame(HEAD, "stand"),
  frame(BLINK, "stand"),
  frame(HEAD, "stride"),
  frame(HEAD, "stand", true),
  frame(HEAD, "stride"),
  frame(HEAD, "stand", true),
  frame(HEAD, "tuck"),
  frame(HEAD, "reach"),
];

function png(width, height, rgba) {
  const crcTable = new Int32Array(256).map((_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c;
  });
  const crc = (buf) => {
    let c = -1;
    for (const b of buf) c = crcTable[(c ^ b) & 255] ^ (c >>> 8);
    return (c ^ -1) >>> 0;
  };
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(body));
    return Buffer.concat([len, body, sum]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const raw = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y++) {
    raw[y * (width * 4 + 1)] = 0;
    rgba.copy(raw, y * (width * 4 + 1) + 1, y * width * 4, (y + 1) * width * 4);
  }
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

const width = W * PX * FRAMES.length;
const height = H * PX;
const rgba = Buffer.alloc(width * height * 4);
FRAMES.forEach((rows, f) => {
  for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) {
    const c = COLORS[rows[y][x]];
    if (!c) continue;
    for (let dy = 0; dy < PX; dy++) for (let dx = 0; dx < PX; dx++) {
      const o = ((y * PX + dy) * width + (f * W + x) * PX + dx) * 4;
      rgba[o] = c[0];
      rgba[o + 1] = c[1];
      rgba[o + 2] = c[2];
      rgba[o + 3] = c[3];
    }
  }
});
fs.mkdirSync(path.dirname(OUT), { recursive: true });
fs.writeFileSync(OUT, png(width, height, rgba));
console.log(`${path.relative(root, OUT)}: ${FRAMES.length} frames of ${W}×${H}, ${width}×${height} px`);
