// The Sliqtly MCP server: tools that turn Markdown (+ a theme, CSS and
// pictures) into a presentation at sliqtly.com and hand back its link.
//
// Tools: sliqtly_guide, create_presentation, update_presentation,
// get_presentation. create/update also name a UI resource (MCP Apps, and the
// same template for ChatGPT) that shows the deck inline in the chat.

import fs from "node:fs";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";
import { THEMES, MAX_MD, MAX_CSS, InputError, loadImages, outline, warnings } from "./deck.js";
import { packText } from "./store.js";

const GUIDE = fs.readFileSync(new URL("../guide.md", import.meta.url), "utf8");
const PREVIEW = fs.readFileSync(new URL("./preview.html", import.meta.url), "utf8");
export const VERSION = "1.0.0";
const PREVIEW_URI = "ui://sliqtly/preview.html";
const APP_MIME = "text/html;profile=mcp-app";
// the preview frames the share link; both of the site's domains serve it
const SITES = ["https://sliqtly.com", "https://sliqtly.web.app"];

const imageSchema = z.object({
  name: z.string().describe('File name the Markdown uses as media/<name>, e.g. "team.jpg" → ![](media/team.jpg)'),
  url: z.string().optional().describe("A public https URL of the picture"),
  data_base64: z.string().optional().describe("The picture's bytes as base64 (instead of url)"),
  mime_type: z.string().optional().describe("image/png, image/jpeg, image/gif, image/webp or image/svg+xml; inferred from the name when left out"),
});

