// Shakki: the rules and the computer player.
//
// The board is 0x88: square = rank * 16 + file, rank 0 is White's first
// rank. Pieces are 1 pawn, 2 knight, 3 bishop, 4 rook, 5 queen, 6 king,
// positive for White and negative for Black. A move is one integer:
// from | to << 7 | promotion piece << 14 | flag << 17
// (flag 1 en passant, 2 castling, 4 pawn's double step).

export const PAWN = 1, KNIGHT = 2, BISHOP = 3, ROOK = 4, QUEEN = 5, KING = 6;
export const EP = 1, CASTLE = 2, DOUBLE = 4;
export const MATE = 100000;

const N_DIRS = [33, 31, 18, 14, -33, -31, -18, -14];
const K_DIRS = [1, -1, 16, -16, 17, 15, -17, -15];
const B_DIRS = [17, 15, -17, -15];
const R_DIRS = [1, -1, 16, -16];
const VALUE = [0, 100, 320, 330, 500, 900, 0];

// castling rights: 1 White short, 2 White long, 4 Black short, 8 Black long;
// a move from or to a square keeps only the rights in its mask
const CASTLE_MASK: number[] = [];
for (let i = 0; i < 128; i++) CASTLE_MASK.push(15);
CASTLE_MASK[4] = 15 - 3; CASTLE_MASK[0] = 15 - 2; CASTLE_MASK[7] = 15 - 1;
CASTLE_MASK[116] = 15 - 12; CASTLE_MASK[112] = 15 - 8; CASTLE_MASK[119] = 15 - 4;

export function from(m: number) { return m & 127; }
export function to(m: number) { return (m >> 7) & 127; }
export function promo(m: number) { return (m >> 14) & 7; }
export function flag(m: number) { return (m >> 17) & 7; }
function mv(f: number, t: number, p: number, fl: number) { return f | (t << 7) | (p << 14) | (fl << 17); }

export type Undo = { m: number; cap: number; castle: number; ep: number; half: number };

export type Game = {
  b: number[];
  side: number;      // 1 White, -1 Black to move
  castle: number;
  ep: number;        // the square a pawn passed over, or -1
  half: number;      // moves since a capture or a pawn move (50-move rule)
  wk: number;        // the kings' squares
  bk: number;
  hist: Undo[];
  keys: string[];    // position keys, for threefold repetition
  nodes: number;
};

const START = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1";
const LETTERS = "pnbrqk";

export function newGame(fen?: string): Game {
  const g: Game = { b: [], side: 1, castle: 0, ep: -1, half: 0, wk: 4, bk: 116, hist: [], keys: [], nodes: 0 };
  for (let i = 0; i < 128; i++) g.b.push(0);
  const parts = (fen || START).split(" ");
  let r = 7, f = 0;
  for (const c of parts[0]) {
    if (c === "/") { r--; f = 0; continue; }
    if (c >= "1" && c <= "8") { f += c.charCodeAt(0) - 48; continue; }
    const lower = c.toLowerCase();
    const p = LETTERS.indexOf(lower) + 1;
    const sq = r * 16 + f;
    g.b[sq] = c === lower ? -p : p;
    if (p === KING) { if (c === lower) g.bk = sq; else g.wk = sq; }
    f++;
  }
  g.side = parts[1] === "b" ? -1 : 1;
  const cs = parts[2] || "-";
  if (cs.indexOf("K") >= 0) g.castle |= 1;
  if (cs.indexOf("Q") >= 0) g.castle |= 2;
  if (cs.indexOf("k") >= 0) g.castle |= 4;
  if (cs.indexOf("q") >= 0) g.castle |= 8;
  g.ep = parts[3] && parts[3] !== "-" ? squareOf(parts[3]) : -1;
  g.half = parts[4] ? parseInt(parts[4], 10) : 0;
  g.keys.push(key(g));
  return g;
}

export function squareOf(name: string) { return (name.charCodeAt(1) - 49) * 16 + (name.charCodeAt(0) - 97); }
export function nameOf(sq: number) { return String.fromCharCode(97 + (sq & 7)) + String.fromCharCode(49 + (sq >> 4)); }

