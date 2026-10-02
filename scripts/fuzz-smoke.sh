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
# shellcheck source=lib/go-modules.sh
. "${_FUZZ_SMOKE_DIR}/lib/go-modules.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

# As in the Makefile: an untracked go.work must not change what is built.
export GOWORK=off

duration="${1:-10s}"

files="$(repo_files '*_test.go')"
listed="$(go_modules)"
if [[ -z "${listed}" ]]; then
  printf 'fuzz-smoke: no Go module listed; refusing to report a pass\n' >&2
  exit 1
fi
modules=()
while IFS= read -r dir; do
  modules+=("${dir}")
done <<<"${listed}"

# Each entry is "<module directory> <package relative to it> <function>". A
# package builds only from inside its own module, so each target runs from the
# directory of the nearest go.mod above it. repo_files rejects paths with
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
    filedir="${file%/*}"
  else
    filedir="."
  fi

  # The longest module directory that holds the file. "." holds every file.
  mod="."
  for dir in "${modules[@]}"; do
    if [[ "${dir}" != "." && ( "${filedir}" == "${dir}" || "${filedir}" == "${dir}/"* ) &&
      ${#dir} -gt ${#mod} ]]; then
      mod="${dir}"
    fi
  done
  if [[ "${mod}" == "." ]]; then
    rel="${filedir}"
  elif [[ "${filedir}" == "${mod}" ]]; then
    rel="."
  else
    rel="${filedir#"${mod}"/}"
  fi
  if [[ "${rel}" == "." ]]; then
    pkg="."
  else
    pkg="./${rel}"
  fi

  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    targets+=("${mod} ${pkg} ${name}")
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
  read -r mod pkg name <<<"${target}"
  # The package as a path from the repository root, for the lines below.
  if [[ "${mod}" == "." ]]; then
    shown="${pkg}"
  elif [[ "${pkg}" == "." ]]; then
    shown="./${mod}"
  else
    shown="./${mod}/${pkg#./}"
  fi
  printf 'fuzz-smoke: %s %s for %s\n' "${shown}" "${name}" "${duration}"
  if ! go -C "${mod}" test "${pkg}" -run '^$' -fuzz "^${name}\$" -fuzztime "${duration}"; then
    printf 'fuzz-smoke: %s failed; the minimized input is now in the working tree at %s/testdata/fuzz/%s/ - keep it as a regression seed or delete it\n' \
      "${name}" "${shown}" "${name}" >&2
    exit 1
  fi
done

printf 'fuzz-smoke: %d module(s)\n' "${#modules[@]}"
printf 'fuzz-smoke: %d target(s) run for %s each\n' "${#targets[@]}" "${duration}"
