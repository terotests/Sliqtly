// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The video encoder, off the page's thread: vp8enc.js (src/PresVp8.rs, Rust
// that Ranger builds) codes the frames the page draws (web/videoexport.js)
// into a WebM. The worker's name is the build's stamp, so the encoder it
// loads is the same build's.
//
//   { kind: "begin", width, height, fps, quality }
//   { kind: "frame", n, rgba }      rgba null: the same picture as frame n - 1
//   { kind: "end", frames }         → { kind: "done", bytes, coded, keys }
// Every message is answered with { kind: "ack" } (or "error"), so the page
// never has more than a few frames waiting.

importScripts("./vp8enc.js?v=" + encodeURIComponent(self.name || ""));

const NONE = new Uint8Array(0);
let enc = null;

self.onmessage = (e) => {
  const m = e.data;
  try {
    if (m.kind === "begin") enc = Vp8Video.new_(m.width, m.height, m.fps, m.quality);
    else if (m.kind === "frame") enc.add(m.rgba || NONE, m.n, !m.rgba);
    else if (m.kind === "end") {
      enc.set_length(m.frames);
      const bytes = Uint8Array.from(enc.webm());
      self.postMessage({ kind: "done", bytes, coded: enc.frame_count(), keys: enc.key_count() }, [bytes.buffer]);
      enc = null;
      return;
    }
    self.postMessage({ kind: "ack" });
  } catch (err) {
    enc = null;
    self.postMessage({ kind: "error", message: String((err && err.message) || err) });
  }
};
