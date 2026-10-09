# Sliqtly MCP server in Ranger, compiled to Go

Sliqtly's MCP server for Claude, ChatGPT, Cursor and other MCP clients,
written in Ranger and compiled to Go for Cloud Run: one static binary in a
distroless container. It writes the same Firestore documents and Storage
paths as the editor's Share. `assets/guide.md` is the syntax guide the
`sliqtly_guide` tool returns: Core without `topic`, one topic with it
(`<!-- topic: name -->` lines cut it, [`rgr/Guide.rgr`](rgr/Guide.rgr)); `assets/preview.html` is the inline preview.

It replaced a Node.js server (`mcp/`, a Cloud Function) on 2026-10-03; the
two drifted apart with every change to one of them, so there is one server
now, and CI (`one-server` in `.github/workflows/mcp-go.yml`) fails if
`mcp/` comes back. The comparisons below are from when both existed.

This is what `sliqtly.com` runs: Hosting rewrites `/mcp`, `/oauth/**`, the
OAuth `/.well-known` documents and `/api/hit` to the Cloud Run service
`sliqtly-mcp` (europe-west1), deployed by Actions → **Deploy MCP (Go)**.
Rolling back is a revert of the change that removed `mcp/`, a Deploy MCP
of it, and the rewrites pointed back at the function.

## What is Ranger and what is Go

