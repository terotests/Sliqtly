#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# THIRD_PARTY_NOTICES for a Personal package: the licenses of what the
# server binary is built from besides Sliqtly's own code, as the Personal
# License's "Third-Party Components" promises.
#   packaging/third-party-notices.sh <os> <out file>
# Run after go generate (fonts/ holds the faces and their licenses) and
# with the modules downloaded, as the build does. The Go modules are the
# ones go list says the nocloud binary for <os> links; a module without a
# license file stops the build.
set -eu
os=${1:?darwin or linux}
out=${2:?output file}
here=$(cd "$(dirname "$0")" && pwd)
mcp=$(dirname "$here")
root=$(dirname "$mcp")
mods=$(mktemp)
trap 'rm -f "$mods"' EXIT
(cd "$mcp" && GOOS="$os" CGO_ENABLED=0 go list -tags nocloud -deps \
  -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' .) > "$mods"
sort -u -o "$mods" "$mods"
ls "$mcp"/fonts/*.license.txt > /dev/null 2>&1 || { echo "no font licenses in fonts/ (go generate)" >&2; exit 1; }

section() {
  printf '\n%s\n%s\n\n' "------------------------------------------------------------------------" "$1"
}

{
  echo "Third-party notices for Sliqtly Personal"
  echo
  echo "The Sliqtly Personal software includes the components below. Each is"
  echo "under its own license, reproduced here; the Sliqtly Personal License"
  echo "does not limit the rights these licenses grant."

  section "The Go standard library and runtime"
  cat "$(go env GOROOT)/LICENSE"

  section "Ranger runtime and EVG layout engine (MIT)"
  sed 's/(c) 2026 /(c) 2017-2026 /' "$root/LICENSE-MIT"

  section "three.js (MIT): Ranger's 3-D engine is a port of three.js"
  cat <<'THREE'
The MIT License

Copyright © 2010-2026 three.js authors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
THREE

  for f in "$mcp"/fonts/*.license.txt; do
    section "Font: $(basename "$f" .license.txt | tr '_' ' ')"
    cat "$f"
  done
  section "Font: DejaVu Sans (symbols)"
  cat "$mcp/symbols/LICENSE"

  while read -r path version dir; do
    section "Go module $path $version"
    found=0
    for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE* "$dir"/PATENTS; do
      [ -f "$f" ] || continue
      cat "$f"
      echo
      found=1
    done
    if [ "$found" = 0 ]; then
      echo "no license file in $dir ($path)" >&2
      exit 1
    fi
  done < "$mods"
} > "$out"
echo "$out"
