#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# The dependency rule, enforced independently of golangci-lint so that editing
# the lint config cannot quietly remove it. What the rule allows is written in
# lib/dependency-rule.sh.
#
# Every package of this module that a guarded tree reaches is examined, not
# only the tree's own packages; otherwise a helper package outside the guarded
# trees would be a way around the rule. A package outside the module is judged
# only as an import: the rule decides whether a guarded tree may use it at all.
# A package that is examined may also hold no file in the go list fields
# refused_files names. go list reports the build of one platform, so a file
# that only another platform compiles, or assembly that needs no import, would
# otherwise pass on every machine the gate runs on.
#
# This script and depguard (.golangci.yml) each cover a half the other misses:
#   - here: the in-module packages the non-test build reaches, which depguard
#     does not see because it reads only each guarded file's own imports, and
#     the files of those packages that are not plain Go built on this platform;
#   - depguard: imports written in _test.go files, which `go list -deps`
#     leaves out of the package's dependency set.
set -euo pipefail

# The source= paths below resolve against this script's directory through the
# source-path directive on line 2. Placed anywhere after the first command, that
# directive covers one command only, and shellcheck -x stops following them.
_CHECK_IMPORTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_CHECK_IMPORTS_DIR}/lib/repo-files.sh"
# shellcheck source=lib/dependency-rule.sh
. "${_CHECK_IMPORTS_DIR}/lib/dependency-rule.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

# Read, never hard-coded: renaming the module must not require touching this
# file. GOWORK=off because inside a go.work workspace `go list -m` prints one
# line per module, and a multi-line value here would turn every in-module path
# built from it into a string no package path can ever equal.
module="$(GOWORK=off go list -m)"
if [[ -z "${module}" || "${module}" == *[[:space:]]* ]]; then
  printf 'check-imports: go list -m did not return exactly one module path: %q\n' \
    "${module}" >&2
  exit 1
fi

# under <path> <prefix>
# True when path is prefix itself or a package below it. Whole segments, so
# net/httptest is not under net/http.
under() {
  [[ "$1" == "$2" || "$1" == "$2/"* ]]
}

# judge <import path>
# Sets `why` to the reason a guarded tree may not import the path, or to the
# empty string when it may. A variable rather than output, because this runs
# once per import and a command substitution would fork each time.
judge() {
  local imp="$1" entry
  why=""
  if under "${imp}" "${module}"; then
    for entry in "${denied[@]}"; do
      if under "${imp}" "${module}/${entry}"; then
        why="a tree the dependency rule denies"
        return 0
      fi
    done
    return 0
  fi
  for entry in "${modules[@]}"; do
    if under "${imp}" "${entry}"; then
      return 0
    fi
  done
  for entry in "${stdlib[@]}"; do
    if [[ "${imp}" == "${entry}" ]]; then
      return 0
    fi
  done
  why="which the dependency rule does not allow"
}

# file_reason <go list field>
# Sets `why` to the reason a package held to the rule may not hold a file that
# go list reports in the field. Only the fields refused_files names reach here.
file_reason() {
  case "$1" in
    IgnoredGoFiles | IgnoredOtherFiles) why="which build constraints leave out on this platform" ;;
    *) why="which is not plain Go ($1)" ;;
  esac
}

# held <package>
# True when the package's own imports and files are held to the rule: it is in
# this module and not generated.
held() {
  local entry
  under "$1" "${module}" || return 1
  for entry in "${generated[@]}"; do
    if under "$1" "${module}/${entry}"; then
      return 1
    fi
  done
  return 0
}

# One line per package the non-test build reaches: its path, its direct
# imports, a lone "|", then each file it holds in a field refused_files names,
# written <field>:<file>. See the note at the top of the file for what depguard
# covers.
template='{{.ImportPath}}{{range .Imports}} {{.}}{{end}} |'
for field in "${refused_files[@]}"; do
  template+="{{range .${field}}} ${field}:{{.}}{{end}}"