function key(g: Game) {
  let s = "";
  for (let sq = 0; sq < 128; sq++) { if (sq & 0x88) { sq += 7; continue; } s += String.fromCharCode(70 + g.b[sq]); }
  return s + g.side + "," + g.castle + "," + g.ep;
}

// Is `sq` attacked by the side `by` (1 or -1)?
export function attacked(g: Game, sq: number, by: number): boolean {
  const b = g.b;
  // pawns: a White pawn attacks up the board
  let s = sq - by * 15;
  if (!(s & 0x88) && b[s] === by * PAWN) return true;
  s = sq - by * 17;
  if (!(s & 0x88) && b[s] === by * PAWN) return true;
  for (let i = 0; i < 8; i++) {
    s = sq + N_DIRS[i];
    if (!(s & 0x88) && b[s] === by * KNIGHT) return true;
    s = sq + K_DIRS[i];
    if (!(s & 0x88) && b[s] === by * KING) return true;
  }
  for (let i = 0; i < 4; i++) {
    const d = B_DIRS[i];
    s = sq + d;
    while (!(s & 0x88)) {
      const p = b[s];
      if (p !== 0) { if (p === by * BISHOP || p === by * QUEEN) return true; break; }
      s += d;
    }
    const e = R_DIRS[i];
    s = sq + e;
    while (!(s & 0x88)) {
      const p = b[s];
      if (p !== 0) { if (p === by * ROOK || p === by * QUEEN) return true; break; }
      s += e;
    }
  }
  return false;
}

export function inCheck(g: Game, side: number) { return attacked(g, side > 0 ? g.wk : g.bk, -side); }

// Pseudo-legal moves of the side to move into `out` (captures only for the
// quiescence search).
function gen(g: Game, out: number[], capsOnly: boolean) {
  const b = g.b, us = g.side;
  for (let sq = 0; sq < 128; sq++) {
    if (sq & 0x88) { sq += 7; continue; }
    const p = b[sq] * us;
    if (p <= 0) continue;
    if (p === PAWN) {
      const fwd = sq + 16 * us;
      const rank = sq >> 4;
      const last = us > 0 ? 6 : 1;   // the rank before promotion
      if (!(fwd & 0x88) && b[fwd] === 0) {
        if (rank === last) {
          out.push(mv(sq, fwd, QUEEN, 0));
          if (!capsOnly) { out.push(mv(sq, fwd, KNIGHT, 0)); out.push(mv(sq, fwd, ROOK, 0)); out.push(mv(sq, fwd, BISHOP, 0)); }
        } else if (!capsOnly) {
          out.push(mv(sq, fwd, 0, 0));
          const two = fwd + 16 * us;
          if (rank === (us > 0 ? 1 : 6) && b[two] === 0) out.push(mv(sq, two, 0, DOUBLE));
        }
      }
      for (let k = 0; k < 2; k++) {
        const t = fwd + (k === 0 ? -1 : 1);
        if (t & 0x88) continue;
        if (b[t] * us < 0) {
          if (rank === last) {
            out.push(mv(sq, t, QUEEN, 0));
            if (!capsOnly) { out.push(mv(sq, t, KNIGHT, 0)); out.push(mv(sq, t, ROOK, 0)); out.push(mv(sq, t, BISHOP, 0)); }
          } else out.push(mv(sq, t, 0, 0));
        } else if (t === g.ep) out.push(mv(sq, t, 0, EP));
      }
    } else if (p === KNIGHT || p === KING) {
      const dirs = p === KNIGHT ? N_DIRS : K_DIRS;
      for (let i = 0; i < 8; i++) {
        const t = sq + dirs[i];
        if (t & 0x88) continue;
        const q = b[t] * us;
        if (q > 0) continue;
        if (q < 0 || !capsOnly) out.push(mv(sq, t, 0, 0));
      }
      if (p === KING && !capsOnly) castles(g, sq, out);
    } else {
      const dirs = p === BISHOP ? B_DIRS : p === ROOK ? R_DIRS : K_DIRS;
      const n = dirs.length;
      for (let i = 0; i < n; i++) {
        const d = dirs[i];
        let t = sq + d;
        while (!(t & 0x88)) {
          const q = b[t] * us;
          if (q > 0) break;
          if (q < 0) { out.push(mv(sq, t, 0, 0)); break; }
          if (!capsOnly) out.push(mv(sq, t, 0, 0));
          t += d;
        }
      }
    }
  }
}

