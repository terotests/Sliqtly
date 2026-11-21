# Sliqtly Game Studio (native)

A desktop app for making RangerGames / Sliqtly Games games from a folder on
your own disk. One window holds the files, Ranger's own code editor and the
game running natively. The window is an SDL3 + OpenGL app written in Ranger
(compiled to C++) and drawn as an EVG display list.

The project folder stays where it is. Git is optional. Any AI agent in a
terminal (Claude Code, Cursor, OpenCode) can edit the same files: within a
second the Studio notices, reloads the open file and restarts the game.
Big pictures are read from the disk directly and never uploaded anywhere.

License: AGPL-3.0-or-later (see `LICENSE`). The code editor (EVGSheets),
Ranger's gallery and the RangerGames engine are AGPL too.

## Start

From the top of the Sliqtly repository:

```bash
npm run start:studio -- ~/games/solarpilot
```

The first run fetches what the Studio is built from (`scripts/setup.sh`) and
builds it (`scripts/build.sh`, about 2 minutes). Later runs build again only
when a source changed. The folder is remembered, so next time
`npm run start:studio` alone opens it. Options after the folder go to the
Studio (`--scene kuumoduli`, `--mcp`, ...); `--rebuild` forces a build.

## Build

```bash
cd studio
scripts/setup.sh     # once: Ranger, EVGSheets and RangerGames into .deps/
scripts/build.sh     # engine (cargo) + app (Ranger -> C++) + link
build/sliqtly-studio ~/games/my-game
```

You need git, node, cargo, a C++17 compiler, SDL3 (`pkg-config sdl3`) and
OpenGL. On macOS: `brew install sdl3`. On Debian/Ubuntu, build SDL3 from
source if your release has no `libsdl3-dev`.

## Use

```
sliqtly-studio <folder> [--game id] [--file rel] [--size WxH]
               [--scene name] [--speed N]
               [--frames N] [--shot out.png] [--dump] [--hidden] [--mcp]
```

- The folder is either **one game** (it holds `game.json`) or **a collection**
  (its subfolders hold `game.json`). A collection lists its games on the left.
- **Ctrl+S** saves the file and restarts the game. **F5** or Ctrl+R restarts
  it. Click the game to give it the keyboard (arrow keys etc.); click the
  editor to get the keyboard back.
- Files changed on disk by someone else (an agent, `git pull`) are picked up
  within a second. If you have unsaved edits in that file, the Studio keeps
  yours and says so in the status line.

### Scenes, hot reload, fast forward

A game can give the Studio jump points and keep its state across a code
change. Other hosts never call these, so the game runs as before everywhere
else:

```ts
const SCENES: { [name: string]: () => void } = {
  "kuun-kiertorata": () => sceneOrbit("moon", 100),
  "laskuvarjo": () => { start(); entryTest(-60, 12, 0.3); },
};
function hotSave() { return { screen, pos, vel, field: field ? field.id : "" }; }
function hotLoad(s: any) { screen = s.screen; pos = s.pos; vel = s.vel; field = BODIES.find((b) => b.id === s.field) || null; }
```

- The bar above the game shows the scenes as buttons, plus **Tauko/Jatka**
  (pause), **Askel** (one frame while paused) and **x1 x4 x16 x64** (game
  frames per window frame; a frame stops early after 40 ms).
- When files change on disk the Studio does as little as it takes. A change
  to pictures or sounds only reads them again, with no restart. A change to
  `style.css` only re-reads the style (`rg_restyle`) and the game goes on.
  A code change restarts the game, then calls `hotLoad(hotSave())` if the game
  has them. Otherwise it jumps back to the scene it was in, or else starts
  from the beginning.
- `--scene name` and `--speed N` do the same from the command line.

### For AI agents (headless)

```bash
xvfb-run -a build/sliqtly-studio ~/games/my-game --scene laskuvarjo --frames 120 --shot shot.png --dump
```

This runs 120 frames at a fixed 60 fps and saves the window as a PNG. The
game's log, and with `--dump` the last frame's draw commands, go to stdout.
On Linux, `xvfb-run` means no screen is needed.

### Pictures and resource folders

