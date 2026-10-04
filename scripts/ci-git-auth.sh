#!/bin/sh
# Lets git clone the private dependencies (RangerFlow, RangerMarkdown) in CI
# with the DEPS_TOKEN secret: a GitHub token that can read them (a
# fine-grained token with Contents: read on those two repositories).
#
# The header is set for those two repositories' URLs only. Git takes the
# most specific http.<url>.extraHeader, so it also wins over the
# github.com-wide one actions/checkout leaves for this repository's own
# token, which cannot read them.
set -eu
if [ -z "${DEPS_TOKEN:-}" ]; then
  echo "DEPS_TOKEN is not set: RangerFlow and RangerMarkdown cannot be cloned" >&2
  exit 1
fi
auth=$(printf 'x-access-token:%s' "$DEPS_TOKEN" | base64 | tr -d '\n')
for repo in RangerFlow RangerMarkdown; do
  for url in "https://github.com/terotests/$repo" "https://github.com/terotests/$repo/" "https://github.com/terotests/$repo.git/"; do
    git config --global "http.$url.extraheader" "AUTHORIZATION: basic $auth"
  done
done
echo "git can read RangerFlow and RangerMarkdown"
