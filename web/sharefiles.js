// SPDX-License-Identifier: AGPL-3.0-or-later

// A deck's files against its share's (PRO, or a server of one's own): what
// changed there since this page last wrote or read it, and so what this page
// takes. No page in it: main.js hands it the lists and does the fetching.
//
// Several people editing a deck together each keep the deck's files in their
// own browser. A file written there again (a drawing on a slide someone
// changed, drawings/x.ink) has to reach the others, or each goes on with the
// copy they first had, and the next save of an old copy puts back what
// someone else removed.
//
// A share's file entry names its version (`sha`, from the server that kept
// it). `seen` is, per path, the version this page's copy is: the one it read,
// or the one its own upload made. A file whose version there is not the one
// seen here, and which was not changed here since (`stamps`, as last sent or
// read), is taken. Changed in both: this page's copy goes up next, as before.
// An entry without a version (a server that names none) is never taken in
// place.

// The version a share's file entry names; "" when it names none.
export function versionOf(entry) {
  return (entry && typeof entry.sha === "string" && entry.sha) || "";
}

// remote: the share's file entries; local: path → stamp of this page's
// files; stamps: path → stamp as last sent or read; seen: path → version.
// Returns the entries to add (new there), to update (changed there only),
// the paths to remove here (gone there, not changed here), and `seen` as
// it is once those are done.
export function planFiles({ remote = [], local = new Map(), stamps = new Map(), seen = new Map() } = {}) {
  const add = [];
  const update = [];
  const remove = [];
  const next = new Map();
  const there = new Map(remote.map((f) => [f.path, f]));
  for (const [path, f] of there) {
    const v = versionOf(f);
    if (!local.has(path)) {
      if (!stamps.has(path)) {
        add.push(f);
        if (v) next.set(path, v);
      }
      continue;
    }
    const was = seen.get(path) || "";
    if (!v) continue;
    // not known which version this copy is: the one there from now on
    if (!was || was === v) {
      next.set(path, v);
    } else if (stamps.has(path) && local.get(path) === stamps.get(path)) {
      update.push(f);
      next.set(path, v);
    } else {
      // changed here too: this copy goes up, and its upload names the
      // version then
      next.set(path, was);
    }
  }
  for (const [path, stamp] of stamps) {
    if (there.has(path)) continue;
    if (local.has(path) && local.get(path) === stamp) remove.push(path);
  }
  return { add, update, remove, seen: next };
}

// What is seen once a save is done: `entries`, the share's entries it
// returned; `sent`, the paths it wrote (their versions are this page's copy
// now). A file it left as it was keeps the version seen before: written
// again there meanwhile, it is still taken next time.
export function seenAfterSave(entries, seen = new Map(), sent = null) {
  const next = new Map();
  for (const e of entries || []) {
    const v = versionOf(e);
    const was = seen.get(e.path);
    if (sent && !sent.has(e.path)) {
      if (was) next.set(e.path, was);
    } else if (v) {
      next.set(e.path, v);
    }
  }
  return next;
}
