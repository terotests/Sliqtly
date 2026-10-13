// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Hull Runner: a side-scrolling shooter over the deck of a giant carrier,
// in TypeScript + JSX, laid out by EVG with the CSS tab's stylesheet.
//
// Fly the length of the carrier, dodge its towers and the drones, knock out
// its reactor cores, then touch down on the landing strip at the far end.
// The ship flips when you turn: speed is kept but the direction changes.
//
//   ← →  thrust / turn     ↑ ↓  climb / dive     Space  fire
//   M    music on / off    (the fence needs allow: sound for the sounds)
//
// Everything is boxes: the ship, the hull and the drones are small trees of
// divs placed with inline style and dressed by the classes in the CSS tab.

const W = 640;
const H = 480;
const DECK_T = 150; // the carrier's deck, top and bottom edge on screen
const DECK_B = 440;
const SKY_T = 34; // the ship may not climb above this (the HUD)
const SHIP_W = 36;
const SHIP_H = 14;
const MAX_V = 560;
const ACC = 520;
const DRAG = 0.6;

type Mode = "title" | "play" | "dying" | "landed" | "over";
type Block = { x: number; w: number; y0: number; y1: number; kind: "tower" | "fin" };
type Core = { x: number; y: number; hp: number; dead: boolean; flash: number };
type Drone = { x: number; y: number; vx: number; ph: number; amp: number; base: number; hp: number };
type Shot = { x: number; y: number; vx: number; life: number };
type Bit = { x: number; y: number; vx: number; vy: number; life: number; c: string };
type Pop = { x: number; y: number; text: string; t: number };
type Star = { x: number; y: number; d: number };

// ---- a seeded random, so each carrier is the same every time ---------------

let seed = 1;
function rnd(): number {
  seed = (seed * 1103515245 + 12345) & 0x7fffffff;
  return seed / 0x7fffffff;
}
function rr(a: number, b: number): number { return a + (b - a) * rnd(); }

// ---- the carrier ------------------------------------------------------------

// each carrier its own colours: the class suffix the CSS tab dresses
const LIVERY = ["slate", "rust", "jade", "violet"];

let len = 5200; // the carrier's length
let blocks: Block[] = [];
let cores: Core[] = [];
let strip = 0; // the landing strip's start
let livery = "slate";

function buildCarrier(n: number) {
  seed = 7919 * n + 13;
  len = 4600 + n * 500;
  livery = LIVERY[(n - 1) % LIVERY.length];
  blocks = [];
  cores = [];
  strip = len - 520;
  let x = 520;
  while (x < strip - 200) {
    const r = rnd();
    if (r < 0.42) {
      // a tower standing on the upper or the lower half of the deck
      const h = rr(70, 150);
      const top = rnd() < 0.5;
      blocks.push({ x, w: rr(28, 56), y0: top ? DECK_T - 40 : DECK_B - h, y1: top ? DECK_T - 40 + h : DECK_B + 10, kind: "tower" });
      x += rr(150, 260);
    } else if (r < 0.62) {
      // a pair of fins with a gap to thread
      const gap = rr(110, 150);
      const at = rr(DECK_T + 30, DECK_B - 30 - gap);
      const w = rr(22, 34);
      blocks.push({ x, w, y0: DECK_T - 40, y1: at, kind: "fin" });
      blocks.push({ x, w, y0: at + gap, y1: DECK_B + 10, kind: "fin" });
      x += rr(200, 300);
    } else {
      cores.push({ x: x + 10, y: rr(DECK_T + 40, DECK_B - 40), hp: 3, dead: false, flash: 0 });
      x += rr(160, 240);
    }
  }
}

// ---- state ------------------------------------------------------------------

let mode: Mode = "title";
let modeClock = 0;
let clock = 0;
let score = 0;
let best = 0;
let lives = 3;
let carrier = 1;
let music = true;
let beat = 0;
let beatClock = 0;

let sx = 0, sy = 0, vx = 0, vy = 0;
let face = 1; // 1 right, -1 left
let flip = 0; // >0 while turning: the ship looks thin
let cam = 0;
let cool = 0;
let shots: Shot[] = [];
let drones: Drone[] = [];
let droneClock = 0;
let bits: Bit[] = [];
let pops: Pop[] = [];
let warned = false;

