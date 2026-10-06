// SPDX-License-Identifier: AGPL-3.0-or-later

// The deck's call on the page: the microphone, the one WebRTC connection to
// the server (mcp-go/meet.go forwards everyone's voice; never a connection
// to another page), the others' voices played, and how loud each one is.
// What the call looks like is the app's (src/PresMeet.rgr, EVGUI
// ParticipantsCtl): this hands it the server's tellings and the levels, and
// does what it asks ("meet:join", "meet:leave", "meet:mute\t1",
// "meet:mute-other\t<client>").
//
// The server makes every offer (on joining, and whenever a voice comes or
// goes); the page answers. Its first offer has one place for this page's
// microphone, sent there; every other voice comes as a stream named by
// whose it is (the page's client in the room).
//
//   const meet = new Meet({ app, session: () => collab, mic, toast, t, paint, micHelp, now })
//   meet.event(m)        a `call` / `call-offer` event of the deck's room
//   meet.request(r)      one of the app's "meet:" requests
//   meet.reset()         the deck's room left: out of the call
//   meet.tick()          every frame: the levels measured now and then

export const LEVEL_MS = 80;

// "client\tname\tcolour\thost\tmuted\tassistant", a line each, for PresMeet
export function memberRows(members) {
  return (members || []).map((m) => [
    m.client, String(m.name || "").replace(/[\t\n]/g, " "), m.color || "#64748b",
    m.host ? 1 : 0, m.muted ? 1 : 0, m.agent ? 1 : 0,
  ].join("\t")).join("\n");
}

// loudness 0..1 of an analyser's last block (RMS, scaled so speech at a
// normal distance is about 0.2–0.5)
export function levelOf(buf) {
  let sum = 0;
  for (let i = 0; i < buf.length; i += 1) {
    const v = (buf[i] - 128) / 128;
    sum += v * v;
  }
  return Math.min(1, Math.sqrt(sum / Math.max(1, buf.length)) * 4);
}

export class Meet {
  constructor(deps) {
    this.d = deps;
    this.pc = null;
    this.mic = null; // the microphone's stream
    this.members = [];
    this.voices = new Map(); // client → { el, stream, an }
    this.ctx = null;
    this.meter = null; // the own microphone's analyser
    this.levelAt = 0;
    this.offers = Promise.resolve();
    this.joined = false;
    this.mutedHere = false;
    // the loudest each one was, for scripts/check-call.mjs
    this.loudest = new Map();
  }

  app() { return this.d.app; }

  client() { return this.d.session()?.me?.client || ""; }

  // --- the room's events ---------------------------------------------------------------

  event(m) {
    if (m.t === "call") {
      this.members = m.members || [];
      const me = this.members.find((r) => r.client === this.client());
      // muted by the host: the microphone stops here too (the server stops
      // forwarding it anyway); only this person unmutes it
      if (me && this.mic) this.setMicOn(!me.muted);
      if (!me && this.joined && this.pc) this.hangUp();
      for (const c of [...this.voices.keys()]) if (!this.members.some((r) => r.client === c)) this.dropVoice(c);
      if (this.app().meetMembers(memberRows(this.members))) this.d.paint();
    } else if (m.t === "call-offer") {
      // one at a time, in the order they came
      this.offers = this.offers.then(() => this.answer(m)).catch((e) => console.warn("call: offer", e));
    }
  }

  async answer(m) {
    const pc = this.pc;
    if (!pc) return;
    await pc.setRemoteDescription({ type: "offer", sdp: m.sdp });
    // the first place for audio is this page's microphone, sent only
    const tr = pc.getTransceivers().find((x) => x.mid === "0");
    if (tr && this.mic && !tr.sender.track) {
      tr.direction = "sendonly";
      await tr.sender.replaceTrack(this.mic.getAudioTracks()[0]);
    }
    const a = await pc.createAnswer();
    await pc.setLocalDescription(a);
    await this.d.session().callOp({ op: "answer", sdp: pc.localDescription.sdp, n: m.n });
  }

  // --- what the app asks -----------------------------------------------------------

  request(r) {
    const [what, arg] = r.slice(5).split("\t");
    if (what === "join") this.join().catch((e) => this.failed(e));
    else if (what === "leave") this.leave();
    else if (what === "mute") this.mute(arg === "1");
    else if (what === "mute-other") this.d.session()?.callOp({ op: "mute", target: arg, muted: true }).catch((e) => this.d.toast(e.message || String(e)));
  }

