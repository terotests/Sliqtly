// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A chrome knot that mirrors its slide: a <scene3d> (allow: 3d) is drawn by
// the host with Ranger's Three.js port. Shiny surfaces (metal > 0) reflect a
// room made of the slide around them, and what no mesh covers shows the
// slide itself.
//
//   <mesh shape="knot" metal={0.9} ry={...} />   box sphere torus knot
//                                                cylinder cone plane teapot
//   <camera z={6} fov={40} />                    looks at the origin
//   <light kind="sun" x y z intensity color />   none given: a key and a fill

let t = 0;
let spin = 1;

function tick(dt: number) {
  t += dt * spin;
}

function onPointerDown() {
  spin = spin === 1 ? 0 : 1;
}

function view() {
  const a = t * 40;
  return (
    <div className="stage">
      <scene3d className="world">
        <camera z={6.2} fov={38} />
        <mesh shape="knot" r={1.05} tube={0.34} metal={0.55} color="#f4f4f7" rx={a * 0.6} ry={a} />
        <mesh shape="sphere" r={0.55} x={-2.5} y={0.9} metal={0.35} color="#ffd36b" ry={-a} />
        <mesh shape="torus" r={0.5} tube={0.16} x={2.5} y={-0.9} metal={0.35} color="#7dd3fc" rx={a * 1.3} ry={a * 0.7} />
      </scene3d>
    </div>
  );
}