const stars: Star[] = [];
for (let i = 0; i < 46; i++) {
  seed = i * 977 + 3;
  stars.push({ x: rnd() * W, y: rnd() * H, d: 0.15 + rnd() * 0.5 });
}

function resetShip() {
  sx = 120;
  sy = (DECK_T + DECK_B) / 2;
  vx = 160;
  vy = 0;
  face = 1;
  flip = 0;
  cam = sx - 200;
  shots = [];
  drones = [];
  droneClock = 2.5;
  warned = false;
}

function newGame() {
  score = 0;
  lives = 3;
  carrier = 1;
  buildCarrier(carrier);
  resetShip();
  mode = "play";
  modeClock = 0;
  sound.play("start");
}

function coresLeft(): number {
  let n = 0;
  for (const c of cores) if (!c.dead) n++;
  return n;
}

function burst(x: number, y: number, n: number, c: string) {
  for (let i = 0; i < n && bits.length < 160; i++) {
    const a = rnd() * Math.PI * 2;
    const s = rr(60, 260);
    bits.push({ x, y, vx: Math.cos(a) * s, vy: Math.sin(a) * s, life: rr(0.35, 0.9), c });
  }
}

function pop(x: number, y: number, text: string) {
  pops.push({ x, y, text, t: 0.9 });
}

function die() {
  if (mode !== "play") return;
  mode = "dying";
  modeClock = 0;
  lives--;
  burst(sx, sy, 40, "bit-hot");
  sound.play("bigboom");
}

// ---- keys -------------------------------------------------------------------

function onKeyDown(key: string) {
  if (key === "m" || key === "M") { music = !music; return; }
  if (mode === "title" || mode === "over") {
    if (key === " " || key === "Enter") newGame();
  }
}

// ---- the frame --------------------------------------------------------------

function hitsBlock(x: number, y: number, w: number, h: number): boolean {
  for (const b of blocks) {
    if (x + w / 2 < b.x || x - w / 2 > b.x + b.w) continue;
    if (y + h / 2 < b.y0 || y - h / 2 > b.y1) continue;
    return true;
  }
  return false;
}

function playMusic(dt: number) {
  if (!music || mode === "title") return;
  // a little bass line of its own: eight steps, two bars
  const STEP = 0.2;
  beatClock += dt;
  if (beatClock < STEP) return;
  beatClock -= STEP;
  const BASS = [55, 0, 55, 82.4, 0, 73.4, 0, 65.4];
  const f = BASS[beat % 8] * (mode === "landed" ? 2 : 1);
  if (f > 0) sound.tone({ wave: "triangle", freq: f, dur: 0.16, vol: 0.14 });
  if (beat % 4 === 2) sound.noise({ dur: 0.04, vol: 0.05, from: 9000, to: 6000 });
  beat++;
}

