// node --test: the recording's voices, its sound format and its clock (web/recorder.js).
import test from "node:test";
import assert from "node:assert/strict";
import { VOICES, voiceChain, buildVoice, pickMime, RecClock, clockText } from "../recorder.js";

test("every voice has a chain; none is the recording as it is", () => {
  for (const v of VOICES) assert.ok(Array.isArray(voiceChain(v)), v);
  assert.deepEqual(voiceChain("none"), []);
  assert.ok(voiceChain("clean").length > 0);
  // an unknown name (an old file, a typo) is the clean voice
  assert.deepEqual(voiceChain("nonsense"), voiceChain("clean"));
  // the radio and the phone cut the low end and the high end
  for (const v of ["radio", "phone"]) {
    const types = voiceChain(v).filter((n) => n.type === "biquad").map((n) => n.filter);
    assert.ok(types.includes("highpass") && types.includes("lowpass"), v);
  }
});

// A stand-in AudioContext: nodes that remember what they were connected to.
function fakeCtx() {
  const made = [];
  const param = () => ({ value: 0 });
  const node = (kind, extra = {}) => {
    const n = { kind, to: [], connect(x) { this.to.push(x); return x; }, disconnect() { this.to = []; }, ...extra };
    made.push(n);
    return n;
  };
  return {
    made,
    createBiquadFilter: () => node("biquad", { type: "", frequency: param(), Q: param(), gain: param() }),
    createDynamicsCompressor: () => node("comp", { threshold: param(), ratio: param(), knee: param(), attack: param(), release: param() }),
    createGain: () => node("gain", { gain: param() }),
    createWaveShaper: () => node("shaper", { curve: null, oversample: "none" }),
    createDelay: () => node("delay", { delayTime: param() }),
    createOscillator: () => node("osc", { frequency: param(), started: false, start() { this.started = true; }, stop() { this.started = false; } }),
  };
}

// every path from `from` ends at `end`
function reaches(from, end, seen = new Set()) {
  if (from === end) return true;
  if (seen.has(from)) return false;
  seen.add(from);
  return from.to.some((n) => reaches(n, end, seen));
}

test("a voice is built as one chain from the input to its output", () => {
  for (const v of VOICES) {
    const ctx = fakeCtx();
    const input = { kind: "input", to: [], connect(x) { this.to.push(x); return x; } };
    const { output, stop } = buildVoice(ctx, input, v);
    if (v === "none") {
      assert.equal(output, input);
      continue;
    }
    assert.ok(reaches(input, output), v + ": the input reaches the output");
    assert.equal(output.to.length, 0, v + ": the output is left for the caller");
    stop();
  }
});

test("the robot runs an oscillator and stops it", () => {
  const ctx = fakeCtx();
  const input = { to: [], connect(x) { this.to.push(x); return x; } };
  const chain = buildVoice(ctx, input, "robot");
  const osc = ctx.made.find((n) => n.kind === "osc");
  assert.ok(osc && osc.started);
  chain.stop();
  assert.equal(osc.started, false);
});

test("the echo keeps the dry voice and feeds the delay back", () => {
  const ctx = fakeCtx();
  const input = { to: [], connect(x) { this.to.push(x); return x; } };
  buildVoice(ctx, input, "echo");
  const delay = ctx.made.find((n) => n.kind === "delay");
  assert.ok(delay.to.some((n) => n.to.includes(delay)), "a loop through the feedback gain");
});

test("the sound format: the first the browser takes", () => {
  assert.deepEqual(pickMime(() => true), { mime: "audio/webm;codecs=opus", ext: "webm" });
  assert.deepEqual(pickMime((m) => m === "audio/mp4"), { mime: "audio/mp4", ext: "m4a" });
  assert.deepEqual(pickMime(() => false), { mime: "", ext: "webm" });
  assert.deepEqual(pickMime(() => { throw new Error("no"); }), { mime: "", ext: "webm" });
});

test("the clock leaves pauses out", () => {
  let now = 1000;
  const c = new RecClock(() => now);
  assert.equal(c.time(), 0);
  c.start();
  now += 2000;
  assert.equal(c.time(), 2);
  c.pause();
  now += 5000;
  assert.equal(c.time(), 2);
  c.resume();
  now += 500;
  assert.equal(c.time(), 2.5);
  c.start(10);
  now += 1000;
  assert.equal(c.time(), 11);
});

test("times as the bar shows them", () => {
  assert.equal(clockText(0), "0:00");
  assert.equal(clockText(65.4), "1:05");
  assert.equal(clockText(-3), "0:00");
});
