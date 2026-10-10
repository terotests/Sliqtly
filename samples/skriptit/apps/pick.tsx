// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Click an item while presenting: it comes forward and the others step
// back. onEnter says which slide the presenter came from.
import { presentation } from "Sliqtly";

const items = presentation.activeSlide.find("li");
let picked = -1;

export function onEnter(from: number) {
  if (from > 0) console.log("arrived from slide " + from);
}

export function onClick(e: any) {
  let at = e;
  while (at && at.kind !== "li") at = at.parent;
  picked = at ? items.indexOf(at) : -1;
  items.each((it, i) => {
    if (picked < 0) it.reset();
    else if (i === picked) it.set({ scale: 1.08, opacity: 1, origin: "left" });
    else it.set({ scale: 1, opacity: 0.4 });
  });
}

// on the way out the list fades, so the next slide comes in clean
export function onLeave(to: number) {
  items.set({ opacity: 0.2 });
}
