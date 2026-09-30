#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Runs the demo the way a release user does: from this machine's demo archive
# in a goreleaser dist/ directory, extracted outside any checkout, with no Go
# on PATH. It fails unless dist/ was built from the checkout's HEAD, the
# archive's gateway reports the version dist/ names, the archive holds every
# file of the demo the repository holds, call.sh is executable, and each
# scenario reports its own passed line on the archive's own binaries. It reads
# the archive goreleaser built, not a copy of its configuration, so what it
# proves is what a tag would publish.
#
#   scripts/check-demo-archive.sh dist
set -euo pipefail

_DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_DEMO_DIR}/lib/repo-files.sh"

die() {
  printf 'check-demo-archive: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 1 ]] || die "name the dist directory goreleaser wrote"
dist="$1"
[[ -d "${dist}" ]] || die "${dist} is not a directory"

case "$(uname -s)" in
  Linux) goos=linux ;;
  Darwin) goos=darwin ;;
  *) die "no demo archive is built for $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) goarch=amd64 ;;
  arm64 | aarch64) goarch=arm64 ;;
  *) die "no demo archive is built for $(uname -m)" ;;
esac

# metadata.json is one line of JSON goreleaser writes. member prints the
# string it gives a name, and refuses one absent, repeated or holding an
# escape rather than decoding it.
[[ -f "${dist}/metadata.json" ]] || die "${dist} holds no metadata.json to say what it was built from"
meta="$(cat "${dist}/metadata.json")"
member() {
  local key="\"$1\":" re
  re="${key}\"([^\"\\]*)\""
  [[ "${meta}" =~ ${re} ]] || die "${dist}/metadata.json names no $1 as a plain string"
  [[ "${meta#*"${key}"}" != *"${key}"* ]] || die "${dist}/metadata.json names $1 more than once"
  printf '%s' "${BASH_REMATCH[1]}"
}
built="$(member commit)" || exit 1
version="$(member version)" || exit 1
[[ "${built}" =~ ^[0-9a-f]{40}$ ]] || die "${dist}/metadata.json names commit ${built}, not a full commit id"
[[ "${version}" =~ ^[0-9A-Za-z][0-9A-Za-z.+_-]*$ ]] || die "${dist}/metadata.json names version ${version}, which is not one word"

root="$(repo_root)"
head="$(git -C "${root}" rev-parse --verify 'HEAD^{commit}')" || die "the checkout's HEAD could not be read"
[[ "${built}" == "${head}" ]] || die "${dist} was built from ${built}, and the checkout is at ${head}"

archives=()
for f in "${dist}"/guardana-control-demo_*_"${goos}_${goarch}".tar.gz; do
  [[ -f "${f}" ]] && archives+=("${f}")
done
[[ ${#archives[@]} -eq 1 ]] || die "want one demo archive for ${goos}/${goarch} in ${dist}, found ${#archives[@]}"
archive="${archives[0]}"

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
# -p keeps each entry's mode as the archive holds it, so the mode check below
# reads the archive and not this machine's umask.
tar -xpzf "${archive}" -C "${work}"

# The archive wraps its files in one directory named after it.
top="${work}/$(basename "${archive}" .tar.gz)"
[[ -d "${top}" ]] || die "$(basename "${archive}") does not hold its files under $(basename "${top}")/"

for bin in guardana-gateway guardana-control vulnerable-mcp-agent; do
  [[ -x "${top}/bin/${bin}" ]] || die "the archive holds no executable bin/${bin}"
done

# The runs below get a PATH with no Go on it and a home of their own, as a
# user's machine without a toolchain would.
path=/usr/bin:/bin
if env -i PATH="${path}" bash -c 'command -v go' >/dev/null 2>&1; then
  die "go is on ${path} here, so the run would not prove it needs none"
fi
mkdir -p "${work}/home"

# With no arguments the gateway prints its name, its version and the product's
# name in parentheses on its first line.
said="$(cd "${work}" && env -i PATH="${path}" HOME="${work}/home" "${top}/bin/guardana-gateway")" ||
  die "the archive's bin/guardana-gateway exited $? when asked its version"
said="${said%%$'\n'*}"
[[ "${said}" == "guardana-gateway ${version} ("*")" ]] ||
  die "the archive's bin/guardana-gateway says \"${said}\", not version ${version}"

# Every file of the demo the repository holds, but its Go sources, ships.
demo=examples/vulnerable-mcp-agent
files=0
while IFS= read -r rel; do
  [[ -n "${rel}" ]] || continue
  [[ "${rel}" == *.go ]] && continue
  files=$((files + 1))
  [[ -f "${top}/${rel}" ]] || die "the archive lacks ${rel}"
  cmp -s "${root}/${rel}" "${top}/${rel}" || die "the archive's ${rel} differs from the repository's"
done < <(cd "${root}" && repo_files "${demo}/*")
[[ ${files} -gt 0 ]] || die "found no file of ${demo} to look for; the listing stopped working"

# An exact mode, read the same way on BSD and GNU find.
[[ -n "$(find "${top}/${demo}/call.sh" -maxdepth 0 -perm 755)" ]] || die "${demo}/call.sh is not mode 0755"

scenarios=()
for s in "${top}/${demo}"/scenarios/*.json; do
  [[ -f "${s}" ]] && scenarios+=("${demo}/scenarios/$(basename "${s}")")
done
[[ ${#scenarios[@]} -gt 0 ]] || die "the archive holds no scenario"

args=()
for s in "${scenarios[@]}"; do
  args+=(--scenario "${s}")
done

out="${work}/run.txt"
rc=0
(cd "${top}" && env -i PATH="${path}" HOME="${work}/home" \
  bin/guardana-gateway dev --decision-point=silent --state "${work}/state" \
  --config "${demo}/demo.yaml" --policy "${demo}/policy.json" "${args[@]}") >"${out}" 2>&1 || rc=$?
# dev names each scenario by its file's base name, and a scenario that passed
# ends with one line saying so.
passed=0
for s in "${scenarios[@]}"; do
  n="$(grep -Fxc "$(basename "${s}") passed" "${out}" || true)"
  [[ "${n}" -eq 1 ]] && passed=$((passed + 1))
done
if [[ ${rc} -ne 0 || ${passed} -ne ${#scenarios[@]} ]]; then
  cat "${out}" >&2
  die "dev exited ${rc} with ${passed} of ${#scenarios[@]} scenario(s) reporting one passed line of their own"
fi
printf 'check-demo-archive: %s: built from %s, version %s, %d file(s) of the demo present, %d scenario(s) passed with no Go on PATH\n' \
  "$(basename "${archive}")" "${head}" "${version}" "${files}" "${passed}"
