# An evidence report from the export

A program outside the plane that reports what agents did. It reads an
evidence export on standard input, follows each request's `prev_event_id`
links, and prints one row per request: the action, the verdict and reason
codes, whether the call was held and how the approval ended, and how the
action ended, then a totals line. It imports only the wire contract's
generated package, which a test pins. The guide
[read the evidence from a program](../../docs/guides/read-the-evidence-from-a-program.md)
shows it in use.

```
go build -o bin/evidence-report ./examples/evidence-report
<gateway> trail export <state>/trail.jsonl | bin/evidence-report
```

It exits 0 only when at least one request was read, each ended on an unbroken
chain the plane's chain validator allows, and the export was whole: trailer
read, end reached, no gap, conflicting or refused record, every record
strictly formed. A request is `completed` only on a successful result;
`aborted` when nothing was sent; `failed` on a failure or a timeout; any other
result is `unknown`, since the effect may have happened. Under a mode that
enforces, an action that ran after a verdict other than an allow is `unknown`
unless the verdict was `REQUIRE_APPROVAL` and the approval was answered yes.
Anything unknown, open or missing exits 1; a usage error exits 2.

## What it cannot see

- A record the plane quarantined never reaches the file: inside a chain it
  shows as `unknown` or `open`, after a trail's end it leaves the row
  complete, and a request whose first record was refused does not appear.
- `prev_event_id` orders records; it does not show one altered.
- It does not read effect classes, so under `APPROVE` or `LOCKDOWN` a
  material run is not told from a read.
- An export cut by `--after`, `--limit` or `--kind` can split a request.
  Export the whole file, or by request, to rebuild lifecycles.
