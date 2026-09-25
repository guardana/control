#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# The dependency rule's negative control. In a copy of the files the repository
# holds, it plants the fixture from internal/core/testdata/dependency-probe and
# requires each mechanism to refuse it for the reasons that mechanism answers
# for:
#   check-imports.sh, layering_test.go  every planted import, os/exec reached
#                                       through a helper package, and each file
#                                       a guarded package holds that is not
#                                       plain Go built on this platform
#   layering_test.go                    each //nolint that excuses a guarded
#                                       file from depguard or forbidigo
#   golangci-lint, depguard             every planted import, and net/http in
#                                       a planted test file
#   golangci-lint, forbidigo            each clock read, I/O function and draw
#                                       on the system's randomness
#   go mod tidy -diff                   a module imported with no direct
#                                       requirement in go.mod
#
# The lint fixture goes into every package of every guarded tree, its package
# clause rewritten, and into one new package per tree, which also takes the
# platform fixture. An exclusion that spares a path, a file name or generated
# code therefore loses a refusal wherever it lands. golangci-lint runs with the
# configuration `make lint` names and every linter that configuration enables,
# so what the probe proves is what `make lint` runs.
#
# A mechanism that fails without naming the reason, because the probe stopped
# compiling for instance, has refused nothing, and the probe fails.
set -euo pipefail

# source= resolves against this script's directory through line 2.
_PROBE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_PROBE_DIR}/lib/repo-files.sh"
# shellcheck source=lib/dependency-rule.sh
. "${_PROBE_DIR}/lib/dependency-rule.sh"

die() {
  printf 'check-imports-probe: %s\n' "$*" >&2
  exit 1
}

root="$(repo_root)"
fixture="${root}/internal/core/testdata/dependency-probe"
[[ -d "${fixture}" ]] || die "missing fixture directory ${fixture}"
for tool in go golangci-lint; do
  command -v "${tool}" >/dev/null 2>&1 || die "${tool} is not on PATH, so the probe cannot run"
done

copy="$(mktemp -d)"
trap 'rm -rf "${copy}"' EXIT
staged="$(repo_stage "${copy}")"

module="$(cd "${copy}" && GOWORK=off go list -m)"
adapter="${module}/adapters/probeadapter"
helper="${module}/internal/probehelper"
not_allowed="which the dependency rule does not allow"
left_out="which build constraints leave out on this platform"

# The packages of the guarded trees as the repository holds them, listed before
# anything is planted, one "<directory> <package name>" line each; then the new
# package the probe adds to every tree. A guarded tree the staged copy does not
# hold is a failure, not a place to plant: creating it here would report every
# expected refusal for a tree the rule no longer examines.
targets=""
for tree in "${guarded[@]}"; do
  [[ -d "${copy}/${tree}" ]] || die "guarded tree ${tree} is not in the staged copy; the probe does not create it"
  listing="$(cd "${copy}" && go list -f '{{.ImportPath}} {{.Name}}' "./${tree}/...")" ||
    die "go list ./${tree}/... failed in the copy"
  [[ -n "${listing}" ]] || die "guarded tree ${tree} holds no package in the staged copy"
  while read -r path name; do
    [[ -n "${path}" ]] || continue
    [[ "${path}" == "${module}/"* ]] || die "go list named ${path}, which is not a package of ${module}"
    targets+="${path#"${module}/"} ${name}"$'\n'
  done <<<"${listing}"
  targets+="${tree}/ioprobe ioprobe"$'\n'
done

