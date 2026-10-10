// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The work goes round the diagram: one node at a time is lit, with the
// arrow that led to it.
import { presentation } from "Sliqtly";

const slide = presentation.activeSlide;
const nodes = slide.find("diagram node");
const edges = slide.find("diagram edge");
let t = 0;

function pose() {
  const at = Math.floor(t / 0.9) % nodes.length;
  nodes.each((n, i) => n.set({ scale: i === at ? 1.12 : 1, opacity: i === at ? 1 : 0.55 }));
  edges.each((e, i) => e.set({ opacity: i === at - 1 ? 1 : 0.35 }));
}

// the slide arrives with the first node lit
export function start() {
  pose();
}

export function tick(dt: number) {
  t += dt;
  pose();
}

// the thumbnail and the PDF show every node as the Markdown has it
export function final() {}
