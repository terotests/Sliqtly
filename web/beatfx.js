// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Slide effects that move with the deck's music (web/music.js):
//
//   {fx=spectrum}   a ring of bars (round fx-x, fx-y), rainbow, bass at the top
//   {fx=kaleido}    a kaleidoscope swirl that pulses on the beat
//   {fx=ridges}     the last second and a half of the spectrum as rainbow
//                   ridges, one behind the other
//
// Each is an EVG surface effect with a model: the model hands the shader what
// PresBeat packed this frame (bars, peaks, waveform, history, pulse) as the
// float texture `uData`. While nothing plays, PresBeat packs calm bars that
// breathe by themselves, so the slide, its thumbnail and its export still
// show the effect.

// What the shaders read: PresBeat.pack()'s layout (src/PresBeat.rgr).
const READ = `
vec4 bTex(int i) { return texelFetch(uData, ivec2(i % FX_DATA_W, i / FX_DATA_W), 0); }
float bAt(int k) {
  vec4 v = bTex(k / 4);
  int c = k - (k / 4) * 4;
  return c == 0 ? v.x : (c == 1 ? v.y : (c == 2 ? v.z : v.w));
}
float bLine(int at, int n, float x) {
  float f = clamp(x, 0.0, 1.0) * float(n - 1);
  int i = int(floor(f));
  int j = min(i + 1, n - 1);
  return mix(bAt(at + i), bAt(at + j), f - float(i));
}
float bBand(float x) { return bLine(8, 64, x); }
float bPeak(float x) { return bLine(72, 64, x); }
float bWave(float x) { return bLine(136, 128, x); }
float bRow(int r, float x) { return bLine(264 + r * 64, 64, x); }
float bPulse() { return bAt(0); }
float bLevel() { return bAt(1); }
float bBass() { return bAt(2); }
vec3 bHue(float h) {
  return clamp(abs(mod(h * 6.0 + vec3(0.0, 4.0, 2.0), 6.0) - 3.0) - 1.0, 0.0, 1.0);
}
// light over a darkened page: the painter blends with SRC_ALPHA, so the light
// is divided by the alpha it is about to be multiplied by
vec4 bOut(vec3 light, float dark) {
  float a = clamp(max(max(light.r, light.g), light.b) + dark, 0.0, 1.0);
  return vec4(light / max(a, 0.0001), a);
}
`;

const SPECTRUM = `${READ}
vec4 fxColor(vec2 p, vec2 local) {
  float asp = uBox.z / max(uBox.w, 1.0);
  vec2 q = (local - vec2(p_x, p_y)) * vec2(asp, 1.0);
  float r = length(q);
  float pulse = bPulse();
  float R = 0.19 * p_size * (1.0 + 0.1 * pulse);
  // round from the top, both ways: the bass at the top, the highs below
  float at = atan(q.x, -q.y);
  float a01 = at / 6.2831853 + 0.5;
  float n = max(8.0, floor(p_bars));
  float seg = floor(a01 * n);
  float mid = (seg + 0.5) / n;
  float x = abs(mid * 2.0 - 1.0);
  float v = bBand(x);
  float len = R * 0.08 + v * v * 0.26 * p_size;
  float across = abs(fract(a01 * n) - 0.5) * 2.0;
  float bar = 1.0 - smoothstep(0.45, 0.65, across);
  float aa = 1.5 / max(uBox.w, 1.0);
  float inBar = smoothstep(R - aa, R + aa, r) * (1.0 - smoothstep(R + len - aa, R + len + aa, r));
  float halo = exp(-max(r - R - len, 0.0) * 30.0) * step(R, r) * bar * 0.35 * p_glow;
  float pkv = bPeak(x);
  float pk = R + R * 0.08 + pkv * pkv * 0.26 * p_size + 0.012;
  float peak = (1.0 - smoothstep(aa, aa * 3.0, abs(r - pk))) * bar;
  vec3 col = bHue(mid + p_hue / 360.0 + uTime * p_spin);
  vec3 light = col * (inBar * bar * 1.1 + halo + peak * 0.9);
  // the ring the bars stand on, and the middle lit on the beat
  float ring = exp(-abs(r - R * 0.93) * 260.0) * (0.35 + 0.8 * pulse);
  light += vec3(1.0) * ring * 0.8;
  light += mix(col, vec3(1.0), 0.5) * exp(-r * 9.0 / max(p_size, 0.1)) * pulse * 0.45 * p_glow;
  // the waveform inside the ring
  float wr = R * 0.62 * (1.0 + bWave(a01) * 0.5);
  light += col * 0.7 * (1.0 - smoothstep(aa, aa * 2.5, abs(r - wr)));
  return bOut(light, p_dark);
}
`;

