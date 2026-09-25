#!/usr/bin/env bash
#
# Applies the GitHub configuration of guardana/control that ADR-0025 and
# GOVERNANCE.md describe: repository settings, security features, Actions
# permissions, the two teams, labels, milestones, the release environment and
# five rulesets. Every step is idempotent: a run sets what this file names, and
# replaces a ruleset or the environment's tag policy of the same name, but it
# leaves a label, a ruleset or a team it does not name as it found it. A change
# to the configuration starts here.
#
# Nothing calls this file. It is not part of `make quality` and no workflow
# runs it: it changes a public repository, so a maintainer with the admin role
# runs it by hand, after reading the diff to this file.
#
#   CONFIRM=i-understand scripts/github-bootstrap.sh
#
# gh must be signed in with the repo and admin:org scopes, because the teams
# belong to the organization. The repository must exist and hold main; this
# script creates neither.
set -euo pipefail

# Resolved from this file's location, so the script works from any directory.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"

OWNER="${OWNER:-guardana}"
REPO_NAME="${REPO_NAME:-control}"
REPO="${OWNER}/${REPO_NAME}"
DEFAULT_BRANCH="main"
# The admin both teams start with, as a team maintainer.
ADMIN_LOGIN="${ADMIN_LOGIN:-karauda}"

DESCRIPTION="An inline control and evidence layer for AI agents: it decides whether a proposed tool call may run, enforces the decision, and records what was decided and why."
TOPICS=(
  ai-agents
  agent-security
  ai-security
  mcp
  model-context-protocol
  mcp-gateway
  authorization
  policy-engine
  policy-as-code
  authzen
  human-in-the-loop
  opentelemetry
  audit-trail
  golang
)

# Status checks the main ruleset requires. A check-run context is a workflow
# job's `name` when it sets one and its job id otherwise.
#
# A required check has to report on every pull request or the merge waits for
# a run that never happens. Three contexts this repository produces are absent
# for that reason: `Supply-chain score`, whose workflow has no pull_request
# trigger; `actionlint, zizmor, pins`, whose pull_request trigger is filtered to
# .github/workflows/**; and the release workflow's jobs, which run on a tag.
REQUIRED_CHECKS=(
  "Quality gate"
  "Wire contract compatibility"
  "CodeQL"
  "Known Go vulnerabilities"
  "Dependency advisories"
  "Secret scan"
  "Dependency review"
  "Conventional title"
  "Sign-off"
)

# The GitHub Actions app. A required check names the app that must report it,
# so a check run of the same name from any other app does not satisfy it.
ACTIONS_APP_ID=15368

# The paths where GOVERNANCE.md asks for two independent approvals: the
# decision path, the enforcement pipeline, the approvals store, the digest and
# the keys, the evidence record, the wire contracts and their fixtures, and the
# release and workflow definitions with the tool digests the release job trusts.
# Each must be owned by
# @guardana/security-maintainers in .github/CODEOWNERS, so the team that has to
# approve is also the team a pull request asks.
TWO_APPROVAL_PATHS=(
  "internal/core/**"
  "internal/policy/**"
  "internal/canon/**"
  "internal/evidence/**"
  "internal/gateway/**"
  "internal/approvals/**"
  "internal/policykey/**"
  "pkg/contract/**"
  "pkg/policyprovider/**"
  "api/proto/**"
  "testdata/**"
  ".github/workflows/**"
  ".goreleaser.yaml"
  "scripts/tool-versions.env"
  "scripts/release-notes.sh"
)

# Repository roles as the rulesets API numbers them.
ROLE_MAINTAIN=2
ROLE_ADMIN=5

if [[ "${CONFIRM:-}" != "i-understand" ]]; then
  cat >&2 <<MSG
github-bootstrap: refusing to run.

This would change the settings, teams, environment and rulesets of the public
repository ${REPO}. Read the header of this file first. If that is what you
want:

  CONFIRM=i-understand scripts/github-bootstrap.sh

No GitHub call has been made.
MSG
  exit 2
fi

