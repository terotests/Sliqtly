// SPDX-License-Identifier: AGPL-3.0-or-later
//
// What is in a workbook, for an assistant: its sheets, each sheet's columns
// (the first row with two or more values, the way the editor finds the header
// under a title row), how many rows of data follow, and the name the deck's
// tables and charts read the sheet by.
//
// The editor keeps an .xlsx as itself and reads each sheet as CSV under
// `data/<book>-<Sheet>.csv` (`data/<book>.csv` for a one-sheet book) —
// web/main.js `workbookSheets`. This gives the same names, so an assistant
// can write a ```table, ```sheet or chart fence that the editor will draw.
//
// No dependency: a zip's central directory, inflate from node:zlib, and the
// few XML elements a sheet's values live in.
import zlib from "node:zlib";

/** name → Buffer for every entry of a zip. */
export function unzip(buf) {
  const out = new Map();
  // the end of central directory record: the last PK\5\6 in the file
  let eocd = -1;
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 65557); i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) { eocd = i; break; }
  }
  if (eocd < 0) throw new Error("not a zip file");
  const count = buf.readUInt16LE(eocd + 10);
  let at = buf.readUInt32LE(eocd + 16);
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(at) !== 0x02014b50) throw new Error("broken zip directory");
    const method = buf.readUInt16LE(at + 10);
    const csize = buf.readUInt32LE(at + 20);
    const nameLen = buf.readUInt16LE(at + 28);
    const extraLen = buf.readUInt16LE(at + 30);
    const commentLen = buf.readUInt16LE(at + 32);
    const local = buf.readUInt32LE(at + 42);
    const name = buf.toString("utf8", at + 46, at + 46 + nameLen);
    const lNameLen = buf.readUInt16LE(local + 26);
    const lExtraLen = buf.readUInt16LE(local + 28);
    const start = local + 30 + lNameLen + lExtraLen;
    const raw = buf.subarray(start, start + csize);
    if (method === 0) out.set(name, raw);
    else if (method === 8) out.set(name, zlib.inflateRawSync(raw));
    at += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}

const decode = (s) => s
  .replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&apos;/g, "'")
  .replace(/&#(\d+);/g, (_, d) => String.fromCodePoint(+d))
  .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCodePoint(parseInt(h, 16)))
  .replace(/&amp;/g, "&");

// all the text of the <t> elements inside `xml` (a rich-text run is several)
const texts = (xml) => [...xml.matchAll(/<t(?:\s[^>]*)?>([\s\S]*?)<\/t>/g)].map((m) => decode(m[1])).join("");

function attr(tag, name) {
  const m = tag.match(new RegExp(`\\s${name}="([^"]*)"`));
  return m ? decode(m[1]) : "";
}

function colIndex(ref) {
  const letters = (ref.match(/^[A-Z]+/) || [""])[0];
  let n = 0;
  for (const ch of letters) n = n * 26 + (ch.charCodeAt(0) - 64);
  return n - 1;
}

// a stored double as the editor's CSV writes it (PresData.rgr): 15
// significant digits, so 0.1+0.2 reads 0.3
function number(v) {
  if (v === "" || !Number.isFinite(Number(v))) return v;
  return String(Number(Number(v).toPrecision(15)));
}