Click a picture in the file list (PNG, JPEG, WebP, GIF, BMP) and it is shown
where the editor is, fitted, with its size in bytes and pixels. The folders
from `studio.json` are listed under **RESURSSIT**: click one to open it and
click a file to see it. `--file img/moon.jpg` does the same from the command
line. **Sulje** or Esc goes back to the editor.

### Comments

**Kommentoi** in the bar above the game pauses it and keeps a picture of the
game area. Write the comment in the line under the editor; Enter saves it and
Esc cancels. Comments go to `<project>/.studio/comments.jsonl` (one JSON object
per line: id, time, game, scene, frame, text, shot, done, note) and the
pictures to `.studio/shots/`. The dot folder is never given to the engine.

Comments are listed under **KOMMENTIT**. Clicking one jumps to its scene and
shows its picture and text; **Merkitse tehdyksi** marks it handled. An agent
can read and answer them too (below), or edit the file: the Studio reads it
again when it changes.

### Sarjakuva: the comic editor

**Sarjakuva** in the top bar turns the window into a comic editor for the
game's cartoon scenes (briefings, story pages, tutorials). It works on
`<game>/cartoons/<name>.cartoon.json`, one file per cartoon, saved on every
change; **Koodi ja peli** goes back.

- **Pohja** (template): format Sivu (800 x 1100), Strippi (1200 x 420), Ruutu
  (900 x 900) or Puhelin (540 x 960); 1-6 panels; straight, slightly or very
  slanted gutters; **Asettelu** cycles the layouts for that panel count
  (`1/1,2/2,1/1` = rows of panel widths). Templates start empty.
