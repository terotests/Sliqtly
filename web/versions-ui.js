// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The version history panel and the merge dialog (web/versions.js does the
// work; main.js passes what they need).

import { t } from "./i18n.js";
import { viewSrc, viewPacket, isReady } from "./version-view.js";

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

function whenText(iso) {
  const d = new Date(iso || 0);
  if (Number.isNaN(d.getTime())) return "";
  const two = (n) => String(n).padStart(2, "0");
  return `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()} ${two(d.getHours())}:${two(d.getMinutes())}`;
}

// A version's message as the list shows it. The editor's own messages are
// kept as "@…" so they read in the language of whoever looks.
export function messageText(m) {
  const s = String(m || "");
  if (!s || s === "@auto") return t("Edited");
  if (s === "@created") return t("Created");
  if (s === "@merge") return t("Merged with the other copy");
  if (s === "@before-merge") return t("Before merging");
  if (s.startsWith("@restore ")) return t("Restored the version of ") + whenText(s.slice(9));
  return s;
}

function statsText(e) {
  const parts = [];
  if (e.added || e.removed) parts.push(`+${e.added || 0} −${e.removed || 0} ` + t("lines"));
  if (e.files) parts.push(e.files + " " + (e.files === 1 ? t("file") : t("files")));
  return parts.join(", ");
}

const KIND = { added: "added", removed: "removed", changed: "changed", recipe: "edited", renamed: "renamed" };
// i18n: "added" "removed" "changed" "edited" "renamed"

// The history panel. api: { entries() → [{ id, time, device, message,
// added, removed, files, remote, current }], changes(id) → rows,
// diff(id, path) → unified text, checkout(id) → the version (View version),
// restore(id), save(message) }
export function showHistory(api) {
  document.getElementById("versions")?.remove();
  const box = el("div", "vPanel");
  box.id = "versions";
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-label", t("Version history"));
  const head = el("div", "vHead");
  head.append(el("h2", "", t("Version history")));
  const close = el("button", "vClose", "×");
  close.title = t("Close");
  close.addEventListener("click", () => box.remove());
  head.append(close);
  const save = el("form", "vSave");
  const msg = el("input");
  msg.placeholder = t("What changed (optional)");
  msg.maxLength = 200;
  const go = el("button", "primary", t("Save version"));
  go.type = "submit";
  save.append(msg, go);
  save.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    go.disabled = true;
    try {
      await api.save(msg.value.trim());
      msg.value = "";
      await fill();
    } finally {
      go.disabled = false;
    }
  });
  const list = el("ol", "vList");
  box.append(head, save, list);
  box.addEventListener("keydown", (ev) => { if (ev.key === "Escape") box.remove(); });
  document.body.appendChild(box);

  let filling = 0;
  async function fill() {
    const run = ++filling;
    list.replaceChildren(el("li", "vNote", t("Loading…")));
    const entries = await api.entries();
    if (run !== filling) return;
    list.replaceChildren();
    if (!entries.length) list.append(el("li", "vNote", t("No versions yet: one is saved as you edit.")));
    for (const e of entries) list.append(item(e));
  }

  function item(e) {
    const li = el("li", "vItem" + (e.current ? " current" : ""));
    const top = el("button", "vTop");
    top.setAttribute("aria-expanded", "false");
    const title = el("span", "vMsg", messageText(e.message));
    const meta = el("span", "vMeta", [whenText(e.time), e.device, statsText(e)].filter(Boolean).join(" · "));
    top.append(title, meta);
    if (e.current) top.append(el("span", "vTag", t("current")));
    const body = el("div", "vBody");
    body.hidden = true;
    top.addEventListener("click", async () => {
      const open = body.hidden;
      body.hidden = !open;
      top.setAttribute("aria-expanded", String(open));
      if (open && !body.childElementCount) await details(e, body);
    });
    li.append(top, body);
    return li;
  }

  async function details(e, body) {
    body.append(el("p", "vNote", t("Loading…")));
    let rows = [];
    try {
      rows = await api.changes(e.id);
    } catch (err) {
      body.replaceChildren(el("p", "vNote", t("This version could not be read: ") + (err?.message || err)));
      return;
    }
    body.replaceChildren();
    const table = el("ul", "vFiles");
    for (const r of rows) {
      const li = el("li");
      const what = [t(KIND[r.kind] || r.kind)];
      if (r.added || r.removed) what.push(`+${r.added} −${r.removed}`);
      if (r.detail) what.push(r.detail);
      li.append(el("span", "vPath", r.path), el("span", "vMeta", what.join(" · ")));
      if (r.text && (r.added || r.removed)) {
        const show = el("button", "vLink", t("Show changes"));
        const pre = el("pre", "vDiff");
        pre.hidden = true;
        show.addEventListener("click", async () => {
          if (!pre.childElementCount) {
            for (const line of (await api.diff(e.id, r.path)).split("\n")) {
              const c = line[0] === "+" ? "add" : line[0] === "-" ? "del" : line.startsWith("@@") ? "hunk" : "";
              pre.append(el("span", c, line + "\n"));
            }
          }
          pre.hidden = !pre.hidden;
        });
        li.append(show, pre);
      }
      table.append(li);
    }
    if (!rows.length) table.append(el("li", "vNote", t("No changes to the files.")));
    body.append(table);
    const row = el("div", "vRow");
    const back = el("button", "vRestore", t("Restore this version"));
    const restore = async () => {
      back.disabled = true;
      try {
        await api.restore(e.id);
        box.remove();
      } catch (err) {
        back.disabled = false;
        body.append(el("p", "vNote", t("Restoring failed: ") + (err?.message || err)));
      }
    };
    back.addEventListener("click", restore);
    const view = el("button", "vShow", t("View version"));
    view.addEventListener("click", () => {
      showVersionView({
        title: messageText(e.message) + " · " + whenText(e.time),
        time: e.time,
        snap: () => api.checkout(e.id),
        restore: e.current ? null : restore,
      });
    });
    row.append(view);
    if (!e.current) row.append(back);
    body.append(row);
  }

  fill().catch((err) => list.replaceChildren(el("li", "vNote", String(err?.message || err))));
  msg.focus();
  return box;
}

