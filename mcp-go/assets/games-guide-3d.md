# 3D in a RangerGames game

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


Planets: `rg.three("planet", { id, x, y, z, r, regions, texture: "img/earth.jpg", ... })` draws a
globe. With `texture` (an equirectangular map, 2:1, longitude −180…180 left to right) hosts that
can draw textured spheres show the map and the regions are the fallback. Copy a map from the
RangerGames library instead of sending base64: `write_files` with
`{ path: "img/earth.jpg", library: "textures/planets/earth.jpg" }`. The library has
`textures/planets/` `earth`, `mars`, `jupiter`, `mercury`, `venus`, `sun` (`.jpg`, 2048×1024, Solar System
Scope, CC BY 4.0: credit "Solar System Scope" in the game).

A game made in the RangerGames repository is brought here file by file without base64:
`{ path: "models/m-kibo.glb", library: "games/marsiin/models/m-kibo.glb" }` copies a file of that
game's pack in the RangerGames build (`packs/<game>.rgpk`, published from its main branch).

A game's own shader: a `.glsl` file with only functions (no `#version`, no `main`) for a hook
the engine offers; the engine wraps it, so it runs the same on the web, Android and Quest.
Today the hook is `liquid` for `shape: "splats"` (a baked `.rgps` particle run):

```tsx
rg.shader("magma", { src: "shaders/magma.glsl", kind: "liquid" });
rg.three("add", { id: "m", shape: "splats", src: "theia.rgps",
  material: { shader: "magma", u: { glow: 1.6, crust: 0.5 } } });
```

```glsl
uniform float u_glow;   // material.u.glow: u_ + the name, 1 to 4 numbers
uniform float u_crust;
vec4 liquid(Fluid f) {  // f.n f.thick f.temp f.view f.sun f.t f.uv f.pal f.edge
  float n = fbm(vec3(f.uv * 18.0, f.t * 0.3), 4);
  float crust = smoothstep(0.1, 0.5, n + u_crust - f.temp);
  vec3 c = mix(heat(f.temp * 1.1) * u_glow, vec3(0.12, 0.09, 0.08), crust);
  return vec4(c, f.edge);   // colour and coverage, not premultiplied
}
```

Kind `surface` paints a textured planet (`rg.three("planet", { texture, material })`): the hook
`vec3 surface(Surface s)` gets `s.p` (the point, unit), `s.n`, `s.uv`, `s.col` (what the engine drew,
lit), `s.sun`, `s.view`, `s.t` (the planet's `flowTime`), `s.light`, `s.day`, and returns the colour:

```glsl
uniform float u_glow;
vec3 surface(Surface s) {
  float crack = 1.0 - smoothstep(0.0, 0.035, abs(fbm(s.p * 3.0, 5)));
  return s.col * (1.0 - crack * 0.8) + heat(0.55 + 0.45 * crack) * u_glow * crack;
}
```

The library (`hash1` `hash2` `vnoise` `fbm` `curl2` `curl3`, `heat`, `fresnel` `aaStep`) is already
included. Errors go to the game's log with the file's own lines (`shaders/magma.glsl:5: ...`) and the
engine's own look is drawn instead. The screenshot tool draws without the shader.
