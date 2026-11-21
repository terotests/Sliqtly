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
| `screenshot` | PNG of the game area or the whole window |
| `eval` | JavaScript in the game's global scope, result as text |
| `log` | the last lines of the log |
| `set_speed` | speed (1–256 frames per window frame) and pause |
| `press_key`, `tap` | a key (KeyboardEvent.key name) or a tap in game coordinates |
| `comments_list`, `comment_add`, `comment_done` | the comments above |
| `list_assets` | files of the game and its resource folders, with pixel sizes |

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
- Creating assets (drawing, vectorizing, generating pictures) inside the Studio.
- Building and installing Android/iOS packages, Git operations, publishing
  to Sliqtly Games, and starting an AI CLI from inside the Studio: these are
  the next steps in the plan (Sliqtly deck "Sliqtly Studio").
