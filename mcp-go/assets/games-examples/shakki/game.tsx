// Shakki: chess with all the rules (castling, en passant, promotion, check,
// mate, stalemate and draws). Two players on one phone, or against the
// phone on three levels. Tapping a piece shows where it can go and says
// its name; the phone thinks a little on every frame (lib/chess.ts), so an
// old phone keeps drawing while it thinks. A game in progress is saved and
// can be continued.

import {
  Game, Level, Status, Thinker, newGame, legalMoves, status, play, takeBack, inCheck,
  startThink, stepThink, think, from, to, promo, flag, EP, CASTLE, PAWN, KNIGHT, BISHOP, ROOK, QUEEN, KING,
} from "lib/chess";

type Screen = "menu" | "level" | "play";

const SQ = 42;                       // a square in pixels
const BOARD = SQ * 8;
const NAMES = ["", "sk_sotilas", "sk_ratsu", "sk_lahetti", "sk_torni", "sk_kuningatar", "sk_kuningas"];
const LETTER = ["", "p", "n", "b", "r", "q", "k"];
const BUDGET = [0, 400, 4000, 25000]; // nodes the phone may look at per move

let screen: Screen = "menu";
let vsPhone = false;
let level: Level = rg.load("level", 1);
let humanSide = rg.load("side", 1);  // 1 White, -1 Black (against the phone)
let g: Game = newGame();
let legal: number[] = [];
let st: Status = "";
let sel = -1;                        // the selected square
let targets: number[] = [];          // where the selected piece can go
let promoMoves: number[] = [];       // a pawn reached the end: pick a piece
let hint = 0;                        // a hint move, shown blinking
let anim: { piece: number; f: number; t: number; k: number } | null = null;
let thinker: Thinker | null = null;
let thinkWait = 0;
let over = false;
let overClock = 0;
let saidPiece = 0;              // the piece kinds named this game (bits): each is named once
let t = 0;

rg.seed(Math.floor(Date.now() % 100000) + 1);

function img(p: number) { return rg.asset("img/" + (p > 0 ? "w" : "b") + LETTER[Math.abs(p)] + ".png"); }
function flipped() { return vsPhone && humanSide < 0; }
function phoneTurn() { return vsPhone && g.side !== humanSide; }
function lastMove() { return g.hist.length ? g.hist[g.hist.length - 1].m : 0; }

// --- saving -------------------------------------------------------------------

function moveList(): number[] { return g.hist.map((u) => u.m); }

function saveGame() {
  rg.save("game", over ? null : { vsPhone, level, humanSide, moves: moveList() });
}

function savedGame(): { vsPhone: boolean; level: Level; humanSide: number; moves: number[] } | null {
  const s = rg.load("game", null);
  return s && s.moves && s.moves.length ? s : null;
}

// --- the game -------------------------------------------------------------------

function refresh() {
  legal = legalMoves(g);
  st = status(g);
  over = st === "mate" || st === "stalemate" || st === "fifty" || st === "repetition" || st === "material";
  sel = -1; targets = []; promoMoves = []; hint = 0;
}

function startGame(phone: boolean, moves?: number[]) {
  vsPhone = phone;
  g = newGame();
  if (moves) for (const m of moves) play(g, m);
  refresh();
  screen = "play";
  anim = null; thinker = null; thinkWait = 0; overClock = 0; saidPiece = 0;
  rg.sound("click");
  if (moves && moves.length) return;
  if (vsPhone) say(humanSide > 0 ? "sk_sinun_vuorosi" : "sk_puhelin_aloittaa");
  else say("sk_valkoisen_vuoro");
  saveGame();
}

function doMove(m: number) {
  const f = from(m), tt = to(m);
  const piece = g.b[f];
  const capture = g.b[tt] !== 0 || flag(m) === EP;
  const mover = g.side;
  play(g, m);
  anim = { piece: promo(m) ? promo(m) * mover : piece, f, t: tt, k: 0 };
  refresh();
  saveGame();
  rg.sound("pop", { pitch: capture ? 0.75 : mover > 0 ? 1 : 1.2 });
  rg.vibrate(capture ? 30 : 12);
  if (over) {
    overClock = 0;
    if (st === "mate") {
      const winner = mover;
      if (vsPhone && winner !== humanSide) { rg.sound("lose"); say("sk_matti_puhelin"); }
      else {
        rg.celebrate(width / 2, height * 0.45, { big: true });
        say(vsPhone ? "sk_matti_voitit" : winner > 0 ? "sk_matti_valkoinen" : "sk_matti_musta");
      }
      rg.vibrate([60, 40, 160]);
    } else {
      rg.sound("wrong");
      say(st === "stalemate" ? "sk_patti" : st === "repetition" ? "sk_toisto" : "sk_tasapeli");
    }
    return;
  }
  if (st === "check") { say("sk_shakki"); rg.vibrate([40, 30, 40]); }
  else if (flag(m) === CASTLE) say("sk_linnoitus");
  else if (flag(m) === EP) say("sk_ohesta");
}

