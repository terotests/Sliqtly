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
| `models/*.glb` | 3D models (glTF binary): `new GLTFLoader().loadSync("models/knight.glb")` |

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
| `rg.celebrate(x, y, { big })` | stars and confetti with a sound: use it for every success |
| `rg.save(key, value)`, `rg.load(key, fallback)` | kept on the device between plays (JSON values) |
| `rg.random()`, `rg.range(lo, hi)`, `rg.pick(arr)`, `rg.shuffle(arr)`, `rg.seed(n)` | seeded random |
| `rg.clamp`, `rg.lerp`, `rg.ease.out/inOut/back/bounce(t)`, `rg.hit(a, b)` | helpers; `hit` overlaps `{x,y,w,h}` rects |
| `rg.box(id)` | an element's laid-out `{x,y,w,h}` from the last frame (give the element an `id`) |
| `rg.asset(path)` | a path in the game's folder, for `<img src>` |
| `rg.t(fi, en, es)`, `rg.lang` | the player's language |
| `rg.closeButton = "left" \| "none"` | the engine draws ✕ (back to the menu) top right |
| `rg.exit()` | back to the menu |
| `<scene3d>`, `THREE` | 3D with three.js's names, as React Three Fiber (below) |
| `rg.raycast(x, y)` | the 3D object under a point: `{ id, name, distance, point: [x,y,z], normal }` or null |
| `rg.three(op, args)` | the older command API for simple 3D (below) |

Speech: `rg.say` plays the engine's own Finnish clips and is not for new words. For spoken
instructions make a sound file with the `speak` tool (when the account has speech) and play it
with `rg.sound("sounds/ohje.wav")`.

## 3D

Write the 3D world as React Three Fiber does: JSX under `<scene3d>` in `view()`, drawn under the
2D view (depth-tested on the GPU; the screenshot tool draws it without one). glTF models, their
skeletal animation and taps on 3D objects work with three.js's own classes.

```tsx
import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";

const gltf = new GLTFLoader().loadSync("models/knight.glb");  // also .load(url, onLoad), .loadAsync(url)
const knight = gltf.scene;                                     // knight.clone(): another copy
const mixer = new THREE.AnimationMixer(knight);
const idle = mixer.clipAction("Idle");                         // a clip name or a clip of gltf.animations
const attack = mixer.clipAction("Attack");
attack.setLoop(THREE.LoopOnce, 1);
attack.clampWhenFinished = true;
attack.addMarker("sword_hit", 0.45);                           // RangerGames: a moment of the clip
mixer.addEventListener("marker", (e) => { if (e.name === "sword_hit") rg.sound("hit"); });
mixer.addEventListener("finished", (e) => idle.reset().play().crossFadeFrom(e.action, 0.3));
idle.play();
let selected = "";

function tick(dt) { mixer.update(dt); }

function view() {
  return (
    <div class="screen">
      <scene3d>
        <color attach="background" args={["#9fd3ff"]} />
        <fog attach="fog" args={["#9fd3ff", 14, 40]} />
        <perspectiveCamera position={[0, 6, 8]} fov={45} lookAt={[0, 0, 0]} />
        <orbitControls target={[0, 0, 0]} minDistance={4} maxDistance={14} maxPolarAngle={1.3} enablePan={false} />
        <hemisphereLight args={["#ffffff", "#8a6e4b", 0.6]} />
        <directionalLight position={[3, 7, 4]} intensity={1.1} />
        <mesh name="floor" position={[0, -0.1, 0]} renderOrder={-1} onClick={(e) => { selected = "floor"; }}>
          <boxGeometry args={[6, 0.2, 6]} />
          <meshLambertMaterial color="#e9d3a6" />
        </mesh>
        <primitive object={knight} position={[0, 0, 0]} scale={0.7}
          onClick={() => { attack.reset().play().crossFadeFrom(idle, 0.15); }} />
      </scene3d>
      <div class="hud">2D on top</div>
    </div>
  );
}
```

