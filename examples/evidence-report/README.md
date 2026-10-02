# An evidence report from the export

A program outside the plane that reads an export on standard input, follows
each request's `prev_event_id` links and prints a row per request, then
totals: the action, the kernel's verdict and reason codes, the block, whether
the call was held, and how its approval and action ended. A module of its
own, it imports only the generated wire contract, as a test pins.
[Read the evidence from a program](../../docs/guides/read-the-evidence-from-a-program.md)
shows it in use.

`block` is the decision `ACTION_BLOCKED` carries, never in place of the
kernel's: `=` when its `decision_id` is the kernel's, else the block's own
verdict and codes, as `DENY PAUSED`; `-` if none.

```
go -C examples/evidence-report build -o "$PWD/bin/" .
<gateway> trail export <state>/trail.jsonl | bin/evidence-report
```

It exits 0 only when it read a request, each ended on an unbroken chain the
plane's validator allows, and the export was whole: trailer read, end
reached, no gap, conflicting or refused record, every record strictly formed.
A request stopped before starting is `blocked`; one that started is
`completed` only on success, `aborted` when nothing was sent, `failed` on a
failure or timeout, otherwise `unknown`: the effect may have happened. Under
an enforcing mode, a run after any verdict but an allow is `unknown`, unless
`REQUIRE_APPROVAL` was answered yes. Anything unknown, open or missing exits
1; a usage error exits 2.

## What it cannot see

- A record the plane quarantined never reaches the file: inside a chain it
  shows as `unknown` or `open`, after a trail's end it leaves the row
  complete, and a request whose first record was refused does not appear.
- `prev_event_id` orders records; it does not show one altered.
- It reads no effect class, so under `APPROVE` or `LOCKDOWN` a material run
  is not told from a read.
- An export cut by `--after`, `--limit` or `--kind` can split a request.
  Export the whole file, or by request.
