# Writing a RangerGames game

RangerGames is an engine for children's phone games. A game is a folder; the engine runs its
`game.tsx` (TypeScript with JSX, no React: a fresh element tree every frame), lays the tree out
with CSS (flexbox) and draws it on a canvas, on the web and natively on Android. The screen is a
phone held upright, about 360 × 640 to 720 points, unless game.json says the game is also made
for tablets or desktops (`"screens"`, below).

## Layout that holds (read this first)

The engine lays out with its own flexbox, not a browser's, and the same tree is drawn by more
than one renderer. These patterns come out the same everywhere:

- **Say the direction.** Every `div` is a flex column; write `flex-direction: row` on every row
  (icon + text, a row of buttons, a score bar). Don't rely on inline flow or `display`.
- **Icon + text column (a card, a menu tile):** a row with `align-items: center`; the icon in a
  box of fixed `width` and `height` with `align-items: center; justify-content: center`; the text
  in a column with a fixed `width` (or `flex-grow: 1`) and `align-items: flex-start`, so every
  line starts at the column's left edge. One `div` per line (title, subtitle, stars).
- **Left alignment comes from the column**, not from `text-align`: give text lines their own
  `div` and let `align-items: flex-start` place them. Use `text-align: center` only on a box of
  known width that should centre its text.
- **Give sizes in px** to anything that must line up (tiles, icon boxes, buttons); keep `%` for
  full-width bars. A box sized only by its text can come out a few px off between renderers.
- **Centre with flex**: `align-items: center; justify-content: center` on the parent, not
  margins or padding guessed by eye.
- **Overlays** (a panel, a toast, a flying score): `position: absolute` with `left`/`top` in px
  from the game's `width`/`height`; everything else in normal flow.
- **Emoji are glyphs**: give an emoji its own box (`font-size` + fixed `width`) when it must sit
  exactly; a row of emoji (⭐⭐⭐) in a text line is fine.
- **Avoid**: `float`, `grid`, `display: inline`, `vh`/`vw`, `calc()`, CSS variables, `::before`
  and `::after`, `:hover`; text that relies on wrapping to line up.

## The folder

| File | What |
| --- | --- |
| `game.json` | `{ "name": "Hyppypeli", "description": "…", "icon": "🐰", "color": "#3a86ff", "players": "1", "width": 360 }`; optional `"speech": false`, `"i18n": { "en": { "name": "…", "description": "…" } }`, `"screens"` (below) |
| `game.tsx` | the game |
| `style.css` | the classes the tree uses |
| `lib/*.ts` | more code: `import { rules } from "lib/rules";` (path from the folder, no extension) |
| `img/*.png`, `.jpg`, `.webp`, `.gif` | pictures: `<img src={rg.asset("img/cat.png")} />` |
| `sounds/*.wav`, `.mp3`, `.ogg` | sounds: `rg.sound("sounds/hop.wav")` |
| `models/*.glb` | 3D models (glTF binary): `new GLTFLoader().loadSync("models/knight.glb")` |

### Screens: phone, tablet, desktop

`"screens"` in game.json lists the screens the game is laid out for: `"mobile"` (upright phone,
the default), `"tablet"` (up to 4:3 lying sideways) and `"desktop"` (any width). On a desktop
browser the game gets the widest of them: `["mobile"]` keeps the phone-shaped column in the
middle, `["mobile", "tablet", "desktop"]` fills the window. On a phone or tablet the game always
fills the screen; full screen turns it upright for a phone-only game, sideways for one without
`"mobile"`, and leaves it free when both are listed.

The screen can change while the game runs (the window is resized, the tablet turned), so a game
made for several screens reads `width` and `height` in `view()` every frame rather than once:

- `"width": 0` (or no width) gives the game the screen's own size in CSS pixels; lay it out from
  `width` and `height`, e.g. a side column when `width > height`.
- A fixed `"width"` scales the game to the screen's width and gives it the height the shape
  leaves; on a screen wider than that allows it gets `"minHeight"` (default 640) and bars at the
  sides. For a landscape game use a wide width (1280) with `"minHeight": 720`.

Write files with `write_files`; open the `preview_url` to play the newest version (it reloads the
files every time it opens).

`screenshot_game` runs the game here and returns a picture of its screen after the frames asked
(60 a second), with what it logged and any error; taps and key presses can play it on the way.
Use it to check a change before giving the user the link.

`play_game` plays the game step by step: tap a button by its text (`{"tap": {"text": "Pelaa"}}`),
a class or x, y; drag; press keys; wait; and take pictures on the way (`"shot": true`). Its answer
lists what is on the screen with places to tap. The game runs the same way every time, so
`save_as` keeps a session under a name and `from` continues from it after the next change.
`inspect_game` answers the game's top-level variables, the laid-out elements and an expression's
value as JSON.

## The frame loop

Define any of these top-level functions; the engine calls them.

