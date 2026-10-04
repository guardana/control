# ADR-0035: A versioned evidence export and a bounded query

Status: accepted
Date: 2026-09-30

Builds on [ADR-0002](0002-wire-contracts-and-versioning.md),
[ADR-0004](0004-evidence-and-privacy-defaults.md),
[ADR-0014](0014-evidence-spool-and-sinks.md) and
[ADR-0020](0020-a-trail-and-counters-without-a-collector.md).

Amended by [ADR-0037](0037-an-evidence-consumer-in-a-module-of-its-own.md):
the external consumer is a module of its own, checked against the chain
grammar and the exporter through committed data rather than imports.

## Context

The plane's evidence reaches a person as the collector's trail file, one
`Event` per line in the protobuf JSON mapping, and as `guardana-gateway trail`,
which prints a verdict per request for a person to read. A program that wants
to report on what agents did has two ways today, both wrong: parse the
terminal output, or read the trail file with `internal/` readers it cannot
import. Neither says how to resume where the last read stopped, what a line
that does not decode means, or whether what was read is all there was.

The trail file is append-only, written by one collector under a lock, and
holds each event id once; a record sent again after a crash is collapsed or
refused (ADR-0020). Bytes after its last newline are a line still being
written while a collector holds the file, and a torn tail is cut only after
the last newline. A record the plane could not deliver sits in its spool's
quarantine and never reaches the file. Events are decoded without a check of
their schema major, and that decoder also feeds the spool, the collector's
receiver and its index rebuild, and the scenario reader.

## Decision

**One command exports a trail file.** `guardana-gateway trail export <file>`
writes JSON Lines to standard output, each line one JSON object whose `type`
says what it is, under the format `guardana.control.evidence-export` version
`"1.0"`, read by the MAJOR.MINOR rules of the wire contracts: a reader refuses
an unknown major and an unknown `type`.

- `header`: the format and version, the file as named, the source's identity
  and the query as understood: cursor, limit, byte bound, filters.
- `event`: `offset`, where the line starts; `cursor`, where it ends; `event`,
  the file's line bytes as a JSON value, never decoded and encoded again.
- `gap`: `offset`, `cursor` and a `reason`, for a whole line that is not one
  event of a major this build reads, or an event id seen earlier in this export
  with other content.
- `duplicate`: `offset`, the event id and the offset it was first seen at,
  for a line repeating an earlier one of this export exactly.
- `trailer`: `next_cursor`, whether the file's end was reached, the bytes after
  its last newline and whether a writer holds the file, the counts of records
  by type and of bytes scanned, and `dedup_scope: "export"`.

The export reads up to the file's last newline only. Bytes after it are a gap
only when no writer holds the file; while the collector holds it they are a
line still being written, reported in the trailer and read by the next export.

**A cursor that cannot land in other content.** A cursor is
`v1:<first>:<offset>:<line>`: the SHA-256 of the file's first line, the byte
offset right after a newline, and the SHA-256 of the line that ends there. An
export after it checks that the byte before the offset is a newline and that
the line ending there hashes as recorded, so a cursor from another file, from
a file restored and appended since, or off a line boundary is refused. Only
those two lines are compared; a line changed between them goes unseen. A file
has an identity once its first line is whole; an empty one gives no cursor.
Identity is content, not the path, so a renamed file can be read on.

**A bounded query.** `--after <cursor>` starts after a line an earlier export
returned; `--limit` bounds the records written, every type counted (1000 by
default, 100 000 at most); `--max-bytes` bounds the bytes of whole lines scanned, and the line
that would cross it is read no further than the bound (the lines a cursor is checked against are
read whole, as [contracts.md](../contracts.md#the-evidence-export) says);
`--request`, `--run`, `--tenant`, `--project` and `--kind` filter, each
repeatable, an event matching one value of every filter given, and an unknown
kind is refused. A filter cuts the links between a request's events, so a
consumer rebuilding lifecycles exports them unfiltered, or by request.

**Duplicates across exports.** Detection covers one export's records and says
so. Across exports delivery is at least once: a consumer drops an event it has
seen by tenant, project and event id, and reads one id with two different
lines as a gap.

**Missing evidence never reads as success.** Exit 0: nothing refused and no
gap. Exit 1: the export is whole, trailer included, and holds a gap or a
conflicting duplicate; the records and the trailer are still there to read.
Exit 2: refused, a bad cursor, an unreadable file or a usage error, and no
trailer is written. An export without its trailer was cut short. A record the
plane quarantined never reached the file. One missing from the middle or the
end of a trail shows as a broken `prev_event_id` link or an unfinished trail,
which a consumer reports as unknown or open, never as completed; a request
whose first record was refused does not appear at all, and a record that
follows a trail's last event, such as a finding, leaves the trail complete.

**The schema major is checked apart from the codec.** The decoder stays a
codec, and `{}` still decodes as an event. A separate check refuses an absent
or malformed `schema_version` and any major other than 1, and admits a higher
minor. The trail reader, the export, the scenario reader and the collector's
receiver call it; the receiver refusing it sends a newer plane's record to
quarantine, loudly. The spool and the collector's open are unchanged. The rule
mirrors `pkg/contract`'s for envelopes, and one table pins both.

**An external consumer.** `examples/evidence-report` reads an export on its
standard input with the generated package `api/gen/go/guardana/control/v1`
alone, groups events by tenant, project and request, follows each request's
`prev_event_id` links, and prints one row per request with its lifecycle:
proposed, decided, held and answered, started, ended or blocked. A broken
lifecycle is unknown and an unfinished one open; either, a gap, a refusal or a
missing trailer exits 1.
A test pins that it imports nothing under `internal/`. The generated package
follows the frozen proto (ADR-0002), which is the compatibility such a
consumer relies on; one outside this module can generate its own from
`api/proto`.

The format goes into `docs/contracts.md` under "The evidence export", with
goldens in `testdata/export/`.

## Security / compatibility impact

The export writes what the file holds, which holds no argument or result
content (ADR-0004); nothing is joined across tenants or sent anywhere. The
format is a public contract from its first release and changes only by a
record. No wire contract changes. The receiver now refuses an event of a major
it does not read, which only a plane of a later major sends.

## Alternatives considered

- **Export messages defined in proto**, in a package of their own. Typed for
  every language, and more generated surface and a compatibility decision
  now, for a format JSON Lines carries.
- **Each export line the trail line itself.** An `Event` has no field saying
  it is one, and a strict reader of events refuses a header.
- **A cursor by line number, or by offset alone.** A line number needs a scan
  from the start; an offset alone can land in the middle of other content
  after a restore.
- **The check in the codec.** It would break the spool's delivery and the
  collector's own open on a record they only carry.
- **An HTTP query endpoint on the plane.** A listener with its own
  authentication, in the request path's process, for a need a file answers.

## Consequences

A consumer depends on a documented format rather than on a terminal or on
`internal/`. The collector's one file stays the store; its growth and rotation
stay the operator's, and a rotated file is read on under its new name until
its end.

## Validation

- Goldens for each record type; fixtures for a partial tail with and without
  a writer, an event of major 2, an unknown field, one id with other content,
  an exact repeat, and a cursor from another file, past the end, off a line
  boundary and after a changed line: each a gap or a refusal as above.
- A property: exports split at any cursor and limit, joined, equal one whole
  export.
- A table of exit codes; the version check's table shared with `pkg/contract`.
- `examples/evidence-report` rebuilds a demo run's allow, deny and approval
  lifecycles from an export, reports a cut chain as unknown, and a
  `go list -deps` test finds no `internal/` package in it.
