# Sliqtly MCP server

Lets an AI assistant (Claude, ChatGPT, Cursor, any MCP client) make a Sliqtly
presentation: it sends Markdown, a theme, extra CSS and pictures, and gets
back a link that opens the presentation, plus a link that opens a copy in the
editor. The instructions page for people is [`web/connect.html`](../web/connect.html)
(`https://sliqtly.com/connect.html`; `sliqtly.web.app` serves the same).

Remote MCP over Streamable HTTP, stateless (`POST` only; `GET` from a browser
is sent to the instructions page). Sign-in is optional ([`src/oauth.js`](src/oauth.js)):
without it everything works as before and decks belong to no one
(`owner: "mcp"`, changed with the `edit_key`); signed in with the PRO
account's Google login, decks are the person's own (`owner: <uid>`), changed
without a key, and `list_presentations` lists them.

| Tool | |
| --- | --- |
| `sliqtly_guide` | The Markdown syntax, themes and CSS selectors ([`guide.md`](guide.md)) |
| `create_presentation` | `title`, `markdown`, `theme`, `css`, `css_mode`, `images`, `files` (`.xlsx`, `.csv`, `.json` kept under `data/`) → `share_url`, `edit_url`, `deck_id`, `edit_key` |
| `update_presentation` | `deck_id` + `edit_key`, and what changes; the link stays |
| `bind_chart_data` | `deck_id` + `edit_key`, `chart` (number or slide title), `source` (CSV/JSON URL or Google Sheet): the chart reads that data live |
| `get_presentation` | `deck_id` → the Markdown, theme, CSS, pictures and files |
| `list_files` | `deck_id` → every file the deck keeps; for each `.xlsx`, its sheets, columns, row counts and the CSV name a sheet is read by |
| `read_file` | `deck_id`, `path` (a data file, a workbook with `sheet`, or a sheet's CSV name), `offset`, `limit` → its rows as CSV, or a JSON/text file's text |
| `write_workbook` | `deck_id`, `edit_key`, `path` (data/x.xlsx), `sheets` [{`name`, `rows` or `csv`}] → writes the whole workbook in place (values only) |
| `list_presentations` | the signed-in user's decks (asks for sign-in otherwise) |

## Sign-in

The server is its own OAuth 2.1 authorization server: `/.well-known/oauth-protected-resource[/mcp]`,
`/.well-known/oauth-authorization-server`, `/oauth/register` (dynamic
registration), `/oauth/authorize`, `/oauth/token` (PKCE S256, refresh tokens
rotate). Clients may also be known by a client ID metadata document URL.
`/oauth/authorize` sends the browser to `web/oauth.html`, which signs in with
Firebase Auth (Google) and posts the ID token to `/oauth/approve`; the server
verifies it with the Admin SDK and hands the client a code. Requests, codes and
tokens are kept as SHA-256 hashes in `mcp_oauth_requests`, `mcp_oauth_codes`,
`mcp_oauth_tokens` and the registered clients in `mcp_oauth_clients` (no
client rule reaches them). A request carries no token → anonymous; a token
that does not hold → 401 with `WWW-Authenticate`, so the client refreshes or
signs in again. Google sign-in needs `sliqtly.com` among Firebase Auth's
authorized domains.

A deck is the same share the editor's Share button makes (`web/sliqtly.js`):
`shares/{id}` in Firestore with `owner: "mcp"`, `source: "mcp"`, its pictures
in Storage under `shares/{id}/media/`. The edit key's SHA-256 is in
`mcp_keys/{id}`, which no client rule reaches. `css` is added after the
theme's own sheet (fetched from `/themes/<theme>.css`), so the deck stores the
whole stylesheet as the editor does for an edited theme.

`create_presentation` and `update_presentation` name a UI resource,
`ui://sliqtly/preview-<hash>.html` ([`src/preview.html`](src/preview.html), named by its contents): MCP Apps
hosts (Claude) and ChatGPT show the presentation inline in the chat, in a
frame of the share link.

Limits: Markdown 300 KB, CSS 100 KB, 20 pictures of 5 MB per call (PNG, JPEG,
GIF, WebP, SVG; public `https` URLs or base64), 60 writes per caller per 10
minutes per instance.

## Run and test

```
cd mcp
npm install
npm test            # the tools over HTTP, Firestore/Storage faked
npm start           # http://localhost:8790/mcp
```

Without `GOOGLE_APPLICATION_CREDENTIALS`, `npm start` keeps nothing: the deck
travels compressed in the link (`#md=…`, the editor's text-only share) and
pictures are dropped. With a service account key of the `sliqtly` project it
writes real shares. `SLIQTLY_URL` changes the site the links point at.

Try it with the MCP Inspector: `npx @modelcontextprotocol/inspector`, transport
Streamable HTTP, URL `http://localhost:8790/mcp`.

## Deploy

The Cloud Function `mcp` (2nd gen, `europe-west1`, [`index.js`](index.js)):
Actions → **Deploy MCP** → Run workflow (`.github/workflows/deploy-mcp.yml`),
or by hand from the repository root:

```
firebase deploy --only functions:mcp --project sliqtly
```

`firebase.json` rewrites `/mcp` to it, so it answers at
`https://sliqtly.com/mcp` (and `https://sliqtly.web.app/mcp`). A Hosting
deploy fails on a rewrite to a function that does not exist, so on a new
project the function is deployed before the page.

## Directories

When it works from the instructions page, the next step is the listings:
Anthropic's connector directory, OpenAI's app directory (Apps SDK), Cursor's
MCP directory. They ask for a privacy policy and a support contact; the
OAuth sign-in they expect is in place.
