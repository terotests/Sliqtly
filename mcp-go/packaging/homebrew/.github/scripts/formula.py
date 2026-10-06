#!/usr/bin/env python3
"""Writes Formula/sliqtly.rb from Sliqtly's latest.json (read from stdin):
{"version": "...", "darwin_arm64": {"url", "sha256"}, "darwin_amd64": {...}}
The Personal package workflow in terotests/Sliqtly uploads that file."""
import json, re, sys

d = json.load(sys.stdin)
v = d["version"]
for k in ("darwin_arm64", "darwin_amd64"):
    if not re.fullmatch(r"[0-9a-f]{64}", d[k]["sha256"]) or not d[k]["url"].startswith("https://"):
        sys.exit(f"latest.json: bad {k}")
if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+([~+.-][A-Za-z0-9.~+-]+)?", v):
    sys.exit("latest.json: bad version")

print(f'''# Written by .github/workflows/update.yml from
# https://firebasestorage.googleapis.com/v0/b/sliqtly.firebasestorage.app/o/downloads%2Fpersonal%2Flatest.json?alt=media
# Do not edit by hand.
class Sliqtly < Formula
  desc "MCP server and presentation viewer: your AI agent makes slides"
  homepage "https://sliqtly.com/local.html"
  version "{v}"
  # Sliqtly Personal License: free for personal use and qualifying solo
  # businesses (annual gross revenue below EUR 200,000); other commercial
  # use needs a commercial license. Not open source.
  license :cannot_represent

  on_arm do
    url "{d["darwin_arm64"]["url"]}"
    sha256 "{d["darwin_arm64"]["sha256"]}"
  end
  on_intel do
    url "{d["darwin_amd64"]["url"]}"
    sha256 "{d["darwin_amd64"]["sha256"]}"
  end

  depends_on :macos

  def install
    bin.install "sliqtly-server"
    pkgshare.install "sliqtly.env"
    prefix.install "LICENSE"
    # the service reads its settings from etc/sliqtly/sliqtly.env
    (bin/"sliqtly-service").write <<~SH
      #!/bin/sh
      set -a
      [ -f "#{{etc}}/sliqtly/sliqtly.env" ] && . "#{{etc}}/sliqtly/sliqtly.env"
      set +a
      : "${{SLIQTLY_DATA:=#{{var}}/sliqtly}}"
      : "${{SLIQTLY_BACKUP=#{{var}}/sliqtly-backup}}"
      : "${{SLIQTLY_LISTEN:=local}}"
      export SLIQTLY_DATA SLIQTLY_BACKUP SLIQTLY_LISTEN
      mkdir -p "$SLIQTLY_DATA"
      exec "#{{opt_bin}}/sliqtly-server" "$@"
    SH
    chmod 0755, bin/"sliqtly-service"
  end

  def post_install
    (etc/"sliqtly").mkpath
    # kept over upgrades: only written when there is none yet
    cp pkgshare/"sliqtly.env", etc/"sliqtly/sliqtly.env" unless (etc/"sliqtly/sliqtly.env").exist?
    (var/"sliqtly").mkpath
    (var/"log").mkpath
  end

  service do
    run [opt_bin/"sliqtly-service"]
    keep_alive true
    log_path var/"log/sliqtly.log"
    error_log_path var/"log/sliqtly.log"
  end

  def caveats
    <<~EOS
      Start it, now and at every login:
        brew services start sliqtly
      Then open http://localhost:8080/ and connect your agent to
      http://localhost:8080/mcp (https://sliqtly.com/local.html).
      Settings (port, who can connect): #{{etc}}/sliqtly/sliqtly.env
      License: #{{opt_prefix}}/LICENSE
    EOS
  end

  test do
    assert_predicate bin/"sliqtly-server", :executable?
  end
end''')
