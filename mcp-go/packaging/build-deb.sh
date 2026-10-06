#!/bin/sh
# Builds sliqtly-server_<version>_<arch>.deb with dpkg-deb, nothing else.
#
# EDITION=personal builds sliqtly-personal_<version>_<arch>.deb instead: the
# server with the viewer only (no editor), listening on this computer only,
# for anyone to download (.github/workflows/personal-package.yml):
#
#   npm --prefix .. ci && npm --prefix .. run build:view
#   SLIQTLY_BUILD_WEB=viewer go generate
#   EDITION=personal packaging/build-deb.sh 0.1.0 amd64
#
# The two install the same files, so either replaces the other.
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
edition=${EDITION:-pro}
here=$(cd "$(dirname "$0")" && pwd)
mcp=$(dirname "$here")
out=${OUT:-$mcp/dist}
root=$(mktemp -d)
chmod 0755 "$root"
trap 'rm -rf "$root"' EXIT

mkdir -p "$root/DEBIAN" "$root/usr/bin" "$root/lib/systemd/system" "$root/etc/sliqtly" "$root/usr/share/doc/sliqtly-server"
(cd "$mcp" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -tags nocloud -trimpath -ldflags="-s -w -X main.version=$version" -o "$root/usr/bin/sliqtly-server" .)
install -m 0644 "$here/deb/sliqtly.service" "$root/lib/systemd/system/sliqtly.service"
install -m 0640 "$here/deb/sliqtly.env" "$root/etc/sliqtly/sliqtly.env"
package=sliqtly-server
other=sliqtly-personal
web="the editor and player built in. Assistants
 (Claude, Cursor, VS Code) make and change presentations over MCP; people
 play and edit them in the browser."
if [ "$edition" = personal ]; then
  # the editor must not be in it: go generate must have built the viewer in
  if [ -f "$mcp/webdist/pres_app.js" ] || [ ! -f "$mcp/webdist/view.js" ]; then
    echo "EDITION=personal: webdist is not the viewer (SLIQTLY_BUILD_WEB=viewer go generate)" >&2
    exit 1
  fi
  package=sliqtly-personal
  other=sliqtly-server
  web="a presentation viewer built in, for one
 computer. Assistants (Claude, Cursor, VS Code) make and change
 presentations over MCP; the front page lists them and the browser plays
 them. Free for personal use and qualifying solo businesses with annual
 gross revenue below EUR 200,000; all other commercial use requires a
 commercial license (/usr/share/doc/sliqtly-server/copyright)."
  # this computer only, unless sliqtly.env says otherwise
  sed -i 's/^Environment=SLIQTLY_LISTEN=network$/Environment=SLIQTLY_LISTEN=local/; s/^# a server: other computers connect.*/# this computer only (sliqtly.env can say otherwise)/; s/MCP, editor and player/MCP and viewer/' "$root/lib/systemd/system/sliqtly.service"
  # its own license (Personal Use and Solo Business Use) as the copyright
  install -m 0644 "$here/personal/LICENSE" "$root/usr/share/doc/sliqtly-server/copyright"
  sed -i 's#^Documentation=.*#Documentation=https://sliqtly.com#' "$root/lib/systemd/system/sliqtly.service"
  sed -i '/^# Who can connect:/,/^#SLIQTLY_LISTEN=network$/c\
# Who can connect: local (this computer only, the package'"'"'s default),\
# wired (also computers on a wired network) or network (every interface).\
#SLIQTLY_LISTEN=local' "$root/etc/sliqtly/sliqtly.env"
fi
install -m 0755 "$here/deb/postinst" "$here/deb/prerm" "$here/deb/postrm" "$root/DEBIAN/"
install -m 0644 "$mcp/../LICENSE" "$root/usr/share/doc/sliqtly-server/copyright" 2>/dev/null || true
echo /etc/sliqtly/sliqtly.env > "$root/DEBIAN/conffiles"
size=$(du -sk "$root/usr" "$root/lib" "$root/etc" | awk '{s+=$1} END {print s}')
cat > "$root/DEBIAN/control" <<CONTROL
Package: $package
Version: $version
Architecture: $arch
Maintainer: Sliqtly <noreply@sliqtly.com>
Depends: ca-certificates
Conflicts: $other
Replaces: $other
Installed-Size: $size
Section: web
Priority: optional
Homepage: https://sliqtly.com
Description: Sliqtly presentations on a server of one's own
 The Sliqtly MCP server with $web
 Decks are kept in /var/lib/sliqtly.
 Settings: /etc/sliqtly/sliqtly.env.
CONTROL
mkdir -p "$out"
dpkg-deb --root-owner-group -Zxz --build "$root" "$out/${package}_${version}_${arch}.deb" >/dev/null
echo "$out/${package}_${version}_${arch}.deb"
