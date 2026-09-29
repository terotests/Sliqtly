#!/usr/bin/env node
/**
 * npm run build: compile PresApp.rgr and assemble web/dist.
 *
 * The output is static — any file server can serve it — and `npm start`
 * serves it together with the local API the later stages add (TTS, agent).
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { ensureRanger, compile, root, webDir, distDir, log } from "./lib.mjs";
import { formatCss } from "./format-css.mjs";

export function build({ ranger } = {}) {
  ranger = ranger || ensureRanger();
  fs.mkdirSync(distDir, { recursive: true });
  const appJs = path.join(distDir, "pres_app.js");
  compile(ranger, "PresApp.rgr", appJs);

  // Loaded without require(), beside ES modules: publish one name.
  const src = fs.readFileSync(appJs, "utf8");
  fs.writeFileSync(appJs, "// scoped: the page loads this beside other scripts, so it publishes one name.\n"
    + "(function () {\n" + src + "\n;globalThis.PresApp = PresApp;\n})();\n");

  const copy = (from, to) => {
    fs.mkdirSync(path.dirname(to), { recursive: true });
    fs.copyFileSync(from, to);
  };
  for (const f of ["index.html", "main.js", "pres.css", "chart-editor.css", "toolbar.css", "hint.css", "panels.css"]) copy(path.join(webDir, f), path.join(distDir, f));
  // the controls' own theme, for the chart editor
  copy(path.join(ranger, "gallery/ui/theme/base.css"), path.join(distDir, "ui.css"));
  copy(path.join(ranger, "lib/evg/gl/evg-webgl.js"), path.join(distDir, "gl/evg-webgl.js"));
  for (const face of ["OpenSans-Regular", "OpenSans-Bold", "OpenSans-Italic", "OpenSans-BoldItalic"]) {
    copy(path.join(ranger, `gallery/pdf_writer/assets/fonts/Open_Sans/${face}.ttf`), path.join(distDir, `fonts/${face}.ttf`));
  }
  for (const face of ["NotoSans-Regular", "NotoSans-Bold"]) {
    copy(path.join(ranger, `gallery/pdf_writer/assets/fonts/Noto_Sans/${face}.ttf`), path.join(distDir, `fonts/${face}.ttf`));
  }
  copy(path.join(ranger, "gallery/pdf_writer/assets/fonts/Noto_Emoji/NotoEmoji-Regular.ttf"), path.join(distDir, "fonts/NotoEmoji-Regular.ttf"));
  for (const f of fs.readdirSync(path.join(root, "themes"))) copy(path.join(root, "themes", f), path.join(distDir, "themes", f));
  // GitHub Pages serves the directory as it is; no Jekyll pass over it.
  fs.writeFileSync(path.join(distDir, ".nojekyll"), "");
  for (const t of ["corporate", "editorial"]) {
    // one declaration per line, as the editor's CSS tab shows our own themes
    fs.mkdirSync(path.join(distDir, "themes"), { recursive: true });
    fs.writeFileSync(path.join(distDir, `themes/${t}.css`),
      formatCss(fs.readFileSync(path.join(ranger, `gallery/markdown/fixtures/themes/${t}.css`), "utf8")));
  }
  for (const f of fs.readdirSync(path.join(root, "samples"))) copy(path.join(root, "samples", f), path.join(distDir, "samples", f));
  copy(path.join(ranger, "gallery/markdown/fixtures/deck.md"), path.join(distDir, "samples/deck.md"));

  // Every URL the page loads carries the hash of the build, so a reload
  // never mixes an old script with a new one.
  const h = crypto.createHash("sha1");
  for (const f of ["pres_app.js", "main.js", "pres.css", "chart-editor.css", "toolbar.css", "hint.css", "panels.css", "ui.css", "gl/evg-webgl.js"]) h.update(fs.readFileSync(path.join(distDir, f)));
  for (const f of fs.readdirSync(path.join(distDir, "themes"))) h.update(fs.readFileSync(path.join(distDir, "themes", f)));
  const stamp = h.digest("hex").slice(0, 10);
  const html = path.join(distDir, "index.html");
  fs.writeFileSync(html, fs.readFileSync(html, "utf8").split("__BUILD__").join(stamp));
  const main = path.join(distDir, "main.js");
  fs.writeFileSync(main, fs.readFileSync(main, "utf8")
    .replace("./gl/evg-webgl.js", "./gl/evg-webgl.js?v=" + stamp)
    .split("__BUILD__").join(stamp));
  log(`build  web/dist (${stamp})`);
  return stamp;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    build();
  } catch (e) {
    log(e.message);
    process.exit(1);
  }
}
