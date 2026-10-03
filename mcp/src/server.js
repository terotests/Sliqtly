// The Sliqtly MCP server: tools that turn Markdown (+ a theme, CSS and
// pictures) into a presentation at sliqtly.com and hand back its link.
//
// Tools: sliqtly_guide, create_presentation, update_presentation,
// bind_chart_data, get_presentation, list_files, read_file, list_presentations. create/update also name a UI resource (MCP Apps, and the
// same template for ChatGPT) that shows the deck inline in the chat.

import { parseCsv, readWorkbook, toCsv, workbookInfo, workbookTables } from "./xlsx.js";
import fs from "node:fs";
import crypto from "node:crypto";
import { McpServer, ResourceTemplate } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";
import { THEMES, MAX_MD, MAX_CSS, InputError, bindChartData, loadDataFiles, loadImages, outline, warnings } from "./deck.js";
import { packText } from "./store.js";

const GUIDE = fs.readFileSync(new URL("../guide.md", import.meta.url), "utf8");
const PREVIEW = fs.readFileSync(new URL("./preview.html", import.meta.url), "utf8");
export const VERSION = "1.0.0";
// named by its contents: a client that kept an older preview by its URI
// asks for this one anew
const PREVIEW_URI = `ui://sliqtly/preview-${crypto.createHash("sha256").update(PREVIEW).digest("hex").slice(0, 10)}.html`;
const APP_MIME = "text/html;profile=mcp-app";
// the preview loads Sliqtly from the site (or frames it); both domains serve it
const SITES = ["https://sliqtly.com", "https://sliqtly.web.app"];

const imageSchema = z.object({
  name: z.string().describe('File name the Markdown uses as media/<name>, e.g. "team.jpg" → ![](media/team.jpg)'),
  url: z.string().optional().describe("A public https URL of the picture"),
  data_base64: z.string().optional().describe("The picture's bytes as base64 (instead of url)"),
  mime_type: z.string().optional().describe("image/png, image/jpeg, image/gif, image/webp or image/svg+xml; inferred from the name when left out"),
});

const dataFileSchema = z.object({
  name: z.string().describe('File name, kept as data/<name>: "sales.xlsx", "sales.csv", "sales.json"'),
  text: z.string().optional().describe("The file's text (CSV, TSV, JSON or plain text)"),
  data_base64: z.string().optional().describe("The file's bytes as base64 (an .xlsx workbook)"),
  url: z.string().optional().describe("A public https URL of the file"),
});

const deckFields = {
  theme: z.enum(THEMES).optional().describe("Theme: aurora (default), nebula, carbon, ember, midnight (dark); corporate, editorial (light)"),
  css: z.string().optional().describe("CSS rules added on top of the theme (selectors: page, document, h1, h2, p, list, li, code, table, chart, diagram, .lead …). See sliqtly_guide."),
  css_mode: z.enum(["extend", "replace"]).optional().describe("extend (default): css is added after the theme's rules. replace: css is the whole stylesheet."),
  images: z.array(imageSchema).optional().describe("Pictures the Markdown refers to as media/<name>"),
  files: z.array(dataFileSchema).optional().describe("Data files kept with the deck under data/: .xlsx workbooks, .csv, .tsv, .json, .txt (10 MB each). A chart or ```table reads data/<name>.csv; a workbook's sheets are read as data/<book>-<Sheet>.csv (data/<book>.csv for a one-sheet book); see sliqtly_guide."),
};

// Sign-in is optional (ChatGPT reads this to offer both)
const EITHER = [{ type: "noauth" }, { type: "oauth2", scopes: ["decks"] }];

function uiMeta() {
  return {
    securitySchemes: EITHER,
    ui: { resourceUri: PREVIEW_URI },
    "ui/resourceUri": PREVIEW_URI,
    "openai/outputTemplate": PREVIEW_URI,
    "openai/widgetAccessible": false,
  };
}

function fail(message) {
  return { isError: true, content: [{ type: "text", text: message }] };
}

