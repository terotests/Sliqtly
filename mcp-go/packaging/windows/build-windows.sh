#!/bin/sh
# The server for Windows, built on Linux like the .deb (build-deb.sh):
#
#   cd mcp-go
#   npm --prefix .. ci && npm --prefix .. run build   # the editor
#   go generate                                       # Ranger → Go, web/dist into the binary
#   packaging/windows/build-windows.sh 0.1.0 amd64    # or arm64
#
# → dist/windows_<arch>/sliqtly-server.exe, and
#   dist/sliqtly-server_<version>_windows_<arch>.zip: the program, its
#   settings file and README.txt, for installing by hand
#   (sliqtly-server service install). The .msi is made from the .exe on
#   Windows (build-msi.ps1).
#
# Static (CGO_ENABLED=0) and without Google's client libraries (-tags
# nocloud), as on Linux: it needs nothing else on the computer.
set -eu
version=${1:?version, e.g. 0.1.0}
arch=${2:-amd64}
here=$(cd "$(dirname "$0")" && pwd)
mcp=$(cd "$here/../.." && pwd)
out=${OUT:-$mcp/dist}
if [ ! -f "$mcp/webdist/pres_app.js" ]; then
  echo "webdist has no editor (npm run build, then go generate)" >&2
  exit 1
fi
bin=$out/windows_$arch
mkdir -p "$bin"
(cd "$mcp" && CGO_ENABLED=0 GOOS=windows GOARCH="$arch" go build -tags nocloud -trimpath -ldflags="-s -w -X main.version=$version" -o "$bin/sliqtly-server.exe" .)
# Notepad and the rest read Windows' line ends
sed 's/$/\r/' "$here/sliqtly.env" > "$bin/sliqtly.env"
sed 's/$/\r/' "$here/README.txt" > "$bin/README.txt"
zip=$out/sliqtly-server_${version}_windows_$arch.zip
rm -f "$zip"
(cd "$bin" && zip -q -X "$zip" sliqtly-server.exe sliqtly.env README.txt)
echo "$zip"