# Reads workflow YAML as this repository writes it rather than parsing YAML in
# general: indentation is the discriminator. Two spaces under `jobs:` is a job
# id, four spaces is a key of that job. A job's `name` wins when it has one,
# because that is the context GitHub reports; the id is used only when it does
# not. A `name` built from an expression is printed as written and will not
# match, which fails towards refusing rather than towards applying.
read -r -d '' CONTEXTS_AWK <<'AWK' || true
function flush() {
  if (job != "") {
    print (name != "" ? name : job)
    job = ""
    name = ""
  }
}
/^jobs:[[:space:]]*$/ { in_jobs = 1; next }
in_jobs == 0 { next }
/^[^[:space:]]/ { flush(); in_jobs = 0; next }
/^  [A-Za-z0-9_-]+:[[:space:]]*$/ {
  flush()
  job = $1
  sub(/:$/, "", job)
  next
}
/^    name:[[:space:]]/ {
  if (job != "" && name == "") {
    name = $0
    sub(/^[[:space:]]*name:[[:space:]]*/, "", name)
    sub(/[[:space:]]+$/, "", name)
    gsub(/^["']|["']$/, "", name)
  }
  next
}
END { flush() }
AWK

# workflow_contexts <dir>; one check-run context per job, one per line.
workflow_contexts() {
  local dir="$1" file
  for file in "${dir}"/*.yml "${dir}"/*.yaml; do
    [[ -f "${file}" ]] || continue
    awk "${CONTEXTS_AWK}" "${file}"
  done
}

# A required check that no job reports blocks every merge to main until an
# admin removes the rule. So the list is checked against the workflow
# definitions in this tree before anything is changed. If it is wrong,
# reconcile the two lists; do not delete the check.
require_contexts_exist() {
  local dir="$1" available missing=0 context
  if [[ ! -d "${dir}" ]]; then
    printf 'github-bootstrap: no workflow directory at %s\n' "${dir}" >&2
    exit 1
  fi
  available="$(workflow_contexts "${dir}")"
  if [[ -z "${available}" ]]; then
    printf 'github-bootstrap: found no workflow jobs in %s\n' "${dir}" >&2
    exit 1
  fi
  for context in "${REQUIRED_CHECKS[@]}"; do
    if ! grep -qxF -- "${context}" <<<"${available}"; then
      printf 'github-bootstrap: no job reports the status check %s\n' "${context}" >&2
      missing=$((missing + 1))
    fi
  done
  if ((missing > 0)); then
    printf 'github-bootstrap: the contexts these workflows do report are:\n' >&2
    printf '%s\n' "${available}" | sed 's/^/  /' >&2
    printf 'github-bootstrap: refusing to require a check nothing reports.\n' >&2
    exit 1
  fi
  printf 'preflight: every required status check is reported by a job in %s\n' "${dir}"
}

# require_security_owned; every TWO_APPROVAL_PATHS entry lies under a path
# .github/CODEOWNERS gives to @guardana/security-maintainers.
require_security_owned() {
  local owned path entry covered missing=0
  owned="$(awk '$1 !~ /^#/ && $2 == "@guardana/security-maintainers" {
    p = $1; sub(/^\//, "", p); sub(/\/$/, "", p); print p }' "${REPO_ROOT}/.github/CODEOWNERS")"
  for path in "${TWO_APPROVAL_PATHS[@]}"; do
    path="${path%/\*\*}"
    covered=0
    while IFS= read -r entry; do
      [[ -n "${entry}" ]] || continue
      if [[ "${path}" == "${entry}" || "${path}" == "${entry}/"* ]]; then
        covered=1
      fi
    done <<<"${owned}"
    if ((covered == 0)); then
      printf 'github-bootstrap: CODEOWNERS gives %s to no security maintainer\n' "${path}" >&2
      missing=$((missing + 1))
    fi
  done
  if ((missing > 0)); then
    exit 1
  fi
  printf 'preflight: every two-approval path is owned by @guardana/security-maintainers\n'
}

# json_strings <value>...; prints a JSON array of strings. A value holding a
# quote, a backslash or a control character is refused rather than escaped:
# every value here is a name this file controls.
json_strings() {
  local separator="" value
  printf '['
  for value in "$@"; do
    case "${value}" in
      *[\"\\]* | *[[:cntrl:]]*)
        printf 'github-bootstrap: refusing to encode %s\n' "${value}" >&2
        return 1
        ;;
    esac
    printf '%s"%s"' "${separator}" "${value}"
    separator=", "
  done
  printf ']'
}

# checks_json; REQUIRED_CHECKS as the ruleset's required_status_checks array,
# each bound to the Actions app, so the list checked above and the list applied
# cannot drift apart.
checks_json() {
  local separator="" context
  printf '['
  for context in "${REQUIRED_CHECKS[@]}"; do
    printf '%s{ "context": "%s", "integration_id": %d }' \
      "${separator}" "${context}" "${ACTIONS_APP_ID}"
    separator=", "
  done
  printf ']'
}

require_contexts_exist "${REPO_ROOT}/.github/workflows"
require_security_owned

# Every JSON fragment is built here, by assignment, so a refusal stops the run.
# Inside a here-document a failed substitution would be silently empty.
TOPICS_JSON="$(json_strings "${TOPICS[@]}")"
SECURITY_PATTERNS_JSON="$(json_strings "${TWO_APPROVAL_PATHS[@]}")"
CHECKS_JSON="$(checks_json)"
DESCRIPTION_JSON="$(json_strings "${DESCRIPTION}")"
DESCRIPTION_JSON="${DESCRIPTION_JSON#[}"
DESCRIPTION_JSON="${DESCRIPTION_JSON%]}"

command -v gh >/dev/null 2>&1 || {
  printf 'github-bootstrap: gh is not on PATH\n' >&2
  exit 1
}

# The scopes of the signed-in token, from the header GitHub answers with.
scopes="$(gh api -i user | tr -d '\r' | sed -n 's/^[Xx]-[Oo][Aa]uth-[Ss]copes:[[:space:]]*//p')"
for scope in repo admin:org; do
  if ! tr ',' '\n' <<<"${scopes}" | sed 's/^[[:space:]]*//' | grep -qxF "${scope}"; then
    printf 'github-bootstrap: the gh token lacks the %s scope; run: gh auth refresh -h github.com -s %s\n' \
      "${scope}" "${scope}" >&2
    exit 1
  fi
done

if ! gh repo view "${REPO}" >/dev/null 2>&1; then
  printf 'github-bootstrap: %s does not exist; this script configures it and never creates it\n' "${REPO}" >&2
  exit 1
fi
if ! gh api "repos/${REPO}/branches/${DEFAULT_BRANCH}" >/dev/null 2>&1; then
  printf 'github-bootstrap: %s has no %s branch; push the history first\n' "${REPO}" "${DEFAULT_BRANCH}" >&2
  exit 1
fi

# Every call is echoed before it is made, so the transcript shows exactly what
# was sent rather than what the script meant to send.
run() {
  local arg
  # %q so the echoed line is the line that ran, quoting included.
  printf '+'
  for arg in "$@"; do printf ' %q' "${arg}"; done
  printf '\n'
  "$@"
}

# run_quiet <command>...; as run, with the command's own output discarded,
# for calls whose answer is a JSON document nobody reads.
run_quiet() {
  local arg
  printf '+'
  for arg in "$@"; do printf ' %q' "${arg}"; done
  printf '\n'
  "$@" >/dev/null
}

# run_json <method> <endpoint>; reads the request body on stdin and prints it
# with the call, because the body is where the setting actually lives.
run_json() {
  local method="$1" endpoint="$2" body
  body="$(cat)"
  printf '+ gh api --method %s %s --input - <<JSON\n%s\nJSON\n' \
    "${method}" "${endpoint}" "${body}"
  printf '%s' "${body}" | gh api --method "${method}" "${endpoint}" --input - >/dev/null
}

run gh auth status

# ensure_team <slug> <repository permission> <description>
ensure_team() {
  local slug="$1" permission="$2" description="$3"
  if gh api "orgs/${OWNER}/teams/${slug}" >/dev/null 2>&1; then
    printf 'team %s already exists\n' "${slug}"
  else
    run_quiet gh api --method POST "orgs/${OWNER}/teams" \
      -f "name=${slug}" -f "description=${description}" -f privacy=closed
  fi
  run_quiet gh api --method PUT "orgs/${OWNER}/teams/${slug}/memberships/${ADMIN_LOGIN}" \
    -f role=maintainer
  run_quiet gh api --method PUT "orgs/${OWNER}/teams/${slug}/repos/${REPO}" \
    -f "permission=${permission}"
}

ensure_team maintainers maintain \
  "Maintainers of guardana/control: merge reviewed pull requests and cut releases; see GOVERNANCE.md."
# Write access, because a code owner without it is ignored.
ensure_team security-maintainers push \
  "Code owners of the paths in guardana/control that carry authorization meaning."
SECURITY_TEAM_ID="$(gh api "orgs/${OWNER}/teams/security-maintainers" --jq .id)"
if [[ ! "${SECURITY_TEAM_ID}" =~ ^[0-9]+$ ]]; then
  printf 'github-bootstrap: no numeric id for the security-maintainers team\n' >&2
  exit 1
fi

# apply_ruleset <name>; reads the ruleset on stdin and creates it, or replaces
# the repository's ruleset of that name, so a re-run converges on this file.
apply_ruleset() {
  local name="$1" id
  id="$(gh api "repos/${REPO}/rulesets?includes_parents=false" --paginate \
    --jq ".[] | select(.name == \"${name}\") | .id" </dev/null)"
  if [[ -z "${id}" ]]; then
    run_json POST "repos/${REPO}/rulesets"
  elif [[ "${id}" =~ ^[0-9]+$ ]]; then
    run_json PUT "repos/${REPO}/rulesets/${id}"
  else
    printf 'github-bootstrap: more than one ruleset is named %s\n' "${name}" >&2
    exit 1
  fi
}

# Rulesets come first after the teams, the simple ones before the one that
# uses the newest rule types, so a setting GitHub refuses further down never
# leaves main or a release tag unprotected.
# No role bypasses this one: nobody deletes main, force-pushes it or merges
# into it with a merge commit.
apply_ruleset "main: history" <<'JSON'
{
  "name": "main: history",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "bypass_actors": [],
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "required_linear_history" }
  ]
}
JSON

# Only the admin and the maintain role create a release tag.
apply_ruleset "release tags: create" <<JSON
{
  "name": "release tags: create",
  "target": "tag",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "bypass_actors": [
    { "actor_id": ${ROLE_ADMIN}, "actor_type": "RepositoryRole", "bypass_mode": "always" },
    { "actor_id": ${ROLE_MAINTAIN}, "actor_type": "RepositoryRole", "bypass_mode": "always" }
  ],
  "rules": [
    { "type": "creation" }
  ]
}
JSON

# No role moves or deletes a release tag once it exists.
apply_ruleset "release tags: immutable" <<'JSON'
{
  "name": "release tags: immutable",
  "target": "tag",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "bypass_actors": [],
  "rules": [
    { "type": "update", "parameters": { "update_allows_fetch_and_merge": false } },
    { "type": "deletion" },
    { "type": "non_fast_forward" }
  ]
}
JSON

# Only the admin and the maintain role update main; the maintain role only by
# merging a pull request. A collaborator with write access cannot merge.
apply_ruleset "main: merges" <<JSON
{
  "name": "main: merges",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "bypass_actors": [
    { "actor_id": ${ROLE_ADMIN}, "actor_type": "RepositoryRole", "bypass_mode": "always" },
    { "actor_id": ${ROLE_MAINTAIN}, "actor_type": "RepositoryRole", "bypass_mode": "pull_request" }
  ],
  "rules": [
    { "type": "update", "parameters": { "update_allows_fetch_and_merge": false } }
  ]
}
JSON

# A change reaches main by squash-merged pull request, reviewed and green. The
# admin bypasses this ruleset, and only this one, to push directly.
apply_ruleset "main: pull requests" <<JSON
{
  "name": "main: pull requests",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "bypass_actors": [
    { "actor_id": ${ROLE_ADMIN}, "actor_type": "RepositoryRole", "bypass_mode": "always" }
  ],
  "rules": [
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": true,
        "require_last_push_approval": true,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["squash"],
        "required_reviewers": [
          {
            "minimum_approvals": 2,
            "file_patterns": ${SECURITY_PATTERNS_JSON},
            "reviewer": { "id": ${SECURITY_TEAM_ID}, "type": "Team" }
          }
        ]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "do_not_enforce_on_create": false,
        "required_status_checks": ${CHECKS_JSON}
      }
    },
    {
      "type": "code_scanning",
      "parameters": {
        "code_scanning_tools": [
          { "tool": "CodeQL", "security_alerts_threshold": "high_or_higher", "alerts_threshold": "errors" }
        ]
      }
    }
  ]
}
JSON

