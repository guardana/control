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

# go_packages_covered <go command> <module directory>
# Fails, naming each one, unless every package of the module that holds a Go
# file repo_files lists is one ./... names there. The module's go.mod can
# leave a directory out of ./... with an ignore directive, and a target that
# ran ./... alone would then pass without ever building that package's tests.
# The inventory is every directory of the module, outside testdata, a nested
# module and a directory whose name starts with "_" or ".", whose Go files
# build on this platform, listed by path, which no ignore directive drops.
go_packages_covered() {
  local go="$1" mod="$2" mods files path dir owner m rel dirs="" want listed missing
  mods="$(go_modules)" || return 1
  files="$(repo_files '*.go')" || return 1
  while IFS= read -r path; do
    [[ -n "${path}" ]] || continue
    case "/${path}" in */testdata/* | */_* | */.*) continue ;; esac
    dir="."
    [[ "${path}" != */* ]] || dir="${path%/*}"
    owner="."
    while IFS= read -r m; do
      if [[ "${m}" != "." && ("${dir}" == "${m}" || "${dir}" == "${m}/"*) && ${#m} -gt ${#owner} ]]; then
        owner="${m}"
      fi
    done <<<"${mods}"
    [[ "${owner}" == "${mod}" ]] || continue
    rel="${dir}"
    [[ "${mod}" == "." ]] || rel="${dir#"${mod}"}"
    rel="${rel#/}"
    if [[ -z "${rel}" || "${rel}" == "." ]]; then
      dirs+="."$'\n'
    else
      dirs+="./${rel}"$'\n'
    fi
  done <<<"${files}"
  local -a args=()
  while IFS= read -r rel; do
    [[ -n "${rel}" ]] && args+=("${rel}")
  done < <(printf '%s' "${dirs}" | LC_ALL=C sort -u)
  if [[ ${#args[@]} -eq 0 ]]; then
    printf 'go_packages_covered: module %s holds no Go file repo_files lists\n' "${mod}" >&2
    return 1
  fi
  # A directory whose every file a build constraint leaves out is no package
  # here, under ./... or by path, so it is not counted.
  want="$("${go}" -C "${mod}" list -e \
    -f '{{if or .GoFiles .CgoFiles .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' "${args[@]}")" || {
    printf 'go_packages_covered: listing the packages of module %s by path failed\n' "${mod}" >&2
    return 1
  }
  listed="$("${go}" -C "${mod}" list -e -f '{{.ImportPath}}' ./...)" || {
    printf 'go_packages_covered: listing ./... in module %s failed\n' "${mod}" >&2
    return 1
  }
  if [[ -z "${want}" ]]; then
    printf 'go_packages_covered: module %s lists no package by path\n' "${mod}" >&2
    return 1
  fi
  missing=""
  while IFS= read -r m; do
    [[ -z "${m}" ]] || grep -Fxq -- "${m}" <<<"${listed}" || missing+=" ${m}"
  done <<<"${want}"
  if [[ -n "${missing}" ]]; then
    printf 'go_packages_covered: ./... in module %s leaves out a package that holds Go files:%s\n' "${mod}" "${missing}" >&2
    return 1
  fi
}