function castles(g: Game, sq: number, out: number[]) {
  const us = g.side, b = g.b;
  const home = us > 0 ? 4 : 116;
  if (sq !== home) return;
  const short = us > 0 ? 1 : 4, long = us > 0 ? 2 : 8;
  if ((g.castle & short) && b[home + 1] === 0 && b[home + 2] === 0 && b[home + 3] === us * ROOK &&
      !attacked(g, home, -us) && !attacked(g, home + 1, -us) && !attacked(g, home + 2, -us))
    out.push(mv(home, home + 2, 0, CASTLE));
  if ((g.castle & long) && b[home - 1] === 0 && b[home - 2] === 0 && b[home - 3] === 0 && b[home - 4] === us * ROOK &&
      !attacked(g, home, -us) && !attacked(g, home - 1, -us) && !attacked(g, home - 2, -us))
    out.push(mv(home, home - 2, 0, CASTLE));
}

// Plays a move without checking it; answers false (and takes it back) when
// it leaves the mover's own king in check.
export function make(g: Game, m: number): boolean {
  const b = g.b, us = g.side;
  const f = m & 127, t = (m >> 7) & 127, pr = (m >> 14) & 7, fl = (m >> 17) & 7;
  const piece = b[f];
  let cap = b[t];
  g.hist.push({ m, cap, castle: g.castle, ep: g.ep, half: g.half });
  b[t] = pr ? pr * us : piece;
  b[f] = 0;
  if (fl === EP) { const c = t - 16 * us; cap = b[c]; b[c] = 0; g.hist[g.hist.length - 1].cap = cap; }
  else if (fl === CASTLE) {
    if (t > f) { b[f + 1] = b[f + 3]; b[f + 3] = 0; } else { b[f - 1] = b[f - 4]; b[f - 4] = 0; }
  }
  if (piece === KING) g.wk = t; else if (piece === -KING) g.bk = t;
  g.castle &= CASTLE_MASK[f] & CASTLE_MASK[t];
  g.ep = fl === DOUBLE ? f + 16 * us : -1;
  g.half = cap !== 0 || piece * us === PAWN ? 0 : g.half + 1;
  g.side = -us;
  g.nodes++;
  if (attacked(g, us > 0 ? g.wk : g.bk, -us)) { unmake(g); return false; }
  return true;
}

export function unmake(g: Game) {
  const u = g.hist.pop() as Undo;
  const b = g.b;
  const m = u.m;
  const f = m & 127, t = (m >> 7) & 127, pr = (m >> 14) & 7, fl = (m >> 17) & 7;
  g.side = -g.side;
  const us = g.side;
  const piece = pr ? PAWN * us : b[t];
  b[f] = piece;
  if (fl === EP) { b[t] = 0; b[t - 16 * us] = u.cap; }
  else b[t] = u.cap;
  if (fl === CASTLE) {
    if (t > f) { b[f + 3] = b[f + 1]; b[f + 1] = 0; } else { b[f - 4] = b[f - 1]; b[f - 1] = 0; }
  }
  if (piece === KING) g.wk = f; else if (piece === -KING) g.bk = f;
  g.castle = u.castle; g.ep = u.ep; g.half = u.half;
}

export function legalMoves(g: Game): number[] {
  const all: number[] = [];
  gen(g, all, false);
  const out: number[] = [];
  for (const m of all) if (make(g, m)) { unmake(g); out.push(m); }
  return out;
}

// --- the game as the players see it ---------------------------------------

export type Status = "" | "check" | "mate" | "stalemate" | "fifty" | "repetition" | "material";

// Plays a legal move in the real game (keeps the repetition keys).
export function play(g: Game, m: number) {
  make(g, m);
  g.keys.push(key(g));
}