function tick(dt: number, input: any) {
  dt = Math.min(dt, 1 / 30);
  clock += dt;
  modeClock += dt;
  playMusic(dt);

  for (const b of bits) { b.x += b.vx * dt; b.y += b.vy * dt; b.vx *= 0.97; b.vy *= 0.97; b.life -= dt; }
  bits = bits.filter((b) => b.life > 0);
  for (const p of pops) { p.t -= dt; p.y -= 26 * dt; }
  pops = pops.filter((p) => p.t > 0);

  if (mode === "dying") {
    if (modeClock > 1.6) {
      if (lives <= 0) { mode = "over"; best = Math.max(best, score); }
      else { resetShip(); mode = "play"; }
      modeClock = 0;
    }
    return;
  }
  if (mode === "landed") {
    vx *= Math.pow(0.2, dt);
    sx += vx * dt;
    if (modeClock > 3) {
      carrier++;
      buildCarrier(carrier);
      resetShip();
      mode = "play";
      modeClock = 0;
      sound.play("start");
    }
    return;
  }
  if (mode !== "play") return;

  const k = input.keys;
  const left = k["ArrowLeft"] || k["a"];
  const right = k["ArrowRight"] || k["d"];
  const up = k["ArrowUp"] || k["w"];
  const down = k["ArrowDown"] || k["s"];
  const fire = k[" "];

  // thrust; pressing the other way turns the ship over
  const want = right && !left ? 1 : left && !right ? -1 : 0;
  if (want !== 0 && want !== face) {
    face = want;
    flip = 0.22;
    sound.play("flip");
  }
  if (want !== 0) vx += want * ACC * dt;
  else vx -= vx * DRAG * dt;
  vx = Math.max(-MAX_V, Math.min(MAX_V, vx));
  if (flip > 0) flip = Math.max(0, flip - dt);

  vy += ((down ? 1 : 0) - (up ? 1 : 0)) * 900 * dt;
  vy *= Math.pow(0.02, dt);
  sy = Math.max(SKY_T + 10, Math.min(H - 16, sy + vy * dt));
  sx = Math.max(-200, Math.min(len + 200, sx + vx * dt));

  // the camera keeps more room ahead of the ship
  const goal = sx - (face > 0 ? 210 : W - 210);
  cam += (goal - cam) * Math.min(1, dt * 2.2);

  // fire
  cool -= dt;
  if (fire && cool <= 0 && flip === 0) {
    cool = 0.14;
    shots.push({ x: sx + face * 20, y: sy + 1, vx: vx + face * 900, life: 0.7 });
    sound.play("shot");
  }
  for (const s of shots) { s.x += s.vx * dt; s.life -= dt; }

  // shots against towers, cores and drones
  for (const s of shots) {
    if (s.life <= 0) continue;
    if (hitsBlock(s.x, s.y, 6, 2)) { s.life = 0; burst(s.x, s.y, 3, "bit-spark"); continue; }
    for (const c of cores) {
      if (c.dead || Math.abs(s.x - c.x) > 18 || Math.abs(s.y - c.y) > 18) continue;
      s.life = 0;
      c.hp--;
      c.flash = 0.1;
      if (c.hp <= 0) {
        c.dead = true;
        score += 500;
        pop(c.x, c.y - 20, "500");
        burst(c.x, c.y, 26, "bit-hot");
        sound.play("boom");
      } else sound.play("hit");
      break;
    }
    for (const d of drones) {
      if (d.hp <= 0 || s.life <= 0 || Math.abs(s.x - d.x) > 16 || Math.abs(s.y - d.y) > 12) continue;
      s.life = 0;
      d.hp = 0;
      score += 150;
      pop(d.x, d.y - 16, "150");
      burst(d.x, d.y, 14, "bit-cold");
      sound.play("hit");
    }
  }
  shots = shots.filter((s) => s.life > 0);
  for (const c of cores) if (c.flash > 0) c.flash -= dt;

  // drones come in waves from ahead of the ship
  droneClock -= dt;
  if (droneClock <= 0 && sx > 300 && sx < strip) {
    droneClock = rr(3.2, 5.5) - Math.min(1.5, carrier * 0.25);
    const n = 3 + Math.floor(rnd() * 3);
    const base = rr(DECK_T + 30, DECK_B - 30);
    const from = cam + (face > 0 ? W + 40 : -40);
    for (let i = 0; i < n; i++) {
      drones.push({ x: from + face * i * 46, y: base, vx: -face * rr(170, 230), ph: i * 0.6, amp: rr(30, 70), base, hp: 1 });
    }
  }
  for (const d of drones) {
    d.x += d.vx * dt;
    d.ph += dt * 3;
    d.y = d.base + Math.sin(d.ph) * d.amp;
  }
  drones = drones.filter((d) => d.hp > 0 && d.x > cam - 200 && d.x < cam + W + 200);

  // what the ship runs into
  const w = flip > 0 ? 14 : SHIP_W;
  if (hitsBlock(sx, sy, w - 6, SHIP_H - 4)) { die(); return; }
  for (const d of drones) if (Math.abs(d.x - sx) < 22 && Math.abs(d.y - sy) < 13) { die(); return; }
  for (const c of cores) if (!c.dead && Math.abs(c.x - sx) < 22 && Math.abs(c.y - sy) < 18) { die(); return; }

  // the strip: once the cores are out, slow and low over it lands
  const left2 = coresLeft();
  if (left2 === 0 && !warned) {
    warned = true;
    pop(sx, sy - 30, "LAND NOW");
    sound.play("warn");
  }
  if (left2 === 0 && sx > strip && sx < strip + 400 && sy > DECK_B - 60 && Math.abs(vx) < 260) {
    mode = "landed";
    modeClock = 0;
    const bonus = 1000 * carrier + lives * 250;
    score += bonus;
    pop(sx, sy - 30, "+" + bonus);
    sound.play("land");
  }
}

