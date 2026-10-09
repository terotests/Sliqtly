// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Every shape a <scene3d> has, turning; a click on a button makes them all
// that shiny, so the slide's colours show in them. The buttons' words are
// the deck's own keys (matte-label, mirror-label).

const SHAPES = ["box", "sphere", "torus", "knot", "cylinder", "cone", "teapot"];
let t = 0;
let metal = 0.0;

function tick(dt: number) {
  t += dt;
}

function view() {
  const a = t * 35;
  const meshes = SHAPES.map((s, i) => {
    const x = (i - (SHAPES.length - 1) / 2) * 1.55;
    return <mesh shape={s} size={1.0} r={0.55} tube={0.18} h={1.0} x={x} rx={20 + a * 0.4} ry={a + i * 25} metal={metal} color={i % 2 ? "#e2e8f0" : "#fda4af"} />;
  });
  return (
    <div className="stage">
      <scene3d className="world">
        <camera z={5.6} fov={42} />
        {meshes}
      </scene3d>
      <div className={metal === 0 ? "btn matte on" : "btn matte"} onClick={() => { metal = 0.0; }}><span className="label">{deck.get("matte-label") || "matte"}</span></div>
      <div className={metal > 0 ? "btn mirror on" : "btn mirror"} onClick={() => { metal = 0.75; }}><span className="label">{deck.get("mirror-label") || "mirror"}</span></div>
    </div>
  );
}