# The release job deploys to this environment, and only a v* tag may.
run_json PUT "repos/${REPO}/environments/release" <<'JSON'
{ "deployment_branch_policy": { "protected_branches": false, "custom_branch_policies": true } }
JSON
# Any other policy on the environment would let another ref deploy to it, so
# every policy but the one for v* tags is removed.
# Read by assignment, so a failed listing stops the run instead of leaving a
# stray policy in place behind a report of success.
policies="$(gh api "repos/${REPO}/environments/release/deployment-branch-policies" --paginate \
  --jq '.branch_policies[] | "\(.id) \(.type) \(.name)"' </dev/null)"
have_tag_policy=0
while read -r policy_id policy_type policy_name; do
  [[ -n "${policy_id}" ]] || continue
  if [[ "${policy_type}" == "tag" && "${policy_name}" == 'v*' ]]; then
    have_tag_policy=1
  else
    run_quiet gh api --method DELETE \
      "repos/${REPO}/environments/release/deployment-branch-policies/${policy_id}" </dev/null
  fi
done <<<"${policies}"
if ((have_tag_policy == 1)); then
  printf 'environment release already takes v* tags\n'
else
  run_json POST "repos/${REPO}/environments/release/deployment-branch-policies" <<'JSON'
{ "name": "v*", "type": "tag" }
JSON
fi

