#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Runs the benchmarks in bench/ and records one run under bench/results/.
#
# The results directory is per-machine and .gitignore keeps everything in it
# out of the repository except .keep. A benchmark number is a measurement on
# one machine at one moment, so the file starts with what that machine was and
# what it was doing; bench/README.md says why that matters.
#
# Nothing here compares a run against a threshold. A gate that failed a build
# on a duration would fail on a busy machine and pass on an idle one, which is
# a check reporting on the host rather than on the change.
set -euo pipefail

_BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_BENCH_DIR}/lib/repo-files.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

readonly PKG=./bench
readonly RESULTS=bench/results

benchtime="${BENCHTIME:-1s}"
count="${COUNT:-1}"

die() {
  printf 'bench: %s\n' "$*" >&2
  exit 1
}

# The subject is enumerated, never assumed: a directory that lost its benchmark
# would otherwise produce a results file holding a clean run of nothing.
files="$(repo_files 'bench/*_test.go')"
[[ -n "${files}" ]] || die "no test file under bench/; there is nothing to measure"

names=""
while IFS= read -r file; do
  [[ -n "${file}" ]] || continue
  names+="$(sed -n 's/^func \(Benchmark[A-Za-z0-9_]*\)(.*/\1/p' "${file}")"$'\n'
done <<<"${files}"
names="$(printf '%s' "${names}" | sed '/^$/d')"
[[ -n "${names}" ]] || die "no Benchmark function under bench/; there is nothing to measure"

# Correctness before speed. Both benchmarked calls are at their fastest on input
# they refuse, so a run over a fixture that stopped validating would report a
# number rather than a failure.
printf 'bench: checking that the measured path succeeds\n'
go test -count=1 "${PKG}" ||
  die "the guard test failed; refusing to record a benchmark of a path that does not work"

mkdir -p "${RESULTS}"
stamp="$(date -u '+%Y%m%dT%H%M%SZ')"
out="${RESULTS}/${stamp}.txt"
partial="${out}.partial"
trap 'rm -f "${partial}"' EXIT

# A commit alone would name code that is not what ran: the benchmark measures
# the work tree, and a dirty one is a different program from the commit it sits
# on. Recording the commit without that mark is the kind of pass over something
# nobody examined this project treats as a failure.
commit="$(git -C "${root}" rev-parse --short HEAD 2>/dev/null || true)"
if [[ -z "${commit}" ]]; then
  commit='unknown (not a git work tree)'
elif ! git -C "${root}" diff --quiet HEAD 2>/dev/null; then
  commit="${commit} (work tree modified; the code measured is not this commit)"
fi

{
  printf 'date:      %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf 'commit:    %s\n' "${commit}"
  printf 'host:      %s\n' "$(uname -srm)"
  printf 'go:        %s\n' "$(go version)"
  printf 'benchtime: %s, count %s\n' "${benchtime}" "${count}"
  # A duration measured on a loaded machine is a duration on a loaded machine.
  printf 'load:      %s\n' "$(uptime | sed 's/^ *//')"
  printf '\n'
} >"${partial}"

# -run '^$' so no test runs inside the timing run; the guard above already ran.
go test "${PKG}" -run '^$' -bench '^Benchmark' -benchmem \
  -benchtime "${benchtime}" -count "${count}" | tee -a "${partial}"

# `go test` reports ok for a -bench pattern that matched nothing, so the exit
# status alone does not say that anything was measured.
grep -q 'ns/op' "${partial}" || die "the run produced no ns/op line; nothing was measured"

mv -- "${partial}" "${out}"
printf 'bench: wrote %s\n' "${out}"
