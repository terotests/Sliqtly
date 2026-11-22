#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Builds Sliqtly Game Studio: build/sliqtly-studio and build/fonts/.
#
#   scripts/setup.sh      once: fetches Ranger, EVGSheets and RangerGames into .deps/
#   scripts/build.sh      the engine (cargo), the app (Ranger -> C++), the link
#
# Needs: node, cargo, a C++17 compiler, SDL3 (pkg-config sdl3) and OpenGL.
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
DEPS="${STUDIO_DEPS:-$HERE/.deps}"
RANGER="$DEPS/Ranger"
OUT="$HERE/build"
CXX="${CXX:-c++}"
mkdir -p "$OUT"

for d in Ranger EVGSheets EVGUI RangerGames; do
  [[ -d "$DEPS/$d" ]] || { echo "missing $DEPS/$d: run scripts/setup.sh first" >&2; exit 1; }
done

echo "==> 1/4 engine: cargo build (librg_studio.a)"
(cd "$HERE/native/rg_studio" && cargo build --release --quiet)
LIB="$HERE/native/rg_studio/target/release/librg_studio.a"

echo "==> 2/4 stage the Ranger tree"
# The app imports Ranger's gallery (text editor, fonts, UI context) and the
# EVGSheets code editor by relative path, so it is compiled from inside a
# Ranger checkout: gallery/studio is this folder, gallery/datagrid EVGSheets'.
rm -rf "$RANGER/gallery/studio"
mkdir -p "$RANGER/gallery/studio"
cp -R "$HERE/src" "$HERE/native" "$HERE/ranger.json" "$RANGER/gallery/studio/"
rm -rf "$RANGER/gallery/studio/native/rg_studio"
# EVGUI's controllers as gallery/evgui (its lib/evg is ../../lib/evg)
rm -rf "$RANGER/gallery/evgui"
mkdir -p "$RANGER/gallery/evgui"
cp -R "$DEPS/EVGUI/src" "$DEPS/EVGUI/ranger.json" "$RANGER/gallery/evgui/"
if [[ ! -e "$RANGER/gallery/datagrid/src/script/ScriptEditor.rgr" ]]; then
  rm -rf "$RANGER/gallery/datagrid"
  cp -R "$DEPS/EVGSheets/datagrid" "$RANGER/gallery/datagrid"
fi

echo "==> 3/4 Ranger -> C++"
GEN="$OUT/gen"
mkdir -p "$GEN"
(cd "$RANGER" && node dist/rgrc.js -l=cpp gallery/studio/src/StudioApp.rgr -nodecli -d="$GEN" -o=studio_app.cpp) > "$OUT/ranger.log" 2>&1 || true
if grep -q "FAIL" "$OUT/ranger.log" || [[ ! -s "$GEN/studio_app.cpp" ]]; then
  grep -B2 -A6 "FAIL\|rror" "$OUT/ranger.log" | head -60 >&2
  echo "Ranger compile failed (full log: $OUT/ranger.log)" >&2
  exit 1
fi
cp "$RANGER/gallery/invaders/variant.hpp" "$GEN/" 2>/dev/null || true

echo "==> 4/4 link"
GL="-lGL"
EXTRA="-lpthread -ldl -lm"
if [[ "$(uname -s)" == "Darwin" ]]; then
  GL="-framework OpenGL -DGL_SILENCE_DEPRECATION -Wno-deprecated-declarations"
  EXTRA="-framework CoreFoundation -framework Security"
fi
# shellcheck disable=SC2046,SC2086
"$CXX" ${CXX_OPT:--O2} -std=c++17 -w \
  -I"$HERE/native" -I"$GEN" \
  "$GEN/studio_app.cpp" "$HERE/native/studio_host.cpp" "$HERE/native/studio_mcp.cpp" "$HERE/native/studio_gl.cpp" \
  "$LIB" $(pkg-config --cflags --libs sdl3) $GL $EXTRA \
  -o "$OUT/sliqtly-studio"

mkdir -p "$OUT/fonts"
for f in Open_Sans Noto_Sans Noto_Emoji; do
  rm -rf "$OUT/fonts/$f"
  cp -R "$RANGER/gallery/pdf_writer/assets/fonts/$f" "$OUT/fonts/$f"
done
mkdir -p "$OUT/ui"
cp "$DEPS/EVGUI/theme/base.css" "$OUT/ui/base.css"
cp "$HERE/ui/studio.css" "$OUT/ui/studio.css"
echo "Ready: $OUT/sliqtly-studio <game folder>   (fonts: $OUT/fonts)"
