# Benchmarks

Three benchmarks on the authorization path: `contract.Validate`,
`canon.DigestV1` and `core.Decide`. What they measure, the numbers from one
machine and what those numbers are not are in
[docs/reference/benchmarks.md](../docs/reference/benchmarks.md).

## Run

    scripts/bench.sh

`BENCHTIME` and `COUNT` are read from the environment and default to `1s` and
`1`. The script refuses to record a run when the guard test fails or when the
benchmark pattern matched nothing, so a results file always describes work that
happened.

## Test

    go test ./bench/

`TestBenchmarkedPathSucceeds` and `TestBenchmarkedDecideSucceeds` check every
input before it is timed: the three functions return early when they refuse
their input, so without the guard a broken fixture or a stale snapshot would be
reported as fast work.

## Traps

- Each run is written to `bench/results/`, which `.gitignore` keeps out of the
  repository except for `.keep`: a result belongs to the machine that produced
  it. The file records the date, the commit, the host, the Go version and the
  load average, because a duration without them says nothing.
- No gate fails on a duration, and none should: a build that failed on one
  would be reporting on the host rather than on the change.
- `BenchmarkDecide` reads the real clock, because the latency field is part of
  what a decision costs; run it on a quiet machine or read the medians, not
  the tails.
