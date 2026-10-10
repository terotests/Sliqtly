// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Each word of the sentence rises into its place on its own.

const words = find("p.lead word");
let t = 0;

function tick(dt: number) {
  t += dt;
  words.each((w, i) => {
    const k = Math.min(1, Math.max(0, (t - i * 0.12) / 0.5));
    const ease = 1 - (1 - k) * (1 - k);
    w.set({ y: w.box.y + (1 - ease) * 40, opacity: ease });
  });
}

function final() {}
