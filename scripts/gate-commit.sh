#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Runs the whole gate, cold, over exactly what a commit holds: `git archive` of
# the commit into a scratch directory, never the working tree or the index,
# which can differ from the commit by a file staged, deleted or left out. A
# green run leaves a stamp under the repository's git directory that
# scripts/pre-push.sh requires before the commit may leave this machine.
#
#   scripts/gate-commit.sh [<commit>]    # HEAD by default
set -euo pipefail

die() {
  printf 'gate-commit: %s\n' "$*" >&2
  exit 1
}

[[ $# -le 1 ]] || die "name at most one commit"
sha="$(git rev-parse --verify --quiet "${1:-HEAD}^{commit}")" || die "${1:-HEAD} names no commit"
stamps="$(git rev-parse --git-common-dir)/gate-green"
mkdir -p "${stamps}"

work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT
git archive "${sha}" | tar -x -C "${work}"
log="${stamps}/${sha}.log"

printf 'gate-commit: %s: cold make quality on its archive; log %s\n' "${sha}" "${log}"
rc=0
(cd "${work}" && go clean -testcache && make quality) >"${log}" 2>&1 || rc=$?
last="$(tail -n 1 "${log}")"
if [[ ${rc} -ne 0 || "${last}" != "quality: green" ]]; then
  rm -f "${stamps:?}/${sha:?}"
  grep -nE '^(FAIL|--- FAIL)|Error [0-9]|panic:' "${log}" | head -n 20 >&2 || true
  die "${sha} is not green (exit ${rc}, last line: ${last}); nothing stamped"
fi
printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${last}" >"${stamps}/${sha}"
printf 'gate-commit: %s green; stamped\n' "${sha}"
