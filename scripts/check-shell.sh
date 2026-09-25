#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Runs `shellcheck -x` over every shell script repo_files lists, each sourced
# file followed through its source directive and checked with it. An empty
# list fails: a gate over no scripts is not a pass.
#
# Nothing outside the scripts decides what is checked. --norc skips every rc
# file the checker would otherwise find, in the script's directory, each parent
# and the home directory, and SHELLCHECK_OPTS is emptied; a line in either can
# switch every check off for every script. A directive inside a script can do
# that for its own file, so a directive that disables all checks, a range of
# them or the dataflow analysis is refused here. One that names the codes it
# disables stays, and review reads the reason written beside it. A comment that
# begins with the checker's name is read as a directive, malformed or not, so
# no comment here starts with it.
set -euo pipefail

_CHECK_SHELL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_CHECK_SHELL_DIR}/lib/repo-files.sh"

die() {
  printf 'check-shell: %s\n' "$*" >&2
  exit 1
}

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

# Captured before the loop so a failing enumeration aborts here instead of
# leaving the loop with nothing to read and a clean exit.
listed="$(repo_files '*.sh')"
[[ -n "${listed}" ]] || die "no shell script found"
scripts=()
while IFS= read -r file; do
  scripts+=("${file}")
done <<<"${listed}"

# broad <the text after "shellcheck" in a directive>
# Sets `why` to the setting when the directive switches off more than the codes
# it names, and to the empty string otherwise. A "#" ends the directive, since a
# reason may follow it on the same line.
broad() {
  local word code
  local -a words codes
  why=""
  read -r -a words <<<"${1%%#*}"
  # bash 3.2 fails on an empty array expanded under `set -u`.
  [[ ${#words[@]} -gt 0 ]] || return 0
  for word in "${words[@]}"; do
    case "${word}" in
      disable=*)
        IFS=',' read -r -a codes <<<"${word#disable=}"
        [[ ${#codes[@]} -gt 0 ]] || continue
        for code in "${codes[@]}"; do
          if [[ "${code}" =~ ^[Aa][Ll][Ll]$ || "${code}" == *-* ]]; then
            why="${word}"
          fi
        done
        ;;
      extended-analysis=false)
        why="${word}"
        ;;
    esac
  done
}

broad_directives=0
directive_re='#[[:space:]]*shellcheck[[:space:]]+(.*)$'
for file in "${scripts[@]}"; do
  # grep exits 1 for "no match" and 2 or more for "could not read"; folding the
  # two together would pass a script nobody read.
  hits="$(grep -nE '#[[:space:]]*shellcheck[[:space:]]' "${file}")" && status=0 || status=$?
  (( status <= 1 )) || die "cannot read ${file} (grep exit ${status})"
  while IFS= read -r hit; do
    [[ -n "${hit}" ]] || continue
    [[ "${hit#*:}" =~ ${directive_re} ]] || continue
    broad "${BASH_REMATCH[1]}"
    if [[ -n "${why}" ]]; then
      printf 'FAIL %s:%s: "%s" switches off more than the codes it names\n' \
        "${file}" "${hit%%:*}" "${why}" >&2
      broad_directives=$((broad_directives + 1))
    fi
  done <<<"${hits}"
done
if (( broad_directives > 0 )); then
  die "${broad_directives} directive(s) switch off more than the codes they name; name each code and say why beside it"
fi

SHELLCHECK_OPTS='' shellcheck --norc -x "${scripts[@]}"
printf 'check-shell: %d script(s) clean (shellcheck --norc -x, no directive broader than its codes)\n' \
  "${#scripts[@]}"
