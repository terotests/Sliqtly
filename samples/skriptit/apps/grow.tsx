// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The chart's bars grow from the axis one after another, and the items
// below come in when their bar has.

const bars = find("chart:1 bar");
const items = find("li");
let t = 0;

function tick(dt: number) {
  t += dt;
  bars.each((b, i) => {
    const k = Math.min(1, Math.max(0, (t - i * 0.25) / 0.6));
    b.set({ scaleY: 0.001 + k * 0.999, origin: "bottom" });
  });
  items.each((e, i) => e.set({ opacity: Math.min(1, Math.max(0, t - 0.8 - i * 0.3)) }));
}

function final() {}