run_json PATCH "repos/${REPO}" <<JSON
{
  "description": ${DESCRIPTION_JSON},
  "homepage": "",
  "default_branch": "${DEFAULT_BRANCH}",
  "has_issues": true,
  "has_wiki": false,
  "has_projects": false,
  "has_discussions": false,
  "allow_squash_merge": true,
  "allow_merge_commit": false,
  "allow_rebase_merge": false,
  "squash_merge_commit_title": "PR_TITLE",
  "squash_merge_commit_message": "COMMIT_MESSAGES",
  "delete_branch_on_merge": true,
  "allow_update_branch": true,
  "allow_auto_merge": false,
  "web_commit_signoff_required": true
}
JSON

run_json PUT "repos/${REPO}/topics" <<JSON
{ "names": ${TOPICS_JSON} }
JSON

# The security features come from an organization configuration attached to
# this repository alone, enforced so they cannot drift at the repository. A new
# public repository has no dependency graph, and without it the required
# "Dependency review" check cannot run. Code scanning's default setup stays
# off: security.yml runs CodeQL itself, and GitHub refuses the results of one
# while the other is on.
read -r -d '' SECURITY_CONFIGURATION <<JSON || true
{
  "name": "${REPO_NAME}",
  "description": "The security settings of ${REPO}, applied by its scripts/github-bootstrap.sh.",
  "dependency_graph": "enabled",
  "dependabot_alerts": "enabled",
  "dependabot_security_updates": "enabled",
  "code_scanning_default_setup": "disabled",
  "secret_scanning": "enabled",
  "secret_scanning_push_protection": "enabled",
  "private_vulnerability_reporting": "enabled",
  "enforcement": "enforced"
}
JSON
configuration_id="$(gh api "orgs/${OWNER}/code-security/configurations" --paginate \
  --jq ".[] | select(.name == \"${REPO_NAME}\") | .id" </dev/null)"
