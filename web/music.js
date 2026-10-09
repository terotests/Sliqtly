// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The deck's music: the front matter's `music: media/song.mp3` (a file of
// the deck, or an https address) played while presenting, and heard by a Web
// Audio analyser. Each frame the analyser's spectrum and waveform go to
// PresBeat (src/PresBeat.rgr, pres_beat.js), which turns them into bars,
// beats and a tempo; the beat effects (web/beatfx.js) draw from what it packs.
//
//   sync()        the deck's music changed: the old track goes, the new one
//                 is fetched when it is first played
//   play/pause    by the page: presenting starts and stops it, M toggles it
//   tick(dt)      once a frame: one analyser frame into the model; true while
//                 the bars still move
//   frame(t)      the packed state, for the effects' data texture

const FFT = 2048;

export function createMusic({ app, readDocFile, toast, t = (s) => s }) {
  const Beat = globalThis.PresBeat;
  const beat = Beat ? new Beat() : null;
  let src = "";
  let audio = null;
  let url = "";
  let ctx = null;
  let analyser = null;
  let freqBytes = null;
  let waveFloats = null;
  let freq = [];
  let wave = [];
  let wanted = false;
  let loading = null;
  let failedSrc = "";

  function drop() {
    if (audio) {
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    }
    if (url) URL.revokeObjectURL(url);
    audio = null;
    url = "";
    loading = null;
    if (analyser) analyser.disconnect();
    analyser = null;
  }

  function sync() {
    const now = String(app.musicSrc() || "");
    if (now === src) return;
    const was = wanted;
    drop();
    src = now;
    failedSrc = "";
    wanted = false;
    if (was && src) play();
  }

  async function load() {
    if (/^https:\/\//i.test(src)) return src;
    const blob = await readDocFile(src.replace(/^\.?\//, ""));
    if (!blob) throw new Error(t("The music file is not in the deck: ") + src);
    url = URL.createObjectURL(blob);
    return url;
  }

  // The element, the analyser after it and the speakers after that; the
  // context is made on the first play, which follows a press or a key.
  function wire(address) {
    audio = new Audio();
    audio.crossOrigin = "anonymous";
    audio.loop = true;
    audio.preload = "auto";
    audio.src = address;
    const Ctx = window.AudioContext || window.webkitAudioContext;
    if (!Ctx || !beat) return;
    try {
      if (!ctx) ctx = new Ctx();
      const node = ctx.createMediaElementSource(audio);
      analyser = ctx.createAnalyser();
      analyser.fftSize = FFT;
      analyser.smoothingTimeConstant = 0;
      // loud music short of the top, quiet bins at the bottom
      analyser.minDecibels = -90;
      analyser.maxDecibels = -12;
      node.connect(analyser);
      analyser.connect(ctx.destination);
      freqBytes = new Uint8Array(analyser.frequencyBinCount);
      waveFloats = new Float32Array(analyser.fftSize);
      freq = new Array(analyser.frequencyBinCount).fill(0);
      wave = new Array(analyser.fftSize).fill(0);
      beat.setup(ctx.sampleRate, analyser.frequencyBinCount);
    } catch (e) {
      // no analyser: the music plays and the effects stay calm
      console.warn("music analyser unavailable", e);
      analyser = null;
    }
  }

  function play() {
    wanted = true;
    if (!src || src === failedSrc) return;
    if (!loading) {
      loading = load().then((address) => {
        wire(address);
      }).catch((e) => {
        failedSrc = src;
        loading = null;
        toast(String((e && e.message) || e));
      });
    }
    loading.then(() => {
      if (!wanted || !audio) return;
      if (ctx && ctx.state === "suspended") ctx.resume().catch(() => {});
      audio.play().catch((e) => console.warn("the music did not play", e));
    });
  }

  function pause() {
    wanted = false;
    if (audio) audio.pause();
  }

  function toggle() {
    if (wanted) pause(); else play();
    return wanted;
  }

  const playing = () => !!(audio && !audio.paused && analyser);

  function tick(dt) {
    if (!beat) return false;
    if (playing()) {
      analyser.getByteFrequencyData(freqBytes);
      analyser.getFloatTimeDomainData(waveFloats);
      for (let i = 0; i < freqBytes.length; i++) freq[i] = freqBytes[i] / 255;
      for (let i = 0; i < waveFloats.length; i++) wave[i] = waveFloats[i];
      beat.feed(freq, wave, dt);
      return true;
    }
    if (beat.level > 0.001 || beat.pulse > 0.001) {
      beat.idle(dt);
      return true;
    }
    if (beat.playing) beat.idle(dt);
    return false;
  }

  // RGBA float texels, one row as wide as the effects' data texture
  const packed = new Float32Array(512 * 4);
  function frame(time) {
    if (!beat) return packed;
    const p = beat.pack(time);
    for (let i = 0; i < p.length && i < packed.length; i++) packed[i] = p[i];
    return packed;
  }

  return {
    sync, play, pause, toggle, tick, frame,
    has: () => !!src,
    wanted: () => wanted,
    playing,
    reset() { pause(); drop(); src = ""; },
  };
}
