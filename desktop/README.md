# Sliqtly, native (desktop)

The Sliqtly editor as a desktop app on macOS and Linux: the same editor the
browser runs (`../src/PresApp.rgr` with EVGUI), compiled by Ranger to C++ and
hosted in an SDL2 + OpenGL window (`native/editor_host.cpp`), painted by EVG's
native painter (`evg::gl::Painter`). It edits the presentations of a Sliqtly
server (File → Presentations…, Cmd/Ctrl+S saves there), or a Markdown deck
file given on the command line.

## Quick start

```bash
cd desktop
npm install               # the Ranger compiler (ranger-compiler 4.0.2)
npm run deps              # EVG at the commit ranger.json pins (rgrc install)

npm run native            # native/build-editor/sliqtly-native (+ Sliqtly.app on macOS)
npm run native:run        # build and start it with the welcome deck
npm run native:run -- ~/deck.md   # … or with a deck file of your own
npm run native:check      # headless check: click, typing, wheel direction, a painted frame
```

### The server

At start the editor connects to `http://localhost:8080` (`npm run serve` in
the repository root), or to `--server URL` / `SLIQTLY_SERVER`; `--token T` /
`SLIQTLY_TOKEN` when the server was started with `-token`. `--server none`
works on files only.

```bash
npm run native:run -- --server http://localhost:8080
```

- File → Presentations… lists the server's presentations (sort by a column,
  delete one that is not open); a row opens it.
- Cmd/Ctrl+S saves the open one there. If it was changed on the server since
  it was opened (a browser, an assistant over MCP), the editor asks which to
  keep: the server's version or this one.
- The welcome deck, saved, becomes a new presentation on the server.
- A deck file given on the command line saves to that file.

What the editor does with the server is `../src/PresServer.rgr` (REST API v1,
`../docs/api-v1.md`, over `../src/ApiClient.rgr`), tested in
`../src/PresCheck.rgr`; the host only performs its HTTP requests with libcurl.
Not yet here: sign-in (OAuth), a server's own HTTPS CA, rooms and pictures.

The build compiles the whole editor (PresApp.cpp, some 360 000 lines), so it
takes a few minutes. It uses the Ranger, EVGUI, RangerFlow, RangerMarkdown and
RangerPPTX clones the web build uses (`../scripts/lib.mjs`, `.deps/`).
`EVG_NATIVE=<evg>/storm/native` builds against a local EVG checkout.

On macOS the build also writes `native/build-editor/Sliqtly.app` (bundle id
`com.sliqtly.app`, the stylesheets, themes and fonts in its Resources).

## The first editor (`old:*`)