if [[ -z "${configuration_id}" ]]; then
  printf '+ gh api --method POST orgs/%s/code-security/configurations --input - <<JSON\n%s\nJSON\n' \
    "${OWNER}" "${SECURITY_CONFIGURATION}"
  configuration_id="$(printf '%s' "${SECURITY_CONFIGURATION}" |
    gh api --method POST "orgs/${OWNER}/code-security/configurations" --input - --jq .id)"
else
  printf '%s' "${SECURITY_CONFIGURATION}" |
    run_json PATCH "orgs/${OWNER}/code-security/configurations/${configuration_id}"
fi
if [[ ! "${configuration_id}" =~ ^[0-9]+$ ]]; then
  printf 'github-bootstrap: no numeric id for the security configuration\n' >&2
  exit 1
fi
REPO_ID="$(gh api "repos/${REPO}" --jq .id </dev/null)"
if [[ ! "${REPO_ID}" =~ ^[0-9]+$ ]]; then
  printf 'github-bootstrap: no numeric id for %s\n' "${REPO}" >&2
  exit 1
fi
run_json POST "orgs/${OWNER}/code-security/configurations/${configuration_id}/attach" <<JSON
{ "scope": "selected", "selected_repository_ids": [${REPO_ID}] }
JSON

# A published release keeps its tag and its assets as they were published.
run gh api --method PUT "repos/${REPO}/immutable-releases"

