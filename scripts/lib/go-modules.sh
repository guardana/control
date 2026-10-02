#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# go_modules: the Go modules the repository holds. A go command run with ./...
# stops at a directory with a go.mod of its own, so a gate that runs it from the
# root alone never looks at a nested module; every Go gate loops over this list.
#
# A script that sources this file puts `# shellcheck source-path=SCRIPTDIR` on
# line 2, as for repo-files.sh, which this file sources itself.

_GO_MODULES_SH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=repo-files.sh
. "${_GO_MODULES_SH_DIR}/repo-files.sh"

# go_modules
# Prints each module directory relative to the repository root, one per line:
# "." first, then the directory of every other go.mod repo_files lists, sorted.
# A go.mod under a testdata directory is a fixture, not a module of the
# repository, and the go command ignores testdata as well. Fails when the root
# holds no go.mod or the listing fails, and refuses two kinds of file the
# callers could not loop over safely:
#   - a go.mod spelt in another case: a case-insensitive file system makes it a
#     module boundary for the go command, while an exact match would miss it;
#   - a module directory holding a character other than a letter, a digit, ".",
#     "_", "-" or "/": a shell glob or quote in it changes what a loop reads.
go_modules() {
  local root listed path dir out="."
  local safe='^[A-Za-z0-9._/-]+$'
  root="$(repo_root)" || return 1
  if [[ ! -f "${root}/go.mod" ]]; then
    printf 'go_modules: no go.mod at the repository root %s\n' "${root}" >&2
    return 1
  fi
  # Captured first, so a failing listing stops here instead of ending the loop
  # below with nothing read and a clean exit.
  listed="$(repo_files '*[Gg][Oo].[Mm][Oo][Dd]')" || return 1
  while IFS= read -r path; do
    [[ -n "${path}" ]] || continue
    case "${path##*/}" in
      go.mod) ;;
      [Gg][Oo].[Mm][Oo][Dd])
        printf 'go_modules: %s is a go.mod in another case, which a case-insensitive file system makes a module; name it go.mod\n' "${path}" >&2
        return 1
        ;;
      *) continue ;;
    esac
    case "${path}" in
      go.mod|testdata/*|*/testdata/*) continue ;;
    esac
    dir="${path%/go.mod}"
    if [[ ! "${dir}" =~ ${safe} ]]; then
      printf 'go_modules: refusing module directory %s: only letters, digits, ".", "_", "-" and "/" are allowed\n' "${dir}" >&2
      return 1
    fi
    out+=$'\n'"${dir}"
  done <<<"${listed}"
  printf '%s\n' "${out}"
}
