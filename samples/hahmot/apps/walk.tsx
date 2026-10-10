// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A robot walks along the first point of the list, jumps to the second and
// from there up onto the heading.

sprites.sheet("robot", {
  src: "sprites/robot.png",
  grid: [8, 1],
  frame: [20, 20],
  feet: 1,
  anims: {
    idle: { from: 0, frames: 2, fps: 1.5 },
    walk: { from: 2, frames: 4, fps: 8 },
    jump: { from: 6, frames: 2, loop: false },
  },
});

function start() {
  const items = find("li");
  const robot = sprites.add("robot", { on: items[0], along: 0.05, size: 50 });
  robot.walkTo(items[0], { at: 0.9 })
    .jump(items[1], { at: 0.2 })
    .walkTo(items[1], { at: 0.8 })
    .jump(find("h2"), { at: 0.95 })
    .say("Hei!")
    .face("left");
}
