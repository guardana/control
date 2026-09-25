#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Every workflow step must name a third-party action by commit SHA. A tag or a
# branch is mutable, so a green run of a tag-referenced action proves nothing
# about the code that executed.
#
# This is a shape check and says so: it cannot tell whether the SHA is the one
# the version comment names. `scripts/pin-actions.sh --check` answers that, and
# .github/workflows/workflow-lint.yml runs both.
set -euo pipefail

_CHECK_ACTIONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_CHECK_ACTIONS_DIR}/lib/repo-files.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

# What scripts/pin-actions.sh writes before it has resolved a tag. It is a
# well-formed 40-hex string, so the shape check alone would accept it.
readonly PLACEHOLDER='@0000000000000000000000000000000000000000'

# owner/repo, an optional subdirectory, a 40-hex commit, then the trailing
# "# vX.Y.Z" comment that says which release that commit is. This is the shape
# Dependabot reads and rewrites, which is the reason for it: a marker only this
# repository understands would leave the comment to go stale behind an
# automated SHA bump.
#
# Every path segment and the version start with an alphanumeric. That is not
# cosmetic: without it "..", "." and a leading "/" are all admitted, and the
# version comment is what scripts/pin-actions.sh feeds to a GitHub API path.
readonly SEG='[A-Za-z0-9][A-Za-z0-9_.-]*'
readonly VER='v?[0-9][A-Za-z0-9._+-]*'
readonly PINNED_RE="^${SEG}/${SEG}(/${SEG})*@[0-9a-f]{40}[[:space:]]+#[[:space:]]*${VER}[[:space:]]*\$"

# A composite action stored in this repository moves with the commit that uses
# it, so there is nothing to pin.
readonly LOCAL_RE='^\./\.github/actions/[A-Za-z0-9_./-]+[[:space:]]*$'

# A "uses" key that owns its physical line, list dash allowed, in the three
# spellings YAML reads as the same key: uses:, "uses": and 'uses':, with
# optional space before the colon. A key in flow style, a folded or escaped key
# and a key inside a multi-line scalar are not read here; they are reported as
# unreadable rather than skipped, and the workflow audit (zizmor's
# unpinned-uses) is the check that reads them. A red run of zizmor that comes
# from a crash or a parse failure is not a finding about pins; it is read as
# the audit not having run.
readonly KEY="[\"']?uses[\"']?[[:space:]]*:"
readonly BLOCK_RE="^[[:space:]]*(-[[:space:]]+)?${KEY}(.*)\$"
readonly ANY_USES_RE="(^|[[:space:]]|[{,])${KEY}"

# Captured before the loop so a failing enumeration aborts here instead of
# leaving the loop with nothing to read and a clean exit.
workflows="$(repo_files '.github/workflows/*.yml' '.github/workflows/*.yaml')"

if [[ -z "${workflows}" ]]; then
  printf 'check-actions: no workflow files under .github/workflows\n' >&2
  exit 1
fi

# Composite actions in this repository pin their own dependencies too. None
# exist yet, so this list is allowed to be empty; the workflow list is not.
# Statted first: git walks the work tree for untracked files and warns on
# stderr for a pathspec under a directory that does not exist. A gate that
# prints warnings nobody can act on teaches readers to skim the channel its
# real failures use.
local_actions=""
if [[ -d .github/actions ]]; then
  local_actions="$(repo_files '.github/actions/*.yml' '.github/actions/*.yaml')"
fi

files="${workflows}"
if [[ -n "${local_actions}" ]]; then
  files="${files}"$'\n'"${local_actions}"
fi

checked=0
local_refs=0
violations=0

while IFS= read -r file; do
  [[ -n "${file}" ]] || continue

  # grep exits 1 for "no match" and 2 or more for "could not read". Folding
  # those together with `|| true` would report an unreadable workflow as a
  # file with no action references, which is the false green this whole script
  # exists to prevent.
  hits="$(grep -nE "${ANY_USES_RE}" "${file}")" && status=0 || status=$?
  if (( status > 1 )); then
    printf 'FAIL %s: cannot read (grep exit %d)\n' "${file}" "${status}" >&2
    violations=$((violations + 1))
    continue
  fi

  while IFS= read -r hit; do
    [[ -n "${hit}" ]] || continue
    lineno="${hit%%:*}"
    line="${hit#*:}"
    checked=$((checked + 1))

    if [[ ! "${line}" =~ ${BLOCK_RE} ]]; then
      printf 'FAIL %s:%s "uses:" not in block form, cannot be verified: %s\n' \
        "${file}" "${lineno}" "${line# }" >&2
      violations=$((violations + 1))
      continue
    fi

    # The value and its comment, without the list dash and the key.
    ref="${BASH_REMATCH[2]}"
    ref="${ref#"${ref%%[![:space:]]*}"}"

    if [[ "${ref}" =~ ${LOCAL_RE} ]]; then
      local_refs=$((local_refs + 1))
      continue
    fi

    if [[ "${ref}" == *"${PLACEHOLDER}"* ]]; then
      printf 'FAIL %s:%s placeholder SHA, run scripts/pin-actions.sh\n' \
        "${file}" "${lineno}" >&2
      violations=$((violations + 1))
      continue
    fi

    if [[ ! "${ref}" =~ ${PINNED_RE} ]]; then
      printf 'FAIL %s:%s not owner/repo@<40 hex> with a "# vX.Y.Z" comment: %s\n' \
        "${file}" "${lineno}" "${ref}" >&2
      violations=$((violations + 1))
    fi
  done <<<"${hits}"
done <<<"${files}"

printf 'check-actions: %d reference(s) checked, %d local, %d violation(s)\n' \
  "${checked}" "${local_refs}" "${violations}"

# A workflow set with no "uses:" at all means the scan found nothing to judge.
# Reporting that as clean is the false green this check exists to prevent.
if (( checked == 0 )); then
  printf 'check-actions: examined no action references\n' >&2
  exit 1
fi

if (( violations > 0 )); then
  exit 1
fi
