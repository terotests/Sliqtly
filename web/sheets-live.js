// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Live spreadsheets: EVGSheets (github.com/terotests/EVGSheets) inside Sliqtly.
//
//   on a slide   A ```sheet fence lays out a box like ```table and the slide
//                paints the table into it — that picture is what exports,
//                thumbnails and transitions use. While presenting, once the
//                slide has come to rest, the workbook itself is put over the
//                box: read-only, and out of the keyboard's way, so the arrows
//                still turn slides.
//
//                Edit (the button on the sheet, or E) hands the keyboard to
//                the sheet: arrows move between cells, typing edits, the
//                ribbon is there. Done, Esc (outside a cell edit) or
//                Ctrl+Enter hands it back, and an edited workbook is saved
//                into the deck's files — the .xlsx and the CSV the still is
//                drawn from, so the next export shows the change.
//
//   in Files     An .xlsx opens in a dialog with the full editor; Save keeps
//                it and its sheets' CSVs.
//
// The sheet is its own canvas with its own accessibility mirror (a grid a
// reader can walk), and it is inert until Edit, so a reader is not dropped
// into it on the way through the slides.

let modPromise = null;
function loadEvgSheets(base) {
  if (!modPromise) {
    modPromise = import(new URL(base + "evgsheets.mjs", location.href).href).catch((e) => {
      modPromise = null;
      throw e;
    });
  }
  return modPromise;
}

const STYLE = `
.sheet-layer { position: absolute; inset: 0; pointer-events: none; z-index: 3; }
.sheet-live { position: absolute; border-radius: 8px; overflow: hidden; box-shadow: 0 6px 24px rgba(15,23,42,.18); background: #fff; }
.sheet-live[inert] { pointer-events: none; }
.sheet-live.editing { pointer-events: auto; outline: 3px solid #2563eb; outline-offset: 2px; }
.sheet-live-host { position: absolute; inset: 0; }
.sheet-edit {
  position: absolute; right: 8px; bottom: 8px; z-index: 2; pointer-events: auto;
  font: 600 13px/1 system-ui, sans-serif; padding: 7px 12px; border-radius: 7px; cursor: pointer;
  border: 1px solid #1d4ed8; background: #2563eb; color: #fff; box-shadow: 0 2px 8px rgba(0,0,0,.2);
}
.sheet-edit:focus-visible { outline: 2px solid #fff; outline-offset: 2px; }
.sheet-dialog {
  position: fixed; inset: 0; z-index: 50; background: rgba(15,23,42,.45);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.sheet-dialog-box {
  width: min(1200px, 100%); height: min(780px, 100%); background: #fff; border-radius: 12px;
  display: flex; flex-direction: column; overflow: hidden; box-shadow: 0 20px 60px rgba(0,0,0,.35);
}
.sheet-dialog-bar {
  display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid #e2e8f0;
  font: 14px/1.2 system-ui, sans-serif; color: #0f172a;
}
.sheet-dialog-bar .grow { flex: 1; }
.sheet-dialog-bar button {
  font: inherit; padding: 6px 12px; border-radius: 6px; cursor: pointer;
  border: 1px solid #cbd5e1; background: #fff; color: #0f172a;
}
.sheet-dialog-bar button.primary { background: #2563eb; border-color: #2563eb; color: #fff; font-weight: 600; }
.sheet-dialog-host { flex: 1; min-height: 0; position: relative; }
`;

