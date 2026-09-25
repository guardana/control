#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Keeps every "uses:" in .github/workflows pinned to the commit SHA of the tag
# named in its trailing "# vX.Y.Z" comment.
#
#   scripts/pin-actions.sh            rewrite each SHA from its comment
#   scripts/pin-actions.sh --check    verify each SHA against its comment
#
# Bumping a pin is therefore: edit the tag in the comment, run this, review the
# new SHA. The comment is the source of truth and the SHA is derived from it.
#
# --check exists because scripts/check-actions-pinned.sh can only see shape: a
# SHA from an entirely different repository still looks like a pin. Forks share
# an object store, so any commit in an action's fork network is fetchable under
# that action's name, and this is what refuses it.
#
# The comment is written in the shape Dependabot reads and rewrites, so an
# automated bump lands as a matching pair rather than a new SHA under a stale
# version.
set -euo pipefail

_PIN_ACTIONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_PIN_ACTIONS_DIR}/lib/repo-files.sh"

check_only=0
case "${1-}" in
  --check) check_only=1 ;;
  "") ;;
  *)
    printf 'usage: pin-actions.sh [--check]\n' >&2
    exit 2
    ;;
esac

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

if ! command -v gh >/dev/null 2>&1; then
  printf 'pin-actions: gh is required to resolve tags to commits\n' >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT
resolved="${tmpdir}/resolved"
: >"${resolved}"

# lookup <key>
# Prints the SHA already resolved for "<repo> <tag>", or fails. A flat file
# rather than an associative array: bash 3.2 is still the default /bin/bash on
# macOS and has none.
lookup() {
  local key="$1" name tag sha
  while read -r name tag sha; do
    if [[ "${name} ${tag}" == "${key}" ]]; then
      printf '%s\n' "${sha}"
      return 0
    fi
  done <"${resolved}"
  return 1
}

