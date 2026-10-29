# ADR-0069: An egress proxy's access log is an independent sensor on a kind-neutral descriptor

Status: accepted
Date: 2026-10-28

Amends [ADR-0040](0040-observations-a-record-a-log-and-one-importer.md): a
table of source kinds, a descriptor at `0.2`, records at `0.3`, a
connection's identity, and independent trust decided per kind. Amends
[ADR-0041](0041-coverage-per-declared-path.md) and
[ADR-0044](0044-coverage-judges-each-plane-and-joins-only-a-whole-export.md):
an egress path is observed by a connection, an egress host no path declares
is a row of its own, and either kind of row makes the exit 1. Amends
[ADR-0045](0045-one-run-is-supervised-against-its-procedure.md): `supervise`
refuses a source of a kind whose observations name no run. Builds on
[ADR-0039](0039-many-channels-into-one-core.md) and
[ADR-0064](0064-milestone-two-starts-with-an-independent-sensor.md), which
put this sensor first in milestone 2.

## Context

Every observation came from the agent's own runtime. The source descriptor
accepted one kind, `otel-genai-traces`, required its convention, OTLP version
and selector, and refused `TRUST_INDEPENDENT` for every source, so no source
the agent cannot write could be declared at all. The record codec refused an
observation without a trace and span id, so a connection could not be
written. The inventory already declared `egress` paths, and nothing could
ever observe one.

A second kind could not be added without editing the descriptor's checks, and
a third would need the same edit again. Which kind may claim independent
trust is policy, and it must not move into an importer (invariant 7).

## Decision

1. **A table of kinds in `internal/observe`.** Each row names a kind, the
   descriptor members it owns, those it requires, whether it may declare
   `TRUST_INDEPENDENT`, and whether its observations can name a run.
   `observe.CheckNeutral` refuses an unknown kind, a member another kind owns,
   a trust the kind refuses and a missing required member; it does not judge
   the values of a kind's own members. A kind the table does not know reads
   as a row that owns nothing, refuses independent trust and names no run.

   | Kind | Members | Independent | Names runs |
   | --- | --- | --- | --- |
   | `otel-genai-traces` | `convention`, `otlp_version`, `select`, `run_attribute` | refused | yes |
   | `egress-access-log` | `egress_access_log` | allowed | no |

2. **One reader through a registry.** `internal/ingest` holds, per kind, the
   check of its members, its importer and the counts `observe import`
   prints. `ingest.ReadDescriptor` runs the neutral checks, then the kind's;
   a kind with no entry is refused. `observe.ReadDescriptor` is removed, so no
   caller stops at the neutral half. `observe import`, `coverage` and
   `supervise` read descriptors through it.
3. **Versions.** A descriptor is `0.2` exactly when it carries
   `egress_access_log`; the reader takes `0.1` and `0.2`. A record is `0.3`
   exactly when it carries `connection` or `refused_by`; the reader takes
   `0.1` to `0.3`. Every `0.1` descriptor keeps its bytes and its digest.
4. **The egress importer** (`internal/ingest/egresslog`) reads JSON lines
   under a mapping the descriptor declares: the top-level member that holds
   each of time, destination, port, method, status, bytes sent, bytes
   received and caller. Time and destination are required. It reads no
   member the mapping does not name, so a path, query, header or body the
   proxy logged is never copied (invariant 9). A destination that is not a
   host alone is refused, never parsed down to its host. A method outside a
   fixed list of nine is dropped and counted. Each refused line is counted
   under its reason in `refused_by`.
5. **A connection's identity is its content**: `obs-` and 32 hex digits of
   SHA-256 under the domain `observation-id:connection:1`, over the tenant,
   project, source, event time, host, port, method, status, both byte counts
   and the caller. A re-import of a grown or rotated file yields the same ids,
   and two connections alike in all of these are one duplicate. The codec
   holds a connection to one form: no trace, span or run, basis
   `BASIS_NONE`, stage `STAGE_INDIRECT`, an event time, and this id.
6. **Trust.** An egress descriptor may declare `TRUST_INDEPENDENT`, unsigned.
   It is the operator's statement that the agent can write neither the log
   nor the descriptor. Under it `observe import` refuses an input the group or
   others can write. `coverage` prints such trust as `independent (unsigned
   descriptor)`. A finding that rests on it needs a signed descriptor
   (ADR-0039) before continuous supervision uses it.
7. **No connection names a run in this release.** The caller is kept as the
   proxy wrote it, with the basis the descriptor declares (none, address or
   authenticated), for a later join. `supervise` refuses, as an input error, a
   `--source` whose kind names no run, and one with no kind.
