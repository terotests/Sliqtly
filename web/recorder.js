// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The voice of a recorded presentation (Record mode, src/PresRecord.rgr).
//
// Recording, the microphone goes to a MediaRecorder as it is spoken (the
// browser's echo cancelling and noise suppression on); the file in the deck
// is the voice as it was. Played back, it goes through a VOICE: a short chain
// of Web Audio nodes (a filter, a compressor, an echo, a ring modulator)
// chosen in Record → Voice, so a voice can be changed after the fact and
// changed back.
//
// The time a recording's operations are stamped with is `RecClock`'s: from
// the moment the recorder started, pauses left out, the same clock the
// sound's frames follow. A replay goes by the sound's own position when
// there is sound, else by a RecClock of its own.
//
// voiceChain, pickMime and RecClock are plain functions and data; the node
// test (web/test/recorder.test.mjs) checks them and builds a chain on a
// stand-in AudioContext.

// --- voices -------------------------------------------------------------------------

/** The voices, in the menu's order. */
export const VOICES = ["clean", "warm", "radio", "phone", "echo", "robot", "none"];

/**
 * The nodes a voice is made of, input first. Each is one of:
 *   { type: "biquad", filter, freq, q?, gain? }   BiquadFilterNode
 *   { type: "compressor", threshold, ratio, knee, attack, release }
 *   { type: "gain", db }
 *   { type: "drive", amount }    a soft clip (WaveShaper), 0..1
 *   { type: "echo", delay, feedback, mix }
 *   { type: "ring", freq }       ring modulation (the robot)
 * An unknown name is "clean"; "none" is the recording as it is.
 */
export function voiceChain(name) {
  const comp = (threshold, ratio) => ({ type: "compressor", threshold, ratio, knee: 12, attack: 0.004, release: 0.2 });
  switch (VOICES.includes(name) ? name : "clean") {
    case "none":
      return [];
    case "warm":
      return [
        { type: "biquad", filter: "highpass", freq: 70, q: 0.7 },
        { type: "biquad", filter: "lowshelf", freq: 220, gain: 4 },
        { type: "biquad", filter: "highshelf", freq: 6000, gain: -3 },
        comp(-24, 3),
        { type: "gain", db: 2 },
      ];
    case "radio":
      return [
        { type: "biquad", filter: "highpass", freq: 400, q: 0.9 },
        { type: "biquad", filter: "lowpass", freq: 3800, q: 0.9 },
        { type: "biquad", filter: "peaking", freq: 1600, q: 1, gain: 6 },
        { type: "drive", amount: 0.25 },
        comp(-30, 6),
        { type: "gain", db: 3 },
      ];
    case "phone":
      return [
        { type: "biquad", filter: "highpass", freq: 320, q: 1.2 },
        { type: "biquad", filter: "lowpass", freq: 3300, q: 1.2 },
        { type: "drive", amount: 0.45 },
        comp(-28, 8),
      ];
    case "echo":
      return [
        { type: "biquad", filter: "highpass", freq: 80, q: 0.7 },
        comp(-24, 3),
        { type: "echo", delay: 0.26, feedback: 0.32, mix: 0.3 },
      ];
    case "robot":
      return [
        { type: "biquad", filter: "highpass", freq: 120, q: 0.7 },
        { type: "ring", freq: 55 },
        comp(-26, 4),
        { type: "gain", db: 4 },
      ];
    default:
      // clean: no rumble, a little presence, even loudness
      return [
        { type: "biquad", filter: "highpass", freq: 80, q: 0.7 },
        { type: "biquad", filter: "peaking", freq: 3000, q: 1, gain: 3 },
        comp(-24, 3),
        { type: "gain", db: 2 },
      ];
  }
}

const dbGain = (db) => Math.pow(10, db / 20);

// a soft clip: tanh, harder with `amount`
function driveCurve(amount, n = 1024) {
  const k = 1 + amount * 20;
  const c = new Float32Array(n);
  for (let i = 0; i < n; i += 1) {
    const x = (i / (n - 1)) * 2 - 1;
    c[i] = Math.tanh(k * x) / Math.tanh(k);
  }
  return c;
}

/**
 * The voice `name` built on `ctx` after `input`: returns { output, stop }.
 * `output` is the last node (connect it to where it goes); `stop` stops
 * the oscillators a voice runs.
 */
