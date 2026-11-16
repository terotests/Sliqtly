// The menu of a Games link (sliqtly.com/games/p/...): the game the link
// plays, to play again, and how to make games of your own with an AI
// assistant through the Games MCP. Added to every pack as /games/launcher.
const fi = (rg.lang || "fi") !== "en";
const T = fi ? {
  play: "Pelaa", again: "Pelaa uudelleen",
  make: "Tee oma peli",
  how: "Pelit tehdään tekoälyavustajalla (esim. Claude) Sliqtly Gamesin MCP-yhteyden kautta:",
  s1: "Lisää avustajaan oma yhteys (custom connector) osoitteella",
  s2: "Kirjaudu Google-tilillä, kun avustaja pyytää.",
  s3: "Pyydä avustajaa tekemään peli. Se antaa linkin, jolla pelin voi kokeilla heti.",
  invite: "Pelien tekeminen on toistaiseksi kutsupohjaista; pelaaminen linkillä on kaikille avointa.",
  copy: "Kopioi", copied: "Kopioitu ✓",
} : {
  play: "Play", again: "Play again",
  make: "Make your own game",
  how: "Games are made with an AI assistant (such as Claude) through the Sliqtly Games MCP connection:",
  s1: "Add a custom connector to your assistant with the address",
  s2: "Sign in with your Google account when the assistant asks.",
  s3: "Ask the assistant to make a game. It gives you a link to try it at once.",
  invite: "Making games is by invitation for now; anyone with a link can play.",
  copy: "Copy", copied: "Copied ✓",
};
let games: any[] = [];
let played = false;
const MCP = "https://sliqtly.com/games/mcp";
// seconds the copy button still says it copied
let copiedFor = 0;

function init() {
  games = rg.games();
  played = rg.load("played", false);
}

function tick(dt: number) {
  if (copiedFor > 0) copiedFor = Math.max(0, copiedFor - dt);
}

function copyMcp() {
  if (!rg.copy) return;
  rg.copy(MCP);
  rg.sound("click");
  rg.vibrate(15);
  copiedFor = 2;
}

function play(id: string) {
  rg.save("played", true);
  rg.sound("click");
  rg.vibrate(15);
  rg.launch(id);
}

function view() {
  return (
    <div className="page">
      {games.map((g) => (
        <div className="game" style={{ backgroundColor: g.color || "#3a86ff" }}>
          <div className="icon">{g.icon || "🎮"}</div>
          <div className="name">{g.name}</div>
          {g.description ? <div className="desc">{g.description}</div> : null}
          <div className="play" onClick={() => play(g.id)}>{played ? T.again : T.play}</div>
        </div>
      ))}
      <div className="make">
        <div className="title">{T.make}</div>
        <div className="info">{T.how}</div>
        <div className="step"><div className="n">1</div><div className="sinfo">{T.s1}</div></div>
        <div className="urlrow">
          <div className="url">sliqtly.com/games/mcp</div>
          {rg.copy ? <div className={copiedFor > 0 ? "copy done" : "copy"} onClick={copyMcp}>{copiedFor > 0 ? T.copied : T.copy}</div> : null}
        </div>
        <div className="step"><div className="n">2</div><div className="sinfo">{T.s2}</div></div>
        <div className="step"><div className="n">3</div><div className="sinfo">{T.s3}</div></div>
        <div className="note">{T.invite}</div>
      </div>
    </div>
  );
}
