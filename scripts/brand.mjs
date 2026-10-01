// Puts Sliqtly's name and icon on the editor built into app/web/dist:
// the page title, the bar's name (which the canvas bar and the canvas's
// accessible label take from index.html), the favicon, and the PRO button.
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), "..");
const dist = path.join(root, "app/web/dist");
const html = path.join(dist, "index.html");
let page = fs.readFileSync(html, "utf8");

function swap(re, to, what) {
  if (!re.test(page)) throw new Error(`brand: ${what} not found in index.html`);
  page = page.replace(re, to);
}
swap(/<title>[^<]*<\/title>/, "<title>Sliqtly</title>", "the title");
swap(/<span class="brand">[^<]*<\/span>/, '<span class="brand">Sliqtly</span>', "the bar's name");
swap(/<link rel="icon"[^>]*>/, '<link rel="icon" type="image/svg+xml" href="./favicon.svg" />', "the icon");

// PRO: Google sign-in (web/sliqtly.js), a button the canvas bar draws too
swap(/(<span class="grow"><\/span>)/,
  '<button id="pro" data-canvas="accent" title="Kirjaudu Google-tilillä">PRO</button>\n  $1', "the bar's end");
swap(/(<\/body>)/, '<script type="module" src="./sliqtly.js"></script>\n$1', "</body>");

fs.writeFileSync(html, page);
fs.copyFileSync(path.join(root, "web/sliqtly.js"), path.join(dist, "sliqtly.js"));
fs.copyFileSync(path.join(root, "brand/sliqtly-icon.svg"), path.join(dist, "favicon.svg"));
console.log("brand  app/web/dist (Sliqtly)");
