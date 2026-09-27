#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Runs the benchmarks in bench/ and records one run.
#
#   scripts/bench.sh             writes bench/results/local/<stamp>.txt, which
#                                .gitignore keeps out of the repository
#   scripts/bench.sh --publish   writes bench/results/<stamp>-<os>-<arch>.txt,
#                                which is tracked and is what a page may cite
#
# A benchmark number is a measurement on one machine at one moment, so the file
# starts with what that machine was and what it was doing; bench/README.md says
# why that matters. A published run is refused when the machine cannot be
# named, when the work tree differs from its commit or changes while the run
# goes on, when git hides a change or an ignored Go source, when a workspace
# file or a Go setting changes what is built, and when the toolchain targets
# another platform or runs translated.
#
# Nothing here compares a run against a threshold. A gate that failed a build
# on a duration would fail on a busy machine and pass on an idle one, which is
# a check reporting on the host rather than on the change.
set -euo pipefail

# The header is read by people and by scripts; a locale's decimal comma in the
# load averages or the memory would make the same machine read differently.
export LC_ALL=C

_BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck source=lib/repo-files.sh
. "${_BENCH_DIR}/lib/repo-files.sh"

# Assigned first: `cd "$(repo_root)"` would swallow a repo_root failure, because
# cd with an empty argument succeeds and stays put.
root="$(repo_root)"
cd "${root}"

readonly PKG=./bench
readonly RESULTS=bench/results
readonly UNKNOWN=unknown

benchtime="${BENCHTIME:-1s}"
count="${COUNT:-1}"

die() {
  printf 'bench: %s\n' "$*" >&2
  exit 1
}

publish=0
case "$#:${1:-}" in
  0:) ;;
  1:--publish) publish=1 ;;
  *) die "usage: scripts/bench.sh [--publish]" ;;
esac

# A value that reads as empty, zero or not a number is recorded as unknown
# rather than guessed.
positive_int() {
  [[ "$1" =~ ^[0-9]+$ && "$1" -gt 0 ]]
}

gib() {
  awk -v b="$1" 'BEGIN { printf "%.1f GiB (%.0f bytes)", b / 1073741824, b }'
}

cpu="${UNKNOWN}"
cores="${UNKNOWN}"
memory="${UNKNOWN}"
os_name="${UNKNOWN}"

machine_darwin() {
  local v perf eff
  v="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || true)"
  [[ -n "${v}" ]] && cpu="${v}"
  v="$(sysctl -n hw.ncpu 2>/dev/null || true)"
  if positive_int "${v}"; then
    cores="${v}"
    perf="$(sysctl -n hw.perflevel0.physicalcpu 2>/dev/null || true)"
    eff="$(sysctl -n hw.perflevel1.physicalcpu 2>/dev/null || true)"
    if positive_int "${perf}" && positive_int "${eff}"; then
      cores="${v} (${perf} performance, ${eff} efficiency)"
    fi
  fi
  v="$(sysctl -n hw.memsize 2>/dev/null || true)"
  positive_int "${v}" && memory="$(gib "${v}")"
  v="$(sw_vers -productVersion 2>/dev/null || true)"
  [[ -n "${v}" ]] && os_name="macOS ${v}"
  return 0
}

machine_linux() {
  local v
  if [[ -r /proc/cpuinfo ]]; then
    v="$(sed -n '/^model name/{s/^model name[[:space:]]*:[[:space:]]*//p;q;}' /proc/cpuinfo)"
    [[ -n "${v}" ]] && cpu="${v}"
    v="$(grep -c '^processor' /proc/cpuinfo || true)"
    positive_int "${v}" && cores="${v}"
  fi
  if [[ -r /proc/meminfo ]]; then
    v="$(sed -n 's/^MemTotal:[[:space:]]*\([0-9]*\) kB$/\1/p' /proc/meminfo)"
    positive_int "${v}" && memory="$(gib "$((v * 1024))")"
  fi
  if [[ -r /etc/os-release ]]; then
    v="$(sed -n 's/^PRETTY_NAME="\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' /etc/os-release)"
    [[ -n "${v}" ]] && os_name="${v}"
  fi
  return 0
}

goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
gohostos="$(go env GOHOSTOS)"
gohostarch="$(go env GOHOSTARCH)"
gowork="$(go env GOWORK)"
[[ -n "${goos}" && -n "${goarch}" ]] || die "go env reported no GOOS or GOARCH"
case "${gohostos}" in
  darwin) machine_darwin ;;
  linux) machine_linux ;;
esac

# What the run could use, which an affinity mask or a container can make less
# than the cores the machine has. nproc also obeys the OpenMP variables, which
# say nothing about what Go may use.
usable="$(env -u OMP_NUM_THREADS -u OMP_THREAD_LIMIT nproc 2>/dev/null ||
  sysctl -n hw.logicalcpu 2>/dev/null || true)"
positive_int "${usable}" || usable="${UNKNOWN}"
cgroup_cpu=none
if [[ -r /sys/fs/cgroup/cpu.max ]]; then
  cgroup_cpu="$(cat /sys/fs/cgroup/cpu.max)"
fi

# The settings that change what go test builds, read through go env because
# `go env -w` persists them where the environment does not show them, and the
# two runtime variables go env does not report.
goflags="$(go env GOFLAGS)"
goexperiment="$(go env GOEXPERIMENT)"
goenv() {
  local name v
  for name in GOFLAGS GOEXPERIMENT CGO_ENABLED GOAMD64 GOARM64; do
    printf ' %s=%s' "${name}" "$(go env "${name}")"
  done
  for name in GODEBUG GOMAXPROCS; do
    if v="$(printenv "${name}")"; then
      printf ' %s=%s' "${name}" "${v}"
    else
      printf ' %s unset' "${name}"
    fi
  done
}