| | |
| --- | --- |
| [`rgr/App.rgr`](rgr/App.rgr) | routing, CORS, the `/mcp` transport checks, sign-in on a request, the `/api/hit` beacon |
| [`rgr/Stats.rgr`](rgr/Stats.rgr) | the site's cookieless visitor counts (`stats/<day>`), read by the Stats workflow (`ops/stats.mjs`) |
| [`rgr/Mcp.rgr`](rgr/Mcp.rgr) | MCP: JSON-RPC, `initialize`, `tools/*`, `resources/*` |
| [`rgr/Tools.rgr`](rgr/Tools.rgr) | the tools, their schemas and UI metadata, the preview resource; review comments (`list_comments`, `add_comment`, `resolve_comment`) read and write the editor's `review/comments.json` through its own model ([`src/PresReview.rgr`](../src/PresReview.rgr)) |
| [`rgr/Work.rgr`](rgr/Work.rgr), [`rgr/WorkStore.rgr`](rgr/WorkStore.rgr) | two assistants on one deck: `begin_work` / `end_work` claims (which slides, by whom, until when; `mcp_work/{id}`), and `update_presentation`'s `base_version`: an edit made on an older version is merged with what was saved since by RangerDiff's diff3 ([`src/RdText.rgr`](https://github.com/terotests/RangerDiff/blob/main/src/RdText.rgr), linked into Ranger as `gallery/rangerdiff` by `gen.mjs`), and refused with the slides it is on when both changed the same lines. Versions handed out are kept in `mcp_bases/{id}-{version}` (the newest 12). Board and deck are written only over what was read (`host_update_if`, a Firestore transaction or the folder store's lock) (`work_test.go`) |
| [`rgr/UserEdits.rgr`](rgr/UserEdits.rgr) | what the user changed by hand since an assistant last read or saved the deck (the board's `seen` version and `seenCss`): each change as a line diff with its slide and place (fenced block, front matter, css rule), and whether a save kept or overwrote it; shown on `get_presentation` and `update_presentation` as text and `structuredContent.user_edits` (`useredits_test.go`) |
| [`rgr/OAuth.rgr`](rgr/OAuth.rgr) | the OAuth 2.1 server: registration, authorize, approve, token, refresh |
| [`rgr/Store.rgr`](rgr/Store.rgr) | shares, who may read and change them (owner, visibility, MCP sessions), pictures, listing |
| [`rgr/Deck.rgr`](rgr/Deck.rgr) | the checks: picture names and types, outline, warnings |
| [`rgr/Charts.rgr`](rgr/Charts.rgr) | `bind_chart_data`: finds a deck's ```` ```vega-lite ```` charts and points one at a CSV/JSON URL or a Google Sheet |
| [`rgr/Files.rgr`](rgr/Files.rgr) | a deck's data files: `list_files`, `read_file`, `write_workbook` and `files` on create/update; workbooks read and tidied as the editor reads them |
| [`src/PresTrace.rgr`](../src/PresTrace.rgr) | `vectorize_image`: a deck's PNG or JPEG traced into an SVG with lib/evg's `EvgBitmapTracer`, compiled into the server like the rest; uses in the Markdown and the theme CSS pointed at it (`vectorize_test.go`) |
| [`xlsxwrite.go`](xlsxwrite.go) | the .xlsx `write_workbook` keeps (values only), behind `host_xlsx_write` |
| [`rgr/PresDataGo.rgr`](rgr/PresDataGo.rgr) | the editor's workbook reader ([`src/PresData.rgr`](../src/PresData.rgr), datagrid's XlsxLoader) compiled on its own to the package `presdata/` (generated, not committed): its XmlLite and the deck model's XmlCore both define `XmlAttr`, so they cannot share one compile |
| [`rgr/View.rgr`](rgr/View.rgr) | `GET /api/view/<id>`: a shared presentation for the public viewer (`web/view.js`), each slide laid out here by `Check.prepare` and sent as its display list at rest, so sliqtly.com ships a painter and no layout or editor code (`view_test.go`) |
| [`linkcard.go`](linkcard.go) | `GET /s/<id>`: the site's own `index.html` (read from Hosting, kept 30 s) with the deck's link card in its head (`og:title`, `og:description`, `og:image`; `rgr/View.rgr` `LinkCard`), so Slack, Teams and other chat apps show the deck's name, first words and first slide; `?slide=<n>` (the viewer's Share slide) gives that slide's heading, words and picture. `GET /api/card/<id>.jpg?v=<stamp>`: that first slide, 1200 px wide, drawn once per change of the deck. A private deck has no card (`linkcard_test.go`) |
| [`rgr/Check.rgr`](rgr/Check.rgr) | the deck read by the editor's own model ([`src/PresDeck.rgr`](../src/PresDeck.rgr)): slide count, slides that run over, charts and diagrams that are not drawn |
| [`rgr/Report.rgr`](rgr/Report.rgr) | the layout report: each element's place and size, the smallest text, how much of the slide is used, overlaps, a chart's or diagram's labels over each other, a table column that wraps; reads blocks and text runs only (`report_test.go`) |
| [`rgr/Layout.rgr`](rgr/Layout.rgr) | the report's input from the deck model: the layout's boxes grouped into elements, and what each drew in the slide's display list (`PresDeck.boxOfCmd`) |
| [`render.go`](render.go), [`fonts.go`](fonts.go) | `render_slide` / `render_overview`: a slide's display list (`EVGDisplayList.toJson`) painted to a JPEG with the editor's faces (copied from Ranger by `gen.mjs`, not committed) and DejaVu Sans for the symbols they lack (`symbols/`, committed), as `lib/evg/html/evg-html.js` paints it |
| [`svgraster.go`](svgraster.go), [`svgraster/`](svgraster/) | SVG pictures drawn for the renders and the contrast check: resvg as WebAssembly (`svgraster.wasm`, rebuilt by `svgraster/build.sh`, committed) run by wazero |
| [`rgr/SvgCheck.rgr`](rgr/SvgCheck.rgr) | each SVG picture as the player will show it: viewBox, shape against the slide, outside references, text, filters (`svgcheck_test.go`) |
| [`../src/PresExport.rgr`](../src/PresExport.rgr) | `export_presentation`: the editor's own PDF, PPTX, Word and web page export, moved out of `PresApp` so the server runs it too (Word and HTML pictures of charts and diagrams drawn by `render.go` `RenderCrop`); the file goes to Storage as `shares/<id>/exports/<name>.<format>` (`export_test.go`) |
| [`rgr/Edits.rgr`](rgr/Edits.rgr) | `update_presentation` edits: find/replace, one slide replaced or deleted, slides added after one; slides by number or title from the editor's slide spans (`src/PresSlideSpans.rgr`), all placed on the text before the edits (`edits_test.go`) |
| [`rgr/GitHub.rgr`](rgr/GitHub.rgr) | `read_github_pr`: a pull request, its files and commits from the GitHub API, and a review deck drafted from them (`github_test.go`) |
| [`rgr/McpJson.rgr`](rgr/McpJson.rgr) | JSON: Ranger's own `MfJ` (gallery/mfiles) and a writer |
| [`rgr/McpHost.rgr`](rgr/McpHost.rgr) | the operators the Go host implements |
| `sliqtly_mcp.go` | what Ranger compiles `rgr/` and the editor's model to (generated by `go generate`, not committed) |
| [`host.go`](host.go), [`net.go`](net.go), [`firebase.go`](firebase.go), [`main.go`](main.go) | the Go around it |

The Ranger code uses what Ranger's Go target already has: `HttpRequest` /
`HttpResponse` and their operators (method, path, headers, status, body out),
strings, lists, and the `MfJ` JSON value and reader from `gallery/mfiles`.
The Go host adds what it does not have, behind the operators in
`McpHost.rgr`: Firestore, Cloud Storage and Firebase Auth (Google's Go
clients), outbound HTTP to public addresses, SHA-256, random ids, deflate,
URL parsing, the request body, and the state that lasts between requests
(rate limiter, theme cache). Values cross as strings; documents as JSON.

`create_presentation` and `update_presentation` lay the deck out with the
editor's own model, `PresDeck` from `src/`, compiled into the same binary,
with the theme's sheet. The slide count is the player's, and the warnings say
which slide runs over and which chart or diagram is not drawn and why ("that
is not JSON"), so the model can fix the deck before anyone opens the link.
They also name text that does not stand out from what it is drawn over
(`rgr/Contrast.rgr`): each slide is drawn to the editor's display list, the
background pictures are decoded and sampled (`picgrid.go`), and every run of
text is judged as the editor's painter judges it (WCAG 4.5:1, 3:1 for large
text), with the least `bg-dim` or a text colour that would read. The old Node
server (`mcp/src/contrast.js`) had no layout and estimated the same from the
theme's colours and the picture.

Every create and update also returns a layout report (`rgr/Report.rgr`), in
the pixels of a 1920×1080 screen: each element's place and size, the
smallest text, how much of the slide the elements cover, and what looks
wrong (text under 20 px, elements over each other or past the edge, a lone
chart or picture on an empty slide, a chart's or diagram's labels drawn over
each other, a table column that wraps its cells). The deck is laid out with
the editor's faces (Open Sans, Noto Sans, Noto Emoji, from the same Ranger
checkout the editor's build copies them from), so lines break where the
editor breaks them, and the charts read the deck's data files. `render_slide`
and `render_overview` draw the same display lists to JPEG (`render.go`): one
slide at 960×540, or every slide as a numbered thumbnail in one picture.
GPU effects and picture corners are not drawn there. SVG pictures are
(`svgraster.go`): resvg built to WebAssembly (`svgraster/`, `./build.sh`
writes the committed `svgraster.wasm`) and run by wazero, so the server
stays one Go binary without cgo. The SVG is sized as the player sizes it
(`web/picture.js`), loads nothing from outside itself, and its text uses
the editor's faces. The contrast check reads an SVG background from the
same drawing, and `rgr/SvgCheck.rgr` reads each SVG picture for the result
of create/update (viewBox, shape against the slide, what loads from
outside, text, filters; `svgcheck_test.go`). How closely the preview matches the player
is measured against the W3C SVG 1.1 test suite in
[`bench/svgcompat/`](bench/svgcompat/README.md): 95 % of its 513 tests
draw the same or nearly the same.

Building `PresDeck` for Go needed fixes in Ranger's Go target (terotests/Ranger):
an array parameter the callee grows is now passed by pointer (ISSUES.md #58,
which also made `MfJ.toJson()` return ""), plus four smaller ones. `sha256`
still has no Go template and is a host operator here. With them, Sliqtly's own
deck checks (`npm run check`, `src/PresCheck.rgr`) pass on Go as on Node.

## Measured

Both servers in containers on this machine, `--cpus 1 --memory 512m`; Node is
`mcp/` with its production dependencies on `node:22-slim`, started with what
`index.js` does. Cold start is `docker run` until the first MCP `initialize`
is answered, median of 10. Throughput: `create_presentation` (link mode, no
Firestore), 40 concurrent, 5000 calls.

| | Ranger → Go | Node.js |
| --- | --- | --- |
| Image (uncompressed / compressed) | **53 MB / 12 MB** | 467 MB / 97 MB on node:22-slim |
| Cloud Functions' own runtime base image | — | 1.8 GB / 450 MB, plus 118 MB `node_modules` |
| Process ready (log line) | **under 1 ms** | 1.3 s (loading modules) |
| Cold start to first answer | **0.24 s** | 1.7 s |
| Memory, idle after first request | **5 MB** | 58 MB |
| Memory after the load test | **17–18 MB** | 81 MB |
| Throughput on 1 vCPU | **1600–2100 req/s**, p50 11–16 ms | ~200 req/s, p50 172 ms |

The table is the server before it carried `PresDeck`. With it the binary is
44 MB instead of 36 MB (built as the container builds it), it still starts in
about 15 ms, and memory after 20 creates of the 23-chart sample deck
(`samples/vegalite.md`) is 56 MB instead of 35 MB. Laying that deck out takes
about 200 ms per call; a deck of a few text slides takes a few milliseconds.

On Cloud Run the platform adds its own start-up to both. Firestore and Storage
calls take the same time from either language and were not part of the load
test.

## Who may do what, and how much

The deck id locates a deck and grants nothing to change it. sliqtly.com is
an open demo: a deck is read by anyone with its link (`visibility: "link"`,
the default; every result says so) unless its signed-in owner made it
`private`. A signed-in user's deck is changed and deleted
(`delete_presentation`: the record, then `shares/<id>/` in Storage) by its
owner; a deck made without sign-in only by the MCP session that made it
(`mcp_sessions/{sha256(Mcp-Session-Id)}`, a day after its last change, or
until `DELETE /mcp`). On a shared server
(`rgr/Store.rgr` `Limits`, `host.go` `dailyQuota`/`dailyRenders`): 3 decks
per session without sign-in and 50 per account, 20 / 100 slides per deck,
200 MB of pictures and files per deck, decks without sign-in deleted 7
days after their last change, 50 / 500 saved changes a day, 100 / 500
drawings (render_slide, render_overview, export_presentation) a day per
caller, a signed-in user's address 1000, at most two drawings at once per
caller. A server of one's own has none of these.

## Run and test

```
cd mcp-go
go generate         # compiles rgr/ and the editor's model (needs node)
go test ./...       # end to end over HTTP with the official MCP Go client, Firestore and Storage faked
go run .            # http://localhost:8080/mcp, decks travel in the link
go run . -data ./data   # decks kept in ./data (below)
```

`go generate` compiles with the Ranger checkout the editor builds with
(`npm run setup`, `.deps/Ranger` at the ref in `presentation.config.json`), or
`RANGER_DIR`, and needs Node. `sliqtly_mcp.go` is not committed: it carries the
editor's model (8 MB of Go), which changes with every change to `src/`. The
container builds it in its first stage.

With `GOOGLE_APPLICATION_CREDENTIALS` (or on Cloud Run, `K_SERVICE` set) it
writes real shares and offers sign-in. `SLIQTLY_URL`, `SLIQTLY_BUCKET`,
`GOOGLE_CLOUD_PROJECT` and `PORT` set the site, bucket, project and port; `SLIQTLY_STORE=link`
forces the link-only mode. `SLIQTLY_GITHUB_TOKEN` (a fine-grained token with
read access to pull requests and contents) lets `read_github_pr` read
private repositories and lifts GitHub's limit of 60
requests an hour. Anyone may call the server, so a private repository is read
only for the Sliqtly users (Firebase uids) in `SLIQTLY_GITHUB_USERS`
(comma-separated; on a server of one's own, for everyone); the refusal tells a
signed-in user their id. Actions → Deploy MCP (Go) sets both on the service
from the repository secrets of the same names when `SLIQTLY_GITHUB_TOKEN` is
set (a later deploy without it keeps what the service has).

## A server of one's own (decks in a folder)

The same binary keeps decks in a folder instead of Firestore and Storage,
for a laptop or a company network: no Google, no sign-in, every caller is
one user (`SLIQTLY_USER`, default `local`), and the server serves the
editor and player itself, as sliqtly.com does.

```
npm install && npm run build                  # web/dist: the editor and player (from the repository root)
cd mcp-go
go generate                                   # Ranger → Go, and copies web/dist into the binary
CGO_ENABLED=0 go build -o sliqtly-server .
./sliqtly-server -data ./data -port 8080      # http://localhost:8080, MCP at /mcp
```

`npm run serve` (from the repository root) does all of that in one go and
starts the server on `mcp-go/data`: `-- --port=8080`, `-- --data=<folder>`,
`-- --check` (npm run check and the Go tests first), `-- --no-build`
(start the last compiled one); other flags go to the server.

Without `npm run build` the binary has no editor: `/s/{id}` then shows the
slides as pictures drawn on the server. `-web ../web/dist` serves a build
from disk instead of the copy built in.

Another platform: `GOOS=windows GOARCH=amd64` (or `darwin`/`linux`,
`arm64`) before `go build`; the binary has no other dependency. In Docker:

```
docker build -f mcp-go/Dockerfile --target local -t sliqtly-server .     # from the repository root
docker run -p 8080:8080 -v sliqtly-data:/data sliqtly-server
# with backups on another disk
docker run -p 8080:8080 -v sliqtly-data:/data -v /mnt/disk2/sliqtly:/backup -e SLIQTLY_BACKUP=/backup sliqtly-server
```

| Flag | Environment | Default | |
| --- | --- | --- | --- |
| `-data` | `SLIQTLY_DATA` | | the folder; without it the server keeps nothing (link mode) |
| `-port` | `PORT` | 8080 | |
| `-url` | `SLIQTLY_URL` | `http://localhost:<port>` | the address in the links the tools return; set it to the name people reach the server by |
| `-user` | `SLIQTLY_USER` | `local` | owner of the decks |
| `-token` | `SLIQTLY_TOKEN` | | `/mcp` and `/api/v1` then need `Authorization: Bearer <token>` |
| `-cors-origins` | `SLIQTLY_CORS_ORIGINS` | | pages of other origins that may call `/api/v1` and `/oauth/token` (below) |
| `-oidc-issuer`, `-oidc-client-id`, `-oidc-client-secret`, `-oidc-allow`, `-oidc-scopes` | `SLIQTLY_OIDC_ISSUER`, `…_CLIENT_ID`, `…_CLIENT_SECRET`, `…_ALLOW`, `…_SCOPES` | scopes `openid email profile` | sign-in through an OpenID Connect provider (below) |
| `-tls-cert`, `-tls-key` | `SLIQTLY_TLS_CERT`, `SLIQTLY_TLS_KEY` | | a certificate (PEM) served on the port's https:// side instead of the server's own (below) |
| `-web` | `SLIQTLY_WEB` | the copy built in | a built `web/dist` to serve |
| `-listen` | `SLIQTLY_LISTEN` | `local` (the settings page decides) | who can connect: `local`, `wired` or `network` (below) |
| `-allow` | `SLIQTLY_ALLOW` | | other computers' address ranges let in, e.g. `10.20.0.0/16` |
| | `SLIQTLY_HOSTS` | | further names the server answers to, comma separated (below) |
| | `SLIQTLY_TRUSTED_PROXIES` | | address ranges of reverse proxies in front of the server, so the rate limits key the caller behind them (`clientip.go`) |
| | `SLIQTLY_BACKUP` | (the `.deb`: `/var/lib/sliqtly-backup`) | a folder for incremental backups (below); unset: none |
| | `SLIQTLY_BACKUP_EVERY` | `24h` | how often |
| | `SLIQTLY_BACKUP_KEEP` | `last=3,daily=14,weekly=8` | which backups are kept; `all` keeps every one |

**Who can connect** (`netaccess.go`). By default only this computer: the
server listens on 127.0.0.1 and ::1, so one run on a laptop is not open to
the café's Wi-Fi. `/settings` (in a browser on the same computer) or
`SLIQTLY_LISTEN` can open it further:

| | |
| --- | --- |
| `local` | this computer only |
| `wired` | also computers on a wired network. The server listens on the wired interfaces' addresses only, and takes a connection only from an address on their subnets. Wi-Fi, a phone's connection (USB, Bluetooth), a VPN and virtual interfaces are never opened. Interfaces are looked at every 5 s, so plugging a cable in or out takes effect at once. |
| `network` | every interface: a server, a container (the `.deb` and the Docker images set this) |

- **Address ranges:** `allow` narrows `wired` and `network` to the listed
  ranges, e.g. the office's. This computer is always let in.
- **Refused connections** are closed before any HTTP is read, and are logged.
- **Tightening the rule** closes the connections it no longer takes, kept-alive
  pages and event streams included.
- **Interface kinds:** read from `networksetup -listallhardwareports` on
  macOS and from `/sys/class/net` on Linux. Elsewhere every interface is
  "other", so `wired` opens nothing.
- **Changing it:** only a browser on the server's own computer can change
  the setting on the page. When `SLIQTLY_LISTEN` or `SLIQTLY_ALLOW` sets it,
  the page cannot change it.

**Which pages may use it** (`localguard.go`). The server answers only
names that are its own: `localhost` (and `*.localhost`), any IP address,
the machine's name (and `name.local`), the host of `SLIQTLY_URL`, and the
names in `SLIQTLY_HOSTS`. Any other name gets `421`, so a page on the
internet that points a name of its own at 127.0.0.1 (DNS rebinding) reaches
nothing. Behind a reverse proxy, or reached by a DNS name, set
`SLIQTLY_URL` or `SLIQTLY_HOSTS` to that name. A page of another origin
(and the assistant's preview, whose origin is `null`) may only read what a
link to one deck opens: the page and its files, the themes, one deck by its
id (`GET /api/shares/{id}`), its files and pictures, and `/api/status`.
The deck list, rooms, editing together, the event streams, settings, every
write and `/mcp` answer only the server's own pages and programs that send
no `Origin` (MCP clients, curl). A deck's file opened by itself
(`/files/…`) is sent with `Content-Security-Policy: sandbox`, so an
uploaded SVG or HTML runs as no origin and cannot use the API.

What it serves besides `/mcp` (`local.go`, `localweb.go`):

| | |
| --- | --- |
| `/` | the editor |
| `/s/{id}`, `/s/{id}?edit` | the player and the editor, as on sliqtly.com; the assistant's inline preview loads the same page |
| `/decks` | the decks kept here, only when listing is turned on in `/settings` (off by default: a deck opens only by its link). Without the editor, `/` is this page and `GET /api/shares` answers only when listing is on |
| `/s/{id}/slides` | a deck as its slides, drawn on the server (`render.go`) |
| `/s/{id}/{n}.jpg`, `/s/{id}/overview.jpg` | one slide, or all as thumbnails: for Markdown in a wiki, an issue or a merge request |
| `/files/shares/{id}/…` | the deck's pictures and data files |
| `/themes/{name}.css` | the themes (the built page's, else the ones copied from `../themes`) |
| `/api/…` | what the page keeps decks with (`assets/sliqtly-local.js`, which the server sends as `/sliqtly.js` in place of the Firebase one) |
| `/api/rooms/{op}` | rooms (ADR 0001), `POST` with JSON: `list_rooms`, `get_room`, `create_room`, `move_presentation` (optional `folder_id`), `create_folder`, `rename_folder`, `delete_folder`, `set_room_member`, `archive_room`, `link_types`, `add_link`, `remove_link`, `links_of`. The assistant has the same operations as MCP tools on this server (`roomsapi.go`). Every deck has a home room; decks start in General (or the `room_id` given to `create_presentation`), and there is a Playground beside it. A room has one level of folders (kept on the room, a deck's `folder` field; `store/folders.go`). `list_rooms` takes `query` (words in the name or description), `order` (`created`, `active`, `title`) and pages of at most 1000 (`limit`, `offset`, answer `total`, `next_offset`). No access limits on this server for now: everyone sees every room |
| `/settings` | the server's settings: listing the decks (changed only from the server's own computer) and the naming rule below (`localsettings.go`) |
| `/api/status` | `{"state","version"}`: `migrating`, `failed`, `ready` or `stopping` (`localstatus.go`) |
| `/api/socket` | the page's one stream, a WebSocket: the server's state, decks changed, the room of a deck edited together (`localevents.go`, `web/eventline.js`). A browser opens at most six HTTP/1.1 connections to a server for all its tabs, and WebSockets are counted apart from them. `/api/events` is the same as Server-Sent Events, which a page uses when a proxy in front does not pass WebSockets on |
| `/healthz` | `ok`, or 503 while the folder is not ready |

The folder (`fsstore.go`), since data format 4:

| | |
| --- | --- |
| `format.json` | the layout's version and what was done to it |
| `sliqtly.db` | SQLite: the documents (`store.SQLiteStore`), the kept files by path (`file_refs`: path → blob hash, size, type) and append-only logs such as a room's chat (`file_lines`) |
| `blobs.db` | SQLite: the files' bytes by SHA-256, in 255 KiB chunks (`store.SQLiteBlobStore`); the same bytes under two paths are kept once, and an older version still named elsewhere (a copied deck) as a delta against the newer |
| `backups/` | the folder as it was before each migration (the three newest) |
| `.lock` | held by the server using the folder |

Both files use WAL with `synchronous=FULL`: a write is on disk when it
returns. A file is written as its blob first and its path after, so a crash
between the two leaves a blob nothing names, which the hourly sweep removes
(after an hour's grace). Do not back up by copying the files while the
server runs; use the backups below.

Each database's schema is a numbered list of migrations
(`store.SQLiteSchema`, `store.SQLiteBlobSchema`; `store/sqlmigrate.go`):
`PRAGMA user_version` is the last applied, `schema_history` records each,
a file at a newer version is refused, and an existing file is copied to
`backups/` with `VACUUM INTO` before it is migrated (not for a migration
that only adds columns or indexes, such as blobs.db's deltas: a copy of
blobs.db can be gigabytes).

**Deltas** (`store/sqliteblobs.go`, `rdiff/`). A blob is kept whole or as
a delta against another blob, by RangerDiff's `RdSmart` compiled to Go
(`go generate` writes `rdiff/rdsmart.go`): a byte delta, a ZIP-part delta
for XLSX/DOCX/PPTX or a PNG delta. Only a delta that rebuilds the very
bytes is kept, and only when it is under 80 % of the file; the newest
version stays whole and older ones are deltas against it, at most 16 deep.
Reads give the same bytes as before, checked against the hash. Files over
64 MB (video) are always whole; JPEG and video gain nothing from deltas.

**Backups** (`backup.go`, `store/backup.go`). With `SLIQTLY_BACKUP` set the
server takes a backup at start when the last is older than
`SLIQTLY_BACKUP_EVERY`, then on that interval, and prunes by
`SLIQTLY_BACKUP_KEEP` after each. A backup folder holds:

| | |
| --- | --- |
| `backup.json` | what the folder is |
| `blobs.db` | every file kept, once, by hash; older versions as deltas |
| `snapshots/<time>.json` | one per backup: its copy of `sliqtly.db` and every file it names, by path and hash |

A backup copies `sliqtly.db` with `VACUUM INTO` (consistent, beside the
running server), then only the files the backup folder does not have yet,
then writes its manifest last, so a backup cut short is not a backup and
its blobs are pruned later. A file removed by the sweep while being copied
makes the backup start over once. The next backup costs what changed: an
edited deck adds its new text, the previous text becomes a delta.

```
sliqtly-server backup run     -data /var/lib/sliqtly -repo /mnt/b   # now, beside the running server
sliqtly-server backup list    -repo /mnt/b
sliqtly-server backup verify  -repo /mnt/b [-deep]   # every blob read back against its hash, sliqtly.db checked
sliqtly-server backup restore -repo /mnt/b [-id 20261006T023135Z] -into /var/lib/sliqtly-restored
sliqtly-server backup prune   -repo /mnt/b -keep last=3,daily=14,weekly=8
```

`-repo` defaults to `SLIQTLY_BACKUP`, `-data` to `SLIQTLY_DATA`. A restore
writes a new data folder (never over one), checks it (SQLite's integrity
check, every file reference's blob there, every blob against its hash)
and removes it again if anything is wrong. Serve it with
`sliqtly-server -data <folder>`, or stop the server and move it into place.
With the `.deb` (run as root; the backup folder's files keep the service's
owner, and systemd gives a moved-in data folder to the service on start):

```
sudo systemctl stop sliqtly
sudo sliqtly-server backup restore -repo /var/lib/sliqtly-backup -into /var/lib/sliqtly-restored
sudo mv /var/lib/private/sliqtly /var/lib/private/sliqtly-old
sudo mv /var/lib/sliqtly-restored /var/lib/private/sliqtly
sudo systemctl start sliqtly
```
`backup_test.go` takes backups beside a running server, restores them and
reads the decks and pictures back through a server on the restored folder.

Formats 1–3 kept a JSON file per document (`db/<collection>/<sh>/<id>.json`)
and the files under `files/shares/<sh>/{id}/…`; the migration to format 4
copies them into the two databases, checks every document and file against
the folder, and only then removes `db/` and `files/` (the backup keeps them).

**Updates** (`datafmt.go`). A server started on a folder locks it, so a
second server on the same folder stops with an error. A folder written by a
newer server is refused rather than read wrong. An older one is migrated
before anything reads it:

1. A backup in `backups/<time>-format-<n>/`: every file of `db/` and
   `files/` hard-linked, so it takes no room and keeps the old contents,
   and from format 4 on a copy of `sliqtly.db`.
2. Each migration in turn. A migration only renames, one entry at a time,
   and skips what is already in place, so a run cut short continues on the
   next start. Anything it would overwrite goes to `backups/conflicts/`.
   The move to SQLite (3 → 4) builds the databases as `*.migrating`, checks
   them, and renames them into place; a run cut short before that starts
   over, one cut short after it only finishes removing the old folders.
3. The number of files is checked against the number before. If it differs,
   the server stops there, with the folder and the backup as they are.

Meanwhile the port answers: pages and assistants get 503 with
`"code":"maintenance"`, and an open page shows that the server is being
updated. On `SIGTERM` (`systemctl restart`, a package upgrade) the server
tells open pages it is restarting and lets saves under way finish. A page
that loses the server says it is offline. A page that sees a new version
offers to reload. Edits made meanwhile stay in the browser (the editor keeps
every deck there first) and go to the server when it is back.

**A naming rule** (`names.go`). On `/settings` the server can require a
form for presentations' names, for example a ticket key first:
`^([A-Z][A-Z0-9]+-[0-9]+) +\S`, "ABC-1234 Quarterly review". The rule is
off until it is turned on there, and is kept in the folder. The cloud server
has none. When it is on:

- `create_presentation` and `update_presentation` state the rule in their
  descriptions, and so does `sliqtly_guide`.
- A title that does not follow the rule is refused, with the rule and an
  example.
- `list_presentations` gives each deck's `key` (the pattern's first group)
  apart from its name.

The editor does not enforce the rule. The settings page lists the names that
do not follow it.

The page is sliqtly.com's own; only `/sliqtly.js` differs. The editor is
signed in as the folder's user, so a deck opened with `?edit` is saved back
to the folder as you type, and an assistant's change shows up in it. Google
Sheets and Drive are not available. `/api/` has no sign-in: whoever
reaches the server can change its decks there (`-token` and the `-oidc-*`
flags guard `/mcp` and `/api/v1`, below).

### Sign-in, the REST API and https://

`/api/v1` (`apiv1.go`, the contract in [`docs/api-v1.md`](../docs/api-v1.md))
is the API for the desktop and web editors and other programs: decks
listed, read, created, saved (with `ifVersion`: 409 when someone saved
meanwhile) and deleted, and a deck's slides laid out as `/api/view` has
them. A save goes the editor's own way (`saveShare`), so a deck open in a
room hears it. The old `/api` is the built-in page's and stays as it is:
no sign-in, guarded by `localguard.go`.

**Who may call it.** With `-token`, `/mcp` and `/api/v1` need
`Authorization: Bearer <token>`. With the `-oidc-*` flags the server is also
an OAuth 2.1 authorization server (`rgr/OAuth.rgr`, `oidc.go`) whose sign-in
is the provider's: MCP clients find it from the 401 on `/mcp` and register
themselves, the desktop app signs in as `sliqtly-desktop` (its code to
`http://127.0.0.1:<port>/callback`), a web editor as `sliqtly-web` (to
`<its origin>/callback.html`). The ID token is verified against the
provider's keys, and only the accounts `-oidc-allow` names get in:
emails and `@domains` (an address the provider marks unverified never
matches), or `*` for every account the provider signs in, which suits a
provider that already holds only the company's accounts. The tokens work on
`/mcp` and `/api/v1`, alongside `-token`. The server keeps only hashes of
codes and tokens. Every caller still works as the server's one user
(`-user`); sign-in decides who may, not whose decks.

Register `<-url>/oauth/callback` as the redirect URI at the provider, and
start the server with `-url` set to the address people use, so the two
match. The secret is better in the environment than on the command line.

```
# Google: console.cloud.google.com → APIs & Services → Credentials → OAuth client ID (Web application)
SLIQTLY_OIDC_CLIENT_SECRET=… sliqtly-server -data ./data -url https://slides.example.com \
  -oidc-issuer https://accounts.google.com -oidc-client-id 1234-abc.apps.googleusercontent.com \
  -oidc-allow @example.com,partner@other.org

# Microsoft Entra ID: App registrations → New (Web, the redirect URI above), Certificates & secrets.
# The tenant's issuer lets in that tenant's accounts only; add the optional claim "email" to the
# ID token to match by address, or use *.
SLIQTLY_OIDC_CLIENT_SECRET=… sliqtly-server -data ./data -url https://slides.example.com \
  -oidc-issuer https://login.microsoftonline.com/<tenant id>/v2.0 -oidc-client-id <application id> \
  -oidc-allow '*'

# Keycloak: a confidential OpenID Connect client in the realm, the redirect URI above
SLIQTLY_OIDC_CLIENT_SECRET=… sliqtly-server -data ./data -url https://slides.example.com \
  -oidc-issuer https://sso.example.com/realms/company -oidc-client-id sliqtly \
  -oidc-allow @example.com
```

**Pages of other origins.** A browser page on another origin may call
`/api/v1` and `/oauth/token` when its origin is on `-cors-origins`
(`https://editor.example.com,https://10.0.0.5:8443`); pages on this
computer (`localhost`, `127.0.0.1`, `[::1]`, any port) always may. Their
requests carry a bearer token, never a cookie. Without `-token` and sign-in,
`/api/v1` answers only the server's own pages, pages on this computer and
programs that send no `Origin`.

**https://.** The port answers https:// with the server's own authority
(`/ca`, above) unless `-tls-cert` and `-tls-key` name a certificate and its
key (PEM, the chain after the certificate), such as the company's or one
from Let's Encrypt: that one is served instead, and read again when the
files change (a renewal), so nothing needs installing on the computers that
connect. `/api/v1/info` says which (`tls.ownCA`) and, for the server's own,
its fingerprint for a client to pin.

### Connectors (trial)

`-connectors` (`SLIQTLY_CONNECTORS=1`) lets scripts and workflows in decks
use services outside Sliqtly through the server, never directly: the
connectors are files in `<data>/connectors/` (address, allowed hosts,
sign-in, operations with argument schemas and the fields handed back),
the admin approves each deck's grant, each person connects their own
account by OAuth (tokens encrypted on the server), and every call is
checked, limited and logged. Model and tests in `connectors/`, the routes
in `connectorsapi.go`, the settings page's Connectors section in
`localsettings.go`; how to try it with GitHub in
[`docs/connectors.md`](../docs/connectors.md). `-admin` names signed-in
accounts that may approve grants; the server's own user on its own
computer always may.

### A Debian/Ubuntu package

`packaging/build-deb.sh <version> <amd64|arm64>` (after `npm run build` and
`go generate`) makes `dist/sliqtly-server_<version>_<arch>.deb` with
`dpkg-deb` alone. It holds the static binary, a systemd unit (its own user,
decks in `/var/lib/sliqtly`) and `/etc/sliqtly/sliqtly.env`, and depends only
on `ca-certificates`. The binary is built with `-tags nocloud` (`nocloud.go`):
without Firestore, Storage, Firebase Auth and Google's client libraries
(gRPC, protobuf, OpenTelemetry), about 26 MB of the 58 MB. What is left
links only `golang.org/x/image`, `x/sys` and `x/text` besides Go itself.

```
sudo apt install ./sliqtly-server_0.1.0_amd64.deb   # starts it, and at boot
sudo nano /etc/sliqtly/sliqtly.env                  # SLIQTLY_URL, SLIQTLY_TOKEN, PORT
sudo systemctl restart sliqtly
journalctl -u sliqtly -f
```

A newer package installed the same way restarts the service; decks and
settings stay. `apt remove` stops it and leaves the decks.

That is the PRO package (the editor built in), which is not public. The
Personal package, `sliqtly-personal`, is the one anyone can download
(sliqtly.com/download/sliqtly-personal_amd64.deb, the front page's "Wanna
run local?"): the same server with the public viewer built in instead of the
editor (`npm run build:view`, `SLIQTLY_BUILD_WEB=viewer go generate`,
`EDITION=personal packaging/build-deb.sh …`), listening on this computer
only, its front page the list of decks, under its own license
(`packaging/personal/LICENSE`). The **Personal package** workflow builds and
uploads it. Either package replaces the other.

The same workflow makes the Personal server for macOS and Docker
(`packaging/build-personal-bin.sh`): `sliqtly-personal_<v>_darwin_<arch>.tar.gz`
in Storage with `downloads/personal/latest.json`, which the Homebrew tap
terotests/homebrew-sliqtly reads to update its formula (the tap's files are
in `packaging/homebrew/`), and the image `ghcr.io/terotests/sliqtly-personal`
(`packaging/personal/Dockerfile`). How people run them: web/local.html
(sliqtly.com/local.html).

### Connecting an assistant

Claude Code:
```
claude mcp add --transport http sliqtly http://localhost:8080/mcp
claude mcp add --transport http sliqtly http://localhost:8080/mcp --header "Authorization: Bearer <token>"
```

Cursor (`~/.cursor/mcp.json`, or `.cursor/mcp.json` in a project):
```json
{
  "mcpServers": {
    "sliqtly": {
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

VS Code (`.vscode/mcp.json`):
```json
{
  "servers": {
    "sliqtly": { "type": "http", "url": "http://localhost:8080/mcp" }
  }
}
```

Claude Desktop, which starts its servers as programs:
```json
{
  "mcpServers": {
    "sliqtly": { "command": "npx", "args": ["-y", "mcp-remote", "http://localhost:8080/mcp"] }
  }
}
```

Leave out `headers` when the server has no token. Clients that connect from
their vendor's cloud (claude.ai connectors, ChatGPT) cannot reach a server
that is only on a laptop or inside a network.

## Deploy

How the hosted service on sliqtly.com is deployed, the editor's sign-in and
licenses, and the owner's dashboard are in
[docs/operations.md](../docs/operations.md#the-mcp-server-on-cloud-run).
