// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Sound for programs on slides (```app with `allow: sound`). A program asks
// for a sound (sound.play("shot"), sound.tone({...}), sound.noise({...})) and
// the page plays it here, synthesized with WebAudio: no sound files, nothing
// fetched. A program's worker has no AudioContext, so its asks come to the
// page with the frame (web/apps.js) like the deck's do.
//
//   tone:  { wave, freq, to, dur, vol, attack, pan }   a pitch, swept to `to`
//   noise: { dur, vol, from, to, pan }                 filtered noise, swept
//   play:  a preset by name (PRESETS), or an array of tones and noises
//   stop:  everything this program started
//
// The context starts on the first sound after the person has pressed a key
// or clicked on the page (the browser's rule); before that, sounds are
// dropped rather than queued, so a game does not burst out all at once.

const MAX_VOL = 0.6;
const MAX_DUR = 4;
const MAX_A_FRAME = 16;
const WAVES = new Set(["sine", "square", "sawtooth", "triangle"]);

/** The built-in sounds: each a list of parts played together. */
export const PRESETS = {
  shot: [{ wave: "square", freq: 1400, to: 380, dur: 0.09, vol: 0.12 }],
  laser: [{ wave: "sawtooth", freq: 2200, to: 300, dur: 0.14, vol: 0.1 }],
  hit: [{ wave: "square", freq: 300, to: 90, dur: 0.12, vol: 0.18 }, { noise: true, dur: 0.12, vol: 0.2, from: 3000, to: 600 }],
  boom: [{ noise: true, dur: 0.6, vol: 0.4, from: 2400, to: 80 }, { wave: "sine", freq: 120, to: 35, dur: 0.5, vol: 0.35 }],
  bigboom: [{ noise: true, dur: 1.4, vol: 0.5, from: 1800, to: 40 }, { wave: "sine", freq: 90, to: 25, dur: 1.2, vol: 0.45 }],
  pickup: [{ wave: "triangle", freq: 660, to: 1320, dur: 0.12, vol: 0.2 }, { wave: "triangle", freq: 990, to: 1980, dur: 0.12, vol: 0.12, at: 0.06 }],
  warn: [{ wave: "square", freq: 880, dur: 0.12, vol: 0.12 }, { wave: "square", freq: 660, dur: 0.12, vol: 0.12, at: 0.16 }],
  land: [{ wave: "triangle", freq: 220, to: 880, dur: 0.6, vol: 0.2 }],
  flip: [{ wave: "triangle", freq: 500, to: 900, dur: 0.08, vol: 0.1 }],
  start: [
    { wave: "square", freq: 392, dur: 0.1, vol: 0.12 },
    { wave: "square", freq: 523, dur: 0.1, vol: 0.12, at: 0.11 },
    { wave: "square", freq: 659, dur: 0.1, vol: 0.12, at: 0.22 },
    { wave: "square", freq: 784, dur: 0.25, vol: 0.12, at: 0.33 },
  ],
};

function num(v, lo, hi, dflt) {
  const n = Number(v);
  if (!isFinite(n)) return dflt;
  return Math.max(lo, Math.min(hi, n));
}

/**
 * A part as asked, made safe: frequencies and lengths in range, a known
 * wave. null for a part that is no sound at all.
 */
export function cleanPart(p) {
  if (!p || typeof p !== "object") return null;
  const noise = !!p.noise;
  const out = {
    noise,
    dur: num(p.dur, 0.01, MAX_DUR, 0.15),
    vol: num(p.vol, 0, MAX_VOL, 0.2),
    at: num(p.at, 0, MAX_DUR, 0),
    pan: num(p.pan, -1, 1, 0),
    attack: num(p.attack, 0, 1, 0.005),
  };
  if (noise) {
    out.from = num(p.from, 40, 16000, 2000);
    out.to = num(p.to, 40, 16000, out.from);
  } else {
    out.wave = WAVES.has(p.wave) ? p.wave : "square";
    out.freq = num(p.freq, 20, 12000, 440);
    out.to = num(p.to, 20, 12000, out.freq);
  }
  return out;
}

