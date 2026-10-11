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

// The 3-D flight's looks among the rows (PresApp.flightLookRows: a fourth
// cell "glow;air;grid", each "r,g,b" 0..255): each tile's key and colours.
export function lookTiles(rows) {
  const out = [];
  for (const line of rows.split("\n")) {
    const [key, , , cs] = line.split("\t");
    if (!key || !key.startsWith("look3d:") || !cs) continue;
    const [glow, air, grid] = cs.split(";").map((c) => c.split(",").map(Number));
    if (![glow, air, grid].every((c) => c && c.length === 3 && c.every((v) => v >= 0 && v <= 255))) continue;
    out.push({ key: key.trim(), glow, air, grid });
  }
  return out;
}

// A look's tile picture onto a 2-D context w × h: its air, its grid going
// off into the distance, and a glowing screen with corner brackets.
export function drawLookTile(g, w, h, look) {
  const rgb = (c, a) => `rgba(${c[0]},${c[1]},${c[2]},${a})`;
  g.fillStyle = rgb(look.air.map((v) => Math.max(v, 4)), 1);
  g.fillRect(0, 0, w, h);
  // the floor: lines to a vanishing point, and across
  g.strokeStyle = rgb(look.grid, 0.55);
  g.lineWidth = Math.max(1, w / 220);
  const hy = h * 0.62;
  for (let i = -6; i <= 6; i++) {
    g.beginPath();
    g.moveTo(w / 2 + i * w * 0.02, hy);
    g.lineTo(w / 2 + i * w * 0.22, h);
    g.stroke();
  }
  for (let j = 0; j < 4; j++) {
    const y = hy + (h - hy) * ((j + 1) / 4) ** 1.6;
    g.beginPath();
    g.moveTo(0, y);
    g.lineTo(w, y);
    g.stroke();
  }
  // the screen
  const x0 = w * 0.2, y0 = h * 0.14, sw = w * 0.6, sh = h * 0.5;
  g.fillStyle = rgb(look.glow, 0.1);
  g.fillRect(x0, y0, sw, sh);
  g.shadowColor = rgb(look.glow, 0.9);
  g.shadowBlur = w / 40;
  g.strokeStyle = rgb(look.glow, 0.95);
  g.lineWidth = Math.max(1, w / 160);
  g.strokeRect(x0, y0, sw, sh);
  const k = sw * 0.12;
  g.lineWidth = Math.max(1.5, w / 110);
  for (const [cx, cy, dx, dy] of [[x0, y0, 1, 1], [x0 + sw, y0, -1, 1], [x0, y0 + sh, 1, -1], [x0 + sw, y0 + sh, -1, -1]]) {
    const ox = cx - dx * w * 0.02, oy = cy - dy * w * 0.02;
    g.beginPath();
    g.moveTo(ox + dx * k, oy);
    g.lineTo(ox, oy);
    g.lineTo(ox, oy + dy * k);
    g.stroke();
  }
  // a heading and two lines of words
  g.fillStyle = rgb(look.glow.map((v) => Math.round(v * 0.6 + 102)), 1);
  g.fillRect(x0 + sw * 0.1, y0 + sh * 0.2, sw * 0.5, sh * 0.12);
  g.fillRect(x0 + sw * 0.1, y0 + sh * 0.5, sw * 0.7, sh * 0.06);
  g.fillRect(x0 + sw * 0.1, y0 + sh * 0.66, sw * 0.55, sh * 0.06);
  g.shadowBlur = 0;
}