export function createLiveSheets({ stageEl, canvas, keys, base, readFile, saveWorkbook, t = (s) => s, onChange = () => {}, toast = () => {} }) {
  const style = document.createElement("style");
  style.textContent = STYLE;
  document.head.appendChild(style);
  const layer = document.createElement("div");
  layer.className = "sheet-layer";
  stageEl.appendChild(layer);

  // key → { el, host, button, sheet (EVGSheets controller), file, sheetName, editing, dirty }
  const live = new Map();
  // a workbook that would not open is not tried again every frame
  const failed = new Set();
  let shownKeys = "";

  function placeBox(entry, layout, s) {
    const [sx, sy, sc] = layout.stage;
    const [bx, by, bw, bh] = s.box;
    const ox = canvas.offsetLeft;
    const oy = canvas.offsetTop;
    Object.assign(entry.el.style, {
      left: Math.round(ox + sx + bx * sc) + "px",
      top: Math.round(oy + sy + by * sc) + "px",
      width: Math.max(160, Math.round(bw * sc)) + "px",
      height: Math.max(120, Math.round(bh * sc)) + "px",
    });
  }

  async function workbookBytes(file, data) {
    const f = await readFile(file);
    if (!f) return { bytes: null, csv: null };
    if (/\.xlsx$/i.test(file)) return { bytes: await f.arrayBuffer(), csv: null };
    return { bytes: null, csv: await f.text() };
  }

  function mount(s) {
    const el = document.createElement("div");
    el.className = "sheet-live";
    el.setAttribute("inert", "");
    el.dataset.key = s.key;
    const host = document.createElement("div");
    host.className = "sheet-live-host";
    el.appendChild(host);
    const button = document.createElement("button");
    button.type = "button";
    button.className = "sheet-edit";
    button.textContent = t("Edit");
    button.setAttribute("aria-label", t("Edit the spreadsheet") + " " + (s.sheet || s.file));
    el.appendChild(button);
    layer.appendChild(el);
    const entry = { el, host, button, sheet: null, file: s.file, sheetName: s.sheet, data: s.data, editing: false, dirty: false, ready: null };
    live.set(s.key, entry);
    button.addEventListener("click", () => (entry.editing ? finish(entry) : edit(entry)));
    entry.ready = (async () => {
      const { mountSheets } = await loadEvgSheets(base);
      const { bytes, csv } = await workbookBytes(s.file, s.data);
      entry.sheet = await mountSheets(host, {
        base,
        ui: "viewer",
        fonts: "minimal",
        theme: "light",
        xlsx: bytes || undefined,
        name: s.file.split("/").pop(),
        sheet: s.sheet || undefined,
        label: t("Spreadsheet") + " " + (s.sheet || s.file),
        onChange: () => { if (entry.editing) entry.dirty = true; },
        onSave: (raw) => { entry.dirty = true; finish(entry); },
        onKey: (ev) => {
          if (!entry.editing) return true;
          const st = entry.sheet.state();
          const leave = (ev.key === "Escape" && !st.editing && !st.menuOpen && !st.modal)
            || ((ev.ctrlKey || ev.metaKey) && ev.key === "Enter");
          if (leave) {
            ev.preventDefault();
            ev.stopPropagation();
            finish(entry);
            return false;
          }
          return true;
        },
      });
      if (!bytes && csv != null) {
        // A CSV: a fresh workbook with the rows pasted in at A1.
        await entry.sheet.run("file.new", "");
        entry.sheet.app.pasteText(csvToTsv(csv));
        await entry.sheet.redraw();
      }
    })().catch((e) => {
      console.warn("live sheet:", e);
      failed.add(s.key + "|" + s.file);
      el.remove();
      live.delete(s.key);
    });
    return entry;
  }

  async function edit(entry) {
    if (!entry) return;
    await entry.ready;
    if (!entry.sheet) return;
    entry.editing = true;
    entry.el.removeAttribute("inert");
    entry.el.classList.add("editing");
    entry.button.textContent = t("Done");
    entry.button.setAttribute("aria-label", t("Done editing the spreadsheet"));
    await entry.sheet.setPreset("compact");
    await entry.sheet.setOption("tools", "undo,redo,bold,italic,underline,color,fill,left,center,right,numfmt,percent,sortasc,sortdesc");
    entry.sheet.focus();
    toast(t("Editing the sheet — Esc or Done to go back to the slides"));
  }

  async function finish(entry) {
    if (!entry || !entry.editing) return;
    entry.editing = false;
    entry.el.setAttribute("inert", "");
    entry.el.classList.remove("editing");
    entry.button.textContent = t("Edit");
    entry.button.setAttribute("aria-label", t("Edit the spreadsheet") + " " + (entry.sheetName || entry.file));
    if (entry.sheet) {
      const changed = entry.dirty || entry.sheet.state().canUndo;
      if (changed) {
        try {
          await saveWorkbook(entry.file, entry.sheet.saveBytes(), entry.sheetName);
        } catch (e) {
          console.warn("live sheet save:", e);
        }
        entry.dirty = false;
      }
      await entry.sheet.setPreset("viewer");
    }
    keys.focus({ preventScroll: true });
    onChange();
  }

  /** Called after every paint with the app's layout. */
  function sync(layout) {
    const presenting = layout && layout.mode === "present";
    const sheets = presenting ? layout.sheets || [] : [];
    const keysNow = sheets.map((s) => s.key + "|" + s.file).join(",");
    // Another slide, or out of the show: the sheets that were up go.
    if (keysNow !== shownKeys) {
      for (const [key, entry] of live) {
        if (!sheets.some((s) => s.key === key && s.file === entry.file)) {
          if (entry.editing) finish(entry);
          entry.sheet?.destroy();
          entry.el.remove();
          live.delete(key);
        }
      }
      shownKeys = keysNow;
    }
    for (const s of sheets) {
      if (failed.has(s.key + "|" + s.file)) continue;
      const entry = live.get(s.key) || mount(s);
      placeBox(entry, layout, s);
      entry.el.style.visibility = layout.settled || entry.editing ? "visible" : "hidden";
    }
  }

  function editing() {
    for (const entry of live.values()) if (entry.editing) return entry;
    return null;
  }

  return {
    sync,
    editing: () => !!editing(),
    /** E while presenting: into the first sheet on the slide. */
    editFirst() {
      const first = live.values().next().value;
      if (first) edit(first);
    },
    finishEditing() {
      const e = editing();
      if (e) finish(e);
    },
    owns(node) {
      return !!(node && node.closest && (node.closest(".sheet-live") || node.closest(".sheet-dialog")));
    },
    openDialog: (opts) => openSheetDialog({ ...opts, base, t }),
  };
}