```tsx
let score = 0;                       // module state lives across frames

function init() { }                  // once, when the game starts (width, height are known)
function tick(dt: number, input) { } // every frame before view(); dt in seconds
function view() {                    // every frame: return the whole screen
  return (
    <div className="screen">
      <div className="score">{"Pisteet " + score}</div>
      <div className="button" onClick={() => { score++; rg.sound("coin"); }}>+1</div>
    </div>
  );
}
function onPointerDown(x: number, y: number, info) { }  // a touch where no element handles it
function onPointerUp(x: number, y: number, info) { }
function onKeyDown(key: string) { }  // "ArrowLeft", " ", "a" … (desktop browsers)
function onKeyUp(key: string) { }
function onBack() { }                // Android's back button (default: back to the menu)
```

- `width`, `height`: the screen in points (globals).
- `input.pointer` `{ x, y, down }` is the first finger now; `input.pointers` all of them;
  `input.keys[key]` true while held. Use them in `tick` for dragging and holding.
- Element handlers: `onClick` (a tap: down and up on the same element), `onPointerDown`,
  `onPointerUp`; each gets `{ x, y, id, pointer }`.
- Keep `view()` cheap: build plain objects, no DOM, no `document`, no `fetch`, no timers
  (`setTimeout` does not exist; count time in `tick`).

## Layout and CSS

Every `div` is a flex column by default. Supported: `width`, `height` (px or %), `min-/max-`,
`margin*`, `padding*`, `flex-direction`, `flex-grow`, `flex-wrap`, `align-items`,
`justify-content`, `gap`, `position: absolute` with `left`/`top`/`right`/`bottom`,
`background-color`, `linear-gradient`, `border-radius`, `border`, `opacity`, `transform`
(`rotate(…)`, `scale(…)`), `color`, `font-size`, `font-weight`, `text-align`, `line-height`,
`letter-spacing`, `overflow: hidden`. Inline styles: `style={{ left: x, top: y, width: 40 }}`
(numbers are px). Compound selectors `.a.b` work; the later rule wins (no specificity).
Emoji draw in colour and make good game pieces (🐰 ⭐ 🍎 🚀).
Text has no line breaks: white space collapses as in HTML, and `\n` written in JSX text or as
`\\n` in a string shows as the characters themselves. Put each line (or list item) in its own
`<div>`; write_files warns when a file would show a literal `\n`.

## rg.* API

| Call | What |
| --- | --- |
| `rg.sound(name, opts?)` | built-in effect: `jump coin click win lose pop hit tick whoosh star wrong`, or `note` with `{ note: 60..96, len, wave: "square"\|"sine"\|"triangle" }`; or a file `"sounds/x.wav"`. opts `{ volume, pitch }` |
| `rg.music(file, opts?)`, `rg.stopMusic()` | a looping sound file |
| `rg.vibrate(ms \| [on, off, on…])` | phone vibration |
| `rg.copy(text)` | puts text on the clipboard (call it from a tap) |
| `rg.celebrate(x, y, { big })` | stars and confetti with a sound: use it for every success |
| `rg.save(key, value)`, `rg.load(key, fallback)` | kept on the device between plays (JSON values) |
| `rg.random()`, `rg.range(lo, hi)`, `rg.pick(arr)`, `rg.shuffle(arr)`, `rg.seed(n)` | seeded random |
| `rg.clamp`, `rg.lerp`, `rg.ease.out/inOut/back/bounce(t)`, `rg.hit(a, b)` | helpers; `hit` overlaps `{x,y,w,h}` rects |
| `rg.box(id)` | an element's laid-out `{x,y,w,h}` from the last frame (give the element an `id`) |
| `rg.asset(path)` | a path in the game's folder, for `<img src>` |
| `rg.t(fi, en, es)`, `rg.lang` | the player's language |
| `rg.closeButton = "left" \| "none"` | the engine draws ✕ top right; it starts the game again at its first page |
| `rg.exit()` | back to the menu |
| `<scene3d>`, `THREE` | 3D with three.js's names, as React Three Fiber (games_guide topic "3d") |
| `rg.raycast(x, y)` | the 3D object under a point: `{ id, name, distance, point: [x,y,z], normal }` or null |
| `rg.three(op, args)` | the older command API for simple 3D (topic "3d") |

Speech: `rg.say` plays the engine's own Finnish clips and is not for new words. For spoken
instructions make a sound file with the `speak` tool (when the account has speech) and play it
with `rg.sound("sounds/ohje.wav")`.

## Good games for children

- Big targets (at least 60 points), little text, pictures and colours that tell what to do.
- Every success: `rg.celebrate` and a sound; a mistake: a gentle sound, no punishment.
- Two players on one phone: split the screen, the top half rotated 180°.
- Test the preview on a phone. Old phones are slow: keep the number of elements modest
  (a few hundred) and pictures small (under 500 KB each).

## More: ask games_guide for a topic

This page is the core. Call `games_guide` again with `topic` for more when the game needs it:

| topic | What |
| --- | --- |
| `menus` | screens and menus: a start menu, choosing a level or size, two players or the phone, a results panel, onBack, keeping progress |
| `3d` | 3D: `<scene3d>` as React Three Fiber, glTF models and animation, taps on 3D objects, the older `rg.three` API |
| `examples` | whole example games to learn from (small, few files) |
| `example:<name>` | one example's files, e.g. `example:muistipeli`; `create_game` with `example` starts a game from it |