export function takeBack(g: Game) {
  if (!g.hist.length) return;
  unmake(g);
  g.keys.pop();
}

export function status(g: Game): Status {
  const moves = legalMoves(g);
  const check = inCheck(g, g.side);
  if (!moves.length) return check ? "mate" : "stalemate";
  if (insufficient(g)) return "material";
  if (g.half >= 100) return "fifty";
  const k = g.keys[g.keys.length - 1];
  let n = 0;
  for (const x of g.keys) if (x === k) n++;
  if (n >= 3) return "repetition";
  return check ? "check" : "";
}

// Neither side can mate: kings alone, a king and one minor piece against a
// king, or bishops all on squares of one colour.
function insufficient(g: Game) {
  let minors = 0, bishopColours = 0, knights = 0;
  for (let sq = 0; sq < 128; sq++) {
    if (sq & 0x88) { sq += 7; continue; }
    const p = Math.abs(g.b[sq]);
    if (p === 0 || p === KING) continue;
    if (p === PAWN || p === ROOK || p === QUEEN) return false;
    minors++;
    if (p === KNIGHT) knights++;
    else bishopColours |= ((sq >> 4) + (sq & 7)) % 2 === 0 ? 1 : 2;
  }
  if (minors <= 1) return true;
  return knights === 0 && bishopColours !== 3;
}

// --- the computer player ----------------------------------------------------

// piece-square bonuses, made from simple rules (centre, advance, king safety)
const PST: number[][] = [[], [], [], [], [], [], [], []];
const KING_END: number[] = [];
for (let sq = 0; sq < 128; sq++) {
  const f = sq & 7, r = sq >> 4;
  const centre = 7 - (Math.abs(2 * f - 7) + Math.abs(2 * r - 7)) / 2;   // 0 … 6
  const fileC = 3.5 - Math.abs(f - 3.5);
  PST[PAWN].push(r * 6 + (r >= 2 && r <= 4 ? fileC * 5 : 0) + (r === 1 && (f === 3 || f === 4) ? -15 : 0));
  PST[KNIGHT].push(centre * 7 - 20);
  PST[BISHOP].push(centre * 4 - 8 + (r === 0 ? -8 : 0));
  PST[ROOK].push((r === 6 ? 18 : 0) + (f === 3 || f === 4 ? 5 : 0));
  PST[QUEEN].push(centre * 2 - 5);
  PST[KING].push(r === 0 ? (f === 6 || f === 1 || f === 2 ? 25 : f === 4 ? 5 : 0) : -15 * r);
  KING_END.push(centre * 8 - 20);
}

export function evaluate(g: Game): number {
  const b = g.b;
  let score = 0, heavy = 0;
  for (let sq = 0; sq < 128; sq++) {
    if (sq & 0x88) { sq += 7; continue; }
    const p = b[sq];
    if (p === 0) continue;
    if (p > 0) {
      score += VALUE[p] + (p === KING ? 0 : PST[p][sq]);
      if (p !== PAWN && p !== KING) heavy += VALUE[p];
    } else {
      const q = -p, mir = sq ^ 0x70;   // the same square seen from Black's side
      score -= VALUE[q] + (q === KING ? 0 : PST[q][mir]);
      if (q !== PAWN && q !== KING) heavy += VALUE[q];
    }
  }
  // kings hide early and come out in the endgame
  const end = heavy < 1500;
  score += end ? KING_END[g.wk] : PST[KING][g.wk];
  score -= end ? KING_END[g.bk ^ 0x70] : PST[KING][g.bk ^ 0x70];
  return score * g.side;
}

// captures first, the biggest victim by the smallest attacker
function order(g: Game, moves: number[], best: number) {
  const b = g.b;
  const sc: number[] = [];
  for (const m of moves) {
    let s = 0;
    if (m === best) s = 100000;
    else {
      const v = b[(m >> 7) & 127];
      if (v !== 0) s = 1000 + VALUE[Math.abs(v)] * 10 - VALUE[Math.abs(b[m & 127])] / 10;
      else if ((m >> 17) & 7 & EP) s = 1000;
      if ((m >> 14) & 7) s += 900;
    }
    sc.push(s);
  }
  // insertion sort: the lists are short
  for (let i = 1; i < moves.length; i++) {
    const m = moves[i], s = sc[i];
    let j = i - 1;
    while (j >= 0 && sc[j] < s) { moves[j + 1] = moves[j]; sc[j + 1] = sc[j]; j--; }
    moves[j + 1] = m; sc[j + 1] = s;
  }
}

