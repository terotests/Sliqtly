// Nappikisa: two players, one phone on the table between them. Each has a
// half of the screen (the top half is turned towards the far player). When
// the light turns green, tap your half — the faster wins the round. Tap
// too early and the point goes to the other player. First to five wins.

type Mode = "title" | "wait" | "go" | "point" | "won";

const WIN = 5;
let mode: Mode = "title";
let t = 0;
let clock = 0;
let goAt = 0;
let score = [0, 0];          // 0 bottom, 1 top
let last = -1;               // who got the last point
let early = false;
let reaction = 0;
let flash = [0, 0];

function newRound() {
  mode = "wait";
  clock = 0;
  goAt = 1.4 + rg.random() * 2.6;
  early = false;
  if (score[0] + score[1] === 0) goAt += 1.2;   // the first round: a little longer to get ready
}

function tick(dt: number) {
  t += dt;
  clock += dt;
  flash = flash.map((f) => Math.max(0, f - dt * 3));
  if (mode === "wait" && clock >= goAt) { mode = "go"; clock = 0; rg.sound("note", { note: 84, len: 0.25, wave: "square" }); }
  if (mode === "point" && clock > 1.6) {
    if (score[0] >= WIN || score[1] >= WIN) { mode = "won"; clock = 0; rg.celebrate(width / 2, score[0] >= WIN ? height * 0.75 : height * 0.25, { big: true }); rg.vibrate([100, 60, 100, 60, 300]); }
    else newRound();
  }
}

function tap(p: number) {
  flash[p] = 1;
  if (mode === "title" || (mode === "won" && clock > 1)) {
    score = [0, 0];
    rg.sound("click");
    newRound();
    return;
  }
  if (mode === "wait") {
    early = true;
    last = 1 - p;
    score[last]++;
    mode = "point"; clock = 0;
    rg.sound("wrong");
    rg.vibrate(150);
  } else if (mode === "go") {
    reaction = clock;
    early = false;
    last = p;
    score[p]++;
    mode = "point"; clock = 0;
    rg.sound("coin");
    rg.vibrate(40);
  }
}

function onPointerDown(x: number, y: number) {
  tap(y < height / 2 ? 1 : 0);
}

function onKeyDown(key: string) {
  if (key === "ArrowUp" || key === "w") tap(1);
  if (key === "ArrowDown" || key === "s" || key === " ") tap(0);
}

function message(p: number): string {
  if (mode === "title") return rg.t("Napauta, kun olette valmiit!", "Tap when you are both ready!", "¡Tocad cuando estéis listos!");
  if (mode === "wait") return rg.t("Odota…", "Wait…", "Espera…");
  if (mode === "go") return rg.t("NYT!", "NOW!", "¡YA!");
  if (mode === "won") return score[p] >= WIN ? rg.t("VOITIT! 🏆", "YOU WON! 🏆", "¡GANASTE! 🏆") : rg.t("Hävisit tällä kertaa", "You lost this time", "Esta vez perdiste");
  if (last === p) return early ? rg.t("Kaveri oli liian nopea!", "Your friend was too early!", "¡Tu amigo se adelantó!") : rg.t("Piste! ", "Point! ", "¡Punto! ") + Math.round(reaction * 1000) + " ms";
  return early ? rg.t("Liian aikaisin!", "Too early!", "¡Demasiado pronto!") : rg.t("Kaveri ehti ensin", "Your friend was first", "Tu amigo fue más rápido");
}

function Half(p: number) {
  const color = mode === "go" ? "#06d6a0" : mode === "wait" ? "#ef476f" : p === 0 ? "#3a86ff" : "#ff006e";
  const pulse = mode === "go" ? 1 + Math.sin(t * 20) * 0.04 : 1;
  const dots = [];
  for (let i = 0; i < WIN; i++) dots.push(<div className={i < score[p] ? "pt on" : "pt"} />);
  return (
    <div className="half" style={{ backgroundColor: color, transform: p === 1 ? "rotate(180deg)" : "none", opacity: 1 - flash[p] * 0.25 }}>
      <div className="points">{dots}</div>
      <div className="light" style={{ width: 150 * pulse, height: 150 * pulse }}>{mode === "go" ? "⚡" : mode === "wait" ? "✋" : p === 0 ? "🔵" : "🔴"}</div>
      <div className="msg">{message(p)}</div>
    </div>
  );
}

function view() {
  return (
    <div className="screen">
      {Half(1)}
      <div className="mid" />
      {Half(0)}
    </div>
  );
}
