# A refund run, supervised and stopped

An operator opens a run for an agent's refund on a live plane; the run is
checked against its procedure and stopped on a confirmed finding.
[supervision.md](../../docs/reference/supervision.md) and
[reaction.md](../../docs/reference/reaction.md) are the references.

| File | Holds |
| --- | --- |
| `plane.yaml` | The plane: `ENFORCE`, `runs.dir`, file approvals, a route, and the orders server of [vulnerable-mcp-agent](../vulnerable-mcp-agent/) |
| `policy.json` | Order `ord-1` may be read, `ord-2` to `ord-5` are denied, a refund waits for an approval |
| `refund.procedure.json` | Fetch the order, then refund it; four denials of one tool are the limit |
| `route.json` | A confirmed `REPEATED_DENIAL` of this procedure may stop a run |
| `runtime.source.json` | The agent runtime's spans, self-reported |
| `*_test.go` | Every step below, with its exact commands, on a Unix-like system |

`<gateway>` and `<control>` are a release archive's two binaries.

1. Sign the policy and fill in `plane.yaml`'s keys as
   [run-the-gateway.md](../../docs/guides/run-the-gateway.md) shows. Put a
   lift key's line into `route.json`, sign it with `<control> route sign`
   under a route key, and make the route floor and stop list with
   `<control> policy state init --kind route` and `<control> stops init`,
   each where `plane.yaml` names it.
2. Open two runs with `<control> runs open`; start a collector and the plane.
3. Under the first run, read `ord-1`, then `ord-2` to `ord-5` (denied), then
   refund, approve and retry.
4. Export the plane's trail with `<gateway> trail export` while it runs, and
   import the runtime's spans, one an upload the plane never saw, with
   `<control> observe import`.
5. `<control> supervise` finds `REPEATED_DENIAL` confirmed and
   `STEP_OUTSIDE_PROCEDURE` suspected, resting on the runtime's word.
6. `<control> react` writes one stop, of the first run. Within a poll
   interval its next call is refused `RUN_STOPPED`, and the second run's calls go on.
7. `<control> stops lift`, with the lift key, ends the stop.
8. `<control> notify --init` hands each alert to a program once.
