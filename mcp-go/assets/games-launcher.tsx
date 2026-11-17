// The menu of a Games link (sliqtly.com/games/p/...) for a signed-in player:
// the game the link plays, to play again, and the player's own games.
// The server puts MINE ([{name, url, here}]) before this when it builds the
// pack; a player who is not signed in gets no menu (the close button is not
// drawn then).
const fi = (rg.lang || "fi") !== "en";
const T = fi
  ? { play: "Pelaa", again: "Pelaa uudelleen", mine: "Omat pelit", here: "tämä", none: "Ei muita pelejä vielä." }
  : { play: "Play", again: "Play again", mine: "Your games", here: "this one", none: "No other games yet." };
const PAGE = 6;
let games: any[] = [];
let played = false;
let page = 0;

function init() {
  games = rg.games();
  played = rg.load("played", false);
}

function play(id: string) {
  rg.save("played", true);
  rg.sound("click");
  rg.vibrate(15);
  rg.launch(id);
}

function open(m: any) {
  rg.sound("click");
  rg.vibrate(15);
  if (rg.open) rg.open(m.url);
}

function turn(d: number) {
  const pages = Math.max(1, Math.ceil(MINE.length / PAGE));
  page = Math.min(pages - 1, Math.max(0, page + d));
  rg.sound("click");
}

function view() {
  const pages = Math.max(1, Math.ceil(MINE.length / PAGE));
  const shown = MINE.slice(page * PAGE, page * PAGE + PAGE);
  return (
    <div className="page">
      {games.map((g) => (
        <div className="game" style={{ backgroundColor: g.color || "#3a86ff" }}>
          <div className="icon">{g.icon || "🎮"}</div>
          <div className="name">{g.name}</div>
          <div className="play" onClick={() => play(g.id)}>{played ? T.again : T.play}</div>
        </div>
      ))}
      <div className="mine">
        <div className="title">{T.mine}</div>
        {MINE.length <= 1 ? <div className="info">{T.none}</div> : null}
        {MINE.length > 1 ? shown.map((m: any) => (
          <div className={m.here ? "row here" : "row"} onClick={() => { if (!m.here) open(m); }}>
            <div className="rname">{m.name.length > 24 ? m.name.slice(0, 23) + "…" : m.name}</div>
            <div className="rgo">{m.here ? T.here : T.play}</div>
          </div>
        )) : null}
        {pages > 1 ? (
          <div className="pager">
            <div className={page > 0 ? "pg" : "pg off"} onClick={() => turn(-1)}>‹</div>
            <div className="pn">{(page + 1) + " / " + pages}</div>
            <div className={page < pages - 1 ? "pg" : "pg off"} onClick={() => turn(1)}>›</div>
          </div>
        ) : null}
      </div>
    </div>
  );
}