/** The parts an ask plays: a preset's, or the ones it gave. */
export function partsOf(ask) {
  if (!ask) return [];
  if (ask.k === "sound.play") {
    const pre = PRESETS[String(ask.name || "")];
    const list = pre || (Array.isArray(ask.parts) ? ask.parts : []);
    return list.map(cleanPart).filter(Boolean);
  }
  if (ask.k === "sound.tone") {
    const p = cleanPart({ ...ask.part, noise: false });
    return p ? [p] : [];
  }
  if (ask.k === "sound.noise") {
    const p = cleanPart({ ...ask.part, noise: true });
    return p ? [p] : [];
  }
  return [];
}

/** Whether an ask is one for this file to play. */
export function isSoundAsk(a) {
  return !!a && typeof a.k === "string" && a.k.indexOf("sound.") === 0;
}

export function createSound() {
  let ctx = null;
  let master = null;
  let noiseBuf = null;
  let activated = false;
  const live = new Map(); // key -> Set of nodes still playing

  // the browser lets the context start only after a key press or a click
  function onGesture() {
    activated = true;
    if (ctx && ctx.state === "suspended") ctx.resume().catch(() => {});
  }
  if (typeof window !== "undefined") {
    window.addEventListener("keydown", onGesture, true);
    window.addEventListener("pointerdown", onGesture, true);
  }

  function ensure() {
    if (ctx) return ctx;
    if (!activated) return null;
    const AC = typeof window !== "undefined" && (window.AudioContext || window.webkitAudioContext);
    if (!AC) return null;
    try {
      ctx = new AC();
    } catch (_) {
      ctx = null;
      return null;
    }
    master = ctx.createGain();
    master.gain.value = 0.8;
    master.connect(ctx.destination);
    const n = Math.floor(ctx.sampleRate * 1);
    noiseBuf = ctx.createBuffer(1, n, ctx.sampleRate);
    const d = noiseBuf.getChannelData(0);
    for (let i = 0; i < n; i++) d[i] = Math.random() * 2 - 1;
    return ctx;
  }

  function track(key, node, end) {
    let set = live.get(key);
    if (!set) live.set(key, (set = new Set()));
    set.add(node);
    node.onended = () => set.delete(node);
    node.stop(end);
  }

  function playPart(key, p, t0) {
    const c = ctx;
    const t = t0 + p.at;
    const end = t + p.dur;
    const g = c.createGain();
    g.gain.setValueAtTime(0.0001, t);
    g.gain.linearRampToValueAtTime(p.vol, t + Math.min(p.attack, p.dur / 2));
    g.gain.exponentialRampToValueAtTime(0.0001, end);
    let out = g;
    if (p.pan && c.createStereoPanner) {
      const pn = c.createStereoPanner();
      pn.pan.value = p.pan;
      g.connect(pn);
      out = pn;
    }
    out.connect(master);
    if (p.noise) {
      const src = c.createBufferSource();
      src.buffer = noiseBuf;
      src.loop = true;
      const f = c.createBiquadFilter();
      f.type = "lowpass";
      f.frequency.setValueAtTime(p.from, t);
      f.frequency.exponentialRampToValueAtTime(p.to, end);
      src.connect(f);
      f.connect(g);
      src.start(t);
      track(key, src, end + 0.02);
    } else {
      const o = c.createOscillator();
      o.type = p.wave;
      o.frequency.setValueAtTime(p.freq, t);
      if (p.to !== p.freq) o.frequency.exponentialRampToValueAtTime(p.to, end);
      o.connect(g);
      o.start(t);
      track(key, o, end + 0.02);
    }
  }

  /** Plays a frame's sound asks for program `key`; how many it played. */
  function play(key, asks) {
    let n = 0;
    for (const a of asks.slice(0, MAX_A_FRAME)) {
      if (a.k === "sound.stop") {
        stop(key);
        continue;
      }
      const parts = partsOf(a);
      if (!parts.length) continue;
      if (!ensure()) return n;
      if (ctx.state === "suspended") ctx.resume().catch(() => {});
      const t0 = ctx.currentTime + 0.005;
      for (const p of parts) playPart(key, p, t0);
      n++;
    }
    return n;
  }

  /** Stops what program `key` has playing (its slide left the stage). */
  function stop(key) {
    const set = live.get(key);
    if (!set) return;
    for (const node of set) {
      try { node.stop(); } catch (_) { /* already over */ }
    }
    set.clear();
  }

  return { play, stop };
}