export function buildVoice(ctx, input, name) {
  let last = input;
  const oscs = [];
  const to = (node) => {
    last.connect(node);
    last = node;
    return node;
  };
  for (const n of voiceChain(name)) {
    if (n.type === "biquad") {
      const f = ctx.createBiquadFilter();
      f.type = n.filter;
      f.frequency.value = n.freq;
      if (n.q !== undefined) f.Q.value = n.q;
      if (n.gain !== undefined) f.gain.value = n.gain;
      to(f);
    } else if (n.type === "compressor") {
      const c = ctx.createDynamicsCompressor();
      c.threshold.value = n.threshold;
      c.ratio.value = n.ratio;
      c.knee.value = n.knee;
      c.attack.value = n.attack;
      c.release.value = n.release;
      to(c);
    } else if (n.type === "gain") {
      const g = ctx.createGain();
      g.gain.value = dbGain(n.db);
      to(g);
    } else if (n.type === "drive") {
      const w = ctx.createWaveShaper();
      w.curve = driveCurve(n.amount);
      w.oversample = "2x";
      to(w);
    } else if (n.type === "echo") {
      // dry and a delayed copy fed back into itself, mixed
      const from = last;
      const out = ctx.createGain();
      const wet = ctx.createGain();
      const d = ctx.createDelay(2);
      const fb = ctx.createGain();
      d.delayTime.value = n.delay;
      fb.gain.value = n.feedback;
      wet.gain.value = n.mix;
      from.connect(out);
      from.connect(d);
      d.connect(fb);
      fb.connect(d);
      d.connect(wet);
      wet.connect(out);
      last = out;
    } else if (n.type === "ring") {
      // the voice times a low sine: its gain is the oscillator
      const g = ctx.createGain();
      g.gain.value = 0;
      const o = ctx.createOscillator();
      o.frequency.value = n.freq;
      o.connect(g.gain);
      o.start();
      oscs.push(o);
      to(g);
    }
  }
  return {
    output: last,
    stop() {
      for (const o of oscs) {
        try { o.stop(); } catch (_) { /* already stopped */ }
      }
    },
  };
}

// --- the file -----------------------------------------------------------------------

const MIMES = [
  ["audio/webm;codecs=opus", "webm"],
  ["audio/ogg;codecs=opus", "ogg"],
  ["audio/mp4", "m4a"],
  ["audio/webm", "webm"],
];

/** The first sound format the browser records: { mime, ext }, mime "" its own. */
export function pickMime(isSupported) {
  for (const [mime, ext] of MIMES) {
    try {
      if (isSupported(mime)) return { mime, ext };
    } catch (_) { /* not this one */ }
  }
  return { mime: "", ext: "webm" };
}

// --- the clock ----------------------------------------------------------------------

/** Seconds since start(), pauses left out. `now` is in milliseconds. */
export class RecClock {
  constructor(now = () => performance.now()) {
    this.now = now;
    this.base = 0;
    this.at = -1;
    this.paused = true;
  }
  start(from = 0) {
    this.base = from;
    this.at = this.now();
    this.paused = false;
  }
  pause() {
    if (this.paused) return;
    this.base = this.time();
    this.paused = true;
  }
  resume() {
    if (!this.paused) return;
    this.at = this.now();
    this.paused = false;
  }
  time() {
    if (this.paused || this.at < 0) return this.base;
    return this.base + (this.now() - this.at) / 1000;
  }
}

/** "1:05" */
export function clockText(s) {
  const all = Math.max(0, Math.floor(s));
  const m = Math.floor(all / 60);
  const sec = all % 60;
  return m + ":" + (sec < 10 ? "0" : "") + sec;
}

// --- recording the voice ------------------------------------------------------------

/**
 * The microphone into a file. start() asks for the microphone; when it is
 * refused (or there is none) it throws, and the presentation is recorded
 * without sound. The clock starts when the recorder does.
 */
