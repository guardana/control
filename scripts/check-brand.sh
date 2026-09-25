#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Fails when a brand literal appears in the content or the path of a file that
# is not on the allowlist. Code must reach the product name through the
# internal/brand package; the allowlist names the paths that
# scripts/rename-product.sh rewrites instead.
set -euo pipefail

_CHECK_BRAND_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_CHECK_BRAND_DIR}/lib/repo-files.sh"

ROOT="$(repo_root)"
BRAND_GO="${ROOT}/internal/brand/brand.go"
ALLOWLIST="${_CHECK_BRAND_DIR}/brand-allowlist.txt"

die() {
  printf 'check-brand: %s\n' "$*" >&2
  exit 1
}

# brand_const <name>
# Prints the string literal assigned to <name> in internal/brand/brand.go.
# An unparsable constant is fatal and never defaulted: a guessed brand would
# turn a broken source of truth into a silent pass.
brand_const() {
  local name="$1" value
  local re="s/^[[:space:]]*${name}[[:space:]]*=[[:space:]]*\"([^\"]*)\".*\$/\1/p"
  value="$(sed -n -E "${re}" "${BRAND_GO}")"
  value="${value%%$'\n'*}"
  [[ -n "${value}" ]] || die "cannot parse ${name} from ${BRAND_GO}"
  printf '%s\n' "${value}"
}

# ere_escape <string>
# Escapes extended-regex metacharacters. The dot carries the whole point here:
# unescaped, the dot in the OTel namespace also matches the hyphen in the slug.
# The set is ordered, not arbitrary: "]" must come first to stay literal, and
# "." must not follow "[" or the bracket opens a POSIX collating symbol.
ere_escape() {
  printf '%s' "$1" | sed -e 's/[]^$*+?(){}|.[\\]/\\&/g'
}

# allowlisted <relative path>
# Entry shapes are documented in brand-allowlist.txt. A bare name is an exact
# path, not a prefix: as a prefix, "README.md" also exempted "README.mdx.go".
allowlisted() {
  local rel="$1" entry mid
  for entry in "${entries[@]}"; do
    case "${entry}" in
      '**/'*/)
        mid="${entry#'**/'}"
        case "${rel}" in
          "${mid}"* | *"/${mid}"*) return 0 ;;
        esac
        ;;
      */)
        case "${rel}" in
          "${entry}"*) return 0 ;;
        esac
        ;;
      *)
        if [[ "${rel}" == "${entry}" ]]; then
          return 0
        fi
        ;;
    esac
  done
  return 1
}

[[ -f "${BRAND_GO}" ]] || die "missing ${BRAND_GO}"
[[ -f "${ALLOWLIST}" ]] || die "missing ${ALLOWLIST}"

name="$(brand_const Name)"
slug="$(brand_const Slug)"
gateway="$(brand_const Gateway)"
otel="$(brand_const OTelNamespace)"
env_prefix="$(brand_const EnvPrefix)"
# The trailing underscore is dropped so a full variable name is caught as well
# as the bare prefix.
env_stem="${env_prefix%_}"

# The module path and the image reference are not in the pattern: both are
# slashed forms that appear legitimately in every Go import line. They are
# still rewritten by the rename, they are just not something this guard can
# usefully forbid.
pattern="$(ere_escape "${name}")|$(ere_escape "${slug}")|$(ere_escape "${gateway}")"
pattern="${pattern}|$(ere_escape "${otel}")|$(ere_escape "${env_stem}")"

entries=()
while IFS= read -r line || [[ -n "${line}" ]]; do
  line="${line%%#*}"
  line="${line#"${line%%[![:space:]]*}"}"
  line="${line%"${line##*[![:space:]]}"}"
  [[ -n "${line}" ]] || continue
  entries+=("${line}")
done < "${ALLOWLIST}"
[[ ${#entries[@]} -gt 0 ]] || die "${ALLOWLIST} has no entries"

# Snapshot the listing before grepping so the scan cannot race a lane that is
# adding files to the tree at the same moment.
files="$(repo_files)"

listed=0
scanned=0
offending=0

while IFS= read -r rel; do
  [[ -n "${rel}" ]] || continue
  listed=$((listed + 1))
  if allowlisted "${rel}"; then
    continue
  fi
  scanned=$((scanned + 1))

  # A brand string in a path is invisible to a content grep and survives a
  # rename, so the path is checked too. rename-product.sh renames the directory
  # directly under cmd/, so that one segment may carry the brand; no other part
  # of a path may.
  probe="${rel}"
  case "${rel}" in
    cmd/*/*) probe="cmd/${rel#cmd/*/}" ;;
  esac
  if [[ "${probe}" =~ ${pattern} ]]; then
    offending=$((offending + 1))
    printf '%s\n' "${rel}" >&2
    printf '  brand string in the path\n' >&2
    continue
  fi

  # grep exits 2 on an unreadable file. Treating that as "no match" reports a
  # pass for a file nothing looked at.
  rc=0
  hits="$(grep -nE -- "${pattern}" "${ROOT}/${rel}" 2>&1)" || rc=$?
  case "${rc}" in
    0)
      offending=$((offending + 1))
      printf '%s\n' "${rel}" >&2
      printf '%s\n' "${hits}" | sed -e 's/^/  /' >&2
      ;;
    1) ;;
    *) die "grep failed on ${rel} (exit ${rc}): ${hits}" ;;
  esac
done <<< "${files}"

[[ ${listed} -gt 0 ]] || die "repo_files listed no files"
[[ ${scanned} -gt 0 ]] || die "scanned 0 files: the allowlist covers the whole tree"

if [[ ${offending} -gt 0 ]]; then
  printf 'check-brand: %d file(s) hard-code a brand string; use internal/brand instead\n' \
    "${offending}" >&2
  exit 1
fi

printf 'scanned %d files, content and path (%d listed, %d allowlisted)\n' \
  "${scanned}" "${listed}" "$((listed - scanned))"
printf 'brand ok\n'
