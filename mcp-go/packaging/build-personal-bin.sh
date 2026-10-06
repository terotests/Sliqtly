#!/bin/sh
# The Personal server for macOS (Homebrew) and the Docker image:
#   packaging/build-personal-bin.sh <version> darwin arm64
#     → dist/sliqtly-personal_<version>_darwin_arm64.tar.gz
#       (sliqtly-server, LICENSE, sliqtly.env), what the Homebrew formula in
#       terotests/homebrew-sliqtly installs
#   packaging/build-personal-bin.sh <version> linux amd64
#     → dist/bin/linux_amd64/sliqtly-server, for packaging/personal/Dockerfile
# Like the Personal .deb, only the viewer may be built in: run
# SLIQTLY_BUILD_WEB=viewer go generate first.
set -eu
version=${1:?version, e.g. 0.1.0}
os=${2:?darwin or linux}
arch=${3:?arm64 or amd64}
here=$(cd "$(dirname "$0")" && pwd)
mcp=$(dirname "$here")
out=${OUT:-$mcp/dist}
if [ -f "$mcp/webdist/pres_app.js" ] || [ ! -f "$mcp/webdist/view.js" ]; then
  echo "webdist is not the viewer (SLIQTLY_BUILD_WEB=viewer go generate)" >&2
  exit 1
fi
bin=$out/bin/${os}_$arch
mkdir -p "$bin"
(cd "$mcp" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -tags nocloud -trimpath -ldflags="-s -w -X main.version=$version" -o "$bin/sliqtly-server" .)
if [ "$os" = darwin ]; then
  cp "$here/personal/LICENSE" "$bin/LICENSE"
  cp "$here/personal/sliqtly-mac.env" "$bin/sliqtly.env"
  tar -C "$bin" -czf "$out/sliqtly-personal_${version}_${os}_$arch.tar.gz" sliqtly-server LICENSE sliqtly.env
  echo "$out/sliqtly-personal_${version}_${os}_$arch.tar.gz"
else
  echo "$bin/sliqtly-server"
fi
