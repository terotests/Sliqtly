// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Three.js's geometries, turning; a click on a button makes them all metal,
// so the slide's colours show in them. The buttons' words are the deck's own
// keys (matte-label, mirror-label).

let t = 0;
let metal = false;

function tick(dt: number) {
  t += dt;
}

function shape(i: number) {
  if (i === 0) return <boxGeometry args={[0.9, 0.9, 0.9]} />;
  if (i === 1) return <sphereGeometry args={[0.55, 48, 32]} />;
  if (i === 2) return <torusGeometry args={[0.45, 0.16, 24, 96]} />;
  if (i === 3) return <torusKnotGeometry args={[0.42, 0.14, 180, 20]} />;
  if (i === 4) return <cylinderGeometry args={[0.45, 0.45, 1.0, 48]} />;
  if (i === 5) return <coneGeometry args={[0.5, 1.0, 48]} />;
  return <teapotGeometry args={[0.45, 10]} />;
}

function view() {
  const a = t * 0.6;
  const meshes = [0, 1, 2, 3, 4, 5, 6].map((i) => (
    <mesh position={[(i - 3) * 1.55, 0, 0]} rotation={[0.35 + a * 0.4, a + i * 0.44, 0]}>
      {shape(i)}
      <meshStandardMaterial
        color={i % 2 ? "#e2e8f0" : "#fda4af"}
        metalness={metal ? 0.9 : 0}
        roughness={metal ? 0.2 : 0.55}
      />
    </mesh>
  ));
  return (
    <div className="stage">
      <scene3d className="world">
        <perspectiveCamera position={[0, 0, 5.6]} fov={42} />
        {meshes}
      </scene3d>
      <div className={metal ? "btn matte" : "btn matte on"} onClick={() => { metal = false; }}><span className="label">{deck.get("matte-label") || "matte"}</span></div>
      <div className={metal ? "btn mirror on" : "btn mirror"} onClick={() => { metal = true; }}><span className="label">{deck.get("mirror-label") || "mirror"}</span></div>
    </div>
  );
}