/** The sheets of a workbook as rows of strings (values as stored; dates are serial numbers). */
export function readWorkbook(buf) {
  const files = unzip(buf);
  const text = (name) => (files.has(name) ? files.get(name).toString("utf8") : "");
  const shared = [...text("xl/sharedStrings.xml").matchAll(/<si>([\s\S]*?)<\/si>/g)].map((m) => texts(m[1]));
  const rels = new Map();
  for (const m of text("xl/_rels/workbook.xml.rels").matchAll(/<Relationship\b[^>]*>/g)) {
    rels.set(attr(m[0], "Id"), attr(m[0], "Target"));
  }
  const sheets = [];
  for (const m of text("xl/workbook.xml").matchAll(/<sheet\b[^>]*>/g)) {
    const name = attr(m[0], "name");
    const target = rels.get(attr(m[0], "r:id")) || "";
    const path = target.startsWith("/") ? target.slice(1) : "xl/" + target.replace(/^\.\//, "");
    const rows = [];
    for (const r of text(path).matchAll(/<row\b[^>]*>([\s\S]*?)<\/row>/g)) {
      const row = [];
      for (const c of r[1].matchAll(/<c\b([^>]*?)(?:\/>|>([\s\S]*?)<\/c>)/g)) {
        const head = "<c" + c[1] + ">";
        const body = c[2] || "";
        const type = attr(head, "t");
        const v = (body.match(/<v>([\s\S]*?)<\/v>/) || [, ""])[1];
        let value = "";
        if (type === "s") value = shared[+v] ?? "";
        else if (type === "inlineStr") value = texts(body);
        else if (type === "b") value = v === "1" ? "TRUE" : "FALSE";
        else if (type === "" || type === "n") value = number(decode(v));
        else value = decode(v);
        const ref = attr(head, "r");
        const at = ref ? colIndex(ref) : row.length;
        while (row.length < at) row.push("");
        row[at] = value;
      }
      rows.push(row);
    }
    sheets.push({ name, rows });
  }
  return sheets;
}

const filled = (row) => row.filter((v) => String(v).trim() !== "").length;

// The sheets with values, each with the row its header is on (a title row
// above the header is skipped, as the editor does) and the CSV name the
// deck reads it by.
function tables(buf, path) {
  const sheets = readWorkbook(buf).filter((s) => s.rows.some((r) => filled(r) > 0));
  const base = path.replace(/\.xlsx$/i, "");
  return sheets.map((s) => {
    let top = 0;
    while (top < s.rows.length - 1 && filled(s.rows[top]) < 2 && s.rows.slice(top + 1).some((r) => filled(r) >= 2)) top++;
    const header = (s.rows[top] || []).map((v) => String(v).trim());
    while (header.length && header[header.length - 1] === "") header.pop();
    const data = s.rows.slice(top + 1).filter((r) => filled(r) > 0);
    const csv = sheets.length > 1 ? `${base}-${s.name.replace(/[\\/:*?"<>|\s]+/g, "-")}.csv` : base + ".csv";
    return { name: s.name, columns: header, data, csv };
  });
}

/**
 * What an assistant needs to know about a kept workbook at `path`
 * ("data/sales.xlsx"): per sheet its name, columns, data rows, and the CSV
 * name the deck reads it by.
 */
export function workbookInfo(buf, path) {
  return tables(buf, path).map((t) => ({ name: t.name, columns: t.columns, rows: t.data.length, csv: t.csv }));
}

/**
 * The values of a workbook's sheets: [{ name, csv, columns, data: rows of
 * strings }] (dates stay Excel serial numbers).
 */
export function workbookTables(buf, path) {
  return tables(buf, path);
}

/** Rows of a CSV (or, with sep "\t", TSV) text. */
export function parseCsv(text, sep = ",") {
  const rows = [];
  let row = [];
  let cell = "";
  let quoted = false;
  const t = String(text).replace(/^\uFEFF/, "");
  for (let i = 0; i < t.length; i++) {
    const ch = t[i];
    if (quoted) {
      if (ch === '"' && t[i + 1] === '"') { cell += '"'; i++; }
      else if (ch === '"') quoted = false;
      else cell += ch;
    } else if (ch === '"' && cell === "") quoted = true;
    else if (ch === sep) { row.push(cell); cell = ""; }
    else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && t[i + 1] === "\n") i++;
      row.push(cell); rows.push(row); row = []; cell = "";
    } else cell += ch;
  }
  if (cell !== "" || row.length) { row.push(cell); rows.push(row); }
  return rows;
}

/** Rows as CSV text. */
export function toCsv(rows) {
  const cell = (v) => {
    const s = v == null ? "" : String(v);
    return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  return rows.map((r) => r.map(cell).join(",")).join("\n");
}