# resolve <owner/repo> <tag>
# Prints the commit SHA the tag points at. An annotated tag points at a tag
# object, which has to be dereferenced once more to reach the commit.
#
# Both arguments land in a GitHub API path, so both are checked here rather
# than trusted from the caller's regexp. The API honours "..", so a version
# comment shaped like a traversal would otherwise read a tag out of an
# unrelated repository and this script would write that SHA into the workflow.
# --hostname is passed explicitly for the same reason in the other direction:
# $GH_HOST must not be able to move these reads to another API.
resolve() {
  local name="$1" tag="$2" object type sha
  if [[ ! "${name}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    printf 'pin-actions: refusing repository name %s\n' "${name}" >&2
    return 1
  fi
  if [[ ! "${tag}" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]]; then
    printf 'pin-actions: refusing tag %s for %s\n' "${tag}" "${name}" >&2
    return 1
  fi
  object="$(gh api --hostname github.com "repos/${name}/git/ref/tags/${tag}" \
    --jq '.object.type + " " + .object.sha')" || return 1
  type="${object%% *}"
  sha="${object##* }"
  if [[ "${type}" == "tag" ]]; then
    sha="$(gh api --hostname github.com "repos/${name}/git/tags/${sha}" \
      --jq '.object.sha')" || return 1
  fi
  if [[ ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
    printf 'pin-actions: %s %s resolved to %s, not a commit SHA\n' \
      "${name}" "${tag}" "${sha}" >&2
    return 1
  fi
  printf '%s\n' "${sha}"
}

# sha_for <owner/repo> <tag>
# resolve() through the per-run cache, so a repeated action costs one API call.
sha_for() {
  local name="$1" tag="$2" sha
  if sha="$(lookup "${name} ${tag}")"; then
    printf '%s\n' "${sha}"
    return 0
  fi
  sha="$(resolve "${name}" "${tag}")" || return 1
  printf '%s %s %s\n' "${name}" "${tag}" "${sha}" >>"${resolved}"
  printf 'pin-actions: %s %s -> %s\n' "${name}" "${tag}" "${sha}" >&2
  printf '%s\n' "${sha}"
}

# The version has to start with a digit, optionally after a "v", and admits no
# "/" — see resolve() for why. A step whose comment is prose is reported below
# instead of being sent to the API as if it named a tag.
#
# The key is matched in every spelling YAML reads as "uses" ("uses":, 'uses':,
# `uses :`), for the reason given in check-actions-pinned.sh.
readonly KEY="[\"']?uses[\"']?[[:space:]]*:"
readonly PIN_RE="^([[:space:]]*(-[[:space:]]+)?${KEY}[[:space:]]*)([^[:space:]@]+)@([^[:space:]]+)([[:space:]]+#[[:space:]]*)(v?[0-9][A-Za-z0-9._+-]*)[[:space:]]*\$"
readonly USES_RE="^[[:space:]]*(-[[:space:]]+)?${KEY}(.*)\$"

pinned=0
rewritten=0
local_refs=0
unpinnable=0
mismatched=0

# process_file <file>
# Writes the settled form of <file> to ${tmpdir}/out and updates the counters.
process_file() {
  local file="$1" line prefix path old comment tag owner rest name sha ref
  local out="${tmpdir}/out"
  : >"${out}"

  while IFS= read -r line || [[ -n "${line}" ]]; do
    if [[ "${line}" =~ ${PIN_RE} ]]; then
      prefix="${BASH_REMATCH[1]}"
      path="${BASH_REMATCH[3]}"
      old="${BASH_REMATCH[4]}"
      comment="${BASH_REMATCH[5]}"
      tag="${BASH_REMATCH[6]}"

      owner="${path%%/*}"
      rest="${path#*/}"
      name="${owner}/${rest%%/*}"

      # A failure here must not abort the run through `set -e`: the summary and
      # the exit status below are the whole report.
      if ! sha="$(sha_for "${name}" "${tag}")"; then
        unpinnable=$((unpinnable + 1))
        printf '%s\n' "${line}" >>"${out}"
        continue
      fi
      pinned=$((pinned + 1))

      if [[ "${check_only}" -eq 1 ]]; then
        if [[ "${old}" != "${sha}" ]]; then
          printf 'FAIL %s: %s is pinned to %s but %s is %s\n' \
            "${file}" "${path}" "${old}" "${tag}" "${sha}" >&2
          mismatched=$((mismatched + 1))
        fi
        printf '%s\n' "${line}" >>"${out}"
        continue
      fi

      printf '%s%s@%s%s%s\n' \
        "${prefix}" "${path}" "${sha}" "${comment}" "${tag}" >>"${out}"
      continue
    fi

    if [[ "${line}" =~ ${USES_RE} ]]; then
      ref="${BASH_REMATCH[2]}"
      ref="${ref#"${ref%%[![:space:]]*}"}"
      if [[ "${ref}" == ./.github/actions/* ]]; then
        local_refs=$((local_refs + 1))
      else
        printf 'pin-actions: %s: cannot pin %s, it has no "# vX.Y.Z" comment\n' \
          "${file}" "${ref}" >&2
        unpinnable=$((unpinnable + 1))
      fi
    fi

    printf '%s\n' "${line}" >>"${out}"
  done <"${file}"
}

# Captured before the loop so a failing enumeration aborts here instead of
# leaving the loop with nothing to read and a clean exit.
files="$(repo_files '.github/workflows/*.yml' '.github/workflows/*.yaml')"

if [[ -z "${files}" ]]; then
  printf 'pin-actions: no workflow files under .github/workflows\n' >&2
  exit 1
fi

while IFS= read -r file; do
  [[ -n "${file}" ]] || continue
  process_file "${file}"
  if [[ "${check_only}" -eq 0 ]] && ! cmp -s "${tmpdir}/out" "${file}"; then
    cat "${tmpdir}/out" >"${file}"
    rewritten=$((rewritten + 1))
  fi
done <<<"${files}"

if [[ "${check_only}" -eq 1 ]]; then
  printf 'pin-actions --check: %d reference(s) verified, %d local, %d mismatch(es)\n' \
    "${pinned}" "${local_refs}" "${mismatched}"
else
  printf 'pin-actions: %d reference(s) pinned, %d file(s) rewritten, %d local\n' \
    "${pinned}" "${rewritten}" "${local_refs}"
fi

# Verifying nothing is not verifying. Same rule as check-actions-pinned.sh.
if (( pinned == 0 )); then
  printf 'pin-actions: resolved no action references\n' >&2
  exit 1
fi

# Silently leaving a reference unpinned would hand check-actions-pinned.sh a
# failure that this script was asked to prevent.
if (( unpinnable > 0 )); then
  printf 'pin-actions: %d reference(s) could not be pinned\n' "${unpinnable}" >&2
  exit 1
fi

if (( mismatched > 0 )); then
  printf 'pin-actions: %d SHA(s) do not match their version comment; run scripts/pin-actions.sh\n' \
    "${mismatched}" >&2
  exit 1
fi