const KALEIDO = `${READ}
vec4 fxColor(vec2 p, vec2 local) {
  float asp = uBox.z / max(uBox.w, 1.0);
  vec2 q = (local - 0.5) * vec2(asp, 1.0);
  float r = length(q) + 0.0001;
  float pulse = bPulse();
  float bass = bBass();
  float k = max(2.0, floor(p_segments));
  float sa = 6.2831853 / k;
  float a = atan(q.y, q.x) + uTime * p_speed * 0.4;
  float am = abs(mod(a, sa) - sa * 0.5);
  float lr = log(r);
  float zoom = lr - uTime * p_speed * 0.8 - pulse * 0.12;
  float sw = am * 3.0 + p_twist * lr * 1.6 + uTime * p_speed;
  float petals = 0.5 + 0.5 * sin(sw * 6.0 + zoom * 7.0);
  float rings = 0.5 + 0.5 * sin(zoom * 11.0 - bass * 3.0);
  float m = petals * (0.45 + 0.55 * rings);
  float v = bBand(clamp(r * 1.3, 0.0, 1.0));
  float sharp = mix(3.0, 1.2, clamp(v + pulse * 0.5, 0.0, 1.0));
  float lit = pow(m, sharp);
  vec3 base = bHue(p_hue / 360.0);
  vec3 col = mix(base * 0.18, mix(base, vec3(1.0), 0.45), lit);
  float bright = (0.35 + 0.9 * v + 0.6 * pulse) * (1.0 - smoothstep(0.25, 1.1, r));
  // the eye in the middle, white on the beat
  col += vec3(1.0) * exp(-r * 22.0) * (0.3 + pulse);
  return bOut(col * bright, p_dark);
}
`;

const RIDGES = `${READ}
vec4 fxColor(vec2 p, vec2 local) {
  float rows = clamp(floor(p_rows), 2.0, 24.0);
  float lw = max(p_line, 0.5) / max(uBox.w, 1.0);
  float pulse = bPulse();
  // the newest row at the front, lowest; each further one higher and
  // narrower; a row hides what is behind it below its line
  for (int r = 0; r < 24; r++) {
    float fr = float(r);
    if (fr >= rows) break;
    float base = 0.92 - fr * (0.4 / rows);
    float persp = 1.0 - fr * 0.016;
    float x = (local.x - 0.5) / persp + 0.5;
    float xs = abs(x * 2.0 - 1.0);
    float v = 0.0;
    if (xs <= 1.0) {
      // a little across, so a ridge is a hill and not a step
      float s = 0.025;
      v = (bRow(r, xs - s) + 2.0 * bRow(r, xs) + bRow(r, xs + s)) * 0.25;
      v = v * v * (1.0 - xs * 0.6);
    }
    float lift = r == 0 ? 1.0 + 0.15 * pulse : 1.0;
    float yl = base - v * 0.45 * p_height * lift;
    float d = local.y - yl;
    if (d > -lw * 1.5) {
      float line = 1.0 - smoothstep(lw * 0.5, lw * 1.5, abs(d));
      float fade = 1.0 - fr / rows * 0.65;
      vec3 col = bHue(x * 0.85 + p_hue / 360.0 + fr * 0.015 + uTime * 0.04);
      return bOut(col * line * fade * (1.0 + 0.4 * pulse), p_dark);
    }
  }
  return bOut(vec3(0.0), p_dark);
}
`;

export const BEAT_EFFECTS = ["spectrum", "kaleido", "ridges"];

// The three registered with the painter; `frame(t)` is the music's packed
// state (web/music.js), one row of RGBA float texels.
export function registerBeatEffects(registerSurfaceEffect, frame) {
  const model = {
    frame(params, w, h, t) {
      return { width: 512, height: 1, data: frame(t) };
    },
  };
  registerSurfaceEffect({
    name: "spectrum", layer: "source", model,
    params: { bars: 96, size: 1, x: 0.5, y: 0.5, hue: 0, spin: 0.03, glow: 1, dark: 0 },
    frag: SPECTRUM,
  });
  registerSurfaceEffect({
    name: "kaleido", layer: "source", model,
    params: { segments: 8, twist: 1, speed: 0.25, hue: 130, dark: 0.5 },
    frag: KALEIDO,
  });
  registerSurfaceEffect({
    name: "ridges", layer: "source", model,
    params: { rows: 24, height: 1, line: 1.6, hue: 0, dark: 0 },
    frag: RIDGES,
  });
}
