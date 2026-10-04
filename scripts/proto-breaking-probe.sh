#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# The negative control of buf.yaml's breaking rules. In two scratch copies of
# buf.yaml and api/proto it renumbers one field, and buf breaking between the
# copies has to fail for a field of the frozen v1 package and pass for one of
# the unstable observation package, which buf.yaml ignores (ADR-0040). A buf
# that is missing, or a field this script cannot find to renumber, fails it.
set -euo pipefail

_PROBE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_PROBE_DIR}/lib/repo-files.sh"

root="$(repo_root)"
cd "${root}"

die() {
  printf 'proto-breaking-probe: %s\n' "$*" >&2
  exit 1
}

command -v buf >/dev/null 2>&1 || die "buf is not on PATH; nothing was probed"

work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT

# renumbered copies the module into $1 and moves the field on line $3 of
# $2's file to number 99.
renumbered() {
  local dir="$1" file="$2" field="$3"
  mkdir -p "${dir}"
  cp buf.yaml "${dir}/"
  cp -R api "${dir}/"
  grep -qF "${field}" "${dir}/api/proto/${file}" || die "${file} holds no '${field}'"
  sed -i.orig "s/${field}/${field%= *}= 99;/" "${dir}/api/proto/${file}"
  rm -f "${dir}/api/proto/${file}.orig"
  ! cmp -s "api/proto/${file}" "${dir}/api/proto/${file}" || die "${file} was not changed"
}

mkdir -p "${work}/base"
cp buf.yaml "${work}/base/"
cp -R api "${work}/base/"

renumbered "${work}/v1" guardana/control/v1/action_envelope.proto "string request_id = 2;"
if (cd "${work}/v1" && buf breaking --against "${work}/base") >/dev/null 2>&1; then
  die "a v1 field renumbered passed buf breaking: the frozen package is not compared"
fi

renumbered "${work}/observe" guardana/control/observe/v1alpha1/observation.proto "string source_id = 1;"
if ! out="$(cd "${work}/observe" && buf breaking --against "${work}/base" 2>&1)"; then
  printf '%s\n' "${out}" >&2
  die "an observation field renumbered failed buf breaking: the unstable package is compared"
fi

printf 'proto-breaking-probe: v1 compared, observe/v1alpha1 ignored\n'