`src/EditorApp.rgr` is a separate, smaller editor written from scratch: a
client of a Sliqtly server's REST API v1, native and web. It is kept for its
server pieces (connect, OAuth sign-in, the server's own CA) until those move
to the editor above. Its scripts are `old:*`:

```bash
npm test                  # its models and UI, against the mock server
npm run old:mock -- --port 8080 --auth token --token secret   # a server to talk to
npm run old:web           # http://127.0.0.1:8140/
npm run old:native        # native/build/sliqtly-editor (+ "Sliqtly Editor.app" on macOS)
npm run old:native:run    # build and start it
npm run old:native:check  # headless smoke run with screenshots (native/build/shots/)
npm run icon              # native/icon/icon-1024.png and AppIcon.icns, drawn by it
```

It edits the presentations of a Sliqtly server of one's own. Markdown is on
the left. On the right is a preview of the slides, laid out by the server
(`GET /api/v1/decks/{id}/view`) and painted by the client from the server's
EVG display lists.

Native build (both editors) needs:

| | |
|---|---|
| macOS | Xcode command line tools, `brew install sdl2` (libcurl and OpenGL are part of macOS). For an app to give to others, set `SDL2_FRAMEWORK=<path>/SDL2.framework` (from the official SDL2 .dmg) and `UNIVERSAL=1`: the framework goes inside the .app, which then opens on macOS 11 and later on both Apple silicon and Intel, without Homebrew; CI builds it so |
| Debian / Ubuntu | `sudo apt-get install libsdl2-dev libgl-dev libcurl4-openssl-dev` (`xvfb` for the headless checks without a display) |

On macOS `old:native` writes `native/build/Sliqtly Editor.app`:
- bundle id `com.sliqtly.editor`;
- `AppIcon.icns` made with sips / iconutil from `native/icon/icon-1024.png`, or the committed `native/icon/AppIcon.icns` when those tools are missing.

The window icon and Dock icon are also set at runtime.

## Using it

**Connect.** Type a server address (default `http://localhost:8080`) and an
optional token (the server's `SLIQTLY_TOKEN`), then Connect. The connect screen
also asks `localhost:8080`–`8090` for `/api/v1/info` and lists the Sliqtly
servers it finds. Every server you connect to is kept under **Servers**, so you
can switch between servers or forget one.

**Sign in.** When `info.auth.oauth` is true, a "Sign in with …" button appears.
It uses OAuth 2.1 with PKCE S256:

| | client_id | redirect |
|---|---|---|
| native | `sliqtly-desktop` | `http://127.0.0.1:<free port>/callback`, a one-shot listener in the app |
| web | `sliqtly-web` | `<origin>/callback.html` |

The access token and the refresh token are kept with the server. On a 401 the
client refreshes the token once and sends the request again. A refused refresh
signs you out.

**HTTPS with the server's own CA.** When `info.tls.ownCA` is true, the editor
shows the CA's SHA-256 fingerprint. You compare it with the one the server
prints. On Trust, the editor:
1. downloads `/ca.crt`;
2. checks that its fingerprint matches;
3. pins it for that server only (libcurl `CAINFO`, `<settings>/ca/<fingerprint>.pem`).

The browser uses its own trust store, so the web build cannot pin a CA.

**Presentations.** The list shows each deck's name, slide count and last update,
with New, Open, Delete (after a confirm) and Refresh. Keys: ↑ ↓ and Enter.

**Editor.** A monospace Markdown text area with:
- UTF-8 input;
- arrows, Home / End, Page Up / Down, word moves (Alt, or Ctrl on Linux), document start / end;
- mouse placement, double click (word), triple click (line), drag selection;
- wheel scrolling, clipboard, undo / redo.

The preview follows the caret from slide to slide, and ‹ › step through the slides.

Saving:
- **Cmd+S** (Ctrl+S on Linux) saves with `ifVersion`.
- A 409 offers **Reload** (the server's version) or **Overwrite** (yours).

The status line shows the server, the user, saved / unsaved, and the caret position.

**Settings** live in:

| | |
|---|---|
| macOS | `~/Library/Application Support/Sliqtly Editor/settings.json` |
| Linux | `$XDG_CONFIG_HOME/sliqtly-editor/settings.json` (or `~/.config/…`) |
| web | `localStorage` (`sliqtly-editor.settings`) |

Natively the folder is `0700` and the file `0600`. The file is written to a
temporary file and then renamed. `SLIQTLY_EDITOR_CONFIG=<dir>` overrides the
folder (the checks use it).

## How it is put together

```
../src/         shared with the editor above
  JsonValue.rgr   a small JSON value + parser (UTF-8, \u escapes, surrogates)
  ApiClient.rgr   REST v1 requests as a queue the host runs; Bearer, refresh on 401
src/            Ranger: the app, compiled to both targets
  TextBuffer.rgr  the text model: lines, caret / selection in code points, undo
  Settings.rgr    the servers and their tokens / CA, as JSON
  Session.rgr     the state machine: connect, probe, sign-in, trust, decks, save, preview
  EditorApp.rgr   the UI: EVG element trees → display lists; input; a script runner
assets/editor.css the dark theme (EVG stylesheet)
native/         the host: SDL2 window, GL painting, libcurl, loopback OAuth, settings files
web/            the host: canvas + evg-webgl, fetch, localStorage, PKCE with crypto.subtle
scripts/        build-web, build-native, check-native, make-icon, png, ranger
test/           mock-server.mjs, run-tests.mjs, fixtures/ (a real /api/view answer)
```

The models (`Json`, `TextBuffer`, `ApiClient`, `Settings`, `Session`) know
nothing of the UI or of the platform. `test/run-tests.mjs` tests them on the
JavaScript build, and the native check exercises the C++ build.

The app does no I/O itself. The host:
1. takes queued requests (`takeRequest`, `reqMethod/Url/Headers/Body…`), runs them and calls `deliver(id, status, body, error)`;
2. takes commands (`takeCommand`): save-settings, copy, paste, open-url, oauth, fetch-ca, shot, quit…;
3. paints `displayListJson()`, then paints the current slide of `viewBody()` into the rectangle `previewX/Y/W/H()`.

**The mock server** (`test/mock-server.mjs`, Node, no dependencies) speaks REST v1:

| Option | What it serves |
|---|---|
| `--auth token\|oauth\|none` | Bearer token, or the OAuth flow (metadata, authorize that approves at once, token with PKCE and rotating refresh tokens) |
| `--tls` | HTTPS from a CA of its own, made with openssl, with `/ca.crt` and the fingerprint in info |
| `--token-ttl` | access tokens that expire |

It returns 409 conflicts. `/view` is canned for the sample deck and generated for any other deck. Test hooks live under `/__test/`.

`test/fixtures/view-sample.json` is a real `/api/view/<id>` answer from the Go
server (`mcp-go`, at this branch's base) for `sample-deck.md`.

**The headless check** (`npm run old:native:check`) runs the real binary three
times, scripted with the app's own steps:

| Run | What it does |
|---|---|
| token | probe, connect, list, open, type Finnish text, save, an edit elsewhere → 409 → Overwrite |
| oauth | the loopback sign-in |
| https | trust the server's own CA and pin it |

It checks the server's state, the settings file and its mode, and that the
preview and the text area were painted. Screenshots go to `native/build/shots/`.
On Linux it runs under `xvfb-run`.

## Known limits

- **Non-ASCII text on the native build** (ä ö, `·`, `‹ ›`) comes out as
  mojibake at the pinned EVG commit `e16a7a8`. The text itself is right: it is
  saved to the server intact. terotests/evg#55 (`4019de0`) fixes it, and with
  that commit everything renders correctly. Bump `ranger.json` once it is
  merged.
- The native painter does not draw images yet, so pictures in a slide are
  missing from the native preview.
- The preview shows what was last saved, because the server lays out the slides.

## Licences

MIT (`LICENSE`).

Some files are copied from or modelled on Carnivore (github.com/terotests/CarnivoreMusicPlayer, MIT, Copyright (c) 2026 Tero Tolonen) and EVG Player (github.com/terotests/evg, MIT). Each of those files says so in its header.

The fonts are in `native/fonts/` with their own licences:
- Noto Sans and Open Sans: Apache 2.0;
- DejaVu Sans Mono: the DejaVu / Bitstream Vera licence.
