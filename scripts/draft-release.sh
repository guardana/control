#!/usr/bin/env bash
# Makes the draft release the release workflow uploads into, before anything
# is built. goreleaser once uploaded into a draft it had created a moment
# earlier and the upload host answered 404, so the draft is made here, read
# back until the API serves it, and proven on the upload host with a probe
# asset that is removed again; goreleaser then takes the draft as its own
# (.goreleaser.yaml: use_existing_draft), finding it by its name, so <name>
# is what goreleaser's name_template renders. Run by the release workflow only.
#
#   scripts/draft-release.sh <tag> <commit> <name>
#
# Prints the draft's id. Any release for the tag that exists already, a
# published one or a draft a failed run left, refuses the script: the workflow
# publishes only the one draft for its tag, and a leftover is the maintainer's
# to delete (RELEASING.md).
set -euo pipefail

die() {
  printf 'draft-release: %s\n' "$*" >&2
  exit 1
}

# How often, and how long apart, each host is asked before the draft is given
# up: a minute in all, far past the lag seen so far.
tries=12
pause=5

[[ $# -eq 3 ]] || die "usage: scripts/draft-release.sh <tag> <commit> <name>"
tag="$1"
commit="$2"
name="$3"
[[ "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "${tag} is not a release tag"
[[ "${commit}" =~ ^[0-9a-f]{40}$ ]] || die "${commit} is not a commit"
[[ -n "${name}" && "${name}" != *$'\n'* ]] || die "the name is empty or holds a newline"
[[ -n "${GITHUB_REPOSITORY:-}" ]] || die "GITHUB_REPOSITORY is not set"
repo="${GITHUB_REPOSITORY}"
command -v jq >/dev/null 2>&1 || die "jq is not on PATH"

# A tag with a hyphen is a pre-release, as goreleaser's prerelease: auto reads it.
prerelease=false
[[ "${tag}" == *-* ]] && prerelease=true

existing="$(gh api "repos/${repo}/releases?per_page=100" --paginate |
  jq -r -s --arg tag "${tag}" '[.[][] | select(.tag_name == $tag) | "\(.id) (draft: \(.draft))"] | .[]')" ||
  die "the releases of ${repo} could not be listed"
[[ -z "${existing}" ]] ||
  die "a release for ${tag} exists already: ${existing//$'\n'/, }; a draft a failed run left is deleted by hand, and a published tag never moves"

id="$(gh api --method POST "repos/${repo}/releases" \
  -f "tag_name=${tag}" -f "target_commitish=${commit}" -f "name=${name}" \
  -F draft=true -F "prerelease=${prerelease}" --jq .id)" || die "the draft for ${tag} could not be made"
[[ "${id}" =~ ^[0-9]+$ ]] || die "the draft's id is not a number: ${id}"

served=false
for _ in $(seq "${tries}"); do
  answer="$(gh api "repos/${repo}/releases/${id}" --jq '"\(.tag_name) \(.draft)"' 2>/dev/null || true)"
  if [[ "${answer}" == "${tag} true" ]]; then
    served=true
    break
  fi
  sleep "${pause}"
done
[[ "${served}" == true ]] ||
  die "the API does not serve draft ${id} for ${tag}; delete it by hand and run the job again"

probe="$(mktemp)"
printf 'probe\n' >"${probe}"
asset=""
for _ in $(seq "${tries}"); do
  if asset="$(gh api --method POST "https://uploads.github.com/repos/${repo}/releases/${id}/assets?name=draft-probe.txt" \
    -H 'Content-Type: text/plain' --input "${probe}" --jq .id 2>/dev/null)"; then
    break
  fi
  asset=""
  sleep "${pause}"
done
rm -f "${probe}"
[[ "${asset}" =~ ^[0-9]+$ ]] ||
  die "the upload host does not serve draft ${id} for ${tag}; delete it by hand and run the job again"
gh api --method DELETE "repos/${repo}/releases/assets/${asset}" >/dev/null ||
  die "the probe asset ${asset} on draft ${id} could not be removed; remove it by hand, then delete the draft and run the job again"
left="$(gh api "repos/${repo}/releases/${id}/assets?per_page=100" --paginate --jq '.[].name' | paste -sd, -)" ||
  die "the assets of draft ${id} could not be listed"
[[ -z "${left}" ]] || die "draft ${id} holds assets before the build: ${left}"
printf '%s\n' "${id}"