function quiesce(g: Game, alpha: number, beta: number, qd: number): number {
  const stand = evaluate(g);
  if (stand >= beta) return stand;
  if (stand > alpha) alpha = stand;
  if (qd <= 0) return stand;
  const moves: number[] = [];
  gen(g, moves, true);
  order(g, moves, 0);
  for (const m of moves) {
    if (!make(g, m)) continue;
    const s = -quiesce(g, -beta, -alpha, qd - 1);
    unmake(g);
    if (s >= beta) return s;
    if (s > alpha) alpha = s;
  }
  return alpha;
}

type Search = { stop: boolean; limit: number; qdepth: number };

function negamax(g: Game, depth: number, alpha: number, beta: number, ply: number, sr: Search): number {
  if (g.nodes > sr.limit) { sr.stop = true; return 0; }
  if (depth <= 0) return quiesce(g, alpha, beta, sr.qdepth);
  if (g.half >= 100) return 0;
  const moves: number[] = [];
  gen(g, moves, false);
  order(g, moves, 0);
  let any = false, best = -MATE * 2;
  for (const m of moves) {
    if (!make(g, m)) continue;
    any = true;
    const s = -negamax(g, depth - 1, -beta, -alpha, ply + 1, sr);
    unmake(g);
    if (sr.stop) return 0;
    if (s > best) best = s;
    if (s > alpha) alpha = s;
    if (alpha >= beta) break;
  }
  if (!any) return inCheck(g, g.side) ? -MATE + ply : 0;
  return best;
}

export type Level = 1 | 2 | 3;

// One root move scored to `depth`; scores above `floor` are exact.
function scoreOne(g: Game, m: number, depth: number, floor: number, sr: Search): number {
  make(g, m);
  const s = -negamax(g, depth - 1, -MATE * 2, -floor, 1, sr);
  unmake(g);
  return s;
}

// The computer thinks a little on every frame, so an old phone keeps
// drawing: startThink, then stepThink each frame until it answers true and
// `result` holds the move. Levels: 1 easy, 2 medium, 3 hard. `budget` is
// the most nodes the search may visit.
export type Thinker = {
  level: Level; depth: number; maxDepth: number; qd: number; margin: number;
  moves: number[]; idx: number; alpha: number; scores: { m: number; s: number }[];
  last: { m: number; s: number }[]; limit: number; result: number; done: boolean;
};

export function startThink(g: Game, level: Level, budget: number): Thinker {
  const moves = legalMoves(g);
  order(g, moves, 0);
  const th: Thinker = {
    level, depth: 1, maxDepth: level === 1 ? 1 : level === 2 ? 2 : 5, qd: level === 1 ? 0 : level === 2 ? 3 : 6,
    margin: level === 1 ? MATE * 4 : level === 2 ? 60 : 8,
    moves, idx: 0, alpha: -MATE * 2, scores: [], last: [], limit: g.nodes + budget, result: moves[0] || 0, done: false,
  };
  if (moves.length <= 1) th.done = true;
  // easy looks only one move ahead, but a mate in one it always sees
  if (level === 1) {
    for (const m of moves) {
      make(g, m);
      const mate = inCheck(g, g.side) && legalMoves(g).length === 0;
      unmake(g);
      if (mate) { th.result = m; th.done = true; break; }
    }
  }
  return th;
}

