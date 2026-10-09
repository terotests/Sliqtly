// SPDX-License-Identifier: AGPL-3.0-or-later
//
// An excavator from plain shapes, digging. A <group> moves everything in it
// as one, in its own frame, so the arm is three groups one inside the other:
// the boom turns on the body, the stick on the boom's end, the bucket on the
// stick's. The upper body is a group that swings on the tracks. Click to stop.

let t = 0;
let run = 1;

function tick(dt: number) {
  t += dt * run;
}

function onPointerDown() {
  run = run === 1 ? 0 : 1;
}

const YELLOW = "#f5c400";
const DARK = "#26272b";
const STEEL = "#c9ced6";

function track(z: number) {
  return (
    <group z={z} y={0.32}>
      <mesh shape="box" w={3.0} h={0.56} d={0.55} color={DARK} />
      <mesh shape="cylinder" r={0.28} h={0.55} rx={90} x={-1.5} color={DARK} />
      <mesh shape="cylinder" r={0.28} h={0.55} rx={90} x={1.5} color={DARK} />
      <mesh shape="cylinder" r={0.2} h={0.58} rx={90} x={-1.5} color={STEEL} metal={0.3} />
      <mesh shape="cylinder" r={0.2} h={0.58} rx={90} x={1.5} color={STEEL} metal={0.3} />
      {[-0.9, -0.3, 0.3, 0.9].map((x) => <mesh shape="cylinder" r={0.11} h={0.6} rx={90} x={x} y={-0.12} color={STEEL} />)}
    </group>
  );
}

function view() {
  // one dig: reach out, scoop, lift, swing to the pile and back
  const c = t * 0.9;
  const swing = -45 + 45 * Math.sin(c * 0.5);
  const boom = 38 + 16 * Math.sin(c);
  const stick = -100 + 30 * Math.sin(c + 1.2);
  const bucket = -75 + 45 * Math.sin(c + 2.2);
  return (
    <div className="stage">
      <scene3d className="world">
        <camera x={2.0} y={2.8} z={7.0} fov={40} lookX={0.9} lookY={1.2} />
        <light kind="sun" x={0.6} y={1} z={0.8} intensity={0.95} />
        <light kind="sun" x={-0.8} y={0.3} z={-0.4} intensity={0.3} />
        <group ry={-15}>
          <mesh shape="cylinder" r={3.0} h={0.06} y={-0.03} color="#7a6248" />
          <mesh shape="cone" r={0.9} h={0.7} x={2.1} y={0.3} z={1.0} color="#8a6d4d" />
          {track(0.82)}
          {track(-0.82)}
          <mesh shape="box" w={1.3} h={0.32} d={1.1} y={0.62} color={DARK} />
          <mesh shape="cylinder" r={0.55} h={0.12} y={0.82} color={DARK} />
          <group y={0.88} ry={swing}>
            <mesh shape="box" w={2.6} h={0.2} d={2.0} x={-0.25} y={0.1} color={YELLOW} />
            <mesh shape="box" w={1.35} h={0.8} d={1.9} x={-0.85} y={0.6} color={YELLOW} />
            <mesh shape="box" w={0.45} h={0.7} d={2.0} x={-1.5} y={0.55} color={YELLOW} />
            <mesh shape="box" w={0.5} h={0.12} d={1.9} x={-0.85} y={1.06} color={DARK} />
            <mesh shape="cylinder" r={0.06} h={0.5} x={-0.6} y={1.25} z={-0.5} color={DARK} />
            <group x={0.5} y={0.2} z={0.5}>
              <mesh shape="box" w={0.95} h={1.15} d={0.85} y={0.58} color={YELLOW} />
              <mesh shape="box" w={0.97} h={0.62} d={0.7} y={0.78} color="#1d2a38" metal={0.7} />
              <mesh shape="box" w={0.8} h={0.62} d={0.87} y={0.78} color="#1d2a38" metal={0.7} />
              <mesh shape="box" w={1.0} h={0.08} d={0.9} y={1.18} color={YELLOW} />
            </group>
            <group x={0.55} y={0.5} z={-0.2} rz={boom}>
              <mesh shape="box" w={2.8} h={0.34} d={0.32} x={1.4} color={YELLOW} />
              <mesh shape="cylinder" r={0.07} h={1.5} rz={90} x={0.9} y={-0.28} color={STEEL} metal={0.8} />
              <mesh shape="cylinder" r={0.09} h={0.4} rx={90} color={DARK} />
              <group x={2.8} rz={stick}>
                <mesh shape="box" w={2.0} h={0.26} d={0.26} x={1.0} color={YELLOW} />
                <mesh shape="cylinder" r={0.06} h={1.3} rz={90} x={0.9} y={0.22} color={STEEL} metal={0.8} />
                <mesh shape="cylinder" r={0.08} h={0.36} rx={90} color={DARK} />
                <group x={2.0} rz={bucket}>
                  <mesh shape="cylinder" r={0.08} h={0.8} rx={90} color={DARK} />
                  <mesh shape="cylinder" r={0.36} h={0.78} rx={90} x={0.32} y={-0.08} color={YELLOW} />
                  <mesh shape="box" w={0.62} h={0.1} d={0.8} x={0.32} y={0.25} color={YELLOW} />
                  {[-0.27, -0.09, 0.09, 0.27].map((z) => <mesh shape="cone" r={0.06} h={0.22} rz={90} x={0.76} y={0.18} z={z} color={STEEL} />)}
                </group>
              </group>
            </group>
          </group>
        </group>
      </scene3d>
    </div>
  );
}