- **Työkalu**: Kynä draws with the mouse or a finger (the line is smoothed and
  inked in the cartoon's line style), Väripinta draws a filled shape that goes
  under the lines, Kupla places a bubble, Valitse moves things (a bubble's
  orange dot is its tail tip, the corner its size), Kumi erases. Pen widths and
  colours; a colour clicked with something selected recolours it.
- **Kupla**: puhe, ajatus, huuto, kuiskaus, kerronta (caption), ääniefekti
  (a KOP KOP burst) and radio. Type while a bubble is selected; Shift+Enter is
  a new line. The bubble grows around its text. Versaali = capitals.
- **Tyyli**: presets (Lasten kuvakirja, Klassinen sarjis, Manga, Noir, Luonnos,
  Pop art, Avaruus) or each setting on its own: amount of detail 1-5, colour
  tone, colour or black and white, shading, speed lines, filler (halftone dots,
  hatching, cross-hatching), mood, line (siisti, luonnos, muste, sivellin) and
  audience. **Arvo tyyli** rolls a new mix.
- **Ctrl+Z** undoes, Del deletes, arrows nudge. **Vie PNG** saves the page as
  `cartoons/<name>-<scene>.png`, which a game shows with `rg.asset(...)`.

**Kommentoi** leaves a comment with a screenshot of the page. **Pyydä AI:ta**
does the same with instructions for an AI, prefixed `[AI]`, and writes a brief
to `.studio/cartoon-requests/<id>.md`: the instructions, the cartoon file, the
scene and the selected panel, the panels' corners, the style in words and the
JSON forms of bubbles, pen lines, fills and pictures. **Skenen kuvaus** is a
note on what happens in the scene, given to the AI too.

The AI gets it in one of two ways:

- An agent connected to the Studio's MCP (`--mcp`) reads `comments_list`,
  calls `cartoon_get` (the brief and the whole JSON) and
  `screenshot` with `area: "cartoon"`, edits the file, looks again and marks
  the comment done. `cartoon_open` opens a cartoon or makes a new one.
- With `SLIQTLY_STUDIO_AI` set, the Studio runs that command in the game
  folder with the brief as its input, for example
  `SLIQTLY_STUDIO_AI='claude -p --permission-mode acceptEdits'`. Its output
  goes to `.studio/cartoon-requests/<id>.log`.

Either way the editor shows the change as soon as the file is written, so the
person can look, comment again or redraw. A change to a `.cartoon.json` does
not restart the game.

### For AI agents: the Studio as an MCP server

```bash
sliqtly-studio ~/games/my-game --mcp
```

The window stays open, so you can watch what the agent does. The protocol is
MCP over stdio (JSON-RPC, one message per line); the Studio's own messages go
to stderr. In Claude Code:

```bash
claude mcp add sliqtly-studio -- /path/to/build/sliqtly-studio ~/games/my-game --mcp
```

| Tool | What it does |
| --- | --- |
| `status` | project, game, scene, scenes, speed, frames, mounts, last log lines |
| `jump_scene` | restart and jump to a scene of the game's `SCENES` |
| `restart` | sync files from disk and restart |
| `run_frames` | run N frames at 60 fps right away (also when paused), answers the log |
| `screenshot` | PNG of the game area, the whole window or the cartoon page (`area: "cartoon"`) |
| `eval` | JavaScript in the game's global scope, result as text |
| `log` | the last lines of the log |
| `set_speed` | speed (1–256 frames per window frame) and pause |
| `press_key`, `tap` | a key (KeyboardEvent.key name) or a tap in game coordinates |
| `comments_list`, `comment_add`, `comment_done` | the comments above |
| `list_assets` | files of the game and its resource folders, with pixel sizes |
| `cartoon_open`, `cartoon_get` | the comic editor: open or make a cartoon; its brief and JSON |

The agent edits the game's files on disk as usual; the Studio reloads them
(hot reload when the game has `hotSave`/`hotLoad`).

### Big shared resources: `studio.json`

Put `studio.json` beside `game.json` (or at the top of a collection) to mount
folders from anywhere on disk into the game, without copying them:

```json
{ "resources": { "planeetat": "../space-textures", "tiles": "/data/osm-tiles" } }
```

For a single game, `planeetat/earth.png` then is `rg.asset("planeetat/earth.png")`,
in the engine at `/games/<id>/planeetat/earth.png`. In a collection the path is
`/games/planeetat/...`. The folders can live in another repository, public or
private.

## How it works

| Part | File |
| --- | --- |
| Window, layout, input, file watching, save | `src/StudioApp.rgr` |
| The engine: mounts, sync with disk, draw stream to EVG commands | `src/GamePreview.rgr` |
| EVG display list to OpenGL (text atlas, pictures on the GPU, paths) | `src/StudioPainter.rgr` |
| Picture cache (stb_image natively, WebP with Ranger's decoder) | `src/StudioImages.rgr` |
| Comic editor: model and file format, drawing, window | `src/CartoonModel.rgr`, `src/CartoonRender.rgr`, `src/CartoonEditor.rgr` (`src/CartoonJson.rgr` reads the JSON) |
| SDL3 window, files, engine C ABI, PNG screenshot | `native/studio_host.cpp` |
| MCP server over stdio (reader thread, JSON, tool list) | `native/studio_mcp.cpp` |
| GL drawer (from EVGSheets `evg_gl_native.cpp`, plus a texture cache) | `native/studio_gl.cpp` |
| The engine as a static library (RangerGames `rg_web` ABI) | `native/rg_studio/` |
| Ranger operators for the native calls (generated) | `native/gfx_studio.rgr` ← `native/gen_ops.py` |

The engine gets every file in the mounted folders except pictures, sounds and
videos over 64 kB. For those it gets only the first 64 kB, which is enough
for it to read a picture's size. The painter reads the whole file from disk.

## Not done yet

- Sound and speech: the engine's `sound`/`say` commands are counted but not played.
- 3D (`<scene3d>`, glTF) is drawn with the engine's painter fallback (no
  depth buffer). Textured globes (draw kind 8) and their detail pictures are
  drawn natively.
- SVG pictures and colour emoji. Emoji are drawn as outlines from Noto Emoji.
- Shadows have no blur. Gradients are drawn as strips.
- Paths with holes (even-odd) are filled as separate rings.
- Creating game assets (vectorizing, generating pictures) inside the Studio;
  the comic editor draws pen lines and fills, and takes pictures an AI made.
- Comic editor: panel corners cannot be dragged one by one yet (pick another
  layout or slant), there is no pressure from a stylus, and games show a
  cartoon as its exported PNG (no in-game renderer for the JSON yet).
- Building and installing Android/iOS packages, Git operations, publishing
  to Sliqtly Games, and starting an AI CLI from inside the Studio: these are
  the next steps in the plan (Sliqtly deck "Sliqtly Studio").
