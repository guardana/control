#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# buf breaking against the latest release tag before HEAD, the contract as
# published, as CI's proto-breaking job checks every push and pull request.
# Not main, and not a tag on HEAD itself: either can be the tree being checked.
# Kept out of `make quality`: a tree with no git history, an export for
# instance, has nothing to compare against. Then this prints UNKNOWN and exits
# 3, never a pass it did not earn; so does a tree that sits inside some other
# repository, or a history with no release tag.
set -euo pipefail

_BREAKING_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_BREAKING_DIR}/lib/repo-files.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

unknown() {
  printf 'proto-breaking: UNKNOWN, %s; nothing was compared\n' "$*" >&2
  exit 3
}

command -v buf >/dev/null 2>&1 || unknown "buf is not on PATH"
top="$(git rev-parse --show-toplevel 2>/dev/null)" || unknown "this tree has no git history"
# Compared as physical paths, so a symlinked checkout is still recognised.
[[ "$(cd "${top}" && pwd -P)" == "$(pwd -P)" ]] ||
  unknown "the git repository around this tree is ${top}, not this tree"
tag="$(git describe --tags --abbrev=0 --match 'v*' 'HEAD^' 2>/dev/null)" || unknown "no v* tag is reachable from HEAD's parent"
[[ "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || unknown "the latest tag is not a version: ${tag}"
gitdir="$(git rev-parse --path-format=absolute --git-common-dir)"

printf 'proto-breaking: comparing api/proto with %s\n' "${tag}"
buf breaking --against "${gitdir}#tag=${tag}"
printf 'proto-breaking: no breaking change against %s\n' "${tag}"