// opts: { store, baseUrl, fetchImpl, limit(kind) → string|null,
//         user: { uid, name } when signed in, signIn: resource metadata URL }
export function createServer(opts) {
  const { store, baseUrl } = opts;
  const user = opts.user || null;
  const fetchImpl = opts.fetchImpl || fetch;
  const themeCache = new Map();
  const frames = [...new Set([baseUrl, ...SITES])];
  // the preview runs Sliqtly's viewer itself (preview.html): it fetches the
  // site's scripts and fonts, Firebase's from its CDN, the share from
  // Firestore and the pictures from Storage. Claude honors connectDomains
  // only; ChatGPT both.
  const resources = [...frames, "https://www.gstatic.com"];
  const connects = [...frames, "https://www.gstatic.com", "https://firestore.googleapis.com", "https://firebasestorage.googleapis.com"];
  const csp = { frameDomains: frames, resourceDomains: resources, connectDomains: connects };

  async function themeCss(theme) {
    if (!themeCache.has(theme)) {
      const res = await fetchImpl(`${baseUrl}/themes/${theme}.css`);
      if (!res.ok) throw new Error(`theme ${theme}: ${res.status}`);
      themeCache.set(theme, await res.text());
    }
    return themeCache.get(theme);
  }

  // The stylesheet stored with the deck: null keeps the theme as it is.
  async function sheet(theme, css, mode) {
    if (css == null || css.trim() === "") return mode === "replace" ? "" : null;
    if (css.length > MAX_CSS) throw new InputError("css is larger than 100 KB.");
    if (mode === "replace") return css;
    return (await themeCss(theme)) + "\n/* --- added for this deck --- */\n" + css + "\n";
  }

  function links(id) {
    return { share_url: `${baseUrl}/s/${id}`, edit_url: `${baseUrl}/s/${id}?edit` };
  }

  function linkOnly(md, theme, css) {
    const q = new URLSearchParams();
    q.set("md", packText(md));
    if (theme) q.set("theme", theme);
    if (css != null) q.set("css", packText(css));
    const edit = `${baseUrl}/#${q}`;
    q.set("mode", "show");
    return { share_url: `${baseUrl}/#${q}`, edit_url: edit };
  }

  // the web app's Firebase config (public), for the preview: it cannot
  // read Hosting's /__/firebase/init.js across origins
  let firebaseConfig = null;
  async function webConfig() {
    if (!firebaseConfig) {
      firebaseConfig = fetchImpl(`${baseUrl}/__/firebase/init.json`, { signal: AbortSignal.timeout(5000) })
        .then((r) => (r.ok ? r.json() : null)).catch(() => null)
        .then((c) => { if (!c) firebaseConfig = null; return c; });
    }
    return firebaseConfig;
  }

  async function result(out, verb) {
    const lines = [
      `${verb}: "${out.title}" (${out.slides} slides, theme ${out.theme}).`,
      `Presentation: ${out.share_url}`,
      `Open in the editor: ${out.edit_url}`,
    ];
    if (user) lines.push(`Saved in the Sliqtly account of ${user.name || "the signed-in user"}.`);
    if (out.deck_id) lines.push(`deck_id: ${out.deck_id}`);
    if (out.edit_key) lines.push(`edit_key: ${out.edit_key} (needed for update_presentation; do not show it to others)`);
    for (const w of out.warnings) lines.push(`Note: ${w}`);
    lines.push("Give the user the presentation link.");
    const cfg = out.deck_id ? await webConfig() : null;
    return { content: [{ type: "text", text: lines.join("\n") }], structuredContent: out, _meta: { ...uiMeta(), ...(cfg ? { "sliqtly/firebase": cfg } : {}) } };
  }

  // the stored deck, if this caller may change it; throws otherwise
  async function editable(deckId, editKey) {
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured): call create_presentation again with the whole deck.");
    const cur = await store.get(deckId);
    const mine = !!(cur && user && cur.owner === user.uid);
    if (!cur || (cur.source !== "mcp" && !mine)) throw new InputError(`No presentation ${deckId} that this server can change.`);
    if (!mine && !editKey) throw new InputError("edit_key is needed: the presentation is not this signed-in user's own.");
    return { cur, mine };
  }

  async function updated(deckId, saved, note) {
    if (!saved) throw new InputError("The edit_key does not match this presentation.");
    const stored = (saved.files || []).map((f) => f.path.replace(/^media\//, ""));
    const out = await result({
      title: saved.name, theme: saved.theme, slides: outline(saved.md).titles.length,
      warnings: warnings(saved.md, [], stored).filter((w) => !w.startsWith("Image")),
      ...links(deckId), deck_id: deckId,
    }, "Updated");
    if (note) out.content[0].text += "\n" + note;
    return out;
  }

  // the stored data files, and the names a deck reads each one (or each
  // sheet of a workbook) by
  function withData(out, data) {
    if (!data.length) return out;
    const lines = ["Data files kept:"];
    const kept = [];
    for (const f of data) {
      if (/\.xlsx$/i.test(f.path)) {
        const sheets = workbookInfo(f.data, f.path);
        kept.push({ path: f.path, sheets });
        lines.push(`- ${f.path}: ` + (sheets.map((s) => `sheet "${s.name}" (${s.rows} rows; columns ${s.columns.map((c) => JSON.stringify(c)).join(", ")}) read as ${s.csv}`).join("; ") || "no sheet has values"));
      } else {
        kept.push({ path: f.path });
        lines.push(`- ${f.path}`);
      }
    }
    out.content[0].text += "\n" + lines.join("\n");
    out.structuredContent = { ...out.structuredContent, files: kept };
    return out;
  }

  function guarded(kind, fn) {
    return async (args, extra) => {
      const why = opts.limit ? opts.limit(kind, extra) : null;
      if (why) return fail(why);
      try {
        return await fn(args, extra);
      } catch (e) {
        if (e instanceof InputError) return fail(e.message);
        console.error(`${kind} failed`, e);
        return fail(`Sliqtly could not ${kind.replace("_", " ")}: ${e.message}`);
      }
    };
  }

  const server = new McpServer({ name: "sliqtly", title: "Sliqtly", version: VERSION, websiteUrl: baseUrl }, {
    instructions: "Sliqtly turns Markdown into animated slide presentations. Call sliqtly_guide once for the syntax, then create_presentation. Always give the user the returned presentation link.",
  });

  server.registerTool("sliqtly_guide", {
    title: "Sliqtly syntax guide",
    description: "How to write a Sliqtly deck: slide structure, build animations, effects, pictures, charts (Vega-Lite), diagrams (Mermaid, Graphviz), math, themes and the CSS selectors. Read before the first create_presentation.",
    inputSchema: {},
    annotations: { readOnlyHint: true, openWorldHint: false },
    _meta: { securitySchemes: EITHER },
  }, async () => ({ content: [{ type: "text", text: GUIDE }] }));

  server.registerTool("create_presentation", {
    title: "Create a presentation",
    description: "Create a Sliqtly slide presentation from Markdown (# title slide, ## per slide), an optional theme, extra CSS, pictures and data files (.xlsx, .csv, .json) its charts and tables read. Returns a link that opens the presentation and a link to edit a copy in the Sliqtly editor.",
    inputSchema: {
      title: z.string().max(200).describe("The presentation's name"),
      markdown: z.string().describe("The whole deck as Sliqtly Markdown"),
      ...deckFields,
    },
    annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
    _meta: uiMeta(),
  }, guarded("create_presentation", async ({ title, markdown, theme = "aurora", css, css_mode = "extend", images, files }) => {
    if (markdown.length > MAX_MD) throw new InputError("markdown is larger than 300 KB.");
    if (!markdown.trim()) throw new InputError("markdown is empty.");
    const css2 = await sheet(theme, css, css_mode);
    const imgs = await loadImages(images, fetchImpl);
    const data = await loadDataFiles(files, fetchImpl, readWorkbook);
    const names = imgs.map((i) => i.name);
    const base = { title, theme, slides: outline(markdown).titles.length, warnings: warnings(markdown, names) };
    if (store.kind === "link") {
      if (imgs.length) base.warnings.push("Pictures are not stored on this server (no cloud storage configured); the slides show without them.");
      if (data.length) base.warnings.push("Data files are not stored on this server (no cloud storage configured).");
      return result({ ...base, ...linkOnly(markdown, theme, css2) }, "Created");
    }
    const { id, key } = await store.create({ name: title, md: markdown, theme, css: css2, images: [...imgs, ...data], owner: user ? user.uid : "mcp" });
    const out = await result({ ...base, ...links(id), deck_id: id, edit_key: key }, "Created");
    return withData(out, data);
  }));

  server.registerTool("update_presentation", {
    title: "Update a presentation",
    description: "Change a presentation made with create_presentation, keeping its link. Send only what changes: markdown replaces the whole text; images and files are added (or replace ones of the same name).",
    inputSchema: {
      deck_id: z.string().describe("deck_id from create_presentation"),
      edit_key: z.string().optional().describe("edit_key from create_presentation; not needed when signed in as the presentation's owner"),
      title: z.string().max(200).optional(),
      markdown: z.string().optional().describe("The whole deck as Sliqtly Markdown"),
      ...deckFields,
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
    _meta: uiMeta(),
  }, guarded("update_presentation", async ({ deck_id, edit_key, title, markdown, theme, css, css_mode = "extend", images, files }) => {
    if (markdown != null && markdown.length > MAX_MD) throw new InputError("markdown is larger than 300 KB.");
    const { cur, mine } = await editable(deck_id, edit_key);
    const th = theme ?? cur.theme ?? "aurora";
    // a new theme with no new css drops the old theme's sheet
    const css2 = css != null ? await sheet(th, css, css_mode) : theme != null && theme !== cur.theme ? null : undefined;
    const imgs = await loadImages(images, fetchImpl);
    const data = await loadDataFiles(files, fetchImpl, readWorkbook);
    const saved = await store.update(deck_id, mine ? null : edit_key, { name: title, md: markdown, theme, css: css2, images: [...imgs, ...data] });
    return withData(await updated(deck_id, saved), data);
  }));

  server.registerTool("bind_chart_data", {
    title: "Connect a chart to live data",
    description: "Point one ```vega-lite chart of a presentation at live data: a CSV or JSON URL, or a Google Sheet shared as \"Anyone with the link\". The data is read when the deck opens; the rest of the chart's spec stays. Keeps the presentation's link.",
    inputSchema: {
      deck_id: z.string().describe("deck_id from create_presentation"),
      edit_key: z.string().optional().describe("edit_key from create_presentation; not needed when signed in as the presentation's owner"),
      chart: z.union([z.number().int().min(1), z.string()]).describe("Which chart: its number among the deck's vega-lite charts (1 = first), or the title of the slide it is on"),
      source: z.union([
        z.string().describe("https URL of a CSV or JSON file, a Google Sheets link (…/spreadsheets/d/<id>/edit#gid=0), or sheet://<id>/<Sheet>!A:B"),
        z.object({
          google_sheets: z.string().describe("The sheet's id or link"),
          sheet: z.string().optional().describe("Tab name, e.g. Monthly"),
          range: z.string().optional().describe('Cells, e.g. "A:B" or "Monthly!A:B"'),
        }),
      ]).describe("Where the chart's data comes from"),
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
    _meta: uiMeta(),
  }, guarded("bind_chart_data", async ({ deck_id, edit_key, chart, source }) => {
    const { cur, mine } = await editable(deck_id, edit_key);
    const bound = bindChartData(cur.md || "", chart, source);
    if (bound.md.length > MAX_MD) throw new InputError("markdown would be larger than 300 KB.");
    const saved = await store.update(deck_id, mine ? null : edit_key, { md: bound.md });
    const out = await updated(deck_id, saved, `Chart ${bound.index}${bound.title ? ` (on "${bound.title}")` : ""} now reads ${JSON.stringify(bound.spec.data)}. PDF and PPTX exports are snapshots of the data when exported.`);
    out.structuredContent = { ...out.structuredContent, chart: bound.index, spec: bound.spec };
    return out;
  }));

  server.registerTool("get_presentation", {
    title: "Read a presentation",
    description: "Read a Sliqtly presentation's Markdown, theme, CSS, pictures and files (with what is in its .xlsx workbooks) by its deck_id (the id in https://sliqtly.com/s/<id>), to revise it.",
    inputSchema: { deck_id: z.string().describe("The id in the share link /s/<id>") },
    annotations: { readOnlyHint: true, openWorldHint: false },
    _meta: { securitySchemes: EITHER },
  }, guarded("get_presentation", async ({ deck_id }) => {
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured).");
    if (!/^[A-Za-z0-9]{6,32}$/.test(deck_id)) throw new InputError("deck_id is the 10-character id in the share link.");
    const d = await store.get(deck_id);
    if (!d) throw new InputError(`No presentation ${deck_id}.`);
    const files = await filesOf(deck_id, d);
    const out = {
      title: d.name || "", theme: d.theme || "", markdown: d.md || "", css: d.css ?? null,
      images: (d.files || []).filter((f) => /^media\//.test(f.path)).map((f) => ({ name: f.path.replace(/^media\//, ""), type: f.type, size: f.size })),
      files,
      ...links(deck_id),
    };
    const others = files.filter((f) => f.kind !== "picture");
    const text = [
      `"${out.title}", theme ${out.theme || "(document's own)"}, ${out.images.length} pictures: ${out.images.map((i) => i.name).join(", ") || "none"}.`,
      others.length ? "Files:\n" + filesText(others) : "",
      out.css != null ? "It has its own stylesheet (css below)." : "",
      "```markdown", out.markdown, "```",
      out.css != null ? "```css\n" + out.css + "\n```" : "",
    ].filter(Boolean).join("\n");
    return { content: [{ type: "text", text }], structuredContent: out };
  }));

  // Every file a deck keeps, and what is in its workbooks. Shared by
  // list_files and get_presentation.
  async function filesOf(deck_id, d) {
    const kindOf = (p) => /^media\//.test(p) ? "picture" : /\.xlsx$/i.test(p) ? "workbook" : /^charts\//.test(p) ? "chart" : /^data\//.test(p) ? "data" : "file";
    const files = (d.files || []).map((f) => ({ path: f.path, kind: kindOf(f.path), type: f.type || "", size: f.size || 0 }));
    for (const f of files) {
      if (f.kind !== "workbook") continue;
      if (f.size > 20 * 1024 * 1024) { f.note = "too large to read here"; continue; }
      try {
        f.sheets = workbookInfo(await store.fileBytes(deck_id, f.path), f.path);
      } catch (e) {
        f.note = "could not read the workbook: " + (e && e.message ? e.message : e);
      }
    }
    return files;
  }

  function filesText(files) {
    if (!files.length) return "No files.";
    const lines = [];
    if (files.some((f) => f.kind !== "picture")) lines.push("read_file reads the values of a data file or a workbook's sheet.");
    for (const f of files) {
      lines.push(`- ${f.path} (${f.kind}, ${f.size} bytes)${f.note ? " — " + f.note : ""}`);
      for (const s of f.sheets || []) {
        lines.push(`  - sheet "${s.name}": ${s.rows} rows; columns ${s.columns.map((c) => JSON.stringify(c)).join(", ") || "(none)"}; read as ${s.csv}`);
      }
    }
    return lines.join("\n");
  }

  server.registerTool("list_files", {
    title: "List a presentation's files",
    description: "List the files a Sliqtly presentation keeps — pictures (media/), data (data/), chart specs (charts/) and workbooks (.xlsx) — by its deck_id. For each workbook: its sheets, their columns and row counts, and the CSV name a ```table, ```sheet or vega-lite chart reads a sheet by (the editor derives those CSVs from the workbook; they are not separate files). read_file gives the values.",
    inputSchema: { deck_id: z.string().describe("The id in the share link /s/<id>") },
    annotations: { readOnlyHint: true, openWorldHint: false },
    _meta: { securitySchemes: EITHER },
  }, guarded("list_files", async ({ deck_id }) => {
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured).");
    if (!/^[A-Za-z0-9]{6,32}$/.test(deck_id)) throw new InputError("deck_id is the 10-character id in the share link.");
    const d = await store.get(deck_id);
    if (!d) throw new InputError(`No presentation ${deck_id}.`);
    const files = await filesOf(deck_id, d);
    return { content: [{ type: "text", text: filesText(files) }], structuredContent: { files } };
  }));

  // The values of one of a deck's files: a workbook's sheet (by the
  // workbook's path, or the CSV name the deck reads the sheet by) or a CSV,
  // TSV, JSON or text file. Rows come a window at a time.
  async function readFile(deck_id, d, path, sheetName, offset, limit) {
    const want = path.trim().replace(/^\/+/, "");
    const files = d.files || [];
    const bytes = async (f) => {
      if (f.size > 20 * 1024 * 1024) throw new InputError(`${f.path} is too large to read here.`);
      return store.fileBytes(deck_id, f.path);
    };
    let f = files.find((x) => x.path === want);
    let sheets = null;
    if (f && /\.xlsx$/i.test(f.path)) sheets = workbookTables(await bytes(f), f.path);
    else if (!f) {
      // a sheet's CSV name: data/<book>-<Sheet>.csv or data/<book>.csv
      for (const w of files.filter((x) => /\.xlsx$/i.test(x.path))) {
        const base = w.path.replace(/\.xlsx$/i, "");
        if (want !== base + ".csv" && !want.startsWith(base + "-")) continue;
        const all = workbookTables(await bytes(w), w.path);
        const hit = all.find((s) => s.csv === want);
        if (hit) { f = w; sheets = all; sheetName = hit.name; break; }
      }
    }
    if (!f) {
      const have = files.filter((x) => !/^media\//.test(x.path)).map((x) => x.path);
      throw new InputError(`No file ${want} in presentation ${deck_id}.${have.length ? " Its files: " + have.join(", ") + "." : " It keeps no data files."}`);
    }
    if (/^media\//.test(f.path)) throw new InputError(`${f.path} is a picture.`);
    let columns;
    let rows;
    let sheet = null;
    let note = "";
    if (sheets) {
      if (!sheets.length) throw new InputError(`${f.path}: no sheet has values.`);
      const s = sheetName != null ? sheets.find((x) => x.name.toLowerCase() === String(sheetName).trim().toLowerCase()) : sheets[0];
      if (!s) throw new InputError(`${f.path} has no sheet "${sheetName}". Its sheets: ${sheets.map((x) => JSON.stringify(x.name)).join(", ")}.`);
      sheet = { name: s.name, csv: s.csv };
      columns = s.columns;
      rows = s.data;
      if (sheets.length > 1) note = `Other sheets: ${sheets.filter((x) => x !== s).map((x) => JSON.stringify(x.name)).join(", ")} (read with sheet).`;
      note += (note ? " " : "") + "Dates are Excel serial numbers (days since 1899-12-30).";
    } else if (/\.(csv|tsv)$/i.test(f.path)) {
      const all = parseCsv((await bytes(f)).toString("utf8"), /\.tsv$/i.test(f.path) ? "\t" : ",").filter((r) => r.some((v) => v.trim() !== ""));
      columns = all[0] || [];
      rows = all.slice(1);
    } else {
      // JSON, a chart spec, text: as it is, cut at 200 KB
      const text = (await bytes(f)).toString("utf8");
      const cut = text.length > 200 * 1024;
      return {
        content: [{ type: "text", text: `${f.path} (${text.length} characters${cut ? ", first 200 KB" : ""}):\n` + (cut ? text.slice(0, 200 * 1024) : text) }],
        structuredContent: { path: f.path, text: cut ? text.slice(0, 200 * 1024) : text, truncated: cut },
      };
    }
    const window = rows.slice(offset, offset + limit).map((r) => {
      const out = r.map((v) => (v == null ? "" : String(v)));
      while (out.length < columns.length) out.push("");
      return out;
    });
    const next = offset + window.length < rows.length ? offset + window.length : null;
    const text = [
      `${f.path}${sheet ? ` sheet "${sheet.name}" (read in the deck as ${sheet.csv})` : ""}: ${rows.length} data rows, columns ${columns.map((c) => JSON.stringify(c)).join(", ")}.`,
      `Rows ${window.length ? offset + 1 : 0}–${offset + window.length} of ${rows.length}${next != null ? `; next: offset ${next}` : ""}.`,
      note,
      "```csv", toCsv([columns, ...window]), "```",
    ].filter(Boolean).join("\n");
    return {
      content: [{ type: "text", text }],
      structuredContent: { path: f.path, sheet, columns, rows: window, total_rows: rows.length, offset, next_offset: next },
    };
  }

  server.registerTool("read_file", {
    title: "Read a presentation's data file",
    description: "Read the values in one of a Sliqtly presentation's data files by its deck_id and path: a workbook's sheet (path data/x.xlsx with sheet, or the sheet's CSV name from list_files such as data/x-Sheet.csv), a CSV or TSV (as rows), or a JSON or text file. Rows come up to `limit` at a time from `offset`.",
    inputSchema: {
      deck_id: z.string().describe("The id in the share link /s/<id>"),
      path: z.string().describe("The file's path from list_files, e.g. data/sales.xlsx or data/sales-Q1.csv"),
      sheet: z.string().optional().describe("A workbook's sheet name; the first sheet when left out"),
      offset: z.number().int().min(0).optional().describe("First data row to return (0 = the row under the header)"),
      limit: z.number().int().min(1).max(2000).optional().describe("How many data rows (default 200, at most 2000)"),
    },
    annotations: { readOnlyHint: true, openWorldHint: false },
    _meta: { securitySchemes: EITHER },
  }, guarded("read_file", async ({ deck_id, path, sheet, offset = 0, limit = 200 }) => {
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured).");
    if (!/^[A-Za-z0-9]{6,32}$/.test(deck_id)) throw new InputError("deck_id is the 10-character id in the share link.");
    const d = await store.get(deck_id);
    if (!d) throw new InputError(`No presentation ${deck_id}.`);
    return readFile(deck_id, d, path, sheet, offset, limit);
  }));

  server.registerTool("list_presentations", {
    title: "List my presentations",
    description: "List the signed-in user's own Sliqtly presentations (newest first) with their links and deck_ids. Needs sign-in.",
    inputSchema: {},
    annotations: { readOnlyHint: true, openWorldHint: false },
    _meta: { securitySchemes: [{ type: "oauth2", scopes: ["decks"] }] },
  }, guarded("list_presentations", async () => {
    if (!user) {
      return {
        isError: true,
        content: [{ type: "text", text: "Listing presentations needs sign-in. Connect Sliqtly with sign-in (Google) to keep presentations in your own account." }],
        _meta: opts.signIn ? { "mcp/www_authenticate": [`Bearer resource_metadata="${opts.signIn}", error="insufficient_scope", error_description="Sign in to Sliqtly to list your presentations"`] } : undefined,
      };
    }
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured).");
    const decks = (await store.list(user.uid)).map((d) => ({ deck_id: d.id, title: d.name || "", theme: d.theme || "", updated: d.updated || d.created || null, ...links(d.id) }));
    const text = decks.length
      ? decks.map((d) => `- ${d.title || "(untitled)"} (${d.deck_id}): ${d.share_url}`).join("\n")
      : "No presentations yet.";
    return { content: [{ type: "text", text }], structuredContent: { presentations: decks } };
  }));

  const previewMeta = { title: "Sliqtly presentation", description: "Shows the presentation inline", mimeType: APP_MIME, _meta: { ui: { csp, prefersBorder: false } } };
  const previewContents = (uri) => ({
    contents: [{
      uri,
      mimeType: APP_MIME,
      text: PREVIEW,
      _meta: {
        ui: { csp, prefersBorder: false },
        "openai/widgetCSP": { connect_domains: connects, resource_domains: resources, frame_domains: frames },
        "openai/widgetDescription": "The Sliqtly presentation, playable inline.",
        "openai/widgetPrefersBorder": true,
      },
    }],
  });
  server.registerResource("preview", PREVIEW_URI, previewMeta, async () => previewContents(PREVIEW_URI));
  // a client that took the tool list before the preview changed still asks
  // for the older name: it gets the current preview, not "Couldn't open app"
  server.registerResource("preview-older", new ResourceTemplate("ui://sliqtly/preview-{hash}.html", { list: undefined }), previewMeta,
    async (uri) => previewContents(uri.href));

  return server;
}
