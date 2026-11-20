#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Puts what the Studio is built from in .deps/, at the revisions it was built
# and tested with:
#
#   .deps/Ranger        the Ranger compiler, the gallery's text editor, fonts
#                       and UI context; lib/evg and lib/image (its own deps step)
#   .deps/EVGSheets     the code editor (datagrid/src/script, AGPL)
#   .deps/RangerGames   the game engine (crates/rg_engine), linked as a static
#                       library; its own setup fetches its dependencies
#
# Needs git, node and cargo. RANGER_DIR / EVGSHEETS_DIR / RANGERGAMES_DIR set
# to existing checkouts are linked instead of cloned.
set -euo pipefail
cd "$(dirname "$0")/.."
RANGER_REV=ab06d5c3bfd80efa67e390901a9a52455f67a7b1
EVGSHEETS_REV=ab5dd0340a8290732f27af03833e13d5b71876f5
RANGERGAMES_REV=056f932227d4ed75fba537b11c9b7862f580089d
mkdir -p .deps

fetch() { # name url rev
  local dir=".deps/$1"
  if [[ -L "$dir" ]]; then return; fi
  if [[ ! -d "$dir/.git" ]]; then
    git clone --filter=blob:none --no-checkout "$2" "$dir"
  fi
  git -C "$dir" fetch --filter=blob:none origin "$3" 2>/dev/null || git -C "$dir" fetch origin
  git -C "$dir" checkout -q "$3"
}

link_or_fetch() { # name env-var url rev
  local val="${!2:-}"
  if [[ -n "$val" ]]; then ln -sfn "$(cd "$val" && pwd)" ".deps/$1"; else fetch "$1" "$3" "$4"; fi
}

link_or_fetch Ranger RANGER_DIR https://github.com/terotests/Ranger.git "$RANGER_REV"
link_or_fetch EVGSheets EVGSHEETS_DIR https://github.com/terotests/EVGSheets.git "$EVGSHEETS_REV"
link_or_fetch RangerGames RANGERGAMES_DIR https://github.com/terotests/RangerGames.git "$RANGERGAMES_REV"

if [[ ! -e .deps/Ranger/lib/evg/EVGDisplayList.rgr ]]; then
  (cd .deps/Ranger && node scripts/deps.mjs) || true
fi
[[ -e .deps/Ranger/lib/evg/EVGDisplayList.rgr ]] || { echo "Ranger lib/evg missing: run 'npm run deps' in .deps/Ranger" >&2; exit 1; }
if [[ ! -d .deps/RangerGames/.deps/Ranger ]]; then
  (cd .deps/RangerGames && scripts/setup.sh)
fi
echo "ready: scripts/build.sh"
