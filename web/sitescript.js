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

export const STALE = "Sliqtly was updated. Reload the page to go on.";

/**
 * Null when the script that ran is from the build that asked for it (src's
 * ?v=), else the error to give: a page opened before a deploy gets the
 * server's newest copy, whatever stamp it asks for, and the two do not fit
 * together. `built`: the build the script named (scripts/stamp.mjs
 * markBuilt); none named, nothing to compare.
 */
export function staleScript(src, built) {
  const m = /[?&]v=([^&#]+)/.exec(src);
  const asked = m ? m[1] : "";
  if (!asked || !built || asked.startsWith("__") || asked === built) return null;
  const e = new Error(STALE);
  e.stale = true;
  return e;
}

// A classic script beside the modules, run once its promise resolves.
export function loadScript(name) {
  const src = besideUs(name) || besidePage(name);
  const checked = () => {
    const stale = staleScript(src, globalThis.__sliqtlyBuilt && globalThis.__sliqtlyBuilt[name]);
    if (stale) throw stale;
  };
  if (globalThis.__sliqtlyLoadScript) return Promise.resolve(globalThis.__sliqtlyLoadScript(src)).then(checked);
  return new Promise((ok, bad) => {
    const s = document.createElement("script");
    s.src = src;
    s.onload = ok;
    s.onerror = () => bad(new Error("could not load " + name));
    document.head.appendChild(s);
  }).then(checked);
}

// A module worker's script beside the modules.
export function workerUrl(name) {
  const own = besideUs(name);
  if (own) return own;
  const made = globalThis.__sliqtlyWorkerUrl && globalThis.__sliqtlyWorkerUrl(name);
  if (!made) throw new Error(name + ": no worker here");
  return made;
}
