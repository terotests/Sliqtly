#!/bin/sh
# The SVG compatibility benchmark (README.md): the W3C SVG 1.1 test suite
# from web-platform-tests (svg/import), drawn by Chromium as the player
# draws a picture and by the preview (svgraster.go), compared pixel by
# pixel. Needs git, node with playwright-core (npm ci at the repo root) and
# a Chromium (CHROMIUM=…, default the Playwright one in /opt/pw-browsers).
#
#   sh run.sh [work dir]
set -e
here="$(cd "$(dirname "$0")" && pwd)"
work="${1:-/tmp/svgcompat}"
mkdir -p "$work"
if [ ! -d "$work/wpt" ]; then
  git clone --depth 1 --filter=blob:none --sparse https://github.com/web-platform-tests/wpt.git "$work/wpt"
  git -C "$work/wpt" sparse-checkout set svg/import
fi
git -C "$work/wpt" log -1 --format='wpt %H %cs'
FONTCONFIG_FILE="$here/fonts.conf" node "$here/chrome.mjs" "$work/wpt/svg/import" "$work/chrome" 480
cd "$here/../.."
SVGCOMPAT_SUITE="$work/wpt/svg/import" SVGCOMPAT_CHROME="$work/chrome" SVGCOMPAT_OUT="$work/out" \
  go test -run '^TestSvgCompat$' -count=1 -timeout 30m -v . | grep -E 'SVGs|FAIL|^ok'
cp "$work/out/results.json" "$here/results-w3c-svg11.json"
node "$here/report.mjs" "$here/results-w3c-svg11.json"
