#!/usr/bin/env bash
# Refuses a commit on which CI or Security has no successful push run on main,
# so the release workflow builds only what the gate passed on main. Both
# workflows also run on pull requests, whose runs carry the pull request's
# head commit: a green pull request run says nothing about the push run on
# main, so the script counts only a push run on main of exactly this commit
# whose conclusion is success. A push of a tag named main reports main as its
# branch too, for any commit, so the commit must also be main's head or an
# ancestor of it. A run made green by a re-run counts, because GitHub reports
# a run's latest attempt. It asks GitHub, so it needs GH_TOKEN (or a gh login)
# with read access to the repository's contents and Actions, and
# GITHUB_REPOSITORY as owner/name.
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

# The branch is resolved by its full ref, because a bare "main" in a
# comparison may resolve to a tag of that name.
main="$(gh api "repos/${repo}/git/ref/heads/main" \
  --jq 'if .ref != "refs/heads/main" then error("the answer names no refs/heads/main") else .object.sha end')" ||
  die "could not read the head of main from GitHub"
[[ "${main}" =~ ^[0-9a-f]{40}$ ]] || die "GitHub answered '${main}' for the head of main"

status="$(gh api "repos/${repo}/compare/${main}...${sha}" \
  --jq 'if (.status | type) != "string" then error("the answer holds no status") else .status end')" ||
  die "could not compare ${sha} with main's head ${main} on GitHub"
case "${status}" in
  identical | behind) ;;
  *) die "${sha} is not on main: compared with main's head ${main}, GitHub says '${status}'" ;;
esac

# The first line is the total GitHub counts and the number of runs it answered
# with; then one line per run: head_sha, event, head_branch, conclusion, with an
# absent field as "none" so that read cannot shift the columns. An answer
# without a workflow_runs array or a total_count is an error, never an empty list.
filter='if (.workflow_runs | type) != "array" then error("the answer holds no workflow_runs array")
  elif (.total_count | type) != "number" then error("the answer holds no total_count")
  else ([.total_count, (.workflow_runs | length)] | @tsv),
    (.workflow_runs[] | [.head_sha, .event, .head_branch, .conclusion] | map(if . == null or . == "" then "none" else tostring end) | @tsv)
  end'

for workflow in ci.yml security.yml; do
  runs="$(gh api "repos/${repo}/actions/workflows/${workflow}/runs?head_sha=${sha}&event=push&branch=main&per_page=100" --jq "${filter}")" ||
    die "could not read ${workflow}'s runs on ${sha} from GitHub"

  passed=0
  seen=""
  others=0
  {
    IFS=$'\t' read -r total answered || true
    [[ "${total:-}" =~ ^[0-9]+$ && "${answered:-}" =~ ^[0-9]+$ ]] ||
      die "GitHub answered '${total:-} ${answered:-}' for the number of ${workflow}'s runs on ${sha}"
    ((total <= answered)) ||
      die "${workflow}: GitHub counts ${total} run(s) of ${sha} and answered ${answered}; more runs than one page; refusing rather than guessing"
    while IFS=$'\t' read -r run_sha event branch conclusion; do
      [[ -n "${run_sha}" ]] || continue
      if [[ "${run_sha}" != "${sha}" ]]; then
        others=$((others + 1))
        continue
      fi
      if [[ "${event}" == push && "${branch}" == main && "${conclusion}" == success ]]; then
        passed=1
      fi
      seen+="${seen:+; }a ${event} run on ${branch} (conclusion ${conclusion})"
    done
  } <<<"${runs}"

  if ((passed == 0)); then
    [[ -n "${seen}" ]] || seen="no run of this commit"
    ((others == 0)) || seen+="; ${others} run(s) of other commits"
    die "${workflow} has no successful push run on main for ${sha}; GitHub listed: ${seen}"
  fi
  printf 'check-gate-runs: %s passed on %s (a successful push run on main)\n' "${workflow}" "${sha}"
done
