// Whether the open deck is kept anywhere: the one answer the editor's "Not
// saved" pill, its Files tab note and the warning on leaving the page read.
//
// sliqtly.com's editor keeps nothing in the browser (web/main.js CLOUD_ONLY,
// the memory store of web/vfs.js), and neither does a browser that refused
// storage: there a deck lasts only as long as the page, unless the cloud has
// it. It does not when
//   "signed-out"  nobody is signed in to PRO (or PRO did not load) on
//                 sliqtly.com's editor
//   "no-storage"  nor elsewhere, in a browser that refused storage
//   "license"     the license has no place for it (sliqtly.js editRight)
//   "expired"     the license to edit has ended
//   "not-yours"   someone else's deck, opened to view: changes stay here
//   "failing"     saving to the cloud failed (tried again on the next change)
//
// state, from the page:
//   editing     a deck is open to edit (not the viewer, a store opened)
//   loading     it is still opening
//   kept        the browser keeps it (IndexedDB)
//   cloudOnly   sliqtly.com's editor, whose decks live in the cloud
//   persisted   it was changed since it opened (a deck as opened has
//               nothing to lose)
//   signedIn    PRO is signed in
//   cloud       it has a share in the cloud
//   noRight     the license's refusal ("full", "expired", "not-yours"), or ""
//   failed      the last save to the cloud failed for another reason
//   sending     a save to the cloud is waiting or on its way
//   changed     changes not yet in the page's store
//   halted      being deleted: nothing more is sent
//
// Answers { reason, warnLeave }: reason one of the above or "" (kept, or on
// its way); warnLeave, that leaving now loses work.
export function deckSafety(s) {
  const none = { reason: "", warnLeave: false };
  if (!s.editing || s.loading || s.halted) return none;
  // the browser has it
  if (s.kept) return none;
  let reason = "";
  if (!s.signedIn) reason = s.cloudOnly ? "signed-out" : "no-storage";
  else if (s.noRight) reason = s.noRight === "expired" || s.noRight === "not-yours" ? s.noRight : "license";
  else if (s.failed) reason = "failing";
  if (!s.persisted) return none;
  if (reason) return { reason, warnLeave: true };
  // in the cloud, unless not yet or not all of it
  return { reason: "", warnLeave: !s.cloud || !!s.sending || !!s.changed };
}

// What the pill says, and the sentences the Files tab gives (a press on the
// pill opens it): why the deck is not kept and what to do about it. `t` translates;
// `error` is what the failed save said; `max`, the license's presentations.
export function safetyText(reason, t, { error = "", max = 2 } = {}) {
  if (!reason) return { pill: "", text: "" };
  const keep = t("To keep it, export it before closing the page: File → Export → Markdown (.md), or All files (.zip) for its pictures and data too.");
  const why = {
    "signed-out": t("You are not signed in, so this presentation is not saved anywhere."),
    "no-storage": t("This browser does not allow storage, so this presentation is kept only while the page is open."),
    license: t("Your license lets you edit {n} presentations in the cloud, and they are taken: this one is not saved.").replace("{n}", String(max)),
    expired: t("Your license to edit has ended: changes are not saved."),
    "not-yours": t("This presentation belongs to another account: your changes are not saved."),
    failing: t("Saving to the cloud failed: ") + error + ". " + t("Nothing is kept in this browser; it is tried again on the next change."),
  }[reason] || "";
  const more = {
    "signed-out": t("Or sign in again."),
    "no-storage": t("Or sign in with PRO to keep it in the cloud."),
    license: t("Or delete another presentation to free a place for it."),
  }[reason] || "";
  return { pill: t("Not saved"), text: [why, keep, more].filter(Boolean).join(" ") };
}