function tap(sq: number) {
  if (screen !== "play" || over || anim || phoneTurn() || promoMoves.length) return;
  if (sel >= 0 && targets.indexOf(sq) >= 0) {
    const ms = legal.filter((m) => from(m) === sel && to(m) === sq);
    if (ms.length > 1) { promoMoves = ms; say("sk_korotus"); rg.sound("star"); return; }
    doMove(ms[0]);
    return;
  }
  const p = g.b[sq];
  if (p * g.side > 0 && sq !== sel) {
    sel = sq;
    targets = legal.filter((m) => from(m) === sq).map((m) => to(m));
    hint = 0;
    if (!targets.length && inCheck(g, g.side)) { rg.sound("wrong"); say("sk_kuningas_vaarassa"); return; }
    rg.sound("click");
    const kind = Math.abs(p);
    if (!(saidPiece & (1 << kind))) { saidPiece |= 1 << kind; say(NAMES[kind]); }
    return;
  }
  if (sel >= 0) rg.sound("tick");
  sel = -1; targets = [];
}

function choosePromo(kind: number) {
  const m = promoMoves.filter((x) => promo(x) === kind)[0];
  promoMoves = [];
  if (m) { rg.sound("star"); doMove(m); }
}

function undo() {
  if (!g.hist.length) return;
  anim = null; thinker = null; thinkWait = 0;
  takeBack(g);
  // against the phone back to the player's own turn
  while (vsPhone && g.hist.length && g.side !== humanSide) takeBack(g);
  refresh();
  saveGame();
  rg.sound("whoosh");
}

function showHint() {
  if (over || phoneTurn() || anim) return;
  hint = think(g, 2, rg.random, 3000);
  sel = from(hint);
  targets = legal.filter((m) => from(m) === sel).map((m) => to(m));
  rg.sound("star");
}

function tick(dt: number) {
  t += dt;
  if (anim) { anim.k += dt / 0.2; if (anim.k >= 1) anim = null; }
  if (over) overClock += dt;
  if (screen === "play" && !over && !anim && phoneTurn()) {
    if (!thinker) {
      thinkWait += dt;
      if (thinkWait > 0.35) thinker = startThink(g, level, BUDGET[level]);
    } else if (stepThink(g, thinker, rg.random, 18)) {
      const m = thinker.result;
      thinker = null; thinkWait = 0;
      doMove(m);
    }
  }
}

function onBack() {
  if (screen === "play") { screen = "menu"; rg.sound("click"); }
  else if (screen === "level") { screen = "menu"; rg.sound("click"); }
}

// Spoken words go here: a game of its own can make voice clips with speak
// (sounds/<id>.wav) and play them with the engine's say. This example is silent.
function say(_id: string) {}

// --- drawing ---------------------------------------------------------------------

function squareAt(row: number, col: number) {
  return flipped() ? row * 16 + (7 - col) : (7 - row) * 16 + col;
}
function rowCol(sq: number) {
  const r = sq >> 4, f = sq & 7;
  return flipped() ? [r, 7 - f] : [7 - r, f];
}

function Board() {
  const lm = lastMove();
  const kingSq = st === "check" || st === "mate" ? (g.side > 0 ? g.wk : g.bk) : -1;
  const blink = Math.floor(t * 3) % 2 === 0;
  const cells = [];
  for (let row = 0; row < 8; row++) {
    for (let col = 0; col < 8; col++) {
      const sq = squareAt(row, col);
      const light = (row + col) % 2 === 0;
      let cls = light ? "sq light" : "sq dark";
      if (lm && (sq === from(lm) || sq === to(lm))) cls += light ? " lastl" : " lastd";
      if (sq === sel) cls += " sel";
      if (hint && blink && (sq === from(hint) || sq === to(hint))) cls += " hint";
      const kids = [];
      if (sq === kingSq) kids.push(<div className={st === "mate" ? "danger mate" : "danger"} />);
      const p = g.b[sq];
      const moving = anim && anim.t === sq;
      if (p && !moving) kids.push(<img className="piece" src={img(p)} />);
      if (targets.indexOf(sq) >= 0) kids.push(p ? <div className="ring" /> : <div className="dot" />);
      if (col === 0) kids.push(<div className={light ? "coord rank cd" : "coord rank cl"}>{String((sq >> 4) + 1)}</div>);
      if (row === 7) kids.push(<div className={light ? "coord file cd" : "coord file cl"}>{"abcdefgh"[sq & 7]}</div>);
      cells.push(<div className={cls} id={"s" + sq} style={{ left: col * SQ, top: row * SQ }} onClick={() => tap(sq)}>{kids}</div>);
    }
  }
  if (anim) {
    const [r0, c0] = rowCol(anim.f), [r1, c1] = rowCol(anim.t);
    const k = rg.ease.inOut(Math.min(1, anim.k));
    const lift = Math.sin(k * Math.PI) * 6;
    cells.push(<img className="piece flying" src={img(anim.piece)}
      style={{ left: rg.lerp(c0, c1, k) * SQ, top: rg.lerp(r0, r1, k) * SQ - lift }} />);
  }
  return <div className="board">{cells}</div>;
}

