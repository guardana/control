#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Prints the body of CHANGELOG.md's section for a release tag: for v1.2.3-rc.1
# everything under "## [1.2.3-rc.1] - YYYY-MM-DD" up to the next "## [" heading,
# without the heading itself. The release workflow publishes this text as the
# release notes, so a tag with no dated, non-empty section fails the release
# instead of publishing empty or wrong notes.
#
#   scripts/release-notes.sh v0.1.0-alpha
set -euo pipefail

_RELEASE_NOTES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_RELEASE_NOTES_DIR}/lib/repo-files.sh"

die() {
  printf 'release-notes: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 1 ]] || die "usage: scripts/release-notes.sh v<semver>"
tag="$1"

# Semantic versioning 2.0.0 with a leading "v". The version lands in a regular
# expression below, so it is checked before it is used, not escaped.
semver_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
[[ "${tag}" =~ ${semver_re} ]] || die "${tag} is not v followed by a semantic version"
version="${tag#v}"

root="$(repo_root)"
changelog="${root}/CHANGELOG.md"
[[ -f "${changelog}" ]] || die "no CHANGELOG.md at ${root}"

heading=""
body=""
in_section=0
while IFS= read -r line || [[ -n "${line}" ]]; do
  # Keep a Changelog ends the file with link definitions for every version
  # heading; they belong to the file, not to the last section above them. A
  # link definition of any other label is part of a section's text.
  if [[ ${in_section} -eq 1 && "${line}" =~ ^\[(Unreleased|[0-9]+\.[0-9]+\.[0-9]+[^]]*)\]:[[:space:]] ]]; then
    break
  fi
  if [[ "${line}" == "## ["* ]]; then
    if [[ ${in_section} -eq 1 ]]; then
      break
    fi
    if [[ "${line}" == "## [${version}]"* ]]; then
      heading="${line}"
      in_section=1
    fi
    continue
  fi
  if [[ ${in_section} -eq 1 ]]; then
    body="${body}${line}"$'\n'
  fi
done <"${changelog}"

[[ -n "${heading}" ]] || die "CHANGELOG.md has no section ## [${version}]"
# The escaped dots keep 1.2.3 from matching a heading for 1x2y3.
version_re="${version//./\\.}"
version_re="${version_re//+/\\+}"
[[ "${heading}" =~ ^\#\#\ \[${version_re}\]\ -\ [0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] ||
  die "the section for ${version} has no date: ${heading}"

# Blank lines around the body are layout, not notes.
body="$(printf '%s' "${body}" | sed -e '/./,$!d')"
[[ -n "${body//[[:space:]]/}" ]] || die "the section for ${version} is empty"

# On GitHub the notes are shown on the release page, where a relative link
# resolves against the page's address, so a link into the tree is made
# absolute at the tag. A link with a scheme or an anchor holds a ':' or starts
# with '#', and is left as it is.
if [[ -n "${GITHUB_REPOSITORY:-}" ]]; then
  [[ "${GITHUB_REPOSITORY}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] ||
    die "not a repository name: ${GITHUB_REPOSITORY}"
  base="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/blob/${tag}/"
  [[ "${base}" =~ ^https://[A-Za-z0-9.-]+/ ]] || die "not a server address: ${base}"
  body="$(printf '%s\n' "${body}" | sed -E "s#\]\(([A-Za-z0-9._][^):]*)\)#](${base}\1)#g")"
fi

printf '%s\n' "${body}"