  async join() {
    const s = this.d.session();
    if (!s?.active()) throw new Error(this.d.t("Calls work on a Sliqtly server of one's own, with the presentation open in Edit mode."));
    if (!navigator.mediaDevices?.getUserMedia || !window.isSecureContext) {
      const e = new Error("insecure");
      e.name = "SecurityError";
      throw e;
    }
    this.mic = await this.d.mic();
    const pc = new RTCPeerConnection({ iceServers: [] });
    this.pc = pc;
    pc.ontrack = (ev) => this.addVoice(ev.streams[0]?.id || ev.track.id, ev.streams[0] || new MediaStream([ev.track]));
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "failed" && this.pc === pc) {
        this.d.toast(this.d.t("The call's connection to the server failed."));
        this.leave();
      }
    };
    this.joined = true;
    this.mutedHere = false;
    this.listen(this.client(), this.mic, true);
    await s.callOp({ op: "join", muted: false });
  }

  failed(e) {
    this.hangUp();
    // the server's own refusal says why; the microphone's is explained
    Promise.resolve(e?.code ? e.message : this.d.micHelp(e)).then((why) => {
      this.app().meetFailed(why);
      this.d.toast(why);
      this.d.paint();
    });
  }

  leave() {
    const s = this.d.session();
    if (this.joined && s?.active()) s.callOp({ op: "leave" }).catch(() => {});
    this.hangUp();
  }

  // out of the call here: the connection, the microphone, the voices
  hangUp() {
    this.joined = false;
    if (this.pc) this.pc.close();
    this.pc = null;
    if (this.mic) for (const tr of this.mic.getTracks()) tr.stop();
    this.mic = null;
    this.meter = null;
    for (const c of [...this.voices.keys()]) this.dropVoice(c);
    this.offers = Promise.resolve();
  }

  // another deck, or the room gone
  reset() {
    this.leave();
    this.members = [];
    this.app().meetReset();
    this.d.paint();
  }

  mute(on) {
    this.setMicOn(!on);
    this.d.session()?.callOp({ op: "mute", muted: on }).catch((e) => this.d.toast(e.message || String(e)));
  }

  setMicOn(on) {
    this.mutedHere = !on;
    if (this.mic) for (const tr of this.mic.getAudioTracks()) tr.enabled = on;
  }

  // --- voices and levels ---------------------------------------------------------------

  audioCtx() {
    if (!this.ctx) {
      try { this.ctx = new AudioContext(); } catch (_) { this.ctx = null; }
    }
    if (this.ctx?.state === "suspended") this.ctx.resume().catch(() => {});
    return this.ctx;
  }

  // a stream's loudness measured (not played: an element plays it)
  listen(client, stream, own) {
    const ctx = this.audioCtx();
    if (!ctx) return null;
    const an = ctx.createAnalyser();
    an.fftSize = 512;
    ctx.createMediaStreamSource(stream).connect(an);
    if (own) this.meter = { client, an };
    return an;
  }

  addVoice(client, stream) {
    if (this.voices.has(client)) this.dropVoice(client);
    const el = document.createElement("audio");
    el.autoplay = true;
    el.srcObject = stream;
    el.dataset.callVoice = client;
    el.style.display = "none";
    document.body.append(el);
    el.play().catch(() => {});
    this.voices.set(client, { el, stream, an: this.listen(client, stream, false) });
  }

  dropVoice(client) {
    const v = this.voices.get(client);
    if (!v) return;
    v.el.srcObject = null;
    v.el.remove();
    this.voices.delete(client);
  }

  tick(now = this.d.now()) {
    if (!this.joined || now - this.levelAt < LEVEL_MS) return;
    this.levelAt = now;
    let changed = false;
    const buf = new Uint8Array(512);
    const read = (client, an) => {
      if (!an) return;
      an.getByteTimeDomainData(buf);
      const l = levelOf(buf);
      if (l > (this.loudest.get(client) || 0)) this.loudest.set(client, l);
      if (this.app().meetLevel(client, l, now)) changed = true;
    };
    if (this.meter && !this.mutedHere) read(this.meter.client, this.meter.an);
    for (const [c, v] of this.voices) read(c, v.an);
    if (this.app().meetTick(now)) changed = true;
    if (changed) this.d.paint();
  }

  // for scripts/check-web.mjs: what this page hears
  heard() {
    return [...this.voices.keys()];
  }
}
