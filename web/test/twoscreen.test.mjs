// node --test: the presenter view on another screen, the browser's part (web/twoscreen.js)
import test from "node:test";
import assert from "node:assert/strict";
import { audiencePair, audienceSrc, channelName, newPair, otherScreen, windowFeatures, openAudience, openChannel, AUDIENCE_PARAM } from "../twoscreen.js";

test("a pair's name is short letters and digits, and names the audience's address", () => {
  const p = newPair();
  assert.match(p, /^[a-z0-9]{12}$/);
  assert.notEqual(newPair(), p);
  assert.equal(audienceSrc("ab12"), "./?" + AUDIENCE_PARAM + "=ab12");
  assert.equal(channelName("ab12"), "sliqtly-two:ab12");
});

test("the audience's window is the page asked for with ?audience= and opened by another window", () => {
  assert.equal(audiencePair("?audience=ab12", true), "ab12");
  assert.equal(audiencePair("?audience=ab12", false), "");
  assert.equal(audiencePair("?x=1", true), "");
  assert.equal(audiencePair("?audience=a/b", true), "");
  assert.equal(audiencePair("", true), "");
});

const laptop = { left: 0, top: 0, width: 1440, height: 900, availLeft: 0, availTop: 25, availWidth: 1440, availHeight: 875 };
const projector = { left: 1440, top: 0, width: 1920, height: 1080, availLeft: 1440, availTop: 0, availWidth: 1920, availHeight: 1080 };
const small = { left: -800, top: 0, width: 800, height: 600, availLeft: -800, availTop: 0, availWidth: 800, availHeight: 600 };

test("the slides go to a screen that is not this window's, the largest", () => {
  assert.equal(otherScreen({ screens: [laptop, projector], currentScreen: laptop }), projector);
  assert.equal(otherScreen({ screens: [small, laptop, projector], currentScreen: laptop }), projector);
  assert.equal(otherScreen({ screens: [laptop], currentScreen: laptop }), null);
  assert.equal(otherScreen(null), null);
  // the same screen as another object (as the browser may hand it)
  assert.equal(otherScreen({ screens: [{ ...laptop }], currentScreen: laptop }), null);
});

test("the window fills that screen, or opens as a window of its own", () => {
  assert.equal(windowFeatures(projector), "popup,left=1440,top=0,width=1920,height=1080");
  assert.equal(windowFeatures(null), "popup,width=960,height=540");
});

function fakeWindow({ state = "granted", screens = [laptop, projector], api = true, blocked = false } = {}) {
  const opened = [];
  const win = {
    document: { baseURI: "https://example.test/editor/" },
    navigator: { permissions: { query: async () => ({ state }) } },
    open(url, name, features) {
      if (blocked) return null;
      const w = { url, name, features, closed: false, moves: [], moveTo(x, y) { this.moves.push(["move", x, y]); }, resizeTo(w, h) { this.moves.push(["size", w, h]); } };
      opened.push(w);
      return w;
    },
  };
  if (api) win.getScreenDetails = async () => ({ screens, currentScreen: laptop });
  return { win, opened };
}

test("allowed already: the window opens on the other screen", async () => {
  const { win, opened } = fakeWindow();
  const got = await openAudience("ab12", { win });
  assert.equal(got.win, opened[0]);
  assert.equal(opened[0].url, "https://example.test/editor/?audience=ab12");
  assert.match(opened[0].features, /left=1440/);
  assert.equal(await got.placed, true);
});

test("to be asked: the window opens at once and moves once allowed", async () => {
  const { win, opened } = fakeWindow({ state: "prompt" });
  const got = await openAudience("ab12", { win });
  assert.equal(opened[0].features, "popup,width=960,height=540");
  assert.equal(await got.placed, true);
  assert.deepEqual(opened[0].moves, [["move", 1440, 0], ["size", 1920, 1080]]);
});

test("no such API, or one screen: a window to drag there", async () => {
  const none = fakeWindow({ api: false });
  const a = await openAudience("ab12", { win: none.win });
  assert.equal(await a.placed, false);
  assert.equal(none.opened[0].features, "popup,width=960,height=540");
  const one = fakeWindow({ screens: [laptop] });
  assert.equal(await (await openAudience("ab12", { win: one.win })).placed, false);
  const denied = fakeWindow({ state: "denied" });
  assert.equal(await (await openAudience("ab12", { win: denied.win })).placed, false);
});

test("blocked: no window", async () => {
  const { win } = fakeWindow({ blocked: true });
  const got = await openAudience("ab12", { win });
  assert.equal(got.win, null);
  assert.equal(await got.placed, false);
});

test("the channel hands lines and the deck's packet apart", () => {
  class Chan {
    constructor(name) { this.name = name; }
  }
  const lines = [];
  const packets = [];
  const ch = openChannel("ab12", { line: (l) => lines.push(l), packet: (p) => packets.push(p) }, Chan);
  assert.equal(ch.name, "sliqtly-two:ab12");
  ch.onmessage({ data: "hello" });
  ch.onmessage({ data: { kind: "x" } });
  assert.deepEqual(lines, ["hello"]);
  assert.deepEqual(packets, [{ kind: "x" }]);
  assert.equal(openChannel("ab12", {}, null), null);
});
