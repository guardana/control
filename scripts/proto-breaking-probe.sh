#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# The negative control of buf.yaml's breaking rules. buf.yaml's breaking
# section has to be exactly `use: [FILE]` and `ignore: [the observation
# package]`, with no other rule, exception or per-module override. Then, in
# scratch copies of buf.yaml and api/proto, one field of every file of the
# frozen v1 package is renumbered, one file per copy, and buf breaking against
# the unchanged copy has to fail naming that field; a copy that does not
# compile, or a failure that names something else, is not a detected break.
# A field of the unstable observation package, which buf.yaml ignores
# (ADR-0040), renumbered the same way has to pass. A buf that is missing, or a
# v1 file holding no field this script can find to renumber, fails it.
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

v1=guardana/control/v1
observe=guardana/control/observe/v1alpha1
moved=4000

want_breaking="breaking:
  use:
    - FILE
  ignore:
    - api/proto/${observe}"
# Comments and blank lines dropped, so only what buf reads is compared.
config="$(sed -e 's/[[:space:]]*#.*$//' -e '/^[[:space:]]*$/d' buf.yaml)"
[[ "$(grep -c '^breaking:' <<<"${config}")" == 1 ]] ||
  die "buf.yaml must hold exactly one top-level breaking section"
if grep -qE '^[[:space:]-]+breaking:' <<<"${config}"; then
  die "buf.yaml holds a nested breaking section, which overrides the top-level one for a module"
fi
got_breaking="$(awk '/^breaking:/ { on = 1; print; next } on && /^[^ ]/ { on = 0 } on' <<<"${config}")"
if [[ "${got_breaking}" != "${want_breaking}" ]]; then
  printf '%s\n' "${got_breaking}" >&2
  die "buf.yaml's breaking section is not exactly the FILE rule with the observation package ignored"
fi

work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT

mkdir -p "${work}/base"
cp buf.yaml "${work}/base/"
cp -R api "${work}/base/"

# first_field prints "<line> <message|enum> <name> <number>" for the first
# field of a top-level message, or the first non-zero value of a top-level
# enum, in proto file $1; nothing when it holds neither.
first_field() {
  awk '
    /^(message|enum) [A-Za-z_][A-Za-z0-9_]* *\{/ { kind = $1; name = $2; sub(/\{.*/, "", name); next }
    /^\}/ { kind = "" }
    kind == "message" && /^  (optional |repeated )?[A-Za-z_][A-Za-z0-9_.]* [a-z_][a-z0-9_]* = [0-9]+;/ {
      n = $0; sub(/^.*= /, "", n); sub(/;.*$/, "", n)
      print NR, kind, name, n; exit
    }
    kind == "enum" && /^  [A-Z_][A-Z0-9_]* = [1-9][0-9]*;/ {
      n = $0; sub(/^.*= /, "", n); sub(/;.*$/, "", n)
      print NR, kind, name, n; exit
    }
  ' "$1"
}

# renumbered copies the module into $1 and moves the field on line $3 of
# api/proto/$2 from number $4 to ${moved}.
renumbered() {
  local dir="$1" file="$2" line="$3" number="$4"
  mkdir -p "${dir}"
  cp buf.yaml "${dir}/"
  cp -R api "${dir}/"
  ! grep -qF "= ${moved};" "${dir}/api/proto/${file}" || die "${file} already uses number ${moved}"
  sed -i.orig "${line}s/= ${number};/= ${moved};/" "${dir}/api/proto/${file}"
  rm -f "${dir}/api/proto/${file}.orig"
  ! cmp -s "api/proto/${file}" "${dir}/api/proto/${file}" || die "${file} was not changed"
}

files=0
for path in "api/proto/${v1}"/*.proto; do
  [[ -f "${path}" ]] || die "no proto file under api/proto/${v1}"
  file="${path#api/proto/}"
  read -r line kind name number <<<"$(first_field "${path}")" || true
  [[ -n "${number:-}" ]] || die "${file} holds no message field or enum value to renumber"
  dir="${work}/v1-${files}"
  renumbered "${dir}" "${file}" "${line}" "${number}"
  if ! out="$(cd "${dir}" && buf build 2>&1)"; then
    printf '%s\n' "${out}" >&2
    die "${file} with ${kind} ${name} field ${number} renumbered does not compile; nothing was compared"
  fi
  rc=0
  out="$(cd "${dir}" && buf breaking --against "${work}/base" 2>&1)" || rc=$?
  if [[ ${rc} -eq 0 ]]; then
    die "${file}: ${kind} ${name} field ${number} renumbered passed buf breaking: the frozen package is not compared"
  fi
  if ! grep -E "^api/proto/${file}:" <<<"${out}" | grep -F "\"${number}\"" | grep -qF "${kind} \"${name}\""; then
    printf '%s\n' "${out}" >&2
    die "${file}: buf breaking failed (exit ${rc}) without naming ${kind} ${name} field ${number}; nothing shows the change was compared"
  fi
  files=$((files + 1))
  unset line kind name number
done

read -r line kind name number <<<"$(first_field "api/proto/${observe}/observation.proto")" || true
[[ -n "${number:-}" ]] || die "${observe}/observation.proto holds no field to renumber"
renumbered "${work}/observe" "${observe}/observation.proto" "${line}" "${number}"
if ! out="$(cd "${work}/observe" && buf build 2>&1 && buf breaking --against "${work}/base" 2>&1)"; then
  printf '%s\n' "${out}" >&2
  die "an observation field renumbered failed buf breaking: the unstable package is compared"
fi

printf 'proto-breaking-probe: v1 compared in %d file(s), observe/v1alpha1 ignored\n' "${files}"
