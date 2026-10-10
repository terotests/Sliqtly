// SPDX-License-Identifier: AGPL-3.0-or-later
//
// An excavator from plain shapes, digging. A <group> moves everything in it
// as one, in its own frame, so the arm is three groups one inside the other:
// the boom turns on the body, the stick on the boom's end, the bucket on the
// stick's. The upper body is a group that swings on the tracks.
//
// The hydraulic cylinders follow the arm: an empty group with a name is a
// pin, and <SliqRod from="pin" to="pin"> is a rod between two pins wherever
// their groups have moved them; `length` stops it short, so a barrel from
// one pin and a piston from the other slide along each other. Click to stop.

let t = 0;
let run = 1;

function tick(dt: number) {
  t += dt * run;
}

function onPointerDown() {
  run = run === 1 ? 0 : 1;
}

const DEG = Math.PI / 180;
const YELLOW = "#f5c400";
const DARK = "#26272b";
const STEEL = "#c9ced6";

function paint(color: string) {
  return <meshStandardMaterial color={color} roughness={0.55} />;
}

function steel() {
  return <meshStandardMaterial color={STEEL} metalness={0.8} roughness={0.3} />;
}

// a box of w x h x d at [x, y, z]
function block(w: number, h: number, d: number, at: number[], color: string) {
  return (
    <mesh position={at}>
      <boxGeometry args={[w, h, d]} />
      {paint(color)}
    </mesh>
  );
}

// a wheel or a pin across the tracks (a cylinder turned to lie along z)
function axle(r: number, len: number, at: number[], material: any) {
  return (
    <mesh position={at} rotation={[Math.PI / 2, 0, 0]}>
      <cylinderGeometry args={[r, r, len, 32]} />
      {material}
    </mesh>
  );
}

// a hydraulic cylinder from pin a to pin b: a barrel and a piston
function cylinder(a: string, b: string, barrel: number) {
  return [
    <SliqRod from={a} to={b} radius={0.075} length={barrel} color={YELLOW} roughness={0.55} />,
    <SliqRod from={b} to={a} radius={0.04} length={barrel} color={STEEL} metalness={0.8} roughness={0.3} />,
  ];
}

function track(z: number) {
  return (
    <group position={[0, 0.32, z]}>
      {block(3.0, 0.56, 0.55, [0, 0, 0], DARK)}
      {axle(0.28, 0.55, [-1.5, 0, 0], paint(DARK))}
      {axle(0.28, 0.55, [1.5, 0, 0], paint(DARK))}
      {axle(0.2, 0.58, [-1.5, 0, 0], steel())}
      {axle(0.2, 0.58, [1.5, 0, 0], steel())}
      {[-0.9, -0.3, 0.3, 0.9].map((x) => axle(0.11, 0.6, [x, -0.12, 0], steel()))}
    </group>
  );
}