# plant <fixture subdirectory> <directory in the copy> <package name>
# Copies every file of the fixture subdirectory, the package clause of each Go
# file rewritten to the package it joins.
plant() {
  local file
  [[ "$3" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "the package in $2 is named '$3', which is not an identifier"
  mkdir -p "${copy}/$2"
  for file in "${fixture}/$1"/*; do
    sed "s/^package ioprobe\$/package $3/" "${file}" >"${copy}/$2/${file##*/}"
  done
}

plant probeadapter adapters/probeadapter probeadapter
plant probehelper internal/probehelper probehelper
lint_dirs=()
while read -r dir name; do
  [[ -n "${dir}" ]] || continue
  plant lint "${dir}" "${name}"
  lint_dirs+=("${dir}")
done <<<"${targets}"
for tree in "${guarded[@]}"; do
  plant platform "${tree}/ioprobe" ioprobe
done

failures=0
expected=0
runs=0
out=""
shown=""

# run <mechanism> <command...>
# Runs the command in the copy and keeps its output for expect. Exit 0 is a
# failure of the probe: the mechanism accepted what it had to refuse.
run() {
  local name="$1" rc=0
  shift
  runs=$((runs + 1))
  shown=""
  out="$(cd "${copy}" && "$@" 2>&1)" || rc=$?
  if [[ ${rc} -eq 0 ]]; then
    printf 'check-imports-probe: %s exited 0 over the probe\n' "${name}" >&2
    failures=$((failures + 1))
  fi
}

# expect <mechanism> <text a line must hold> [<more text the same line must hold>]
# The first miss also prints the head of the mechanism's output, which is where
# a compile error that stopped it would show.
expect() {
  local lines
  expected=$((expected + 1))
  lines="$(grep -F -- "$2" <<<"${out}" || true)"
  if [[ -n "${lines}" ]] && grep -qF -- "${3:-$2}" <<<"${lines}"; then
    return 0
  fi
  printf 'check-imports-probe: %s did not report: %s %s\n' "$1" "$2" "${3:-}" >&2
  failures=$((failures + 1))
  if [[ "${shown}" != *"[$1]"* ]]; then
    shown+="[$1]"
    printf -- '--- the first 30 lines %s printed:\n' "$1" >&2
    head -n 30 <<<"${out}" >&2
    printf -- '---\n' >&2
  fi
}

# expect_walker <mechanism> <line prefix>
# The refusals the two go list walkers share, in their shared wording.
expect_walker() {
  local dir tree imp
  for dir in "${lint_dirs[@]}"; do
    for imp in net os io/ioutil golang.org/x/sync/errgroup; do
      expect "$1" "$2${module}/${dir} imports ${imp}, ${not_allowed}"
    done
    expect "$1" "$2${module}/${dir} imports ${adapter}, a tree the dependency rule denies"
  done
  for tree in "${guarded[@]}"; do
    expect "$1" "$2${module}/${tree}/ioprobe holds zz_probe_windows.go, ${left_out}"
    expect "$1" "$2${module}/${tree}/ioprobe holds zz_probe_other.go, ${left_out}"
    expect "$1" "$2${module}/${tree}/ioprobe holds zz_probe_s390x.s, ${left_out}"
    expect "$1" "$2${module}/${tree}/ioprobe holds zz_probe.s, which is not plain Go (SFiles)"
  done
  expect "$1" "$2${helper} imports os/exec, ${not_allowed}"
}

run check-imports.sh scripts/check-imports.sh
expect_walker check-imports.sh "FAIL "

run layering_test.go go test -count=1 \
  -run '^(TestGuardedTreesImportOnlyAllowedPackages|TestGuardedTreesTakeNoInlineException)$' ./internal/core/
expect_walker layering_test.go ""
for dir in "${lint_dirs[@]}"; do
  expect layering_test.go "${dir}/zz_probe.go:" "excuses a guarded file from forbidigo"
  expect layering_test.go "${dir}/zz_probe_test.go:" "excuses a guarded file from depguard"
done

# Every linter the configuration enables, over every guarded tree, and no
# collapsing: by default golangci-lint keeps one issue per line and three of the
# same text, which would drop expected refusals for reasons that have nothing
# to do with the rule.
lint=(golangci-lint run --config .golangci.yml --uniq-by-line=false
  --max-issues-per-linter=0 --max-same-issues=0 --output.text.print-issued-lines=false)
for tree in "${guarded[@]}"; do
  lint+=("./${tree}/...")
done
run golangci-lint "${lint[@]}"
for dir in "${lint_dirs[@]}"; do
  for imp in net os io/ioutil golang.org/x/sync/errgroup "${adapter}"; do
    expect depguard "${dir}/zz_probe.go:" "import '${imp}' is not allowed from list 'core'"
  done
  expect depguard "${dir}/zz_probe_test.go:" "import 'net/http' is not allowed from list 'core-tests'"
  expect forbidigo "${dir}/zz_probe.go:" "use of \`time.Now\` forbidden"
  for call in probeclock.Now probeclock.Since probeclock.Until probeclock.After \
    probeclock.AfterFunc probeclock.Tick probeclock.NewTicker probeclock.NewTimer \
    probeclock.Sleep timestamppb.Now context.WithTimeout context.WithDeadline \
    fmt.Scanln probeclock.LoadLocation ed25519.GenerateKey; do
    expect forbidigo "${dir}/zz_probe_reads.go:" "use of \`${call}\` forbidden"
  done
done

run 'go mod tidy -diff' go mod tidy -diff
# The line tidy adds when the module moves into the direct requirements.
expect 'go mod tidy -diff' $'+\tgolang.org/x/sync v'

if [[ ${failures} -ne 0 ]]; then
  printf 'check-imports-probe: %d failure(s) against %d expected refusal(s); the dependency rule let part of the probe through\n' \
    "${failures}" "${expected}" >&2
  exit 1
fi
printf 'check-imports-probe: %d file(s) staged, the fixture planted in %d package(s) of %d guarded tree(s); all %d expected refusal(s) reported by %d command(s)\n' \
  "${staged}" "${#lint_dirs[@]}" "${#guarded[@]}" "${expected}" "${runs}"