// A version's slides, read only, over the editor: the page again in a frame
// (web/version-view.js), handed the version once it says it is ready. The
// open deck is not touched; Restore restores it, Back closes the view.
// { title, time, snap() → checked-out version, restore() (null: the current
// version, nothing to restore) }
export function showVersionView({ title, time, snap, restore }) {
  document.getElementById("versionView")?.remove();
  const box = el("div", "vView");
  box.id = "versionView";
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-label", t("Viewing a version"));
  const bar = el("div", "vViewBar");
  const what = el("div", "vViewWhat");
  what.append(el("strong", "", t("Viewing a version")), el("span", "vMeta", title + " · " + t("read only, the presentation is not changed")));
  const again = el("button", "primary", t("Restore this version"));
  const back = el("button", "", t("Back"));
  const row = el("div", "vRow");
  if (restore) row.append(again);
  row.append(back);
  bar.append(what, row);
  const frame = el("iframe", "vViewFrame");
  frame.title = t("Viewing a version");
  const note = el("p", "vNote vViewNote", t("Loading…"));
  box.append(bar, note, frame);

  const got = Promise.resolve().then(snap);
  const onMessage = async (ev) => {
    if (ev.source !== frame.contentWindow || ev.origin !== location.origin || !isReady(ev.data)) return;
    try {
      const s = await got;
      if (!s) throw new Error(t("This version is not here or in the cloud."));
      frame.contentWindow?.postMessage(viewPacket(s, { time }), location.origin);
      note.remove();
    } catch (err) {
      note.textContent = t("This version could not be read: ") + (err?.message || err);
    }
  };
  const close = () => {
    window.removeEventListener("message", onMessage);
    window.removeEventListener("keydown", onKey, true);
    box.remove();
  };
  const onKey = (ev) => {
    if (ev.key !== "Escape") return;
    ev.preventDefault();
    ev.stopPropagation();
    close();
  };
  window.addEventListener("message", onMessage);
  window.addEventListener("keydown", onKey, true);
  back.addEventListener("click", close);
  again.addEventListener("click", async () => {
    again.disabled = true;
    // failed or not, the history says how it went
    await restore();
    close();
  });
  got.catch((err) => { note.textContent = t("This version could not be read: ") + (err?.message || err); });
  frame.src = viewSrc();
  document.body.appendChild(box);
  back.focus();
  return box;
}

