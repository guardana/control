#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Runs every fuzz target briefly, so a seed corpus that stops building is
# caught by the ordinary gate rather than by a long fuzzing session.
#
# Not read-only on failure: `go test -fuzz` writes the minimized failing input
# to <package>/testdata/fuzz/<FuzzName>/ inside the working tree, and the go
# command has no flag to send it elsewhere. This target is therefore the one
# step of `make quality` that can leave a new file behind. The failure message
# below names the directory so the file is not found later by surprise.
set -euo pipefail

_FUZZ_SMOKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_FUZZ_SMOKE_DIR}/lib/repo-files.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

duration="${1:-10s}"

files="$(repo_files '*_test.go')"

# Each entry is "<package> <function>"; repo_files rejects paths with
# whitespace, so a space is a safe separator.
targets=()
while IFS= read -r file; do
  [[ -n "${file}" ]] || continue
  case "${file}" in
    api/gen/*|testdata/*|*/testdata/*) continue ;;
  esac

  names="$(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' "${file}")"
  [[ -n "${names}" ]] || continue

  if [[ "${file}" == */* ]]; then
    pkg="./${file%/*}"
  else
    pkg="."
  fi

  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    targets+=("${pkg} ${name}")
  done <<<"${names}"
done <<<"${files}"

# bash 3.2 raises "unbound variable" under `set -u` when "${targets[@]}" is
# expanded while empty, so return before the loop rather than inside it.
if [[ ${#targets[@]} -eq 0 ]]; then
  # A gate that reports a pass over none of its subject is what this project
  # calls a false green. This repository has fuzz targets, so finding none is
  # a regression in the gate, not an empty repository.
  printf 'fuzz-smoke: no fuzz targets found; this repository has them, so finding none means the search stopped working\n' >&2
  exit 1
fi

for target in "${targets[@]}"; do
  read -r pkg name <<<"${target}"
  printf 'fuzz-smoke: %s %s for %s\n' "${pkg}" "${name}" "${duration}"
  if ! go test "${pkg}" -run '^$' -fuzz "^${name}\$" -fuzztime "${duration}"; then
    printf 'fuzz-smoke: %s failed; the minimized input is now in the working tree at %s/testdata/fuzz/%s/ - keep it as a regression seed or delete it\n' \
      "${name}" "${pkg}" "${name}" >&2
    exit 1
  fi
done

printf 'fuzz-smoke: %d target(s) run for %s each\n' "${#targets[@]}" "${duration}"
