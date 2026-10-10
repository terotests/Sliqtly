// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A chrome knot that mirrors its slide: a <scene3d> (allow: 3d) is written
// as React Three Fiber writes a scene, with Three.js's own names, and drawn
// by the host with Ranger's Three.js port. Metal surfaces reflect a room made
// of the slide around them, and what no mesh covers shows the slide itself.
//
//   <mesh position rotation scale>            rotation in radians
//     <torusKnotGeometry args={[…]} />         box sphere cylinder cone plane
//                                              torus torusKnot (Three's args)
//     <meshStandardMaterial color metalness roughness />
//   <perspectiveCamera position fov lookAt />  none given: z 5, fov 75
//   <directionalLight position intensity />    none given: a key and a fill

let t = 0;
let spin = 1;

function tick(dt: number) {
  t += dt * spin;
}

function onPointerDown() {
  spin = spin === 1 ? 0 : 1;
}

function view() {
  const a = t * 0.7;
  return (
    <div className="stage">
      <scene3d className="world">
        <perspectiveCamera position={[0, 0, 6.2]} fov={38} />
        <mesh rotation={[a * 0.6, a, 0]}>
          <torusKnotGeometry args={[1.05, 0.34, 180, 20]} />
          <meshStandardMaterial color="#f4f4f7" metalness={0.85} roughness={0.25} />
        </mesh>
        <mesh position={[-2.5, 0.9, 0]} rotation={[0, -a, 0]}>
          <sphereGeometry args={[0.55, 48, 32]} />
          <meshStandardMaterial color="#ffd36b" metalness={0.5} roughness={0.35} />
        </mesh>
        <mesh position={[2.5, -0.9, 0]} rotation={[a * 1.3, a * 0.7, 0]}>
          <torusGeometry args={[0.5, 0.16, 24, 96]} />
          <meshStandardMaterial color="#7dd3fc" metalness={0.5} roughness={0.35} />
        </mesh>
      </scene3d>
    </div>
  );
}
