---
title: Try the demo
summary: Start a plane in front of two vulnerable MCP servers, answer a held call, pause a server, read the trail and the counters, and run the demo's scenarios.
type: tutorial
covers: [examples/vulnerable-mcp-agent/**]
---

# Try the demo

You start a plane on your machine in front of two MCP
servers an agent should not be trusted with, make the calls an agent would,
answer one that is held for a person, pause a server, and read what the plane
recorded. Then you run the demo's seven scenarios, the agent's script as data,
and make one fail on purpose.

Everything here is `experimental` ([status.md](../status.md)) and runs on the
loopback only. It is a demo, not a security boundary. What the demo holds is
in [examples/vulnerable-mcp-agent](../../examples/vulnerable-mcp-agent/README.md).

## What you need

- This repository, at the Go toolchain `go.mod` pins.
- A Unix-like system, `curl` and a browser.
- Port `127.0.0.1:18213` free: the orders server answers there as a decision
  point that never decides. If something else holds it, the orders server
  cannot start, and `dev` stops with an error that ends in `EOF`.

## 1. Build

From the repository's root:

```
go build -o bin/ ./cmd/guardana-gateway ./cmd/guardana-control ./examples/vulnerable-mcp-agent
```

This puts three programs in `bin/`: the gateway, the approver's
`guardana-control`, which `dev` needs beside the gateway, and
`vulnerable-mcp-agent`, the two servers the demo's configuration starts from
there.

## 2. Start the plane

```
bin/guardana-gateway dev --config examples/vulnerable-mcp-agent/demo.yaml --policy examples/vulnerable-mcp-agent/policy.json
```

`dev` signs the policy under a key that lives only in its memory, lays out a
new state directory, and starts a collector, the plane and the approvals page
([reference/dev.md](../reference/dev.md)). The plane starts both servers as
its upstreams. Once it is up it prints one `name: value` line per part,
among them:

```
mcp: http://127.0.0.1:<port>
metrics: http://127.0.0.1:<port>/metrics
page: http://127.0.0.1:<port>/#t=<token>
approvals: <state>/approvals
trail: <state>/trail.jsonl
trail_command: guardana-gateway trail <state>/trail.jsonl
```

Leave it running and open a second terminal in the repository's root. Put
the `mcp` address in a variable:

```
MCP=http://127.0.0.1:<port>
```

## 3. Make a call

`call.sh` makes one MCP `tools/call`, as an agent does, and prints the
answer.

```
examples/vulnerable-mcp-agent/call.sh "$MCP" read_order '{"id":"ord-1"}'
```

The policy allows reads, so the order comes back. The answer's `_meta` names
the `request_id` and `decision_id` the plane recorded for it.

## 4. Answer a held call

The policy holds every update for a person:

```
examples/vulnerable-mcp-agent/call.sh "$MCP" update_order '{"id":"ord-1","status":"cancelled"}'
```

The answer is marked `pending`, with the text `APPROVAL_PENDING`, an
`approval_id` and when it expires. Nothing reached the orders server.

Open the `page` link in your browser within ten minutes of the start; the
link works once. Under **Held calls** the update waits. Press **Approve**,
check that the confirmation names `update_order` on `orders`, and press
**Yes, approve**.

Now make the same call again, with the same arguments. It runs, and its
`request_id` is the held one's: the retry resumed the held request. An
approval covers one exact call: the same update with other arguments would
be held as a request of its own.

The page writes what `guardana-control approvals approve` writes. Without a
browser, answer from the terminal instead, with the `approvals` path and the
`approval_id`:

```
bin/guardana-control approvals approve --approver-id you <state>/approvals <approval_id>
```

## 5. Pause the orders server

On the page, under **Pauses**, choose **every call to one upstream**, type
`orders`, give a reason and press **Pause**. Once the plane has read its pause
file, which the demo has it do every half second, the read

```
examples/vulnerable-mcp-agent/call.sh "$MCP" read_order '{"id":"ord-1"}'
```

is blocked with `PAUSED`, although the policy allows it. Press **Lift this
pause** and the same call runs again.

## 6. Read the trail and the counters

The plane's evidence went through its spool to the collector, which appends
it to the trail file. Run the `trail_command` line:

```
bin/guardana-gateway trail <state>/trail.jsonl
```

It prints one line per request, the kind of its last event, and whether its
chain holds. The counters need no collector:

```
curl -s http://127.0.0.1:<port>/metrics
```

Among them are the calls the plane executed, the ones it held, and its blocks
by reason code.

Stop `dev` with Ctrl-C. It stops every part, gives the exporter a moment to
ship what the spool holds, and says how much it did not. The state directory
stays. Stop it before the next step: only one demo can hold the decision
point's port.

## 7. Run the scenarios

Each scenario is a JSON file of calls and operator steps, and for each call
the answer, the decision and the trail the plane must produce
([reference/scenario-format.md](../reference/scenario-format.md)). With
`--scenario`, `dev` runs each one on a plane of its own:

```
bin/guardana-gateway dev --config examples/vulnerable-mcp-agent/demo.yaml --policy examples/vulnerable-mcp-agent/policy.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/allow.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/deny.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/approval.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/digest-invalidation.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/fail-closed.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/pause.json \
  --scenario examples/vulnerable-mcp-agent/scenarios/toxic-flow.json
```

Every step prints `ok`, every scenario `passed`, and `dev` exits 0. What each
one shows:

| Scenario | The plane |
| --- | --- |
| `allow` | runs a read |
| `deny` | blocks a refund with `RULE_DENY` before the orders server sees it |
| `approval` | holds an update, and resumes it once approved |
| `digest-invalidation` | holds anew an approved update retried with other arguments, then resumes the original |
| `fail-closed` | blocks an export `INDETERMINATE` with `PDP_TIMEOUT` when the decision point never answers |
| `pause` | blocks a read while the orders server is paused, runs it once the pause is lifted |
| `toxic-flow` | after an untrusted page, blocks any mail, since `send_mail`'s destination is declared untrusted: undetermined before an order was read, `TOXIC_FLOW_SENSITIVE_TO_EXTERNAL` after |

In `toxic-flow` the page asks the agent to mail an order away, and no rule
names `fetch_page`: the plane blocks the mail because the page's result is
declared untrusted and the mail's destination is too
([ADR-0021](../adr/0021-a-run-carries-what-it-took-in.md)). To see that the
mail never reached the web server, give the servers a directory for their
journals:

```
journal=$(mktemp -d)
VICTIM_JOURNAL_DIR="$journal" bin/guardana-gateway dev --config examples/vulnerable-mcp-agent/demo.yaml --policy examples/vulnerable-mcp-agent/policy.json --scenario examples/vulnerable-mcp-agent/scenarios/toxic-flow.json
cat "$journal/web.jsonl"
```

It holds the `fetch_page` call and no `send_mail`.

## 8. Make one fail

A scenario is only worth something if it fails when the plane does something
else. Copy one with its expected verdict changed; the file's name is the
scenario's id, so keep it:

```
mutant=$(mktemp -d)
sed 's/"verdict": "ALLOW"/"verdict": "DENY"/' examples/vulnerable-mcp-agent/scenarios/allow.json > "$mutant/allow.json"
bin/guardana-gateway dev --config examples/vulnerable-mcp-agent/demo.yaml --policy examples/vulnerable-mcp-agent/policy.json --scenario "$mutant/allow.json"
echo $?
```

It prints `allow.json step[0].decided.verdict: want DENY, got ALLOW`, then
`allow.json failed`, and exits 1. An exit of 2 means a scenario could not
run, which is never a pass.

## Where next

- [guides/write-and-test-a-policy.md](../guides/write-and-test-a-policy.md) to
  change what the plane decides.
- [guides/run-the-gateway.md](../guides/run-the-gateway.md) to run a plane of
  your own with `run`, which `dev` builds the same way.
