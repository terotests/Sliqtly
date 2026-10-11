// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The files that sit beside the modules (the engines loaded on demand, the
// programs' worker), named with the build's stamp (?v=…) the modules carry.
//
// An assistant's preview (mcp-go/assets/preview.html) runs the modules from
// data: URLs, which no name resolves against. There a script goes by its
// relative name to the preview's loader (window.__sliqtlyLoadScript, which
// fetches it from the site), and a worker comes from the preview, which
// made it beforehand (window.__sliqtlyWorkerUrl: a page whose origin is not
// the site's may start no worker from the site).

function besideUs(name) {
  try {
    return new URL("./" + name + new URL(import.meta.url).search, import.meta.url).href;
  } catch (_) {
    return "";
  }
}

// The name relative to the page, with the page's build stamp (the
// viewer's <meta name="build">), for the preview's loader.
function besidePage(name) {
  const meta = globalThis.document && document.querySelector('meta[name="build"]');
  const build = meta ? meta.content : "";
  return "./" + name + (build && !build.startsWith("__") ? "?v=" + build : "");
}

// The hash each on-demand script of this build carries (filled in by
// scripts/build.mjs markParts; the placeholder when run from the sources).
const PARTS = "__PARTS__";

// After a deploy the host serves only the new files, whatever ?v= a page
// asks with: a page open since an older build gets a script made for other
// modules. Its mark says so, and the page asks to be reloaded.
function sameBuild(name) {
  const want = typeof PARTS === "object" ? PARTS[name] : "";
  const got = globalThis.__sliqtlyParts && globalThis.__sliqtlyParts[name];
  return !want || !got || want === got;
}

// A classic script beside the modules, run once its promise resolves.
export function loadScript(name) {
  const src = besideUs(name) || besidePage(name);
  const checked = () => {
    if (!sameBuild(name)) throw new Error("Sliqtly was updated: reload the page");
  };
  if (globalThis.__sliqtlyLoadScript) return globalThis.__sliqtlyLoadScript(src).then(checked);
  return new Promise((ok, bad) => {
    const s = document.createElement("script");
    s.src = src;
    s.onload = () => {
      try {
        checked();
        ok();
      } catch (e) {
        bad(e);
      }
    };
    s.onerror = () => bad(new Error("could not load " + name));
    document.head.appendChild(s);
  });
}

// A module worker's script beside the modules.
export function workerUrl(name) {
  const own = besideUs(name);
  if (own) return own;
  const made = globalThis.__sliqtlyWorkerUrl && globalThis.__sliqtlyWorkerUrl(name);
  if (!made) throw new Error(name + ": no worker here");
  return made;
}