// the pieces a side has taken, small
function Taken(side: number) {
  const out = [];
  for (const u of g.hist) if (u.cap && u.cap * side < 0) out.push(u.cap);
  out.sort((a, b) => Math.abs(b) - Math.abs(a));
  return <div className="taken">{out.map((p) => <img className="mini" src={img(p)} />)}</div>;
}

function who(side: number) {
  if (vsPhone) {
    if (side === humanSide) return rg.t("Sinä", "You", "Tú");
    return rg.t("Puhelin", "Phone", "Teléfono") + " " + ["", "🙂", "😐", "😎"][level];
  }
  return side > 0 ? rg.t("Valkoinen", "White", "Blancas") : rg.t("Musta", "Black", "Negras");
}

function Player(side: number, top: boolean) {
  const active = !over && g.side === side;
  return (
    <div className={(top ? "player top" : "player") + (active ? " active" : "")}>
      <img className="badge" src={img(side * KING)} />
      <div className={active ? "pname on" : "pname"}>{who(side)}</div>
      {Taken(side)}
    </div>
  );
}

function statusText() {
  if (st === "mate") {
    const winner = -g.side;
    if (vsPhone) return winner === humanSide ? rg.t("Shakkimatti! Voitit! 🎉", "Checkmate! You won! 🎉", "¡Jaque mate! ¡Ganaste! 🎉") : rg.t("Shakkimatti. Puhelin voitti.", "Checkmate. The phone won.", "Jaque mate. Ganó el teléfono.");
    return rg.t("Shakkimatti! ", "Checkmate! ", "¡Jaque mate! ") + who(winner) + rg.t(" voitti! 🎉", " wins! 🎉", " gana! 🎉");
  }
  if (st === "stalemate") return rg.t("Patti: tasapeli", "Stalemate: a draw", "Ahogado: tablas");
  if (st === "repetition") return rg.t("Sama asema 3 kertaa: tasapeli", "Same position 3 times: draw", "Misma posición 3 veces: tablas");
  if (st === "fifty") return rg.t("50 siirtoa ilman lyöntiä: tasapeli", "50 moves without a capture: draw", "50 jugadas sin capturas: tablas");
  if (st === "material") return rg.t("Kumpikaan ei voi voittaa: tasapeli", "Nobody can win: draw", "Nadie puede ganar: tablas");
  if (phoneTurn()) return rg.t("Puhelin miettii", "The phone is thinking", "El teléfono piensa") + ".".repeat(1 + (Math.floor(t * 3) % 3));
  const check = st === "check" ? rg.t("Shakki! ", "Check! ", "¡Jaque! ") : "";
  if (vsPhone) return check + rg.t("Sinun vuorosi", "Your turn", "Te toca");
  return check + (g.side > 0 ? rg.t("Valkoisen vuoro", "White's turn", "Turno de las blancas") : rg.t("Mustan vuoro", "Black's turn", "Turno de las negras"));
}

function Promotion() {
  const side = g.side;
  return (
    <div className="veil">
      <div className="panel">
        <div className="ptitle">{rg.t("Mikä nappula tulee tilalle?", "Which piece will it become?", "¿En qué pieza se convierte?")}</div>
        <div className="prow">
          {[QUEEN, ROOK, BISHOP, KNIGHT].map((k) => (
            <div className="pick" id={"promo" + k} onClick={() => choosePromo(k)}><img className="pickimg" src={img(k * side)} /></div>
          ))}
        </div>
      </div>
    </div>
  );
}

