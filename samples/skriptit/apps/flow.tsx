// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The work goes round the diagram: one node at a time is lit, with the
// arrow that led to it.

const nodes = find("diagram node");
const edges = find("diagram edge");
let t = 0;

function tick(dt: number) {
  t += dt;
  const at = Math.floor(t / 0.9) % nodes.length;
  nodes.each((n, i) => n.set({ scale: i === at ? 1.12 : 1, opacity: i === at ? 1 : 0.55 }));
  edges.each((e, i) => e.set({ opacity: i === at - 1 ? 1 : 0.35 }));
}

// the thumbnail and the PDF show every node as the Markdown has it
function final() {}