done

checked=0
failures=0
packages=""
imports=""
refusals=""

# Every guarded tree exists and holds packages. One that does not resolve is
# a failure, never a skip: a tree renamed or moved out from under its name
# would otherwise leave the rule with nothing to examine there and exit 0.
for dir in "${guarded[@]}"; do
  pattern="./${dir}/..."

  # Statted first, so the failure names the tree instead of git warning on
  # stderr about a directory it cannot walk for untracked files.
  if [[ ! -d "${dir}" ]]; then
    printf 'FAIL %s: the guarded tree does not exist\n' "${pattern}" >&2
    failures=$((failures + 1))
    continue
  fi

  # Assigned before the test: inside [[ ]] a failing repo_files would look like
  # an empty result.
  gofiles="$(repo_files "${dir}/*.go")"
  if [[ -z "${gofiles}" ]]; then
    printf 'FAIL %s: the guarded tree holds no Go file\n' "${pattern}" >&2
    failures=$((failures + 1))
    continue
  fi

  if ! listing="$(go list -deps -f "${template}" "${pattern}")"; then
    printf 'FAIL %s: go list -deps failed\n' "${pattern}" >&2
    failures=$((failures + 1))
    continue
  fi
  if [[ -z "${listing}" ]]; then
    printf 'FAIL %s: go list -deps named no package\n' "${pattern}" >&2
    failures=$((failures + 1))
    continue
  fi
  checked=$((checked + 1))

  while read -r -a fields; do
    [[ ${#fields[@]} -gt 0 ]] || continue
    pkg="${fields[0]}"
    held "${pkg}" || continue
    packages+="${pkg}"$'\n'
    in_files=0
    for ((i = 1; i < ${#fields[@]}; i++)); do
      token="${fields[i]}"
      if [[ ${in_files} -eq 1 ]]; then
        file_reason "${token%%:*}"
        refusals+="FAIL ${pkg} holds ${token#*:}, ${why}"$'\n'
      elif [[ "${token}" == "|" ]]; then
        in_files=1
      else
        imports+="${pkg} ${token}"$'\n'
        judge "${token}"
        if [[ -n "${why}" ]]; then
          refusals+="FAIL ${pkg} imports ${token}, ${why}"$'\n'
        fi
      fi
    done
    # A line that never reached the "|" means the template no longer lists the
    # files, and every file this script refuses would pass unexamined.
    if [[ ${in_files} -eq 0 ]]; then
      printf 'FAIL %s: go list printed no "|" before the files; the template no longer lists them\n' "${pkg}" >&2
      failures=$((failures + 1))
    fi
  done <<<"${listing}"
done

# Two trees can reach the same package; each package and each refusal counts
# once.
count() {
  printf '%s' "$1" | LC_ALL=C sort -u | grep -c . || true
}
npackages="$(count "${packages}")"
nimports="$(count "${imports}")"
nrefusals="$(count "${refusals}")"
if [[ ${nrefusals} -gt 0 ]]; then
  printf '%s' "${refusals}" | LC_ALL=C sort -u >&2
fi

printf 'check-imports: %d of %d tree(s) checked; %d package(s) of this module and %d import(s) examined; %d violation(s)\n' \
  "${checked}" "${#guarded[@]}" "${npackages}" "${nimports}" "$((nrefusals + failures))"

# A run that examined nothing reports that, never a pass.
if [[ ${checked} -eq 0 ]]; then
  printf 'check-imports: no guarded tree resolved to a package; the rule examined nothing\n' >&2
  exit 1
fi
if [[ ${nimports} -eq 0 ]]; then
  printf 'check-imports: %d package(s) listed and not one import among them; the go list template no longer yields imports\n' \
    "${npackages}" >&2
  exit 1
fi
if [[ $((nrefusals + failures)) -ne 0 ]]; then
  exit 1
fi
