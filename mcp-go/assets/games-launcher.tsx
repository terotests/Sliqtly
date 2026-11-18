// What the close button of a Games link (sliqtly.com/games/p/...) opens: it
// starts the game again at its first page, so the player never sees a menu.
let games: any[] = [];
let started = false;

function init() {
  games = rg.games();
}

function view() {
  if (!started && games.length > 0) {
    started = true;
    rg.launch(games[0].id);
  }
  const bg = games.length > 0 && games[0].color ? games[0].color : "#14162a";
  return <div className="page" style={{ backgroundColor: bg }}></div>;
}
