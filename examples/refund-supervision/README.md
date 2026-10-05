# A refund run, supervised

One agent task, refunding an order, run on a live plane under a run an
operator opened, then checked against its procedure. The plane's trail says
what it decided, the agent's runtime says what it did, and `supervise` turns
both into findings that `notify` hands to a program. Nothing here changes a
decision; [supervision.md](../../docs/reference/supervision.md) is the
reference.

| File | Holds |
| --- | --- |
| `plane.yaml` | The plane: `ENFORCE`, `runs.dir`, file approvals, and the orders server of [vulnerable-mcp-agent](../vulnerable-mcp-agent/) |
| `policy.json` | Order `ord-1` may be read, `ord-2` to `ord-5` are denied, a refund waits for an approval |
| `refund.procedure.json` | Fetch the order, then refund it; four denials of one tool and fifteen minutes are the limits |
| `runtime.source.json` | The agent runtime's spans, self-reported, the run id in `app.run_id` |
| `*_test.go` | Every step below, with its exact commands, on a Unix-like system |

`<gateway>` and `<control>` are the two binaries of a release archive.

1. Sign the policy and fill in `plane.yaml`'s keys as
   [run-the-gateway.md](../../docs/guides/run-the-gateway.md) shows; open a
   run with `<control> runs open`; start a collector and `<gateway> run`.
2. Call `read_order` for `ord-1`, then for `ord-2` to `ord-5` (each denied),
   then `refund`, approve it with `<control> approvals approve` and retry it,
   each call carrying the run token and a `traceparent`.
3. Stop the plane and export its trail with `<gateway> trail export`.
4. Import the runtime's spans, one per call plus an upload to a host the plane
   never saw, with `<control> observe import`.
5. `<control> supervise` finds `REPEATED_DENIAL` confirmed, from the plane's
   trail, and `STEP_OUTSIDE_PROCEDURE` suspected, since the upload rests on the
   runtime's word, and exits 1.
6. `<control> notify --init` hands each alert to the program once; a second
   run hands over nothing.
