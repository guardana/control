# ADR-0072: The collector keeps bounded segments of one project, behind a credential

Status: proposed
Date: 2026-10-30

Amends [ADR-0020](0020-a-trail-and-counters-without-a-collector.md) on what
`collect` writes and whom it takes it from, and
[ADR-0035](0035-a-versioned-evidence-export-and-a-bounded-query.md) on what a
cursor names in a log of segments. Builds on
[ADR-0014](0014-evidence-spool-and-sinks.md) and
[ADR-0066](0066-each-decision-about-authority-is-a-port.md).

## Context

`collect` appended everything it received to one file that never rotated, and
its index of event ids grew with it. It took a request from anything that
could reach its loopback port, so any local account could write lines into a
plane's trail, or fill the disk. A file that only grows has no bound to fail
closed on, and no way to drop old evidence that leaves a record of what was
dropped.

## Decision

**Evidence storage is a port, RecordLog,** declared in `internal/segmentlog`:
append whole lines, seal the live segment, prune sealed segments through one
number, locate a segment by the digest of its first line, and read the
manifest. A located segment is Live, Sealed or Pruned, with what the log knows
of it; the zero answer is Unknown, and so is a digest no segment, or more than
one, begins with. The kind is closed, and its default is `segments`. No
remote kind exists yet.

**The default kind is a directory of segments and one manifest,**
`segments.json`, format `guardana.control.segments` version `0.1`. The
manifest names the tenant and the project the log is bound to, and each sealed
or pruned segment by number, with its lines, bytes, the SHA-256 of its first
and last line and when it was sealed and pruned. It is read only in the one
spelling its writer gives it; any other version is refused. One writer holds
the directory under a lock file. An open reads every segment not pruned whole,
and refuses one that does not hold what the manifest names it with, a segment
the manifest does not name, and any name no writer leaves.

**A seal names the segment in the manifest, then renames the live file; a
prune writes the tombstone, then unlinks.** A crash between the steps is
completed at the next open: a live file that is the segment the manifest names
sealed is renamed, and a file at a pruned segment's name is unlinked when it
is the one the tombstone names. Anything else is refused.

**The bound refuses and nothing prunes on its own.** `collect --max-bytes`
bounds the bytes of the segments not pruned; an append that would pass it is
answered 503, the plane's spool fills, and a material call is blocked with
`EVIDENCE_UNAVAILABLE` as ADR-0014 says. `--segment-bytes` seals a live
segment before an append would pass it; the bound on the log is at least twice
it, so a full log always holds a sealed segment. `guardana-gateway prune
--through <n> <dir>` is the operator's act, refused while a collector holds
the directory.

**The collector is bound to one tenant and project and requires a
credential.** `collect --tenant --project --credential-file` is required. The
receiver answers 401, before it reads the body, a request whose one
`Authorization` header is not `Bearer` and the credential, compared as SHA-256
digests in constant time; the plane sends it through `export.headers`. An
append holding an event of another tenant or project is refused whole as 400,
which the plane quarantines.

**The evidence export stays `1.0`.** `trail export <dir>` exports one segment.
With no cursor it is the first segment not pruned; a cursor names its segment
by its first line, and one at the end of a sealed segment moves to the next
once that holds a line. A cursor of a pruned segment is refused as expired,
exit 2. A segment opened is read from its descriptor, so a seal or a prune
while it is read changes nothing read. `trail <dir>` reads every segment not
pruned, and `trail segments <dir>` prints the manifest.

## Security / compatibility impact

`collect --out` is gone, and every collect command line changes once: a plane
sending no credential or another one is answered 401, retries, and blocks its
material calls when its spool is full, as with a collector that is down. A
local account without the credential writes nothing to a trail and cannot
fill its bound. One that can read the credential file, or the plane's
configuration, can; both are the operator's to keep. The dedup index covers
the segments not pruned only: an event sent again after its segment was
pruned is written again. A trail whose first events were pruned fails the
chain check in `trail <dir>`, which sees only its rest. A trail file of the
old shape is still read and exported as before.

## Alternatives considered

- Pruning the oldest sealed segment at the bound: drops evidence nobody chose
  to drop, and lets anyone who can fill the bound erase what came before.
- Splitting appends across segments to keep each under its bound exactly: a
  request's lines would land in two segments, and a cursor would name half a
  request.
- An unbound `collect --out` beside the segments: leaves the refusal at the
  bound, and the credential, for anyone to step around.
- A credential compared as bytes: `subtle.ConstantTimeCompare` returns early
  on a length that differs, so the length would be learned; digests are of one
  length.

## Consequences

An operator watches `trail segments` and prunes; until then the plane blocks
material calls. The findings and observation logs take the same port in a
later change. The collector has no `/metrics` or `doctor` of its own, so the
port's counts are what `trail segments` prints.

## Validation

- `internal/segmentlog`: a crash before every step of a seal and of a prune,
  and again before every step of the open after it, leaves the log whole or
  not begun, located as what it is; the conformance cases in
  `testdata/ports/record-log/`, each with its negative control; a manifest in
  any other spelling, and one that disagrees with its files, is refused;
  `FuzzManifest` holds the one spelling.
- `internal/trailfile`: an event of another project refuses its append; a
  resend across a seal and across a reopen is written once; an export cut at
  every record and resumed writes every event once, across seals; a cursor of
  a pruned segment is refused as expired.
- `adapters/otel`: a request without the credential exactly, in every form
  tried, is answered 401 and reaches no sink.
- `internal/gateway/e2e`: at the trail's bound a material call is blocked and
  never reaches the upstream, nothing is pruned on its own, and after a prune
  the spool drains and the call runs; a plane with another credential writes
  nothing; another project's evidence is quarantined by the plane.
