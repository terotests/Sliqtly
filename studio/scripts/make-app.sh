#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# macOS: puts "Sliqtly Game Studio.app" in ~/Applications, with the Studio's
# icon, so it starts from the Dock, Launchpad or Spotlight. The app runs
# scripts/start.mjs from this checkout (it builds when the sources changed)
# and opens the game folder used last; the first time it asks for one.
# The output goes to ~/Library/Logs/sliqtly-studio.log.
#
#   npm run studio:app        (from the top of the Sliqtly repository)
set -euo pipefail
STUDIO="$(cd "$(dirname "$0")/.." && pwd)"
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "make-app.sh makes a macOS app; on Linux, start with: npm run start:studio" >&2
  exit 1
fi
APP="${APP_DIR:-$HOME/Applications}/Sliqtly Game Studio.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# icon: assets/icon.png -> AppIcon.icns
SET="$(mktemp -d)/AppIcon.iconset"
mkdir -p "$SET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$STUDIO/assets/icon.png" --out "$SET/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d "$STUDIO/assets/icon.png" --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$SET" -o "$APP/Contents/Resources/AppIcon.icns"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Sliqtly Game Studio</string>
  <key>CFBundleDisplayName</key><string>Sliqtly Game Studio</string>
  <key>CFBundleIdentifier</key><string>com.sliqtly.gamestudio</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleExecutable</key><string>launch</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

cat > "$APP/Contents/MacOS/launch" <<LAUNCH
#!/bin/bash
# Started by Finder with a bare PATH: add Homebrew, cargo and nvm's node.
export PATH="/opt/homebrew/bin:/usr/local/bin:\$HOME/.cargo/bin:\$PATH"
if [ -s "\$HOME/.nvm/nvm.sh" ]; then . "\$HOME/.nvm/nvm.sh" >/dev/null 2>&1; fi
STUDIO="$STUDIO"
LOG="\$HOME/Library/Logs/sliqtly-studio.log"
LAST="\$STUDIO/build/last-project"
FOLDER=""
if [ ! -s "\$LAST" ] || [ ! -d "\$(cat "\$LAST")" ]; then
  FOLDER=\$(osascript -e 'POSIX path of (choose folder with prompt "Valitse pelikansio (siinä on game.json) tai pelien kansio")') || exit 0
fi
cd "\$STUDIO"
if [ -n "\$FOLDER" ]; then
  exec node scripts/start.mjs "\$FOLDER" >"\$LOG" 2>&1
fi
exec node scripts/start.mjs >"\$LOG" 2>&1
LAUNCH
chmod +x "$APP/Contents/MacOS/launch"
touch "$APP"
echo "Ready: $APP (drag it to the Dock to keep it there)"
