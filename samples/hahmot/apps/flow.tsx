// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A robot walks the flowchart: it starts on the first box, hops from box to
// box along the work and says what happens in each. Its picture is the
// deck's own spritesheet (sprites/robot.png, scripts/sample-sprites.mjs).
import { presentation, sprites } from "Sliqtly";

const slide = presentation.activeSlide;

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

const boxes = slide.find("diagram node");
let robot = null;

export function start() {
  robot = sprites.add("robot", { on: boxes[0], size: 60 });
  robot.say(boxes[0].text, { secs: 1.6 });
  for (let i = 1; i < boxes.length; i++) {
    const box = boxes[i];
    robot.walkTo(box)
      .call(() => box.set({ scale: 1.08 }))
      .say(box.text, { secs: 1.6 });
  }
  robot.play("idle", 1);
}
