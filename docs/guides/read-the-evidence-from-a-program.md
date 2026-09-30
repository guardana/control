---
title: Read the evidence from a program
summary: Export a trail file in the versioned format, resume where the last export stopped, and rebuild each call's lifecycle without importing the project's internals.
type: how-to
covers: [internal/trailfile/export.go, internal/trailfile/export_records.go, internal/trailfile/cursor.go, cmd/guardana-gateway/trail.go, examples/evidence-report/**]
---

# Read the evidence from a program

## When to use this

A plane's evidence lands in a trail file, one event per line, written by
`guardana-gateway collect` ([watch a plane without a
collector](watch-a-plane-without-a-collector.md)). A report, an alert or a
dashboard of your own should read it through the export, which is a
versioned format with a cursor, a record for every line it could not read, and
a trailer that says whether it reached the end
([ADR-0035](../adr/0035-a-versioned-evidence-export-and-a-bounded-query.md)).
The format is `experimental`, and [contracts.md](../contracts.md#the-evidence-export)
states it member by member.

## Prerequisites

- A trail file: the one `collect` writes, or the demo's `<state>/trail.jsonl`
  ([try the demo](../get-started/try-the-demo.md)).
- For step 3, a checkout of the repository and Go, to build the example.

## Steps

### 1. Export the file

```
guardana-gateway trail export trail.jsonl > export.jsonl
echo $?
```

Each line of `export.jsonl` is one JSON object with a `type`: a `header`, one
`event` per event, a `gap` for a line that is not one readable event or reuses
an event id with other content, a `duplicate` for a line repeated exactly, and
a `trailer`. An event record carries the file's line as it is, never encoded
again. The exit status is 0 when nothing was refused and nothing is missing, 1
when the export is whole but holds a gap or a conflicting id, and 2 when it was
refused, with no trailer written. An export with no trailer was cut short.

### 2. Resume where the last one stopped

The trailer's `next_cursor` names the end of the last line read. Pass it to the
next export:

```
guardana-gateway trail export --after '<next_cursor>' trail.jsonl
```

A cursor is refused when the file's first line or the line it names has
changed, or when its offset is not at the end of a line: another file, or one
restored and written again since. A line changed elsewhere is not detected. When the trailer's `end_reached` is false,
more remains: follow `next_cursor` until it is true. `--limit` bounds the
records one export writes (1000 by default) and `--max-bytes` the bytes of
whole lines it scans.

### 3. Rebuild each call's lifecycle

`examples/evidence-report` reads an export on its standard input with the
generated package `api/gen/go/guardana/control/v1` alone, groups the events by
tenant, project and request, follows each request's `prev_event_id` links and
prints one row per request: its decision, whether it was held and how the
approval ended, and how the call ended.

```
go build -o bin/ ./examples/evidence-report
guardana-gateway trail export trail.jsonl | bin/evidence-report
```

It exits 1 when any request's chain is broken or unfinished, when a call ran
against its decision under an enforcing mode, when a result says the effect
may have happened, or when the export held a gap, was cut or held no request:
missing evidence is reported as unknown or open, never as completed. It does
not read effect classes, so it cannot tell whether a material call ran
without the approval `APPROVE` asks for, or under `LOCKDOWN`.
Export the whole file, or one request at a time, to rebuild lifecycles; a
filter on kinds cuts the links between a request's events.

## Verify

The export's last line is a `trailer` with `end_reached` true, and both
commands exit 0. Any other status names what is missing: read the trailer's
counts and the report's rows before trusting the rest.

## What it does not see

- A record the plane could not deliver stays in its spool's quarantine and
  never reaches the trail file. Missing from the middle or the end of a trail,
  it shows as a broken link or an unfinished trail; a request whose first
  record was refused does not appear at all.
- Duplicates are found within one export. Across exports, drop an event you
  have seen by tenant, project and event id, and read one id with two
  different lines as a gap.
- The export reads what the file holds, which holds no argument or result
  content ([privacy](../concepts/privacy.md)).