function view() {
  // one dig: reach out, scoop, lift, swing to the pile and back
  const c = t * 0.9;
  const swing = (-45 + 45 * Math.sin(c * 0.5)) * DEG;
  const boom = (38 + 16 * Math.sin(c)) * DEG;
  const stick = (-100 + 30 * Math.sin(c + 1.2)) * DEG;
  const bucket = (-75 + 45 * Math.sin(c + 2.2)) * DEG;
  return (
    <div className="stage">
      <scene3d className="world">
        <perspectiveCamera position={[2.6, 3.4, 9.5]} fov={40} lookAt={[0.8, 1.7, 0]} />
        <ambientLight intensity={0.6} />
        <directionalLight position={[0.6, 1, 0.8]} intensity={3} />
        <directionalLight position={[-0.8, 0.3, -0.4]} intensity={0.9} />
        <group rotation={[0, -15 * DEG, 0]}>
          <mesh position={[0, -0.03, 0]}>
            <cylinderGeometry args={[3.0, 3.0, 0.06, 64]} />
            {paint("#7a6248")}
          </mesh>
          <mesh position={[2.1, 0.3, 1.0]}>
            <coneGeometry args={[0.9, 0.7, 48]} />
            {paint("#8a6d4d")}
          </mesh>
          {track(0.82)}
          {track(-0.82)}
          {block(1.3, 0.32, 1.1, [0, 0.62, 0], DARK)}
          <mesh position={[0, 0.82, 0]}>
            <cylinderGeometry args={[0.55, 0.55, 0.12, 48]} />
            {paint(DARK)}
          </mesh>
          <group position={[0, 0.88, 0]} rotation={[0, swing, 0]}>
            {block(2.6, 0.2, 2.0, [-0.25, 0.1, 0], YELLOW)}
            {block(1.35, 0.8, 1.9, [-0.85, 0.6, 0], YELLOW)}
            {block(0.45, 0.7, 2.0, [-1.5, 0.55, 0], YELLOW)}
            {block(0.5, 0.12, 1.9, [-0.85, 1.06, 0], DARK)}
            <mesh position={[-0.6, 1.25, -0.5]}>
              <cylinderGeometry args={[0.06, 0.06, 0.5, 24]} />
              {paint(DARK)}
            </mesh>
            <group position={[0.5, 0.2, 0.5]}>
              {block(0.95, 1.15, 0.85, [0, 0.58, 0], YELLOW)}
              <mesh position={[0, 0.78, 0]}>
                <boxGeometry args={[0.97, 0.62, 0.7]} />
                <meshStandardMaterial color="#1d2a38" metalness={0.7} roughness={0.15} />
              </mesh>
              {block(1.0, 0.08, 0.9, [0, 1.18, 0], YELLOW)}
            </group>
            <group name="boomFoot" position={[0.25, 0.2, -0.2]} />
            <group position={[0.55, 0.5, -0.2]} rotation={[0, 0, boom]}>
              {block(2.8, 0.34, 0.32, [1.4, 0, 0], YELLOW)}
              {axle(0.09, 0.4, [0, 0, 0], paint(DARK))}
              <group name="boomEye" position={[1.3, -0.24, 0]} />
              <group name="stickFoot" position={[1.9, 0.26, 0]} />
              <group position={[2.8, 0, 0]} rotation={[0, 0, stick]}>
                {block(2.0, 0.26, 0.26, [1.0, 0, 0], YELLOW)}
                {block(0.5, 0.22, 0.24, [-0.25, 0.08, 0], YELLOW)}
                {axle(0.08, 0.36, [0, 0, 0], paint(DARK))}
                <group name="stickEye" position={[-0.45, 0.14, 0]} />
                <group name="bucketFoot" position={[0.5, 0.22, 0]} />
                <group position={[2.0, 0, 0]} rotation={[0, 0, bucket]}>
                  {axle(0.08, 0.8, [0, 0, 0], paint(DARK))}
                  <mesh position={[0.32, -0.08, 0]} rotation={[Math.PI / 2, 0, 0]}>
                    <cylinderGeometry args={[0.36, 0.36, 0.78, 48]} />
                    {paint(YELLOW)}
                  </mesh>
                  {block(0.62, 0.1, 0.8, [0.32, 0.25, 0], YELLOW)}
                  <group name="bucketEye" position={[0.05, 0.3, 0]} />
                  {[-0.27, -0.09, 0.09, 0.27].map((z) => (
                    <mesh position={[0.76, 0.18, z]} rotation={[0, 0, -Math.PI / 2]}>
                      <coneGeometry args={[0.06, 0.22, 16]} />
                      {steel()}
                    </mesh>
                  ))}
                </group>
              </group>
            </group>
            {cylinder("boomFoot", "boomEye", 0.75)}
            {cylinder("stickFoot", "stickEye", 0.75)}
            {cylinder("bucketFoot", "bucketEye", 0.6)}
          </group>
        </group>
      </scene3d>
    </div>
  );
}