function GameOver() {
  const win = st === "mate" && (!vsPhone || -g.side === humanSide);
  return (
    <div className="veil">
      <div className="panel">
        <div className="bigicon">{st === "mate" ? (win ? "🏆" : "🤖") : "🤝"}</div>
        <div className="ptitle">{statusText()}</div>
        <div className="btn again" id="again" onClick={() => startGame(vsPhone)}>{rg.t("Uusi peli", "New game", "Nueva partida")}</div>
        <div className="btn alt small" id="look" onClick={() => { overClock = -1e9; rg.sound("click"); }}>{rg.t("Katso lautaa", "Look at the board", "Ver el tablero")}</div>
      </div>
    </div>
  );
}

function Play() {
  const top = flipped() ? 1 : -1;
  const showOver = over && overClock > 1.2;
  const statusCls = st === "check" ? "status check" : over ? "status done" : "status";
  return (
    <div className="screen">
      {Player(top, true)}
      <div className="frame">{Board()}</div>
      {Player(-top, false)}
      <div className={statusCls}>{statusText()}</div>
      <div className="tools">
        <div className="tool" id="menu" onClick={() => { screen = "menu"; rg.sound("click"); }}>{rg.t("← Valikko", "← Menu", "← Menú")}</div>
        <div className={g.hist.length ? "tool" : "tool off"} id="undo" onClick={() => undo()}>{rg.t("↶ Peru", "↶ Undo", "↶ Deshacer")}</div>
        <div className={over || phoneTurn() ? "tool off" : "tool"} id="hint" onClick={() => showHint()}>{rg.t("💡 Vihje", "💡 Hint", "💡 Pista")}</div>
      </div>
      {over && overClock < 0 ? <div className="tool newgame" id="new" onClick={() => startGame(vsPhone)}>{rg.t("Uusi peli", "New game", "Nueva partida")}</div> : null}
      {promoMoves.length ? Promotion() : null}
      {showOver ? GameOver() : null}
    </div>
  );
}

function Menu() {
  const bob = Math.sin(t * 2.5) * 5;
  const saved = savedGame();
  return (
    <div className="screen menu">
      <div className="title">{rg.t("Shakki", "Chess", "Ajedrez")}</div>
      <div className="hero" style={{ marginTop: bob }}>
        <img className="heroimg" src={img(KNIGHT)} />
        <img className="heroimg big" src={img(KING)} />
        <img className="heroimg" src={img(-QUEEN)} />
      </div>
      {saved ? <div className="btn cont" id="continue" onClick={() => { level = saved.level; humanSide = saved.humanSide; startGame(saved.vsPhone, saved.moves); }}>▶ {rg.t("Jatka peliä", "Continue", "Continuar")}</div> : null}
      <div className="btn" id="vsfriend" onClick={() => { humanSide = 1; startGame(false); }}>👫 {rg.t("Kaksi pelaajaa", "Two players", "Dos jugadores")}</div>
      <div className="btn alt" id="vsphone" onClick={() => { screen = "level"; rg.sound("click"); }}>📱 {rg.t("Puhelinta vastaan", "Against the phone", "Contra el teléfono")}</div>
    </div>
  );
}

function LevelMenu() {
  const lv = (n: Level, icon: string, name: string, cls: string) => (
    <div className={"btn " + cls} id={"level" + n} onClick={() => { level = n; rg.save("level", n); startGame(true); }}>{icon + " " + name}</div>
  );
  return (
    <div className="screen menu">
      <div className="subtitle">{rg.t("Kuinka vaikea?", "How hard?", "¿Qué tan difícil?")}</div>
      {lv(1, "🙂", rg.t("Helppo", "Easy", "Fácil"), "easy")}
      {lv(2, "😐", rg.t("Keskitaso", "Medium", "Medio"), "medium")}
      {lv(3, "😎", rg.t("Vaikea", "Hard", "Difícil"), "hard")}
      <div className="sidepick">
        <div className="sidelabel">{rg.t("Sinun nappulasi:", "Your pieces:", "Tus piezas:")}</div>
        <div className={humanSide > 0 ? "side on" : "side"} id="white" onClick={() => { humanSide = 1; rg.save("side", 1); rg.sound("click"); }}><img className="sideimg" src={img(PAWN)} /></div>
        <div className={humanSide < 0 ? "side on" : "side"} id="black" onClick={() => { humanSide = -1; rg.save("side", -1); rg.sound("click"); }}><img className="sideimg" src={img(-PAWN)} /></div>
      </div>
      <div className="btn back" id="back" onClick={() => { screen = "menu"; rg.sound("click"); }}>{rg.t("← Takaisin", "← Back", "← Atrás")}</div>
    </div>
  );
}

function view() {
  if (screen === "menu") return Menu();
  if (screen === "level") return LevelMenu();
  return Play();
}