// ---- the picture --------------------------------------------------------------

function X(wx: number): number { return wx - cam; }
function seen(wx: number, ww: number): boolean { return wx + ww > cam - 20 && wx < cam + W + 20; }

function Stars() {
  return (
    <div className="scene">
      {stars.map((s) => {
        let x = (s.x - cam * s.d) % W;
        if (x < 0) x += W;
        return <div className={s.d > 0.45 ? "star bright" : "star"} style={{ left: x, top: s.y }} />;
      })}
    </div>
  );
}

function Hull() {
  const parts: any[] = [];
  const x0 = Math.max(0, cam - 40);
  const x1 = Math.min(len, cam + W + 40);
  if (x1 <= x0) return <div className="scene" />;
  parts.push(<div className={"hull hull-" + livery} style={{ left: X(x0), top: DECK_T, width: x1 - x0, height: DECK_B - DECK_T }} />);
  // the deck's plates: a seam every 96 px, a row of lights along the edges
  const first = Math.ceil(x0 / 96) * 96;
  for (let x = first; x < x1; x += 96) {
    parts.push(<div className="seam" style={{ left: X(x), top: DECK_T + 4, height: DECK_B - DECK_T - 8 }} />);
    const on = Math.floor(clock * 3 + x / 96) % 4 === 0;
    parts.push(<div className={on ? "edge-light on" : "edge-light"} style={{ left: X(x) + 44, top: DECK_T + 6 }} />);
    parts.push(<div className={on ? "edge-light on" : "edge-light"} style={{ left: X(x) + 44, top: DECK_B - 12 }} />);
  }
  // the stripes down the middle
  parts.push(<div className="stripe" style={{ left: X(x0), top: (DECK_T + DECK_B) / 2 - 2, width: x1 - x0 }} />);
  // the bow and the stern
  if (seen(0, 60)) parts.push(<div className={"cap cap-" + livery} style={{ left: X(-28), top: DECK_T + 30, height: DECK_B - DECK_T - 60 }} />);
  if (seen(len, 60)) parts.push(<div className={"cap cap-" + livery} style={{ left: X(len), top: DECK_T + 30, height: DECK_B - DECK_T - 60 }} />);
  // the landing strip, lit once the cores are out
  if (seen(strip, 400)) {
    const lit = coresLeft() === 0;
    parts.push(<div className={lit ? "strip lit" : "strip"} style={{ left: X(strip), top: DECK_B - 70, width: 400 }} />);
    for (let i = 0; i < 8; i++) {
      const on = lit && Math.floor(clock * 8) % 8 === i;
      parts.push(<div className={on ? "strip-mark on" : "strip-mark"} style={{ left: X(strip) + 18 + i * 48, top: DECK_B - 40 }} />);
    }
  }
  return <div className="scene">{parts}</div>;
}

function Blocks() {
  return (
    <div className="scene">
      {blocks.filter((b) => seen(b.x, b.w)).map((b) => (
        <div className={b.kind + " " + b.kind + "-" + livery} style={{ left: X(b.x), top: b.y0, width: b.w, height: b.y1 - b.y0 }}>
          <div className="block-top" style={{ width: b.w }} />
          <div className="block-shade" style={{ left: b.w - 8, height: b.y1 - b.y0 }} />
        </div>
      ))}
    </div>
  );
}

function Cores() {
  return (
    <div className="scene">
      {cores.filter((c) => seen(c.x - 20, 40)).map((c) => c.dead
        ? <div className="core-hole" style={{ left: X(c.x) - 16, top: c.y - 16 }} />
        : (
          <div className={c.flash > 0 ? "core hurt" : "core"} style={{ left: X(c.x) - 18, top: c.y - 18 }}>
            <div className={"core-eye core-" + c.hp} style={{ left: 9 + Math.sin(clock * 5 + c.x) * 2, top: 9 }} />
          </div>
        ))}
    </div>
  );
}

