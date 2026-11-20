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
               [--frames N] [--shot out.png] [--dump] [--hidden]
```

- The folder is either **one game** (it holds `game.json`) or **a collection**
  (its subfolders hold `game.json`). A collection lists its games on the left.
- **Ctrl+S** saves the file and restarts the game. **F5** or Ctrl+R restarts
  it. Click the game to give it the keyboard (arrow keys etc.); click the
  editor to get the keyboard back.
- Files changed on disk by someone else (an agent, `git pull`) are picked up
  within a second. If you have unsaved edits in that file, the Studio keeps
  yours and says so in the status line.

### For AI agents (headless)

```bash
xvfb-run -a build/sliqtly-studio ~/games/my-game --frames 120 --shot shot.png --dump
```

This runs 120 frames at a fixed 60 fps and saves the window as a PNG. The
game's log, and with `--dump` the last frame's draw commands, go to stdout.
On Linux, `xvfb-run` means no screen is needed.

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
| GL drawer (from EVGSheets `evg_gl_native.cpp`, plus a texture cache) | `native/studio_gl.cpp` |
| The engine as a static library (RangerGames `rg_web` ABI) | `native/rg_studio/` |
| Ranger operators for the native calls (generated) | `native/gfx_studio.rgr` ← `native/gen_ops.py` |

The engine gets every file in the mounted folders except pictures, sounds and
videos over 256 kB. For those it gets only the first 64 kB, which is enough
for it to read a picture's size. The painter reads the whole file from disk.

## Not done yet

- Sound and speech: the engine's `sound`/`say` commands are counted but not played.
- 3D (`<scene3d>`, glTF) and textured globes (draw kind 8): globes are drawn
  with the engine's flat fallback.
- SVG pictures and colour emoji. Emoji are drawn as outlines from Noto Emoji.
- Shadows have no blur. Gradients are drawn as strips.
- Paths with holes (even-odd) are filled as separate rings.
- Building and installing Android/iOS packages, Git operations, publishing
  to Sliqtly Games, and starting an AI CLI from inside the Studio: these are
  the next steps in the plan (Sliqtly deck "Sliqtly Studio").
