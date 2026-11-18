# Screens and menus

A game is one `view()` that returns the whole screen every frame, so a menu is not a page of its
own: keep which screen is showing in a variable and draw that one. The example `muistipeli`
(games_guide topic `example:muistipeli`) does all of this in about 200 lines.

## Which screen

```tsx
type Screen = "menu" | "game" | "done";
let screen: Screen = "menu";

function view() {
  if (screen === "menu") return Menu();
  return Game();             // the results panel is drawn over the game (below)
}
```

Change the screen in a handler, with a sound, and set up what the new screen needs there:

```tsx
function start(level: number) {
  mode = level;
  cards = deal(level);
  screen = "game";
  rg.sound("whoosh");
}
```

## A start menu

Big buttons, one per way to play, an emoji or picture that says what it is, the title on top.
Give every button an `id`: `play_game` and `inspect_game` can tap it by id or by its text.

```tsx
function Menu() {
  return (
    <div className="screen menu">
      <div className="title">Muistipeli</div>
      <div className="btn" id="alone" onClick={() => start(0)}>🙂 Yksin</div>
      <div className="btn alt" id="vsphone" onClick={() => start(2)}>📱 Puhelinta vastaan</div>
      <div className="btn alt2" id="vsfriend" onClick={() => start(1)}>👫 Kaverin kanssa</div>
    </div>
  );
}
```

```css
.menu { align-items: center; padding-top: 44px; }
.btn { width: 260px; height: 58px; margin-top: 14px; border-radius: 29px; background-color: #e76f51;
  color: #fff; font-size: 21px; font-weight: 700; align-items: center; justify-content: center; }
```

## Choosing a level or a size

A row of chips; the chosen one has its own class. Save the choice so the next game starts with it:

```tsx
let size = 1;
function init() { size = rg.load("size", 1); }
const chip = (i: number) =>
  <div className={size === i ? "chip on" : "chip"} id={"size" + i}
       onClick={() => { size = i; rg.save("size", i); rg.sound("click"); }}>{String(SIZES[i])}</div>;
// in Menu(): <div className="chips">{chip(0)}{chip(1)}{chip(2)}</div>
```

Levels that open one by one: keep the highest reached with `rg.save("level", n)` and draw the
later ones locked (a 🔒, `opacity: 0.4`, no `onClick`).

## Two players, or the phone

- **Taking turns** on one phone: a `turn` variable, each player's score in a pill at the top, the
  one whose turn it is highlighted.
- **Facing each other** (the phone flat on the table): split the screen in two halves and turn the
  top one with `transform: rotate(180deg)`; `onPointerDown(x, y)` tells the halves apart by
  `y < height / 2`. The example `nappikisa` is exactly this.
- **Against the phone**: on its turn, count time in `tick` and make its move after a short pause
  (about 0.8 s) so a child can see it; let it make mistakes on purpose.

## The end of a round

Draw a panel over the game rather than a new screen, so the finished board stays visible.
Celebrate first and show the panel a moment later:

```tsx
let over = false, overClock = 0;
function tick(dt: number) { if (over) overClock += dt; }
function finish() { over = true; overClock = 0; rg.celebrate(width / 2, 300, { big: true }); }

// in Game():
if (over && overClock > 1) kids.push(
  <div className="panel" style={{ left: (width - 280) / 2, top: 200 }}>
    <div className="ptitle">Valmis! 🎉</div>
    <div className="btn" id="again" onClick={() => start(mode)}>Uudestaan!</div>
    <div className="btn back" id="menu" onClick={() => { screen = "menu"; rg.sound("click"); }}>← Takaisin</div>
  </div>);
```

## Back and close

- `onBack()` is Android's back button: from the game go back to the game's own menu.
- The engine draws ✕ in the top right corner; it starts the game again at its first page (its
  menu). Leave room for it, or move it with `rg.closeButton = "left"` (or `"none"` when the game
  has its own way back).
- A game played from a shared link has no menu of other games around it, so a "back to the menu
  of games" button (`rg.exit()`) does nothing there: leave it out.

## Keeping progress

`rg.save(key, value)` and `rg.load(key, fallback)` keep JSON values on the device: the chosen
size or level, the best score (`if (!best || tries < best) rg.save("best", tries)`), stickers
collected. Show a best score in the menu or the results panel; it gives a reason to play again.