# A darwin arm64 machine running an amd64 toolchain translates every
# instruction, and the toolchain then reports an amd64 host.
translated=0
if [[ "${gohostos}" == darwin && "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" == 1 &&
  "${goarch}" != arm64 ]]; then
  translated=1
fi

# The tree is modified when git reports anything, untracked files included: a
# new benchmark file is as much a change to what runs as an edited one.
head="$(git -C "${root}" rev-parse HEAD 2>/dev/null || true)"
commit="${head:0:7}"
if [[ -z "${commit}" ]]; then
  tree=unknown
  commit='unknown (not a git work tree)'
elif ! changes="$(git -C "${root}" status --porcelain --untracked-files=all 2>/dev/null)"; then
  tree=unknown
  commit="${commit} (git status failed; whether the work tree matches is unknown)"
elif [[ -n "${changes}" ]]; then
  tree=modified
  commit="${commit} (work tree modified; the code measured is not this commit)"
else
  tree=clean
fi

if [[ ${publish} -eq 1 ]]; then
  for field in "cpu=${cpu}" "cores=${cores}" "memory=${memory}"; do
    [[ "${field#*=}" != "${UNKNOWN}" ]] ||
      die "cannot read the ${field%%=*} of this machine; a published run has to name its hardware"
  done
  [[ -z "${gowork}" || "${gowork}" == off ]] ||
    die "GOWORK is ${gowork}; a workspace can build other modules than the commit names"
  [[ -z "${goflags}" && -z "${goexperiment}" ]] ||
    die "GOFLAGS is '${goflags}' and GOEXPERIMENT '${goexperiment}'; a published run builds with neither"
  [[ "${goos}/${goarch}" == "${gohostos}/${gohostarch}" ]] ||
    die "the toolchain targets ${goos}/${goarch} on a ${gohostos}/${gohostarch} host; a published run is native"
  [[ ${translated} -eq 0 ]] ||
    die "an ${goarch} toolchain on an arm64 Mac runs translated; a published run is native"
  [[ "${tree}" == clean ]] ||
    die "the work tree is ${tree}; a published run measures a commit, so commit or set aside every change first"
  # An ignored or excluded Go source is compiled and never shows as a change.
  hidden="$(git -C "${root}" status --porcelain --ignored=matching --untracked-files=all \
    -- '*.go' '*.s' go.mod go.sum)" || die "git status failed; cannot tell which Go sources are built"
  [[ -z "${hidden}" ]] ||
    die "git ignores Go sources that go test would build: ${hidden}"
  # skip-worktree (S) and assume-unchanged (lower case) entries hide an edit
  # from git status.
  flagged="$(git -C "${root}" ls-files -v)" || die "git ls-files failed; cannot tell whether git hides an edit"
  flagged="$(printf '%s\n' "${flagged}" | grep -E '^([a-z]|S) ' || true)"
  [[ -z "${flagged}" ]] ||
    die "git is told to overlook changes to: ${flagged}"
fi

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

# Correctness before speed. Every benchmarked call is at its fastest on input
# it refuses, so a run over a fixture that stopped validating would report a
# number rather than a failure.
printf 'bench: checking that the measured path succeeds\n'
go test -count=1 "${PKG}" ||
  die "the guard test failed; refusing to record a benchmark of a path that does not work"

stamp="$(date -u '+%Y%m%dT%H%M%SZ')"
if [[ ${publish} -eq 1 ]]; then
  dir="${RESULTS}"
  out="${dir}/${stamp}-${goos}-${goarch}.txt"
else
  dir="${RESULTS}/local"
  out="${dir}/${stamp}.txt"
fi
mkdir -p "${dir}"
[[ ! -e "${out}" ]] || die "${out} already exists; refusing to overwrite a recorded run"
partial="$(mktemp "${dir}/.partial.XXXXXX")"
trap 'rm -f "${partial}"' EXIT

{
  printf 'date:      %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf 'commit:    %s\n' "${commit}"
  printf 'cpu:       %s\n' "${cpu}"
  printf 'cores:     %s\n' "${cores}"
  printf 'memory:    %s\n' "${memory}"
  printf 'os:        %s\n' "${os_name}"
  printf 'host:      %s\n' "$(uname -srm)"
  printf 'usable:    %s logical CPUs, cgroup cpu.max %s\n' "${usable}" "${cgroup_cpu}"
  printf 'go:        %s\n' "$(go version)"
  printf 'goenv:    %s GOWORK=%s\n' "$(goenv)" "${gowork:-}"
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

# -n so a file that appeared at the target meanwhile is kept, and the checksum
# so the file reported is the one this run wrote.
# The commit the header names has to be the one that ran from start to end.
if [[ ${publish} -eq 1 ]]; then
  [[ "$(git -C "${root}" rev-parse HEAD 2>/dev/null || true)" == "${head}" ]] ||
    die "HEAD moved while the benchmarks ran; the run is not recorded"
  [[ "$(git -C "${root}" status --porcelain --untracked-files=all 2>/dev/null || echo failed)" == "${changes}" ]] ||
    die "the work tree changed while the benchmarks ran; the run is not recorded"
fi

chmod 0644 "${partial}"
sum="$(cksum <"${partial}")"
mv -n -- "${partial}" "${out}" || true
if [[ -e "${partial}" ]]; then
  trap - EXIT
  die "${out} appeared while the benchmarks ran; this run is left in ${partial}"
fi
[[ -f "${out}" && "$(cksum <"${out}")" == "${sum}" ]] || die "${out} is not the file this run wrote"
printf 'bench: wrote %s\n' "${out}"