/** The full editor in a dialog, for an .xlsx in the deck's files. */
// `bytes` the workbook to edit; none opens EVGSheets' demo sheet unless
// `blank`, which starts from an empty workbook (File → New → Datasheet).
export async function openSheetDialog({ base, t = (s) => s, name, bytes, blank = false, onSave, onClose }) {
  const { mountSheets } = await loadEvgSheets(base);
  const wrap = document.createElement("div");
  wrap.className = "sheet-dialog";
  wrap.setAttribute("role", "dialog");
  wrap.setAttribute("aria-modal", "true");
  wrap.setAttribute("aria-label", name);
  const box = document.createElement("div");
  box.className = "sheet-dialog-box";
  const bar = document.createElement("div");
  bar.className = "sheet-dialog-bar";
  const title = document.createElement("strong");
  title.textContent = name;
  const grow = document.createElement("span");
  grow.className = "grow";
  const save = document.createElement("button");
  save.className = "primary";
  save.textContent = t("Save");
  const close = document.createElement("button");
  close.textContent = t("Close");
  bar.append(title, grow, save, close);
  const host = document.createElement("div");
  host.className = "sheet-dialog-host";
  box.append(bar, host);
  wrap.appendChild(box);
  document.body.appendChild(wrap);
  const prevFocus = document.activeElement;
  const sheet = await mountSheets(host, {
    base,
    ui: "full",
    xlsx: bytes,
    name,
    menubar: true,
    title: false,
    onSave: async (raw) => { await onSave(raw); save.textContent = t("Saved"); setTimeout(() => (save.textContent = t("Save")), 1200); },
  });
  if (blank && !bytes) {
    await sheet.run("file.new", "");
    await sheet.redraw();
  }
  sheet.focus();
  const done = () => {
    sheet.destroy();
    wrap.remove();
    if (prevFocus && prevFocus.focus) prevFocus.focus({ preventScroll: true });
    if (onClose) onClose();
  };
  save.addEventListener("click", async () => {
    await onSave(sheet.saveBytes());
    save.textContent = t("Saved");
    setTimeout(() => (save.textContent = t("Save")), 1200);
  });
  close.addEventListener("click", done);
  wrap.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && ev.target === close) done();
  });
  return { sheet, close: done };
}

/** CSV → TSV, quotes understood, for pasting into a fresh sheet. */
export function csvToTsv(csv) {
  const out = [];
  let row = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < csv.length; i++) {
    const ch = csv[i];
    if (quoted) {
      if (ch === '"' && csv[i + 1] === '"') { cell += '"'; i++; }
      else if (ch === '"') quoted = false;
      else cell += ch;
    } else if (ch === '"') quoted = true;
    else if (ch === ",") { row.push(cell); cell = ""; }
    else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && csv[i + 1] === "\n") i++;
      row.push(cell);
      out.push(row.join("\t"));
      row = [];
      cell = "";
    } else cell += ch;
  }
  if (cell !== "" || row.length) { row.push(cell); out.push(row.join("\t")); }
  return out.join("\n");
}