# Every action must be pinned to a commit, as scripts/check-actions-pinned.sh
# already requires of this tree. A workflow that needs to write asks for it in
# its own job. A first-time contributor's pull request runs its workflows only
# after a maintainer approves the run.
run_json PUT "repos/${REPO}/actions/permissions" <<'JSON'
{ "enabled": true, "allowed_actions": "all", "sha_pinning_required": true }
JSON
run_json PUT "repos/${REPO}/actions/permissions/workflow" <<'JSON'
{ "default_workflow_permissions": "read", "can_approve_pull_request_reviews": false }
JSON
run_json PUT "repos/${REPO}/actions/permissions/fork-pr-contributor-approval" <<'JSON'
{ "approval_policy": "first_time_contributors" }
JSON

# --force updates a label that already exists instead of failing, so this is
# safe to re-run after the label set changes.
while IFS='|' read -r name color description; do
  [[ -n "${name}" ]] || continue
  run gh label create "${name}" --repo "${REPO}" \
    --color "${color}" --description "${description}" --force </dev/null
done <<'LABELS'
area:core|1d76db|The decision path
area:policy|1d76db|Policy model, matcher, external PDP
area:mcp|1d76db|Model Context Protocol adapter and gateway
area:a2a|1d76db|Agent-to-agent handoff and delegation
area:otel|1d76db|OpenTelemetry export and evidence transport
area:ui|1d76db|Web interface
area:detector|1d76db|Detectors and their fixtures
area:docs|1d76db|Documentation and decision records
area:release|1d76db|Release pipeline, signing and provenance
kind:bug|d73a4a|Behaves differently from what is documented
kind:feature|0e8a16|A problem the project does not solve yet
kind:security-hardening|b60205|Reduces attack surface; not a reported vulnerability
kind:design|5319e7|Needs a design agreed before code
good-first-issue|7057ff|Small, self-contained, well specified
help-wanted|008672|Maintainers would welcome a contributor here
needs-repro|fbca04|Waiting on a reproduction
needs-adr|fbca04|Waiting on an architecture decision record
blocked|e11d21|Waiting on something outside this issue
breaking-change|b60205|Changes a public contract or a verdict's meaning
security-sensitive|b60205|Touches authorization, identity, approvals or release
LABELS

# Named after the first three outcomes in ROADMAP.md. That page has no phase
# numbers and no dates, so neither do these.
existing_milestones="$(gh api "repos/${REPO}/milestones?state=all" --paginate --jq '.[].title')"
while IFS= read -r milestone; do
  [[ -n "${milestone}" ]] || continue
  if grep -qxF -- "${milestone}" <<<"${existing_milestones}"; then
    printf 'milestone %s already exists\n' "${milestone}"
  else
    run_quiet gh api --method POST "repos/${REPO}/milestones" -f "title=${milestone}" </dev/null
  fi
done <<'MILESTONES'
Decision semantics, proven in isolation
First enforceable integration
Evidence an operator can read
MILESTONES

printf 'github-bootstrap: finished for %s\n' "${REPO}"