const deckFields = {
  theme: z.enum(THEMES).optional().describe("Theme: aurora (default), nebula, carbon, ember, midnight (dark); corporate, editorial (light)"),
  css: z.string().optional().describe("CSS rules added on top of the theme (selectors: page, document, h1, h2, p, list, li, code, table, chart, diagram, .lead …). See sliqtly_guide."),
  css_mode: z.enum(["extend", "replace"]).optional().describe("extend (default): css is added after the theme's rules. replace: css is the whole stylesheet."),
  images: z.array(imageSchema).optional().describe("Pictures the Markdown refers to as media/<name>"),
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

  function result(out, verb) {
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
    return { content: [{ type: "text", text: lines.join("\n") }], structuredContent: out, _meta: uiMeta() };
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
    description: "Create a Sliqtly slide presentation from Markdown (# title slide, ## per slide), an optional theme, extra CSS and pictures. Returns a link that opens the presentation and a link to edit a copy in the Sliqtly editor.",
    inputSchema: {
      title: z.string().max(200).describe("The presentation's name"),
      markdown: z.string().describe("The whole deck as Sliqtly Markdown"),
      ...deckFields,
    },
    annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
    _meta: uiMeta(),
  }, guarded("create_presentation", async ({ title, markdown, theme = "aurora", css, css_mode = "extend", images }) => {
    if (markdown.length > MAX_MD) throw new InputError("markdown is larger than 300 KB.");
    if (!markdown.trim()) throw new InputError("markdown is empty.");
    const css2 = await sheet(theme, css, css_mode);
    const imgs = await loadImages(images, fetchImpl);
    const names = imgs.map((i) => i.name);
    const base = { title, theme, slides: outline(markdown).titles.length, warnings: warnings(markdown, names) };
    if (store.kind === "link") {
      if (imgs.length) base.warnings.push("Pictures are not stored on this server (no cloud storage configured); the slides show without them.");
      return result({ ...base, ...linkOnly(markdown, theme, css2) }, "Created");
    }
    const { id, key } = await store.create({ name: title, md: markdown, theme, css: css2, images: imgs, owner: user ? user.uid : "mcp" });
    return result({ ...base, ...links(id), deck_id: id, edit_key: key }, "Created");
  }));

  server.registerTool("update_presentation", {
    title: "Update a presentation",
    description: "Change a presentation made with create_presentation, keeping its link. Send only what changes: markdown replaces the whole text; images are added (or replace pictures of the same name).",
    inputSchema: {
      deck_id: z.string().describe("deck_id from create_presentation"),
      edit_key: z.string().optional().describe("edit_key from create_presentation; not needed when signed in as the presentation's owner"),
      title: z.string().max(200).optional(),
      markdown: z.string().optional().describe("The whole deck as Sliqtly Markdown"),
      ...deckFields,
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
    _meta: uiMeta(),
  }, guarded("update_presentation", async ({ deck_id, edit_key, title, markdown, theme, css, css_mode = "extend", images }) => {
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured): call create_presentation again with the whole deck.");
    if (markdown != null && markdown.length > MAX_MD) throw new InputError("markdown is larger than 300 KB.");
    const cur = await store.get(deck_id);
    const mine = !!(cur && user && cur.owner === user.uid);
    if (!cur || (cur.source !== "mcp" && !mine)) throw new InputError(`No presentation ${deck_id} that this server can change.`);
    if (!mine && !edit_key) throw new InputError("edit_key is needed: the presentation is not this signed-in user's own.");
    const th = theme ?? cur.theme ?? "aurora";
    // a new theme with no new css drops the old theme's sheet
    const css2 = css != null ? await sheet(th, css, css_mode) : theme != null && theme !== cur.theme ? null : undefined;
    const imgs = await loadImages(images, fetchImpl);
    const saved = await store.update(deck_id, mine ? null : edit_key, { name: title, md: markdown, theme, css: css2, images: imgs });
    if (!saved) throw new InputError("The edit_key does not match this presentation.");
    const stored = (saved.files || []).map((f) => f.path.replace(/^media\//, ""));
    return result({
      title: saved.name, theme: saved.theme, slides: outline(saved.md).titles.length,
      warnings: warnings(saved.md, [], stored).filter((w) => !w.startsWith("Image")),
      ...links(deck_id), deck_id,
    }, "Updated");
  }));

  server.registerTool("get_presentation", {
    title: "Read a presentation",
    description: "Read a Sliqtly presentation's Markdown, theme, CSS and picture list by its deck_id (the id in https://sliqtly.com/s/<id>), to revise it.",
    inputSchema: { deck_id: z.string().describe("The id in the share link /s/<id>") },
    annotations: { readOnlyHint: true, openWorldHint: false },
    _meta: { securitySchemes: EITHER },
  }, guarded("get_presentation", async ({ deck_id }) => {
    if (store.kind === "link") throw new InputError("This server keeps no decks (no cloud storage configured).");
    if (!/^[A-Za-z0-9]{6,32}$/.test(deck_id)) throw new InputError("deck_id is the 10-character id in the share link.");
    const d = await store.get(deck_id);
    if (!d) throw new InputError(`No presentation ${deck_id}.`);
    const out = {
      title: d.name || "", theme: d.theme || "", markdown: d.md || "", css: d.css ?? null,
      images: (d.files || []).map((f) => ({ name: f.path.replace(/^media\//, ""), type: f.type, size: f.size })),
      ...links(deck_id),
    };
    const text = [
      `"${out.title}", theme ${out.theme || "(document's own)"}, ${out.images.length} pictures: ${out.images.map((i) => i.name).join(", ") || "none"}.`,
      out.css != null ? "It has its own stylesheet (css below)." : "",
      "```markdown", out.markdown, "```",
      out.css != null ? "```css\n" + out.css + "\n```" : "",
    ].filter(Boolean).join("\n");
    return { content: [{ type: "text", text }], structuredContent: out };
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

  server.registerResource("preview", PREVIEW_URI, {
    title: "Sliqtly presentation",
    description: "Shows the presentation inline",
    mimeType: APP_MIME,
    _meta: { ui: { csp: { frameDomains: frames }, prefersBorder: false } },
  }, async () => ({
    contents: [{
      uri: PREVIEW_URI,
      mimeType: APP_MIME,
      text: PREVIEW,
      _meta: {
        ui: { csp: { frameDomains: frames }, prefersBorder: false },
        "openai/widgetCSP": { connect_domains: [], resource_domains: [], frame_domains: frames },
        "openai/widgetDescription": "The Sliqtly presentation, playable inline.",
        "openai/widgetPrefersBorder": true,
      },
    }],
  }));

  return server;
}
