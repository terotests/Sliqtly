// The music of the bileet sample (samples/bileet/media/bileet.mp3), made
// here rather than taken from anywhere:
//   node scripts/sample-music.mjs loop.wav
//   ffmpeg -i loop.wav -codec:a libmp3lame -b:a 96k samples/bileet/media/bileet.mp3
// a 124 BPM loop: kick, clap, hats, offbeat bass, an arpeggio; mono 44.1 kHz WAV
import fs from "fs";
const SR = 44100, BPM = 124, BEAT = 60 / BPM, BARS = 16;
const N = Math.round(SR * BEAT * 4 * BARS);
const out = new Float32Array(N);
let seed = 7;
const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647) * 2 - 1;
const add = (t0, len, f) => { const a = Math.round(t0 * SR); for (let i = 0; i < len * SR && a + i < N; i++) out[a + i] += f(i / SR); };
const hz = (m) => 440 * Math.pow(2, (m - 69) / 12);
// A minor: Am F C G
const roots = [57, 53, 48, 55];
const chords = [[69, 72, 76], [65, 69, 72], [64, 67, 72], [62, 67, 71]];
for (let b = 0; b < BARS * 4; b++) {
  const t = b * BEAT, bar = Math.floor(b / 4), ch = bar % 4;
  // kick: a falling sine
  add(t, 0.35, (x) => Math.sin(2 * Math.PI * (45 * x + 90 * (1 - Math.exp(-x * 30)) / 30)) * Math.exp(-x * 9) * 0.9);
  // clap on 2 and 4 after the first 4 bars
  if (bar >= 4 && b % 2 === 1) add(t, 0.2, (x) => rnd() * Math.exp(-x * 25) * 0.35);
  // hats on the eighths, after 2 bars
  if (bar >= 2) for (const h of [0, 0.5]) add(t + h * BEAT, 0.05, (x) => rnd() * Math.exp(-x * 90) * (h ? 0.16 : 0.08));
  // bass on the offbeat
  const f = hz(roots[ch] - 12);
  add(t + BEAT / 2, BEAT / 2, (x) => (Math.sin(2 * Math.PI * f * x) + 0.4 * Math.sin(4 * Math.PI * f * x)) * Math.min(1, x * 200) * Math.exp(-x * 5) * 0.4);
  // arpeggio in sixteenths from bar 8
  if (bar >= 8) for (let s = 0; s < 4; s++) {
    const n = chords[ch][(b * 4 + s) % 3] + (s === 3 ? 12 : 0);
    const g = hz(n);
    add(t + s * BEAT / 4, BEAT / 4, (x) => (2 * ((g * x) % 1) - 1) * Math.exp(-x * 14) * 0.08);
  }
}
// a pad under it all
for (let bar = 0; bar < BARS; bar++) for (const m of chords[bar % 4]) {
  const g = hz(m - 12);
  add(bar * 4 * BEAT, 4 * BEAT, (x) => Math.sin(2 * Math.PI * g * x) * Math.min(1, x * 4, (4 * BEAT - x) * 4) * 0.05);
}
let peak = 0; for (const v of out) peak = Math.max(peak, Math.abs(v));
const pcm = Buffer.alloc(44 + N * 2);
pcm.write("RIFF", 0); pcm.writeUInt32LE(36 + N * 2, 4); pcm.write("WAVEfmt ", 8);
pcm.writeUInt32LE(16, 16); pcm.writeUInt16LE(1, 20); pcm.writeUInt16LE(1, 22); pcm.writeUInt32LE(SR, 24); pcm.writeUInt32LE(SR * 2, 28); pcm.writeUInt16LE(2, 32); pcm.writeUInt16LE(16, 34);
pcm.write("data", 36); pcm.writeUInt32LE(N * 2, 40);
for (let i = 0; i < N; i++) pcm.writeInt16LE(Math.round(Math.tanh(out[i] / peak * 1.2) * 30000), 44 + i * 2);
fs.writeFileSync(process.argv[2], pcm);
