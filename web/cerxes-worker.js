// SPDX-License-Identifier: AGPL-3.0-or-later
//
// One program of a slide (```app) in CErXes (componentengine's cerxes-wasm),
// in a Worker of its own: the program sees no page, no network and no
// storage, only the frame the page hands it, and one that never returns
// costs the page nothing (web/apps.js ends the worker).
//
//   { type: "load", runtime, source }  a fresh engine: the runtime, then the
//                                      program                -> { type: "loaded" }
//   { type: "frame", arg, fn? }        __deckFrame(arg), or fn (a slide's
//                                      script: __scriptFrame) -> { type: "frame", out, ms }
//   { type: "final", runtime, source, arg }
//                                      a second engine of its own runs
//                                      __scriptFinal(arg) and is dropped, so
//                                      where a script ends is worked out without
//                                      touching the one that runs -> { type: "final", out }
//
// Every reply carries `ok`, `error` (the uncaught exception or syntax error)
// and `output` (console.log lines). Modelled on componentengine's
// playground/src/evg/worker.js.
import { makeWasi } from "./cerxes-wasi.js";

let x = null;
let engine = 0;
let frameName = null;
const enc = new TextEncoder();
const dec = new TextDecoder();

async function boot() {
  const wasi = makeWasi(() => {});
  // the build's stamp (?v=…) this worker was loaded with, so a new build
  // never runs an engine the browser kept from an old one
  const url = new URL("./cerxes.wasm" + new URL(import.meta.url).search, import.meta.url);
  const res = await fetch(url);
  if (!res.ok) throw new Error("cerxes.wasm: " + res.status);
  const { instance } = await WebAssembly.instantiate(await res.arrayBuffer(), wasi.imports);
  x = instance.exports;
  wasi.setMemory(x.memory);
  if (x._initialize) x._initialize();
}

function put(s) {
  const d = enc.encode(s);
  const p = x.cx_alloc(d.length);
  new Uint8Array(x.memory.buffer, p, d.length).set(d);
  return [p, d.length];
}

function read(ptr, len) {
  return dec.decode(new Uint8Array(x.memory.buffer, ptr, len));
}

function run(src, on = engine) {
  const [p, n] = put(src);
  const failed = x.cx_eval(on, p, n);
  x.cx_free(p, n);
  return { ok: failed === 0, value: read(x.cx_result_ptr(), x.cx_result_len()), output: read(x.cx_output_ptr(), x.cx_output_len()) };
}

const ready = boot();

self.onmessage = async (ev) => {
  const m = ev.data;
  try {
    await ready;
    if (m.type === "load") {
      if (engine) x.cx_drop(engine);
      engine = x.cx_new();
      if (!frameName) frameName = put("__deckFrame");
      const rt = run(m.runtime);
      if (!rt.ok) {
        self.postMessage({ type: "loaded", ok: false, error: "runtime: " + rt.value, output: rt.output });
        return;
      }
      const r = run(m.source);
      self.postMessage({ type: "loaded", ok: r.ok, error: r.ok ? "" : r.value, output: r.output });
    } else if (m.type === "frame") {
      const [ap, an] = put(m.arg);
      const fn = m.fn ? put(m.fn) : frameName;
      const t0 = performance.now();
      const failed = x.cx_call(engine, fn[0], fn[1], ap, an);
      const ms = performance.now() - t0;
      x.cx_free(ap, an);
      if (m.fn) x.cx_free(fn[0], fn[1]);
      const value = read(x.cx_result_ptr(), x.cx_result_len());
      const output = read(x.cx_output_ptr(), x.cx_output_len());
      self.postMessage(failed === 0
        ? { type: "frame", ok: true, out: value, error: "", output, ms }
        : { type: "frame", ok: false, out: "", error: value, output, ms });
    } else if (m.type === "final") {
      const own = x.cx_new();
      try {
        const rt = run(m.runtime, own);
        const r = rt.ok ? run(m.source, own) : rt;
        if (!r.ok) {
          self.postMessage({ type: "final", ok: false, out: "", error: r.value, output: r.output });
          return;
        }
        const [ap, an] = put(m.arg);
        const fn = put("__scriptFinal");
        const failed = x.cx_call(own, fn[0], fn[1], ap, an);
        x.cx_free(ap, an);
        x.cx_free(fn[0], fn[1]);
        const value = read(x.cx_result_ptr(), x.cx_result_len());
        const output = read(x.cx_output_ptr(), x.cx_output_len());
        self.postMessage(failed === 0
          ? { type: "final", ok: true, out: value, error: "", output }
          : { type: "final", ok: false, out: "", error: value, output });
      } finally {
        x.cx_drop(own);
      }
    }
  } catch (e) {
    self.postMessage({ type: m.type === "load" ? "loaded" : m.type, ok: false, error: "engine: " + String((e && e.message) || e), output: "" });
  }
};
