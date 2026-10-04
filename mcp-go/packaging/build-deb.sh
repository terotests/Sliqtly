#!/bin/sh
# Builds sliqtly-server_<version>_<arch>.deb with dpkg-deb, nothing else.
#
#   cd mcp-go
#   npm --prefix .. ci && npm --prefix .. run build   # the editor (else the package has none)
#   go generate                                       # Ranger → Go, web/dist into the binary
#   packaging/build-deb.sh 0.1.0 amd64                # or arm64
#
# The binary is static (CGO_ENABLED=0) and built without Google's client
# libraries (-tags nocloud, nocloud.go): the package needs no libraries,
# only ca-certificates for fetching pictures from https addresses.
set -eu
version=${1:?version, e.g. 0.1.0}
arch=${2:-amd64}
here=$(cd "$(dirname "$0")" && pwd)
mcp=$(dirname "$here")
out=${OUT:-$mcp/dist}
root=$(mktemp -d)
chmod 0755 "$root"
trap 'rm -rf "$root"' EXIT

mkdir -p "$root/DEBIAN" "$root/usr/bin" "$root/lib/systemd/system" "$root/etc/sliqtly" "$root/usr/share/doc/sliqtly-server"
(cd "$mcp" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -tags nocloud -trimpath -ldflags="-s -w" -o "$root/usr/bin/sliqtly-server" .)
install -m 0644 "$here/deb/sliqtly.service" "$root/lib/systemd/system/sliqtly.service"
install -m 0640 "$here/deb/sliqtly.env" "$root/etc/sliqtly/sliqtly.env"
install -m 0755 "$here/deb/postinst" "$here/deb/prerm" "$here/deb/postrm" "$root/DEBIAN/"
install -m 0644 "$mcp/../LICENSE" "$root/usr/share/doc/sliqtly-server/copyright" 2>/dev/null || true
echo /etc/sliqtly/sliqtly.env > "$root/DEBIAN/conffiles"
size=$(du -sk "$root/usr" "$root/lib" "$root/etc" | awk '{s+=$1} END {print s}')
cat > "$root/DEBIAN/control" <<CONTROL
Package: sliqtly-server
Version: $version
Architecture: $arch
Maintainer: Sliqtly <noreply@sliqtly.com>
Depends: ca-certificates
Installed-Size: $size
Section: web
Priority: optional
Homepage: https://sliqtly.com
Description: Sliqtly presentations on a server of one's own
 The Sliqtly MCP server with the editor and player built in. Assistants
 (Claude, Cursor, VS Code) make and change presentations over MCP; people
 play and edit them in the browser. Decks are kept in /var/lib/sliqtly.
 Settings: /etc/sliqtly/sliqtly.env.
CONTROL
mkdir -p "$out"
dpkg-deb --root-owner-group -Zxz --build "$root" "$out/sliqtly-server_${version}_${arch}.deb" >/dev/null
echo "$out/sliqtly-server_${version}_${arch}.deb"
