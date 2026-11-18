// Muistipeli: the cards lie face down; turn two, and if they show the same
// animal you keep them and go again. Alone (fewest turns), with a friend or
// against the phone, which remembers some of the cards it has seen.

type Screen = "menu" | "game";
type C = { k: number; open: boolean; done: number; flip: number };   // done: 0, or the player who found it

const ANIMALS = ["🐶", "🐱", "🐰", "🦊", "🐻", "🐼", "🐸", "🐵", "🦁", "🐮", "🐷", "🐔", "🦉", "🐢", "🐙", "🦋"];
const SIZES = [12, 16, 20];

let screen: Screen = "menu";
let mode = 0;          // 0 alone, 1 friend, 2 phone
let size = 1;          // index into SIZES
let cards: C[] = [];
let picked: number[] = [];
let turn = 1;
let score = [0, 0, 0];
let tries = 0;
let t = 0;
let waitClock = 0;     // two cards open: how long they have been showing
let phoneClock = 0;
let memory: number[] = [];   // what the phone remembers: card index -> animal, -1 not known
let over = false;
let overClock = 0;
let best: number[] = [0, 0, 0];

function init() { size = rg.load("size", 1); best = rg.load("best", [0, 0, 0]); }

function cols() { return 4; }
function rows() { return SIZES[size] / 4; }
function cw() { return 78; }
function ch() { return rows() === 5 ? 86 : 96; }
function gx(i: number) { return Math.floor((width - (cols() * cw() + (cols() - 1) * 8)) / 2) + (i % cols()) * (cw() + 8); }
function gy(i: number) { return (mode === 0 ? 110 : 142) + Math.floor(i / cols()) * (ch() + 8); }

function who(p: number) {
  if (mode === 2) return p === 1 ? rg.t("Sinä", "You", "Tú") : rg.t("Puhelin", "Phone", "Teléfono");
  return p === 1 ? rg.t("Pelaaja 1", "Player 1", "Jugador 1") : rg.t("Pelaaja 2", "Player 2", "Jugador 2");
}
function phoneTurn() { return mode === 2 && turn === 2 && !over; }

function newGame(m: number) {
  mode = m;
  const n = SIZES[size] / 2;
  const pool = rg.shuffle(ANIMALS.slice()).slice(0, n);
  const ks: number[] = [];
  for (let i = 0; i < n; i++) { ks.push(i); ks.push(i); }
  const order = rg.shuffle(ks);
  cards = order.map((k: number) => ({ k: ANIMALS.indexOf(pool[k]), open: false, done: 0, flip: 1 }));
  memory = cards.map(() => -1);
  picked = []; turn = 1; score = [0, 0, 0]; tries = 0; over = false; waitClock = 0; phoneClock = 0;
  screen = "game";
  rg.sound("whoosh");
}

function turnCard(i: number) {
  const c = cards[i];
  if (c.open || c.done || picked.length >= 2) return;
  c.open = true; c.flip = 0;
  picked.push(i);
  rg.sound("tick", { pitch: 1.2 });
  // the phone sees every card; it remembers some
  for (const j of picked) if (rg.random() < 0.6) memory[j] = cards[j].k;
  if (picked.length === 2) waitClock = 0;
}

function tap(i: number) {
  if (phoneTurn() || over) return;
  turnCard(i);
}

function resolve() {
  const a = cards[picked[0]], b = cards[picked[1]];
  tries++;
  if (a.k === b.k) {
    a.done = b.done = turn;
    score[turn]++;
    memory[picked[0]] = memory[picked[1]] = -1;
    rg.sound("coin"); rg.vibrate(15);
    rg.celebrate(gx(picked[1]) + cw() / 2, gy(picked[1]) + ch() / 2, { count: 14, sound: false });
    picked = [];
    let left = 0; for (const c of cards) if (!c.done) left++;
    if (!left) finish();
  } else {
    a.open = b.open = false; a.flip = b.flip = 0;
    picked = [];
    if (mode !== 0) { turn = 3 - turn; rg.sound("whoosh", { volume: 0.4 }); }
  }
  phoneClock = 0;
}

function finish() {
  over = true; overClock = 0;
  if (mode === 0) {
    if (!best[size] || tries < best[size]) { best[size] = tries; rg.save("best", best); }
    rg.celebrate(width / 2, 300, { big: true });
    return;
  }
  if (score[1] === score[2]) { rg.sound("pop"); return; }
  if (mode === 2 && score[2] > score[1]) { rg.sound("lose"); return; }
  rg.celebrate(width / 2, 300, { big: true });
}

// the phone: a pair it remembers, else a new card, and its pair if it knows it
function phonePick(): number {
  const known = (i: number) => memory[i] >= 0 && !cards[i].done && !cards[i].open;
  if (picked.length === 1) {
    const k = cards[picked[0]].k;
    for (let i = 0; i < cards.length; i++) if (i !== picked[0] && known(i) && memory[i] === k) return i;
  } else {
    for (let i = 0; i < cards.length; i++) for (let j = i + 1; j < cards.length; j++) if (known(i) && known(j) && memory[i] === memory[j]) return i;
  }
  const unknown: number[] = [], any: number[] = [];
  for (let i = 0; i < cards.length; i++) if (!cards[i].done && !cards[i].open) { any.push(i); if (memory[i] < 0) unknown.push(i); }
  const from = unknown.length ? unknown : any;
  return from[Math.floor(rg.random() * from.length)];
}

