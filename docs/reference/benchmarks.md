---
title: Benchmarks
summary: What three calls on the authorization path cost on one machine, how they are measured, and what the numbers are not.
type: reference
covers: [bench/**, scripts/bench.sh]
---

# Benchmarks

What three calls on the authorization path cost, measured on one machine.
They are a measurement, not a promise: nothing here is a threshold, no gate
fails on a duration, and the numbers below come from one machine. How to run them is in
[bench/README.md](../../bench/README.md).

## What is measured

Three functions, all `implemented`:

| Benchmark | Function | Input | Outside the number |
| --- | --- | --- | --- |
| `BenchmarkValidateEnvelope` | `contract.Validate` | An envelope already parsed | The parse; `Decode` and `DecodeJSON` bound the bytes before parsing, so the codec cost and the contract's own checks are two costs and are not added together |
| `BenchmarkDigestV1` | `canon.DigestV1` | The same envelope plus its authorized arguments: field extraction, the canonical JSON encoding of the action, one SHA-256 over the domain tag and those bytes | Nothing; the digest is the whole call |
| `BenchmarkDecide` | `core.Decide` | One READ against a loaded snapshot: admission, digest, delegation and tenant checks, freshness, the evaluation of every rule and the decision's fields | Signing and loading the bundle; the clock is the real one, because the latency field is part of what a decision costs |

`BenchmarkDecide` runs four documents, in `bench/policy_test.go`:

| Row | Document | Verdict |
| --- | --- | --- |
| `rules=10`, `rules=100`, `rules=1000` | Every second rule matches the envelope and the rest fail on the action name | `ALLOW` |
| `obligations=100` | 100 rules that all match, with two distinct obligations each, so the decision carries a union of 200, which the kernel clones | `ALLOW_WITH_OBLIGATIONS` |

The obligation union is bounded only by the document, which is why it has its
own row. The inputs of the first two benchmarks are the four golden fixtures
in [testdata/digest](../../testdata/digest/README.md); both read the same
envelopes, so the two numbers can be read against each other.

| Fixture | Envelope, encoded | Authorized arguments |
| --- | --- | --- |
| `minimal` | 126 B | 2 B |
| `refund_prod` | 857 B | 345 B |
| `delegated` | 695 B | 90 B |
| `mutated_amount` | 857 B | 347 B |

Each input is checked before it is timed: all three functions return early
when they refuse their input, so a fixture that stopped validating, or a
snapshot that went stale against the real clock, would be reported as fast
work rather than as a failure. `TestBenchmarkedPathSucceeds` and
`TestBenchmarkedDecideSucceeds` are those checks and they run under
`go test ./bench/`. `BenchmarkDecide` reports, beside the mean `go test`
computes, the median and the 99th percentile of the per-call durations as
`p50-ns/op` and `p99-ns/op` through `ReportMetric`: the mean of a path with a
slow tail says nothing about the tail.

## The machine

One run of `scripts/bench.sh` with `COUNT=5 BENCHTIME=1s`, recorded in
`bench/results/`; an older run sits beside it there and in the history.

| Item | Value |
| --- | --- |
| CPU | Apple M5, 10 cores |
| OS | Darwin 25.6.0 arm64 (macOS 26.6.2) |
| Go | go1.27.1 darwin/arm64 |
| Load | 5.66 over the minute it started, 15.92 over five |

The machine was not idle: other work was running on it throughout, which is
what the load averages record, so these durations are an upper bound for the
same code on the same machine with nothing else on it. The allocation figures
do not depend on the load. The results
file marks the work tree as modified: the benchmark and its page were
themselves uncommitted, and the packages measured were not.

## Validate and DigestV1

Nanoseconds per operation are the lowest, middle and highest of the five
repetitions, because one number would be a lie about the spread. Allocation
counts and bytes were identical in all five.

| Benchmark | ns/op low | Median | High | B/op | allocs/op |
| --- | --- | --- | --- | --- | --- |
| `BenchmarkValidateEnvelope/minimal` | 1847 | 1890 | 1941 | 1232 | 16 |
| `BenchmarkValidateEnvelope/refund_prod` | 8451 | 8519 | 8597 | 3912 | 134 |
| `BenchmarkValidateEnvelope/delegated` | 6168 | 6195 | 6311 | 2416 | 67 |
| `BenchmarkValidateEnvelope/mutated_amount` | 8413 | 8445 | 8461 | 3912 | 134 |
| `BenchmarkDigestV1/minimal` | 6998 | 7068 | 7409 | 9630 | 124 |
| `BenchmarkDigestV1/refund_prod` | 15910 | 16254 | 17172 | 21047 | 308 |
| `BenchmarkDigestV1/delegated` | 12234 | 13155 | 14149 | 16117 | 228 |
| `BenchmarkDigestV1/mutated_amount` | 17039 | 17224 | 19945 | 21047 | 308 |

Read the spread before the numbers. `refund_prod` and `mutated_amount` carry
the same envelope and differ only in one argument value, so both functions do
the same work on both: identical allocations, and for the digest two more
bytes of input. `BenchmarkDigestV1` puts them at medians of 16254 and 17224
and highs of 17172 and 19945. The gap between those two highs is the size of
the noise on every row here. A single number from one run describes the
machine at least as much as it describes the code.

Both functions allocate more than the message they read. The digest builds a
Go map of the action, sorts every object key as UTF-16 code units and encodes
the whole thing before hashing it, which is where its 124 to 308 allocations
come from. Nothing has been tuned; no profile has been taken.

## Decide

`ns/op` is the mean over the repetition, lowest, middle and highest of the
five; `p50` and `p99` are the per-call percentiles, middle repetition of the
five, with the highest p99 in parentheses. Allocation counts were identical in
all five; B/op differed by one byte between repetitions on three rows, which
is the rounding of a per-operation average.

| Benchmark | ns/op low | Median | High | p50 ns | p99 ns (highest) | B/op | allocs/op |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `BenchmarkDecide/rules=10` | 11162 | 11233 | 12430 | 9792 | 58791 (68750) | 12421 | 165 |
| `BenchmarkDecide/rules=100` | 12459 | 12630 | 13316 | 10875 | 66750 (73459) | 14941 | 168 |
| `BenchmarkDecide/rules=1000` | 23958 | 24239 | 26087 | 20292 | 108166 (119791) | 37838 | 171 |
| `BenchmarkDecide/obligations=100` | 83757 | 84698 | 89340 | 65125 | 252334 (297291) | 210137 | 1787 |

Read the p99 as the machine's, not the code's: it is four to six times the
median on every row, and the row that does the least work has the same tail as
the row that evaluates a thousand rules. That is the shape of a busy machine
descheduling the benchmark. Compare medians, not tails.

Ten rules to a thousand doubles the median: the fixed cost of a decision, the
clone of the envelope, the digest and the decision's fields, is most of the
ten-rule number, and a rule costs about ten nanoseconds to evaluate. The
allocation count barely moves with the rules, because the matched-rule list is
the only thing that grows. The obligations row is a different shape: two
hundred obligations cloned onto the decision are eleven times the allocations
and fourteen times the bytes of the hundred-rule document without them.

## What these numbers are not

| Not | Because |
| --- | --- |
| A target or a service level | Comparing a later run against these is only meaningful on the same machine in the same state. |
| A gate | `make quality` fails on no duration, and a build that failed on one would be reporting on the host rather than on the change. |
| End to end | `Decide` is the kernel's decision in memory; the enforcement point around it is `experimental` and [status.md](../status.md) is the inventory. |
