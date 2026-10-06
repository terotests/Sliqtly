// node --test: a photo album (```gallery, {heading=hidden}) on the stage and
// in a PDF for the printing house. `@media print` in the stylesheet lays the
// deck out on the print page; the PDF then has a bleed, crop marks, and the
// stage is back to its own page afterwards.
//
// Runs the built app (web/dist/pres_app.js, `npm run build`).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));

// a plain w × h RGB PNG
function png(w, h) {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf) => {
    let c = 0xffffffff;
    for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const c = Buffer.alloc(4);
    c.writeUInt32BE(crc(td));
    return Buffer.concat([len, td, c]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const o = y * (w * 3 + 1) + 1 + x * 3;
      raw[o] = (x * 255) / w;
      raw[o + 1] = (y * 255) / h;
      raw[o + 2] = 128;
    }
  }
  const sig = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  return Buffer.concat([sig, chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}

function rangerBuffer(buf) {
  const ab = buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength);
  ab._view = new DataView(ab);
  return ab;
}

function pdfText(ab) {
  return Buffer.from(ab).toString("latin1");
}

const css = `
@media print {
  page { width: 297mm; height: 210mm; bleed: 3mm; safe-area: 8mm; }
  deck { crop-marks: on; }
}
`;

const deck = `# Kesä 2026 {heading=hidden}

Kesän kuvat.

## Hietaniemi {heading=hidden}

\`\`\`gallery
- media/ranta.png: Hietaniemi heinäkuussa
\`\`\`
{layout=full fit=cover caption=overlay}
`;

function album() {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  app.addImage("media/ranta.png", rangerBuffer(png(400, 200)), "image/png", 400, 200);
  app.setSource(deck);
  return app;
}

test("a hidden title still names its slide and starts it", () => {
  const app = album();
  const d = app.deck;
  assert.equal(d.slideCount(), 2);
  assert.equal(d.slideAt(0).title, "Kesä 2026");
  assert.equal(d.slideAt(1).title, "Hietaniemi");
  // nothing of the titles is drawn
  const l = d.md.edit.layout;
  assert.ok(!l.boxes.some((b) => b.kind === 0 && /Hietaniemi$|Kesä 2026/.test(b.text)), "no title box");
  // the source of the album slide's title is on the album slide
  assert.equal(d.pageOfOffset(deck.indexOf("## Hietaniemi")), 1);
});

test("the PDF goes to print: print page, bleed, crop marks; the stage stays", () => {
  const app = album();
  const d = app.deck;
  const screenW = d.pageW;
  const screenH = d.pageH;
  const out = pdfText(app.pdf());
  // 297 x 210 mm is 841.89 x 595.28 pt; bleed 3mm = 8.5pt, slug 20pt
  const trim = /\/TrimBox \[([\d.]+) ([\d.]+) ([\d.]+) ([\d.]+)\]/.exec(out);
  assert.ok(trim, "a TrimBox");
  const [x0, y0, x1, y1] = trim.slice(1).map(Number);
  assert.ok(Math.abs(x0 - 28.5) < 0.1, `trim starts after bleed and slug (${x0})`);
  assert.ok(Math.abs(x1 - x0 - 841.89) < 0.1, `trim is A4 wide (${x1 - x0})`);
  assert.ok(Math.abs(y1 - y0 - 595.28) < 0.1, `trim is A4 high (${y1 - y0})`);
  assert.match(out, /\/BleedBox \[20 20 /);
  assert.equal((out.match(/\/Type \/Page\b/g) || []).length, 2, "two slides, no empty one");
  // back on the stage's own page
  assert.equal(d.pageW, screenW);
  assert.equal(d.pageH, screenH);
  assert.equal(d.md.edit.media, "screen");
});

test("no print rules: the PDF is the slides as they are", () => {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet("");
  app.setSource("# One\n\nText.\n");
  const out = pdfText(app.pdf());
  assert.doesNotMatch(out, /\/TrimBox/);
});
