// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Each word of the sentence rises into its place on its own. start() puts
// them below and hidden, so the slide arrives without them.
import { presentation } from "Sliqtly";

const words = presentation.activeSlide.find("p.lead word");
let t = 0;

function pose() {
  words.each((w, i) => {
    const k = Math.min(1, Math.max(0, (t - i * 0.12) / 0.5));
    const ease = 1 - (1 - k) * (1 - k);
    w.set({ y: w.box.y + (1 - ease) * 40, opacity: ease });
  });
}

export function start() {
  pose();
}

export function tick(dt: number) {
  t += dt;
  pose();
}

export function final() {}
