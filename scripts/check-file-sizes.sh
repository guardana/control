#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Size budget for hand-written Go. Above 350 non-blank lines warns, above 500
# fails.
set -euo pipefail

_CHECK_SIZES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_CHECK_SIZES_DIR}/lib/repo-files.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

readonly WARN_LIMIT=350
readonly FAIL_LIMIT=500

# Captured before the loop so a failing enumeration aborts here instead of
# leaving the loop with nothing to read and a clean exit.
files="$(repo_files '*.go')"

checked=0
warnings=0
failures=0

while IFS= read -r file; do
  [[ -n "${file}" ]] || continue
  case "${file}" in
    api/gen/*|testdata/*|*/testdata/*|*_test.go|*.pb.go) continue ;;
  esac

  lines="$(awk 'NF { n++ } END { print n + 0 }' "${file}")"
  checked=$((checked + 1))

  if (( lines > FAIL_LIMIT )); then
    printf 'FAIL %s: %d lines (limit %d)\n' "${file}" "${lines}" "${FAIL_LIMIT}" >&2
    failures=$((failures + 1))
  elif (( lines > WARN_LIMIT )); then
    printf 'warn %s: %d lines\n' "${file}" "${lines}"
    warnings=$((warnings + 1))
  fi
done <<<"${files}"

printf 'check-sizes: %d file(s) checked, %d warning(s), %d failure(s)\n' \
  "${checked}" "${warnings}" "${failures}"

# A run that examined nothing is a broken enumeration, not a clean tree. The
# repository always has hand-written Go outside api/gen/ and _test.go files.
if (( checked == 0 )); then
  printf 'check-sizes: no Go production source found; refusing to report a pass\n' >&2
  exit 1
fi

if (( failures > 0 )); then
  exit 1
fi
