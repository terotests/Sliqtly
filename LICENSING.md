# Licensing

Sliqtly is dual licensed: AGPL-3.0-or-later for everyone, and a separate
license from the copyright holder for those who do not follow the AGPL.

| Path | License | SPDX |
| --- | --- | --- |
| Everything not listed below | GNU Affero General Public License v3.0 or later | `AGPL-3.0-or-later` |
| `desktop/` | MIT ([`desktop/LICENSE`](desktop/LICENSE)) | `MIT` |
| Files whose header says MIT (e.g. `src/ApiClient.rgr`, `src/JsonValue.rgr`) | MIT | `MIT` |
| `fonts/`, `mcp-go/symbols/`, `desktop/native/fonts/` | The font's own license, next to it (OFL, Apache-2.0, DejaVu) | – |

| File | Role |
| --- | --- |
| [`LICENSE`](LICENSE) | Overview |
| [`LICENSE-AGPL-3.0`](LICENSE-AGPL-3.0) | Full GNU AGPLv3 text |
| [`LICENSE-MIT`](LICENSE-MIT) | Full MIT text |
| [`mcp-go/packaging/personal/LICENSE`](mcp-go/packaging/personal/LICENSE) | Sliqtly Personal License, for the Personal binaries |

Source files carry an SPDX line. A file without one is under the license
of the directory it is in, as is a new file.

## What the AGPL means here

You may use, change and share the source under AGPL-3.0-or-later. If you
run a changed version as a network service (for example your own
sliqtly.com), the people who use it over the network must be offered the
source of your changed version (AGPL section 13).

## Commercial licenses

The copyright holder may grant other licenses for the same work. This is
not an AGPL exception; it is a separate grant.

- **Sliqtly Personal** (`.deb`, Homebrew, Docker image): free for personal
  use and qualifying solo businesses; other commercial use needs a
  commercial license. It covers the binaries the copyright holder
  distributes. It does not take away rights anyone has under the AGPL for
  source code they got under the AGPL.
- **Commercial license**: for use, embedding or hosting without the AGPL's
  obligations. Contact the copyright holder. `May` above is deliberate: a
  license is not promised, nor its terms.

## Transfer and relicensing

The copyright holder may transfer or sell the rights in Sliqtly, in whole
or in part, to any third party, including the hosted service together with
its users (see section 11 of the [terms of service](web/terms.html) and
the Personal License). The copyright holder and anyone who receives these
rights may relicense Sliqtly on other terms, including to partners who
sell it under their own name and sublicense it further. Licenses already
granted for versions people have received stay in force.

## Third-party components

Third-party code, fonts and libraries remain under their own licenses.
The Personal packages carry a `THIRD_PARTY_NOTICES` file, written at build
time by
[`mcp-go/packaging/third-party-notices.sh`](mcp-go/packaging/third-party-notices.sh)
from the Go modules linked into the binary and the fonts built into it.

Sliqtly builds on the author's own libraries, fetched at build time:
Ranger (compiler, runtime and EVG layout engine under MIT; Ranger
Gallery under AGPL-3.0-or-later), EVGUI, RangerMarkdown, RangerFlow,
RangerPPTX and RangerDiff. Each states its license in its own repository.

## Contributions

Dual licensing needs the copyright holder to be able to grant both
licenses. Contributions are therefore accepted only on the terms in
[`CONTRIBUTING.md`](CONTRIBUTING.md). Today the commit authors are
Tero Tolonen and automated agents working for him.

## Name and logo

The name Sliqtly and the logo (`brand/`) are not licensed under the AGPL,
the MIT License or the Personal License. A changed version must not be
presented as Sliqtly or as made by its author.
