# ADR-0001: Go, one pinned compiler version

Status: accepted
Date: 2026-09-09

## Context

An enforcement point sits in the request path of every consequential tool call.
It has to ship as one artifact, start without a warm-up, and cost little memory
next to an agent. It also has to build the same way on a contributor's machine
and in CI, or a security fix in the compiler becomes invisible.

## Decision

Go, with the compiler version pinned to exactly `1.27.1` by the `go` directive
in `go.mod`. That directive is the single source: `scripts/bootstrap.sh` reads
the version out of it, and CI resolves the compiler from the same file with
`go-version-file: go.mod`, so no workflow and no pin file repeats the version as
a literal. There is no `toolchain` directive: `go mod
tidy` removes one as redundant once the `go` directive already names the patch
version.

Reasons for Go: a single static binary in the request path, predictable latency
with no runtime to warm, and a standard library that already covers most of what
an enforcement point does.

## Security / compatibility impact

Pinning does not make two builds byte-identical; platform, build flags and
dependency inputs still differ. What it buys is the same compiler behaviour and
the same standard library everywhere, so a difference in results is not a
difference in toolchain. Go patch releases carry security fixes, and one pinned
version in one file makes taking them a reviewed edit rather than a silent
drift. Memory safety removes a class of failure that code on the decision path
cannot afford.

## Alternatives considered

- Rust. Tighter control over allocation and latency, but a smaller pool of
  contributors for the protocol and adapter work this project needs most.
- A JVM or Node service. Startup time and memory cost that a sidecar next to
  every agent pays again on every restart.
- Go without a pin. Builds that differ between machines, and a compiler change
  nobody reviewed.

## Consequences

Contributors need that exact Go version installed, and `scripts/bootstrap.sh`
says so up front, naming the version it read from `go.mod`, instead of letting
the build fail later in a confusing way. The pin is exact: a newer patch release
is a mismatch and not an upgrade, so moving to it is a deliberate edit to
`go.mod`.

Garbage collection pauses have to be measured rather than assumed, so a latency
claim waits for a benchmark that reports the tail and not an average. Any Go
feature newer than the pin is unavailable until the pin moves.

## Validation

Runnable today: `scripts/bootstrap.sh`, which `make bootstrap` calls, parses the
`go` directive out of `go.mod`, requires exactly one such directive rather than
defaulting when it finds none, and exits non-zero when the installed compiler is
not that exact version. `scripts/tool-versions.env` pins the binary tools only,
so there is no second copy of the Go version to drift.

Written but never run: `ci.yml` and `security.yml` in `.github/workflows/`
resolve the compiler with `go-version-file: go.mod`. They are committed and
SHA-pinned, and they have not executed, because the repository does not exist
yet.

Not built: a check that fails on a literal `go-version:` in a workflow, which
would reintroduce a second source. Review is what keeps one out today.
