#!/bin/sh
# Lets git clone the private dependencies (RangerFlow, RangerMarkdown,
# RangerPPTX) in CI
# with the DEPS_TOKEN secret: a GitHub token that can read them (a
# fine-grained token with Contents: read on those three repositories).
#
# The header is set for those repositories' URLs only. The clones are
# made inside the Ranger checkout, outside this repository, so the
# github.com-wide header actions/checkout leaves in this repository's own
# config (its token cannot read them) is not sent with them.
set -eu
if [ -z "${DEPS_TOKEN:-}" ]; then
  echo "DEPS_TOKEN is not set: RangerFlow, RangerMarkdown and RangerPPTX cannot be cloned" >&2
  exit 1
fi
auth=$(printf 'x-access-token:%s' "$DEPS_TOKEN" | base64 | tr -d '\n')
# One URL per repository: git sends the header of every http.<url> that
# matches, so two keys matching the same request ("…/RangerFlow" and
# "…/RangerFlow/") make GitHub refuse it as a duplicate Authorization header.
# lib.mjs clones by the plain URL, which this key matches.
for repo in RangerFlow RangerMarkdown RangerPPTX; do
  git config --global --replace-all "http.https://github.com/terotests/$repo.extraheader" "AUTHORIZATION: basic $auth"
done
echo "git can read RangerFlow, RangerMarkdown and RangerPPTX"