8. **Coverage.** An egress path is observed when a live source it names holds
   a connection to the name that source is given, ASCII letters lowered; the
   port is not compared. The inventory refuses an egress host or source name
   that is not a host, and `server_address` on an egress path's source. No
   plane decides egress, and every egress line says so. Each host an egress
   source observed that no egress path declares is a row after the declared
   paths, sorted, at most 256, with one line counting the rest. Such a row, or
   a cut, makes the exit 1.

## Security / compatibility impact

No language model and no plane reads any of it; the plane is unchanged. The
independent claim is only as good as the deployment: nothing checks which
account writes the proxy's log. A forged line can make a destination look
reached, and a deleted line makes one look unreached, which shows as not
observed, never as covered.

A log holding a `0.3` line cannot be read by 0.14: its `observe import` and
`coverage` refuse the log as damaged, and its `observe export` counts each
such line as an `unsupported_version` gap. The inventory refuses two things
`0.1` accepted, an egress host that is not a host and `server_address` on an
egress path's source; neither could ever match before. A connection
re-imported after its descriptor's trust or caller basis changed is a
conflict, since neither is part of the id.

## Alternatives considered

- A `oneof` per kind with the OpenTelemetry members moved inside: it
  renumbers a released alpha contract, and `kind` and the case could
  disagree.
- An opaque per-kind object: another language gets nothing to compile
  against, which ADR-0039 already rejected.
- Egress members at the top level: the message grows with each kind, and the
  table of owners is still needed.
- Envoy's native format only, or Squid's native text: one proxy per release,
  or a positional parser for a format nobody pins.
- An id from the time and a request id: a client can set `x-request-id`, and
  not every proxy has one. An id from the input's first line and a line
  number breaks on rotation.
- Signing the descriptor now: no signing tool or key exists, and it would
  cost a release. Never independent until signed: coverage would get no trust
  stronger than today's.
- An operator's caller-to-run map, or runs inferred by time: a proxy's caller
  is a workload and a run is a task, so concurrent runs would be confused.
- `supervise` ignoring an egress source: its silence and its refusals would
  still weigh on every run.

## Consequences

Backlog task B21 splits: B21a, the egress access log, is this record; B21b,
process events from the host, is a second importer and its own checks, with
no edit of `internal/observe` beyond a row of the table. Of B21's acceptance,
"an unattributed connection stays unknown" holds, and "attributed to a run as
well as the proxy identifies its caller" does not: every connection is
unattributed. `observe import` prints the total refused, not each reason; the
import report in the log holds them.

## Validation

- `internal/observe/kinds_test.go`: `TestTheKindTableIsPinned`,
  `TestEveryDescriptorMemberHasOneOwner`, `TestCheckNeutralOnTheEgressKind`,
  `TestCheckNeutralOnTheOTelKind`, `TestTheDescriptorVersionFollowsItsMembers`.
- `internal/observe/connection_test.go`: `TestTheCodecChecksAConnection`,
  `TestTheCodecChecksAConnectionsHost`, `TestConnectionIDIsPinned`,
  `TestConnectionIDSeesEveryKeyField`,
  `TestARecordIsSchema03ExactlyWhenItCarriesAMemberOf03`,
  `TestTheEarlierGoldensAreUnchanged`.
- `internal/ingest/kinds_test.go`: `TestEveryKindHasExactlyOneEntry`,
  `TestEachOwnedMemberIsCheckedByItsKind`,
  `TestIndependentTrustIsTheTablesRule`; `internal/ingest/reads_test.go`:
  `TestNoDescriptorIsReadOutsideTheEntryPoint`.
- `internal/ingest/egresslog/`: `TestEnvoyFixture`, `TestSquidFixture`,
  `TestRefusals`, `TestKept`, `TestLineBound`, `TestNothingUnmappedIsCopied`,
  `TestEveryLineLandsInOneCount`, `TestReimportYieldsOnlyDuplicates`,
  `TestRotatedFile`, `TestInputForbidden`.
- `internal/coverage/egress_test.go`: `TestAnEgressPathIsNeverDecidedByAPlane`,
  `TestAToolObservationNeverObservesAnEgressPath`, `TestMixedCaseHostsMatch`,
  `TestUndeclaredRowsAreSortedAndCut`, `TestACutAloneMakesTheExitOne`,
  `TestReadInventoryHoldsEgressToHosts`.
- `internal/supervise/source_kind_test.go`:
  `TestASourceOfAKindThatNamesNoRunIsRefused`.
- `cmd/guardana-control/observe_kinds_test.go`:
  `TestObserveImportRefusesAnIndependentInputOthersCanWrite`,
  `TestSuperviseRefusesASourceWhoseObservationsNameNoRun`,
  `TestCoverageOfAnImportedEgressLog`.
