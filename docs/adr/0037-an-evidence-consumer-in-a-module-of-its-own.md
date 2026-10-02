# ADR-0037: An evidence consumer in a module of its own

Status: accepted
Date: 2026-10-02

Builds on [ADR-0007](0007-repository-layout-and-dependency-rule.md),
[ADR-0026](0026-first-value-and-external-extension-paths.md) and
[ADR-0035](0035-a-versioned-evidence-export-and-a-bounded-query.md).

## Context

ADR-0035 made `examples/evidence-report` the export's external consumer: it
decodes events with the generated wire contract alone, and a test pins that it
imports nothing under `internal/`. It is still a package of this module. Its
tests import `internal/evidence` and `internal/trailfile` to hold it to the
plane's chain grammar and to the exporter's bytes, and its dependency test
takes "this module" from its own listing, so it proves less than a program
outside the module would face. Milestone 2 asks for a consumer outside this
module that reports, raises a local alert, and survives duplicate delivery,
gaps and an outage of its own.

The gate runs `./...` from the root. A nested `go.mod` ends that pattern, so
vet, lint, the tests, govulncheck and the tidy check would pass a nested module
without reading it, and the fuzz smoke test would fail on it.

## Decision

**The example is a module of its own.** `examples/evidence-report` has its own
`go.mod` under the path `example.com/evidence-report`, which no package of this
module's path shares, so the compiler refuses an `internal/` import from it. It
requires `github.com/guardana/control` through `replace => ../..`, so it builds
offline against the tree it ships in; a program copied out drops the replace
and requires a release. It imports the generated contract package and nothing
else of this module, and a test derives the forbidden prefix from the contract
package's own module, so a `pkg/` import fails it too. No `go.work` is tracked,
and the gate sets `GOWORK=off`.

**The gate covers every module.** Every target that runs Go over packages
(vet, lint, test, the race test, govulncheck, the tidy check, the fuzz smoke
test) runs once per `go.mod` the repository lists and says how many it
covered; a module path the shell could misread, or a `go.mod` spelled in
another case, is refused. A probe plants a defect in a nested module of a
staged copy and fails unless every one of those targets but govulncheck turns
red on it and passes without it; govulncheck would need a known vulnerability
and the network.

**Facts it cannot import are data.** The chain grammar, the line bound and
the exporter's bytes for the example's fixtures are committed under the
example's `testdata/`. The example's tests read them, and tests of this
module check them against `internal/evidence` and the real exporter, so a
change on either side fails a test.

**Alerts are local and resumable.** With a state directory the example reads
one export after another. It refuses, leaving its state as it was, an export
whose header names another source or another starting cursor, or any filter,
and one cut short. It drops an event seen before by tenant, project and event
id within a bounded window, and raises `conflicting_event` for one id with two
different lines. It raises alerts from a closed set, each one JSON line
appended to a log and repeated on standard error, and prints the report's rows
for the requests that ended. The log is written and synced before the cursor
is saved, and an alert already in the log is not raised again, so a crash
between the two loses nothing and repeats nothing. It runs no command, opens no
connection, reads no clock of its own and prints no field outside an allowlist
of identifiers, kinds, verdicts and codes.

No wire contract, export format, `pkg/` surface or dependency of a shipped
binary changes.

## Security / compatibility impact

The example ships in no binary. Its module depends on protobuf only, pinned to
the root's version. Dependabot watches both modules, and a root bump the
example's module does not follow fails the gate there, so the two move
together.
The tests that check the example's data belong to this module, whose module
zip leaves the example out, so they fail when run from the module cache, where
the example's files are absent; `go test all` in a program that requires this
module does not reach them. They are this repository's checks, and fail rather
than pass over files they cannot find.

## Alternatives considered

- **Keep the example in this module.** It proves nothing about a program that
  cannot import `internal/`.
- **A new module beside it.** A second reader of the same format, and the next
  fix lands twice.
- **Require the published release instead of the replace.** What a stranger
  gets, but a cold gate then needs the network, and a contract change waits
  for a release before its consumer can follow.
- **Its own generated copy of the contract.** A second generated tree no
  check compares with `api/proto`.
- **A tracked `go.work`.** It would change how the root resolves its
  dependencies and reach the release build.

## Consequences

A module added later is covered by the gate without a change to it. A
contributor builds the example with `go -C examples/evidence-report build`.

## Validation

- The probe: a planted vet error, failing test, gosec finding and untidy
  requirement in a nested module each turn their target red; a clean one
  passes.
- The example's dependency test fails on a planted `pkg/contract` import in a
  copy.
- Tests of this module fail when the example's grammar, line bound or fixture
  bytes differ from `internal/evidence` and the exporter.
- Fixtures for an exact repeat, the same export twice, one id with two lines,
  each kind of gap, a cut export, and an outage with a crash between the alert
  log and the cursor, after which every alert is in the log once; a privacy
  test finds no field outside the allowlist in any output or file.