- Elements: `perspectiveCamera` (`position`, `fov`, `lookAt`, `near`, `far`), `orbitControls` (drei's
  props: `target`, `minDistance`, `maxDistance`, `minPolarAngle`, `maxPolarAngle`, `enableZoom`,
  `enableRotate`, `autoRotate`, `autoRotateSpeed`; one finger turns, two fingers zoom;
  `rg.orbit.reset()`), `ambientLight`, `directionalLight`, `pointLight`, `hemisphereLight`,
  `group`, `mesh` (a `…Geometry` and a `…Material` child), `primitive object={gltf.scene}`,
  `color attach="background"`, `fog attach="fog"`. Without lights only `meshBasicMaterial` shows.
- Every object: `name`, `position`, `rotation` (radians), `quaternion`, `scale` (number or
  `[x,y,z]`), `position-x` and the other dashed forms, `visible`, `renderOrder` (give big floors
  `-1` so the screenshot tool draws them first).
- Geometries with three.js's `args`: `box`, `plane`, `sphere`, `cylinder`, `cone`, `circle`,
  `ring`, `torus`, `torusKnot`, `capsule`, `tetrahedron`, `octahedron`, `icosahedron`,
  `dodecahedron` (+ `Geometry`). Materials: `meshStandardMaterial`, `meshLambertMaterial`,
  `meshPhongMaterial`, `meshBasicMaterial`, `meshNormalMaterial` with `color`, `emissive`,
  `side={THREE.DoubleSide}`, `flatShading`. No textures, shadows or transparency.
- Taps: a mesh's or primitive's `onClick` / `onPointerDown` / `onPointerUp` gets
  `{ object: { name }, point, distance, normal, x, y }` when it is the nearest thing under the
  finger and no 2D element with a handler is there. A drag that turned the orbit camera is not a
  click. Elsewhere: `rg.raycast(x, y)`, `rg.raycast(x, y, true)` (all, nearest first),
  `new THREE.Raycaster().setFromCamera(ndc).intersectObjects(["name"])`, and `rg.project([x,y,z])`
  for where a world point is on the screen (labels over objects).
- Models: `.glb` files written with `write_files` (base64). Meshes, the node tree, skins and
  animation clips are read; textures are not (a model shows its base colours or vertex colours).
  Keep them low-poly (hundreds of triangles): old phones pose and skin them on the CPU.
- AnimationAction: `play`, `stop`, `reset`, `fadeIn`, `fadeOut`, `crossFadeTo`, `crossFadeFrom`,
  `setLoop(THREE.LoopOnce | LoopRepeat | LoopPingPong, reps)`, `clampWhenFinished`,
  `setEffectiveWeight`, `timeScale`, `time`, `paused`; the mixer sends `finished`, `loop` and
  `marker` events. Call `mixer.update(dt)` in `tick`.
- `THREE` also has `Vector2`, `Vector3`, `MathUtils`, `Clock` and the constants.

The older command API still works for quick scenes:

```tsx
function init() {
  rg.three("scene", { background: ["#6ec6ff", "#d8f1ff"], fog: [14, 46], ground: { y: 0, color: "#7bc950" } });
  rg.three("camera", { x: 0, y: 3, z: 6, lookAt: [0, 0, 0], fov: 60 });
  rg.three("add", { id: "ball", shape: "sphere", size: [0.5], color: "#ff006e", x: 0, y: 0.5, z: 0 });
}
function tick(dt) { rg.three("set", { id: "ball", y: 0.5 + Math.abs(Math.sin(Date.now() / 300)) }); }
```

Shapes: `box` `[w,h,d]`, `sphere` `[r]`, `cylinder` `[rTop, rBottom, h]`, `cone`, `plane`,
`torus`, `star`, `prism`, `roof`, `dome`; each with `color`, `x y z`, `rx ry rz`, `sx sy sz`.

## Good games for children

- Big targets (at least 60 points), little text, pictures and colours that tell what to do.
- Every success: `rg.celebrate` and a sound; a mistake: a gentle sound, no punishment.
- Two players on one phone: split the screen, the top half rotated 180°.
- Test the preview on a phone. Old phones are slow: keep the number of elements modest
  (a few hundred) and pictures small (under 500 KB each).
