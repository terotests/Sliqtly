// The presentation player as one page (web/dist/player.html): the site's
// own page with every file it fetches inside it, so an exported deck plays
// from disk with no network (File → Export ▸ Presentation player,
// web/player-file.js).
//
//   index.html ── its three scripts ──► one loader
//              ── fonts, sheets, themes, translations, the compiled engine,
//                 main.js bundled with its modules ──► gzip + base64 in
//                 <script type="application/json" id="sliqtlyAssets">
//              ── <!--sliqtly-deck--> before </body>, where the export puts
//                 the deck
//
// Built from web/dist after the page's own stamps are written and before
// the module imports are (esbuild resolves the plain paths).
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { createRequire } from "node:module";

const SLOT = "<!--sliqtly-deck-->";
const TYPES = { ".css": "text/css", ".js": "text/javascript", ".json": "application/json", ".ttf": "font/ttf" };

/** Writes `player.html` in `dist`; returns its size, or 0 without esbuild. */
export function buildPlayer(dist) {
  let esbuild;
  try { esbuild = createRequire(import.meta.url)("esbuild"); } catch (_) { return 0; }
  const read = (f) => fs.readFileSync(path.join(dist, f));
  const assets = {};
  const put = (key, bytes, type) => {
    assets[key] = { type, gz: true, b64: zlib.gzipSync(bytes, { level: 9 }).toString("base64") };
  };
  // main.js with every module it imports, as one module; the page's
  // cloud script (sliqtly.js) stays out: a file has no account and no share
  const main = esbuild.buildSync({
    entryPoints: [path.join(dist, "main.js")],
    bundle: true,
    format: "esm",
    minify: true,
    keepNames: true,
    legalComments: "none",
    write: false,
    logLevel: "silent",
  }).outputFiles[0].contents;
  put("main.js", main, TYPES[".js"]);
  for (const f of ["pres_app.js", "pres_data.js", "pres.css", "ui.css", "chart-editor.css", "hint.css", "panels.css"]) {
    put(f, read(f), TYPES[path.extname(f)]);
  }
  for (const dir of ["skins", "themes", "fonts", "i18n"]) {
    for (const f of fs.readdirSync(path.join(dist, dir)).sort()) {
      const type = TYPES[path.extname(f)];
      if (type) put(dir + "/" + f, read(dir + "/" + f), type);
    }
  }

  const icon = "data:image/svg+xml;base64," + read("favicon.svg").toString("base64");
  let html = read("index.html").toString("utf8");
  // the faces come from the table, not ahead of it from the network
  html = html.replace(/^.*<link rel="preload"[^>]*\.ttf[^>]*>\s*\n/gm, "");
  html = html.split("./favicon.svg").join(icon);
  const scripts = /<script src="\.\/pres_app\.js[^"]*"><\/script>\s*\n<script type="module" src="\.\/main\.js[^"]*"><\/script>\s*\n<script type="module" src="\.\/sliqtly\.js[^"]*"><\/script>\s*\n/;
  if (!scripts.test(html)) throw new Error("player: index.html's scripts are not where player.mjs expects them");
  // JSON inside a script element: no "<" in it may end the element
  const table = JSON.stringify(assets).replace(/</g, "\\u003c");
  const loader = `<script type="application/json" id="sliqtlyAssets">${table}</script>
<script type="module">
// the engine (a classic script) first, then the page's modules, both from the table above
const table = JSON.parse(document.getElementById("sliqtlyAssets").textContent);
async function own(key) {
  const bin = atob(table[key].b64);
  const raw = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) raw[i] = bin.charCodeAt(i);
  const text = await new Response(new Blob([raw]).stream().pipeThrough(new DecompressionStream("gzip"))).text();
  return URL.createObjectURL(new Blob([text], { type: "text/javascript" }));
}
const engine = document.createElement("script");
engine.src = await own("pres_app.js");
await new Promise((done, bad) => { engine.onload = done; engine.onerror = bad; document.head.appendChild(engine); });
await import(await own("main.js"));
</script>
`;
  html = html.replace(scripts, loader);
  html = html.replace("</body>", SLOT + "\n</body>");
  fs.writeFileSync(path.join(dist, "player.html"), html);
  return html.length;
}