export function stepThink(g: Game, th: Thinker, rand: () => number, ms: number): boolean {
  if (th.done) return true;
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    if (th.idx < th.moves.length) {
      const sr: Search = { stop: false, limit: th.limit, qdepth: th.qd };
      const m = th.moves[th.idx];
      const s = scoreOne(g, m, th.depth, th.alpha - th.margin - 1, sr);
      if (sr.stop) { finish(th, th.last.length ? th.last : th.scores, rand); return true; }
      th.scores.push({ m, s });
      if (s > th.alpha) th.alpha = s;
      th.idx++;
      continue;
    }
    // one depth done: the best first for the next, deeper round
    const sc = th.scores.slice();
    sc.sort((a, b) => b.s - a.s);
    th.last = sc;
    if (th.depth >= th.maxDepth || sc[0].s >= MATE - 100 || g.nodes >= th.limit) { finish(th, sc, rand); return true; }
    th.depth++;
    th.moves = sc.map((x) => x.m);
    th.idx = 0; th.alpha = -MATE * 2; th.scores = [];
  }
  return false;
}

function finish(th: Thinker, scores: { m: number; s: number }[], rand: () => number) {
  th.done = true;
  if (!scores.length) return;
  const sc = scores.slice();
  sc.sort((a, b) => b.s - a.s);
  const bestS = sc[0].s;
  th.result = sc[0].m;
  if (bestS >= MATE - 100) return;   // a mate is always taken
  if (th.level === 1) {
    // easy: a random move now and then, otherwise one step ahead with a
    // lot of noise (it still takes a free queen most of the time)
    if (rand() < 0.3) { th.result = th.moves[Math.floor(rand() * th.moves.length)]; return; }
    let bs = -1e9;
    for (const x of sc) { const s = x.s + (rand() - 0.5) * 260; if (s > bs) { bs = s; th.result = x.m; } }
    return;
  }
  // medium and hard: a random one of the moves (nearly) as good as the best,
  // so the games differ; medium has more noise
  const noise = th.level === 2 ? 50 : 0;
  let bs = -1e9;
  for (const x of sc) {
    if (x.s < bestS - th.margin) break;
    const s = x.s + (noise ? (rand() - 0.5) * noise : rand() * 4);
    if (s > bs) { bs = s; th.result = x.m; }
  }
}

// The whole think at once (tests).
export function think(g: Game, level: Level, rand: () => number, budget: number): number {
  const th = startThink(g, level, budget);
  while (!stepThink(g, th, rand, 1e9)) { /* thinks */ }
  return th.result;
}

// --- notation for the move list --------------------------------------------

const SAN_LETTER = ["", "", "N", "B", "R", "Q", "K"];

// The move in short algebraic notation (before it is played).
export function san(g: Game, m: number, letters?: string[]): string {
  const L = letters || SAN_LETTER;
  const f = from(m), t = to(m), p = Math.abs(g.b[f]);
  let s = "";
  if (flag(m) === CASTLE) s = t > f ? "O-O" : "O-O-O";
  else {
    const capture = g.b[t] !== 0 || flag(m) === EP;
    if (p === PAWN) s = (capture ? nameOf(f)[0] + "x" : "") + nameOf(t) + (promo(m) ? "=" + L[promo(m)] : "");
    else {
      // tell apart two pieces of a kind that can go to the same square
      let sameFile = false, sameRank = false, other = false;
      for (const o of legalMoves(g)) {
        if (o !== m && to(o) === t && Math.abs(g.b[from(o)]) === p) {
          other = true;
          if ((from(o) & 7) === (f & 7)) sameFile = true;
          if ((from(o) >> 4) === (f >> 4)) sameRank = true;
        }
      }
      let dis = "";
      if (other) dis = !sameFile ? nameOf(f)[0] : !sameRank ? nameOf(f)[1] : nameOf(f);
      s = L[p] + dis + (capture ? "x" : "") + nameOf(t);
    }
  }
  make(g, m);
  const check = inCheck(g, g.side);
  const mate = check && legalMoves(g).length === 0;
  unmake(g);
  return s + (mate ? "#" : check ? "+" : "");
}

// how many moves a position allows to `depth` (rule tests)
export function perft(g: Game, depth: number): number {
  if (depth === 0) return 1;
  const all: number[] = [];
  gen(g, all, false);
  let n = 0;
  for (const m of all) {
    if (!make(g, m)) continue;
    n += perft(g, depth - 1);
    unmake(g);
  }
  return n;
}