function Drones() {
  return (
    <div className="scene">
      {drones.map((d) => {
        const wob = Math.abs(Math.sin(d.ph * 2)) * 6;
        return (
          <div className="drone" style={{ left: X(d.x) - 14, top: d.y - 10 }}>
            <div className="drone-wing" style={{ left: 0, top: 8 - wob / 2, height: 4 + wob }} />
            <div className="drone-wing" style={{ left: 24, top: 8 - wob / 2, height: 4 + wob }} />
            <div className="drone-body" />
          </div>
        );
      })}
    </div>
  );
}

function Ship() {
  if (mode === "dying" || mode === "title" || mode === "over") return <div className="scene" />;
  const thin = flip > 0;
  const w = thin ? 12 : SHIP_W;
  const x = X(sx) - w / 2;
  const y = sy - SHIP_H / 2;
  if (thin) return <div className="ship-edge" style={{ left: x, top: y - 4, width: w, height: SHIP_H + 8 }} />;
  // drawn facing right; facing left mirrors the offsets
  const m = (lx: number, pw: number) => (face > 0 ? lx : SHIP_W - lx - pw);
  const flame = 6 + Math.abs(vx) / MAX_V * 14 + Math.sin(clock * 40) * 3;
  return (
    <div className="ship" style={{ left: x, top: y }}>
      <div className="flame" style={{ left: m(-flame + 2, flame), top: 5, width: flame }} />
      <div className="ship-wing" style={{ left: m(4, 18), top: -5 }} />
      <div className="ship-wing" style={{ left: m(4, 18), top: SHIP_H - 2 }} />
      <div className="ship-body" style={{ left: m(0, 30) }} />
      <div className="ship-nose" style={{ left: m(28, 8) }} />
      <div className="ship-band" style={{ left: m(10, 4) }} />
      <div className="ship-glass" style={{ left: m(18, 9) }} />
    </div>
  );
}

function Shots() {
  return (
    <div className="scene">
      {shots.map((s) => <div className="shot" style={{ left: X(s.x) - 7, top: s.y - 1 }} />)}
    </div>
  );
}

function Bits() {
  return (
    <div className="scene">
      {bits.map((b) => <div className={"bit " + b.c} style={{ left: X(b.x), top: b.y, opacity: Math.min(1, b.life * 2) }} />)}
      {pops.map((p) => <span className="pop" style={{ left: X(p.x) - 30, top: p.y }}>{p.text}</span>)}
    </div>
  );
}

function Hud() {
  // how far along the carrier the ship is, and the cores still standing
  const along = Math.max(0, Math.min(1, sx / len));
  return (
    <div className="hud">
      <span className="hud-text" style={{ left: 12 }}>{"SCORE " + score}</span>
      <span className="hud-text" style={{ left: 170 }}>{"CARRIER " + carrier}</span>
      <span className="hud-text" style={{ left: 300 }}>{"CORES " + coresLeft()}</span>
      <div className="track" />
      <div className="track-dot" style={{ left: 410 + along * 150 }} />
      {[0, 1, 2, 3, 4].filter((i) => i < lives).map((i) => <div className="life" style={{ left: 584 + i * 12 }} />)}
    </div>
  );
}

function Banner(p: { title: string; lines: string[] }) {
  return (
    <div className="banner">
      <span className="banner-title">{p.title}</span>
      {p.lines.map((l, i) => <span className="banner-line" style={{ top: 70 + i * 26 }}>{l}</span>)}
    </div>
  );
}

function view() {
  if (blocks.length === 0) { buildCarrier(1); resetShip(); }
  return (
    <div className="stage" onClick={() => onKeyDown(" ")}>
      <Stars />
      <Hull />
      <Cores />
      <Blocks />
      <Drones />
      <Shots />
      <Ship />
      <Bits />
      <Hud />
      {mode === "title" && <Banner title="HULL RUNNER" lines={["Knock out the cores, then land on the strip.", "← → thrust and turn   ↑ ↓ climb and dive", "Space fire   M music", "Space or click to start"]} />}
      {mode === "landed" && <Banner title="TOUCHDOWN" lines={["Carrier " + carrier + " cleared", "Next: carrier " + (carrier + 1)]} />}
      {mode === "over" && <Banner title="GAME OVER" lines={["Score " + score + "    Best " + best, "Space or click to play again"]} />}
    </div>
  );
}