// The merge dialog: what both copies changed differently, side by side, a
// choice for each. result: mergeCopies(...); where: "tab" | "cloud". →
// picks for resolveMerge.
export function askMerge(result, where) {
  return new Promise((done) => {
    document.getElementById("mergeCard")?.remove();
    const box = el("div", "vPanel vMerge");
    box.id = "mergeCard";
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-label", t("Changes in two places"));
    box.append(el("h2", "", t("Changes in two places")));
    box.append(el("p", "", where === "tab"
      ? t("This presentation was changed here and in another tab at the same time. What did not overlap is already combined. Choose what to keep where both changed the same lines.")
      : t("This presentation was changed here and elsewhere (another device or an assistant) at the same time. What did not overlap is already combined. Choose what to keep where both changed the same lines.")));
    const picks = [];
    const list = el("ol", "vList");
    const choice = (name, value, label, on) => {
      const lab = el("label", "vChoice");
      const r = el("input");
      r.type = "radio";
      r.name = name;
      r.value = value;
      r.checked = !!on;
      lab.append(r, document.createTextNode(" " + label));
      return lab;
    };
    let n = 0;
    result.conflicts.forEach((c, i) => {
      if (c.kind === "text") {
        picks[i] = [];
        let k = 0;
        for (const ch of c.merge.chunks) {
          if (!ch.conflict) continue;
          const at = k;
          picks[i][at] = "mine";
          const li = el("li", "vItem");
          li.append(el("p", "vMeta", c.field === "css" ? t("Theme CSS") : t("Slides (Markdown)")));
          const sides = el("div", "vSides");
          const mine = el("pre", "vDiff", ch.mine.join("\n") || t("(removed)"));
          const theirs = el("pre", "vDiff", ch.theirs.join("\n") || t("(removed)"));
          const a = el("div");
          a.append(el("strong", "", t("Here")), mine);
          const b = el("div");
          b.append(el("strong", "", t("Other")), theirs);
          sides.append(a, b);
          const name = "m" + n++;
          const row = el("div", "vRow");
          row.append(choice(name, "mine", t("Here"), true), choice(name, "theirs", t("Other")), choice(name, "both", t("Both")));
          row.addEventListener("change", (ev) => { picks[i][at] = ev.target.value; });
          li.append(sides, row);
          list.append(li);
          k += 1;
        }
      } else {
        picks[i] = "mine";
        const li = el("li", "vItem");
        li.append(el("p", "vMeta", c.path + " — " + t("changed in both")));
        const name = "m" + n++;
        const row = el("div", "vRow");
        row.append(choice(name, "mine", t("Here"), true), choice(name, "theirs", t("Other")));
        row.addEventListener("change", (ev) => { picks[i] = ev.target.value; });
        li.append(row);
        list.append(li);
      }
    });
    const ok = el("button", "primary", t("Use these"));
    const allTheirs = el("button", "", t("Take all from the other"));
    const finish = (p) => { box.remove(); done(p); };
    ok.addEventListener("click", () => finish(picks));
    allTheirs.addEventListener("click", () => finish(result.conflicts.map((c) => (c.kind === "text" ? new Array(c.merge.conflicts).fill("theirs") : "theirs"))));
    const row = el("div", "vRow");
    row.append(ok, allTheirs);
    box.append(list, row);
    document.body.appendChild(box);
    ok.focus();
  });
}
