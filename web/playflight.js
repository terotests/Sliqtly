// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The 3-D flight in the light player (web/play.js): a recording exported
// with it (Export ▸ Cloud HTML, version 3-D) carries the world as the
// editor's PresFlight made it, the slides' 3-D worlds as their programs
// last drew them and the pictures its screens and presenters show
// (web/main.js recordFlight). The same flight as presenting in the editor
// (web/flight3d.js, pres_3d.js) flies it; nothing is laid out here and no
// program runs: everything stands as it was exported.

import { createFlight3d } from "./flight3d.js";
import { createThree3d } from "./three3d.js";

// what the flight says, in Finnish when the reader's browser is
const FI = {
  "The 3-D flight needs WebGL": "3D-lento tarvitsee WebGL:n",
  "The 3-D flight needs full screen": "3D-lento tarvitsee koko näytön",
  "The 3-D flight could not start: ": "3D-lento ei käynnistynyt: ",
  "The 3-D flight stopped: ": "3D-lento pysähtyi: ",
  "Space / → next · ← back · W A S D fly · Q E turn · V layout · Shift fast · Esc leaves": "Välilyönti / → seuraava · ← takaisin · W A S D lennä · Q E käänny · V asettelu · Shift nopeasti · Esc poistuu",
  "Layout: path": "Asettelu: polku",
  "Layout: wall": "Asettelu: seinä",
  "Layout: ring": "Asettelu: kehä",
  "Layout: situation room": "Asettelu: tilannehuone",
};

/** The flight's words: `fi` the reader's language is Finnish. */
export function flightText(fi) {
  return (s) => (fi && FI[s]) || s;
}

/**
 * flight: the recording's `flight` ({ json, scenes, pictures: [{ src, type,
 * b64 }] }); slide() the slide shown, go(i) shows slide i; toast(text)
 * says something a moment; onExit() back in the slides. → { start(),
 * flying() }, start() from a press (it takes the full screen).
 */
export function createPlayFlight({ flight, slide, go, toast, fi, onExit }) {
  const t = flightText(fi);
  const pics = new Map();
  const loading = Promise.all((flight.pictures || []).map(async (p) => {
    try {
      const bin = atob(p.b64);
      const bytes = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
      pics.set(p.src, await createImageBitmap(new Blob([bytes], { type: p.type })));
    } catch (e) {
      console.warn("3-D flight picture not shown: " + p.src, e);
    }
  }));
  // a picture as ImageData, its long side at most `max` px
  const pixels = (src, max) => {
    const img = pics.get(src);
    if (!img) return null;
    const k = Math.min(1, max / Math.max(img.width, img.height));
    const c = document.createElement("canvas");
    c.width = Math.max(1, Math.round(img.width * k));
    c.height = Math.max(1, Math.round(img.height * k));
    const g = c.getContext("2d", { willReadFrequently: true });
    g.drawImage(img, 0, 0, c.width, c.height);
    return g.getImageData(0, 0, c.width, c.height);
  };
  // the editor's app as far as the flight asks it: what was exported
  const app = {
    flightJson: () => flight.json,
    allPlayScenesJson: () => flight.scenes || '{"scenes":[]}',
    // no program runs here: the worlds stand as seeded, none waits
    playScenesJson: () => "[]",
    playsWaiting: () => true,
    selectedSlide: () => slide(),
    selectSlide: (i) => go(i),
  };
  const three3d = createThree3d({
    app, pictures: new Map(), imageChanged: () => {}, slidePicture: () => null, scale: 1,
    repaint: () => {}, toast, readFile: async () => null, t,
  });
  const ride = createFlight3d({
    three3d, app, toast, t,
    picturePixels: pixels,
    spritePixels: (spec) => {
      try {
        return pixels(JSON.parse(spec).src, 768);
      } catch (_) {
        return null;
      }
    },
    onExit,
  });
  return {
    async start() {
      // the press still counts for the full screen while the pictures load
      const go = ride.start();
      await loading;
      return go;
    },
    flying: () => ride.flying(),
  };
}
