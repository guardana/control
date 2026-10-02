#!/usr/bin/env bash
# Refuses a commit on which CI or Security has no successful run, so the
# release workflow builds only what the gate passed. It asks GitHub, so it
# needs GH_TOKEN (or a gh login) with read access to the repository's
# Actions, and GITHUB_REPOSITORY as owner/name.
#
#   GITHUB_REPOSITORY=<owner>/<name> scripts/check-gate-runs.sh <commit sha>
set -euo pipefail

die() {
  printf 'check-gate-runs: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 1 && "$1" =~ ^[0-9a-f]{40}$ ]] || die "usage: scripts/check-gate-runs.sh <40-hex commit sha>"
sha="$1"
repo="${GITHUB_REPOSITORY:-}"
[[ "${repo}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "GITHUB_REPOSITORY must be owner/name, not '${repo}'"

for workflow in ci.yml security.yml; do
  count="$(gh api "repos/${repo}/actions/workflows/${workflow}/runs?head_sha=${sha}&status=success&per_page=1" --jq '.total_count')" ||
    die "could not ask GitHub for ${workflow}'s runs on ${sha}"
  [[ "${count}" =~ ^[0-9]+$ ]] || die "GitHub answered '${count}' for ${workflow}'s runs on ${sha}"
  ((count > 0)) || die "${workflow} has no successful run on ${sha}"
  printf 'check-gate-runs: %s passed on %s\n' "${workflow}" "${sha}"
done
