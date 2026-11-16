# Writing a RangerGames game

RangerGames is an engine for children's phone games. A game is a folder; the engine runs its
`game.tsx` (TypeScript with JSX, no React: a fresh element tree every frame), lays the tree out
with CSS (flexbox) and draws it on a canvas, on the web and natively on Android. The screen is a
phone held upright, about 360 × 640 to 720 points.

## The folder

| File | What |
| --- | --- |
| `game.json` | `{ "name": "Hyppypeli", "description": "…", "icon": "🐰", "color": "#3a86ff", "players": "1", "width": 360 }`; optional `"speech": false`, `"i18n": { "en": { "name": "…", "description": "…" } }` |
| `game.tsx` | the game |
| `style.css` | the classes the tree uses |
| `lib/*.ts` | more code: `import { rules } from "lib/rules";` (path from the folder, no extension) |
| `img/*.png`, `.jpg`, `.webp`, `.gif` | pictures: `<img src={rg.asset("img/cat.png")} />` |
| `sounds/*.wav`, `.mp3`, `.ogg` | sounds: `rg.sound("sounds/hop.wav")` |

Write files with `write_files`; open the `preview_url` to play the newest version (it reloads the
files every time it opens).

`screenshot_game` runs the game here and returns a picture of its screen after the frames asked
(60 a second), with what it logged and any error; taps and key presses can play it on the way.
Use it to check a change before giving the user the link.

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
| `rg.closeButton = "left" \| "none"` | the engine draws ✕ (back to the menu) top right |
| `rg.exit()` | back to the menu |
| `rg.three(op, args)` | a low-poly 3D scene under the 2D view (below) |

Speech: `rg.say` plays the engine's own Finnish clips and is not for new words. For spoken
instructions make a sound file with the `speak` tool (when the account has speech) and play it
with `rg.sound("sounds/ohje.wav")`.

## 3D

```tsx
function init() {
  rg.three("clear");
  rg.three("scene", { background: ["#6ec6ff", "#d8f1ff"], fog: [14, 46], fogColor: "#d8f1ff", ground: { y: 0, color: "#7bc950" } });
  rg.three("camera", { x: 0, y: 3, z: 6, lookAt: [0, 0, 0], fov: 60 });
  rg.three("light", { id: "sun", type: "directional", x: 3, y: 6, z: 2, intensity: 0.6 });
  rg.three("light", { id: "amb", type: "ambient", intensity: 0.5 });
  rg.three("add", { id: "ball", shape: "sphere", size: [0.5], color: "#ff006e", x: 0, y: 0.5, z: 0 });
}
function tick(dt) { rg.three("set", { id: "ball", y: 0.5 + Math.abs(Math.sin(Date.now() / 300)) }); }
```

Shapes: `box` `[w,h,d]`, `sphere` `[r]`, `cylinder` `[rTop, rBottom, h]`, `cone`, `plane`,
`torus`, `star`, `prism`, `roof`, `dome`; each with `color`, `x y z`, `rx ry rz`, `sx sy sz`.
`set` moves or recolours, `remove` takes one away. Objects must not poke through each other.

## Good games for children

- Big targets (at least 60 points), little text, pictures and colours that tell what to do.
- Every success: `rg.celebrate` and a sound; a mistake: a gentle sound, no punishment.
- Two players on one phone: split the screen, the top half rotated 180°.
- Test the preview on a phone. Old phones are slow: keep the number of elements modest
  (a few hundred) and pictures small (under 500 KB each).
