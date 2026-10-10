// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The chart's bars grow from the axis one after another, and the items
// below come in when their bar has. start() sets where they begin, so the
// slide arrives with the bars flat and the items hidden.
import { presentation } from "Sliqtly";

const slide = presentation.activeSlide;
const bars = slide.find("chart:1 bar");
const items = slide.find("li");
let t = 0;

function pose() {
  bars.each((b, i) => {
    const k = Math.min(1, Math.max(0, (t - i * 0.25) / 0.6));
    b.set({ scaleY: 0.001 + k * 0.999, origin: "bottom" });
  });
  items.each((e, i) => e.set({ opacity: Math.min(1, Math.max(0, t - 0.8 - i * 0.3)) }));
}

export function start() {
  pose();
}

export function tick(dt: number) {
  t += dt;
  pose();
}

// the thumbnail and the PDF show the chart as the Markdown has it
export function final() {}