export class VoiceRecorder {
  constructor() {
    this.clock = new RecClock();
    this.stream = null;
    this.rec = null;
    this.chunks = [];
    this.kind = pickMime((m) => typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(m));
  }
  async start() {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
    this.rec = new MediaRecorder(this.stream, this.kind.mime ? { mimeType: this.kind.mime, audioBitsPerSecond: 48000 } : {});
    this.chunks = [];
    this.rec.ondataavailable = (ev) => { if (ev.data && ev.data.size) this.chunks.push(ev.data); };
    await new Promise((resolve) => {
      this.rec.onstart = resolve;
      this.rec.start(1000);
    });
    this.clock.start();
  }
  /** Without a microphone: the clock alone. */
  startSilent() {
    this.clock.start();
  }
  time() {
    return this.clock.time();
  }
  get paused() {
    return this.clock.paused;
  }
  pause() {
    this.clock.pause();
    if (this.rec && this.rec.state === "recording") this.rec.pause();
  }
  resume() {
    if (this.rec && this.rec.state === "paused") this.rec.resume();
    this.clock.resume();
  }
  /** The sound as a Blob (null without a microphone), and its extension. */
  async stop() {
    this.clock.pause();
    const rec = this.rec;
    let blob = null;
    if (rec && rec.state !== "inactive") {
      await new Promise((resolve) => {
        rec.onstop = resolve;
        rec.stop();
      });
      blob = new Blob(this.chunks, { type: rec.mimeType || this.kind.mime || "audio/webm" });
    }
    if (this.stream) for (const tr of this.stream.getTracks()) tr.stop();
    this.stream = null;
    this.rec = null;
    return { blob, ext: this.kind.ext, type: (blob && blob.type) || "audio/webm" };
  }
}

// --- playing it back ----------------------------------------------------------------

/**
 * A recording's sound (a Blob, or null for a silent one) played through a
 * voice. time() is where the sound is; without sound a clock stands in.
 */
export class VoicePlayer {
  constructor(blob, voice, duration) {
    this.duration = duration;
    this.voice = voice;
    this.clock = new RecClock();
    this.audio = null;
    this.ctx = null;
    this.src = null;
    this.chain = null;
    this.url = "";
    if (blob) {
      this.url = URL.createObjectURL(blob);
      this.audio = new Audio(this.url);
      this.audio.preload = "auto";
    }
  }
  wire() {
    if (!this.audio || this.ctx) return;
    try {
      this.ctx = new AudioContext();
      this.src = this.ctx.createMediaElementSource(this.audio);
      this.setVoice(this.voice);
    } catch (e) {
      // no Web Audio: the element plays straight out
      console.warn("voice filters unavailable", e);
      this.ctx = null;
    }
  }
  setVoice(name) {
    this.voice = name;
    if (!this.ctx || !this.src) return;
    this.src.disconnect();
    if (this.chain) {
      this.chain.output.disconnect();
      this.chain.stop();
    }
    this.chain = buildVoice(this.ctx, this.src, name);
    this.chain.output.connect(this.ctx.destination);
  }
  async play(from) {
    this.clock.start(from);
    if (!this.audio) return;
    this.wire();
    this.audio.currentTime = from;
    if (this.ctx && this.ctx.state === "suspended") await this.ctx.resume().catch(() => {});
    await this.audio.play().catch((e) => console.warn("the recording's sound did not play", e));
  }
  get paused() {
    return this.clock.paused;
  }
  pause() {
    this.clock.pause();
    if (this.audio) this.audio.pause();
  }
  resume() {
    this.clock.resume();
    if (this.audio) this.audio.play().catch(() => {});
  }
  seek(t) {
    const to = Math.max(0, Math.min(this.duration, t));
    const was = this.clock.paused;
    this.clock.start(to);
    if (was) this.clock.pause();
    if (this.audio) this.audio.currentTime = to;
  }
  time() {
    // the sound is the clock while it plays (it can stall or start late);
    // past its end (a silent tail) the clock goes on
    if (this.audio && !this.audio.paused && !this.audio.ended) {
      const a = this.audio.currentTime;
      this.clock.start(a);
      return a;
    }
    return this.clock.time();
  }
  ended() {
    return this.time() >= this.duration;
  }
  close() {
    if (this.audio) this.audio.pause();
    if (this.chain) this.chain.stop();
    if (this.ctx) this.ctx.close().catch(() => {});
    if (this.url) URL.revokeObjectURL(this.url);
    this.audio = null;
    this.ctx = null;
  }
}