function tick(dt: number) {
  t += dt;
  if (dt > 0.05) dt = 0.05;
  for (const c of cards) if (c.flip < 1) c.flip = Math.min(1, c.flip + dt * 5);
  if (over) { overClock += dt; return; }
  if (picked.length === 2) {
    waitClock += dt;
    const same = cards[picked[0]].k === cards[picked[1]].k;
    if (waitClock > (same ? 0.6 : 1.3)) resolve();
    return;
  }
  if (phoneTurn()) {
    phoneClock += dt;
    if (phoneClock > 0.8) { phoneClock = 0.2; turnCard(phonePick()); }
  }
}

function onBack() { if (screen === "game") { screen = "menu"; rg.sound("click"); } }

function Card(i: number) {
  const c = cards[i];
  const faceUp = c.open || c.done > 0;
  // flip: the card narrows to nothing showing its old side, then widens
  const k = c.flip;
  const showFace = k < 0.5 ? !faceUp : faceUp;
  const w = cw() * Math.abs(1 - 2 * Math.min(k, 1)) ;
  const ww = k >= 1 ? cw() : Math.max(4, w);
  const st = { left: gx(i) + (cw() - ww) / 2, top: gy(i), width: ww, height: ch() };
  if (!showFace) return <div className="card back" id={"c" + i} style={st} onClick={() => tap(i)}><div className="backin"><div className="q">?</div></div></div>;
  const cls = c.done ? (c.done === 1 ? "card face d1" : "card face d2") : "card face";
  return <div className={cls} id={"c" + i} style={st} onClick={() => tap(i)}>{ww > 30 ? <div className="emo">{ANIMALS[c.k]}</div> : null}</div>;
}

function Pill(p: number) {
  const on = turn === p && !over;
  return <div className={on ? (p === 1 ? "pill on1" : "pill on2") : "pill"}>
    <div className="pname">{who(p)}</div><div className="pscore">{String(score[p])}</div>
  </div>;
}

function status() {
  if (over) {
    if (mode === 0) return rg.t("Valmis! " + tries + " yritystä", "Done! " + tries + " tries", "¡Listo! " + tries + " intentos");
    if (score[1] === score[2]) return rg.t("Tasapeli!", "Draw!", "¡Empate!");
    const w = score[1] > score[2] ? 1 : 2;
    if (mode === 2) return w === 1 ? rg.t("Voitit! 🎉", "You won! 🎉", "¡Ganaste! 🎉") : rg.t("Puhelin voitti!", "The phone won!", "¡Ganó el teléfono!");
    return rg.t(who(w) + " voitti! 🎉", who(w) + " won! 🎉", "¡Ganó " + who(w) + "! 🎉");
  }
  if (mode === 0) return rg.t("Yrityksiä: ", "Tries: ", "Intentos: ") + tries + (best[size] ? rg.t("  (ennätys " + best[size] + ")", "  (best " + best[size] + ")", "  (récord " + best[size] + ")") : "");
  return rg.t("Vuoro: ", "Turn: ", "Turno: ") + who(turn);
}

function Game() {
  const kids = [];
  if (mode !== 0) kids.push(<div className="top" style={{ left: 0, top: 56, width: width }}>{Pill(1)}{Pill(2)}</div>);
  kids.push(<div className={over ? "status sbig" : "status"} style={{ left: 0, top: mode === 0 ? 64 : 106, width: width }}>{status()}</div>);
  for (let i = 0; i < cards.length; i++) kids.push(Card(i));
  if (over && overClock > 1) {
    kids.push(<div className="panel" style={{ left: (width - 280) / 2, top: 200 }}>
      <div className="ptitle">{status()}</div>
      <div className="btn agn" id="again" onClick={() => newGame(mode)}>{rg.t("Uudestaan!", "Again!", "¡Otra vez!")}</div>
      <div className="btn mback pback" id="menu" onClick={() => { screen = "menu"; rg.sound("click"); }}>{rg.t("← Takaisin", "← Back", "← Atrás")}</div>
    </div>);
  }
  return <div className="screen">{kids}</div>;
}

function Menu() {
  const demo = [];
  for (let k = 0; k < 3; k++) demo.push(<div className="mcard" style={{ marginTop: 8 + Math.sin(t * 2.5 + k) * 6 }}><div className="emo">{ANIMALS[(Math.floor(t / 1.5) + k * 5) % ANIMALS.length]}</div></div>);
  const chip = (i: number) => <div className={size === i ? "chip con" : "chip"} id={"size" + i} onClick={() => { size = i; rg.save("size", i); rg.sound("click"); }}>{String(SIZES[i])}</div>;
  return (
    <div className="screen menu">
      <div className="title">{rg.t("Muistipeli", "Memory", "Memoria")}</div>
      <div className="demo">{demo}</div>
      <div className="chips"><div className="clabel">{rg.t("Kortteja:", "Cards:", "Cartas:")}</div>{chip(0)}{chip(1)}{chip(2)}</div>
      <div className="btn mbtn" id="alone" onClick={() => newGame(0)}>🙂 {rg.t("Yksin", "Alone", "Solo")}</div>
      <div className="btn mbtn alt" id="vsphone" onClick={() => newGame(2)}>📱 {rg.t("Puhelinta vastaan", "Against the phone", "Contra el teléfono")}</div>
      <div className="btn mbtn alt2" id="vsfriend" onClick={() => newGame(1)}>👫 {rg.t("Kaverin kanssa", "With a friend", "Con un amigo")}</div>
    </div>
  );
}

function view() { return screen === "menu" ? Menu() : Game(); }
