---
title: Benchmarks
summary: What three calls on the authorization path and one tool call through the gateway cost on one machine, how they are measured, and what the numbers are not.
type: reference
covers: [bench/**, scripts/bench.sh]
---

# Benchmarks

What three calls on the authorization path and one tool call through the
gateway cost, measured on one machine. These are measurements, not thresholds
or gate conditions. How to run and publish them is in
[bench/README.md](../../bench/README.md).

## What is measured

Three functions, all `implemented`, and the `experimental` gateway around
them ([status.md](../status.md) is the inventory):

| Benchmark | Function | Input | Outside the number |
| --- | --- | --- | --- |
| `BenchmarkValidateEnvelope` | `contract.Validate` | An envelope already parsed | The parse; `Decode` and `DecodeJSON` bound the bytes before parsing, so the codec cost and the contract's own checks are two costs and are not added together |
| `BenchmarkDigestV1` | `canon.DigestV1` | The same envelope plus its authorized arguments: field extraction, the canonical JSON encoding of the action, one SHA-256 over the domain tag and those bytes | Nothing; the digest is the whole call |
| `BenchmarkDecide` | `core.Decide` | One READ against a loaded snapshot: admission, digest, delegation and tenant checks, freshness, the evaluation of every rule and the decision's fields | Signing and loading the bundle; the clock is the real one, because the latency field is part of what a decision costs |
| `BenchmarkGatewayRoundTrip` | One MCP `tools/call` | Through the plane, and straight to the upstream as the baseline | See [the round trip](#the-round-trip) |

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
work rather than as a failure. `TestBenchmarkedPathSucceeds`,
`TestBenchmarkedDecideSucceeds` and `TestBenchmarkedRoundTripSucceeds` are
those checks and they run under `go test ./bench/`. `BenchmarkDecide` and
`BenchmarkGatewayRoundTrip` report, beside the mean `go test` computes, the median and the 99th percentile of the per-call durations as
`p50-ns/op` and `p99-ns/op` through `ReportMetric`: the mean of a path with a
slow tail says nothing about the tail.

## The round trip

`BenchmarkGatewayRoundTrip` has three rows, one call at a time:

| Row | The call |
| --- | --- |
| `direct` | An agent's client calls the upstream's one tool |
| `plane/fsync=every_record` | The same call through the plane, with the spool forcing each record to disk, the gateway's default |
| `plane/fsync=interval_100ms` | The same, with the spool forcing records to disk on a 100 ms timer |

Through the plane the call enters the MCP adapter's stdio listener, is
classified as a `READ`, decided in `ENFORCE` against a signed bundle of one
rule, sent upstream, and leaves four records in a spool on a temporary
directory: proposed, decided, started and completed. All four are appended
before the answer reaches the agent, which the guard test checks.

The exporter runs throughout, with the gateway's defaults: it reads each
record back from the spool, sends up to 128 in one request or waits up to 100 ms
for more, encodes them as OTLP/JSON, posts them over loopback HTTP to a collector
in the same process that reads the body and accepts it, and acknowledges each
accepted batch to the spool. Under `fsync=interval_100ms` the spool's timer
runs as well. Their work shares the cores with the call, and `B/op` and
`allocs/op` count the whole process: the exporter, the collector and the
upstream included. A plane row minus `direct` is therefore what the plane
adds in this arrangement, the pipeline, the spool, a second protocol hop and
the exporter's share of the machine together, not the pipeline alone. Both
plane rows include the exporter reading each record back from the spool, a
cost that is not the pipeline's.

| Outside the number | Because |
| --- | --- |
| A network beyond loopback, and TLS | The agent and upstream legs are in-process pipes. The collector runs in this process and is reached over plaintext loopback TCP, so that path and the collector's HTTP server are inside the number |
| A real upstream | The upstream answers at once from memory |
| A real collector | The collector here accepts every request without decoding it |
| Approvals, obligations and an external decision point | One rule allows the call |
| Concurrent calls | One call is in flight at a time |

No published run carries this benchmark yet.

## The machine

One run of `scripts/bench.sh` with `COUNT=5 BENCHTIME=1s`. Its results file
was not published, so no file in the repository stands behind the tables
below; the next published run replaces them and is linked here.

| Item | Value |
| --- | --- |
| CPU | Apple M5, 10 cores |
| OS | Darwin 25.6.0 arm64 (macOS 26.6.2) |
| Go | go1.27.1 darwin/arm64 |
| Load | 5.66 over the minute it started, 15.92 over five |

The machine was not idle: other work was running on it throughout, which is
what the load averages record.

## Validate and DigestV1

Nanoseconds per operation are the lowest, median and highest of the five
repetitions, to show the spread. Allocation counts and bytes were identical in
all five.

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
and highs of 17172 and 19945: 6% apart at the median and 16% at the high, for
two rows that do the same work. A difference of that size between two other
rows in this run is within the variation it shows for identical work.

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

In the middle repetition the p99 is 5.3 to 6.1 times the p50 on the three
rules rows and 3.9 times on the obligations row, and it includes the scheduler
and the other work on the host as well as the code. Compare medians rather than
tails.

Ten rules to a thousand doubles the median: the fixed cost of a decision, the
clone of the envelope, the digest and the decision's fields, is most of the
ten-rule number, and a rule costs on the order of ten nanoseconds to evaluate. The
allocation count barely moves with the rules, because the matched-rule list is
the only thing that grows. The obligations row is a different shape: two
hundred obligations cloned onto the decision are eleven times the allocations
and fourteen times the bytes of the hundred-rule document without them.

## What these numbers are not

| Not | Because |
| --- | --- |
| A target or a service level | Comparing a later run against these is only meaningful on the same machine in the same state. |
| A gate | `make quality` fails on no duration, and a build that failed on one would be reporting on the host rather than on the change. |
| A deployment's latency | The round trip runs in one process, with no network, TLS, real upstream or collector. |
