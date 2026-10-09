// The theme picker's pictures (Slide → Theme…, PresChartEditor "themes"):
// what still has to be drawn and where a slide sits in its tile's picture.
// The drawing is main.js's (themePictures); this has no page.

// The picture a theme's tile shows, as PresThemeGallery.picture names it.
export function themePicture(key) {
  return "/__theme/" + (key || "-");
}

// The themes of the select's rows ("value\tlabel\tgroup") that have no
// picture yet, or one drawn in another language (the sample slide's words
// are the interface's), in the rows' order.
export function picturesToDraw(rows, drawn, lang) {
  const out = [];
  for (const line of rows.split("\n")) {
    const [key, label] = line.split("\t");
    if (label === undefined || !label.trim()) continue;
    const k = key.trim();
    if (drawn.get(themePicture(k)) !== lang) out.push(k);
  }
  return out;
}

// A page of pageW × pageH drawn whole into a box of boxW × boxH, in the
// middle: its scale and corner (a portrait page gets bands at its sides).
export function fitPage(pageW, pageH, boxW, boxH) {
  if (!(pageW > 0) || !(pageH > 0)) return { s: 1, x: 0, y: 0 };
  const s = Math.min(boxW / pageW, boxH / pageH);
  return { s, x: (boxW - pageW * s) / 2, y: (boxH - pageH * s) / 2 };
}
