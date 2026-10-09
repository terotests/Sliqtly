// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Hit the target: a small program that talks to its deck.
//
//   deck.get("hits-label")       the deck's own key (its front matter), read
//   deck.set("score", n)         the footer's {score} follows    allow: deck.data
//   el("#result").style({...})   a block of the slide changes    allow: slide.style
//   slide.next()                 on to the next slide            allow: slide.nav

const W = 480;
const H = 270;
let x = 120, y = 90, vx = 140, vy = 95;
let hits = 0;
let flash = 0;

function tick(dt: number) {
  x += vx * dt;
  y += vy * dt;
  if (x < 0 || x > W - 56) { vx = -vx; x = Math.max(0, Math.min(W - 56, x)); }
  if (y < 0 || y > H - 56) { vy = -vy; y = Math.max(0, Math.min(H - 56, y)); }
  if (flash > 0) flash -= dt;
}

function hit() {
  hits++;
  flash = 0.25;
  vx *= 1.12;
  vy *= 1.12;
  deck.set("score", hits);
  if (hits === 5) el("#result").style({ color: "#16a34a", opacity: 1 });
}

function restart() {
  hits = 0;
  vx = 140;
  vy = 95;
  deck.set("score", 0);
  el("#result").reset();
}

function view() {
  return (
    <div className="board">
      <div id="target" className={flash > 0 ? "target hit" : "target"} style={{ left: x, top: y }} onClick={hit}>
        <div className="ring" />
      </div>
      <span className="count">{deck.get("hits-label") + " " + hits}</span>
      <div className="btn again" onClick={restart}><span className="btn-text">↺</span></div>
      <div className="btn next" onClick={() => slide.next()}><span className="btn-text">›</span></div>
    </div>
  );
}
