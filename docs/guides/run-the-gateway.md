---
title: Run the gateway
summary: Start the MCP gateway in OBSERVE, read its health, classify the tools it sees, and move it to ENFORCE.
type: how-to
covers: [cmd/guardana-gateway/**, adapters/mcp/**, internal/gateway/**, internal/spool/**, adapters/otel/**]
---

# Run the gateway

## When to use this

You have an agent that speaks the Model Context Protocol (MCP) to one or more
servers, and you want every call it makes decided from policy, enforced before
the server sees it, and recorded. Start in `OBSERVE`, which records every call and
enforces nothing, and move to `ENFORCE` once the tools are classified.

The gateway is `experimental`; [status.md](../status.md) says what runs and what
does not. Nothing here is a security boundary yet.

## Prerequisites

- This repository, built: `go build ./cmd/guardana-gateway`.
- A signed policy bundle and the two lines `policy keygen` printed for the key
  that signed it ([write-and-test-a-policy.md](write-and-test-a-policy.md)).
- A directory for the evidence spool, on a disk with room for its budget; the
  gateway does not create it.
- An OpenTelemetry collector that takes OTLP/HTTP logs in JSON. The endpoint is
  required: evidence nobody drains fills the budget, and material calls then
  block.
- To hold calls for a person to answer: two directories the gateway does not
  create, one for the approval records and one for the plane's own hold
  journal. Neither may be the spool's or inside the other.

## Steps

### 1. Write a configuration

Every path in the file resolves against the file's own directory. Every key has
an environment variable under `GUARDANA_CONTROL_`, spelled as the key with its
dots as underscores in capitals, and the variable wins, so a credential or a
per-host path stays out of version control. A key the file names and this build
does not is refused, and so is a variable under that prefix that names no key.
`doctor` prints every key, its value and where it came from, a credential
excepted; [configuration.md](../reference/configuration.md) lists every key.

```yaml
mode: OBSERVE
project_id: orders
tenant_id: acme
environment: dev

listener:
  kind: stateless_http          # 2026-07-28; stateful_http serves 2025-11-25
  address: 127.0.0.1:8080
  principal:
    id: agent-runner            # who the calls are made by; see the note below
  agent:
    id: orders-assistant

policy:
  bundle_id: orders-policy      # the plane serves this bundle and no other
  bundle_file: orders.bundle
  key_id: ed25519-<16 hex digits>   # the two lines policy keygen printed
  public_key: <44 characters of base64>

evidence:
  dir: /var/lib/guardana/spool

export:
  endpoint: http://127.0.0.1:4318/v1/logs
  allow_plaintext: true         # a plaintext collector is a loopback one, and you say so

upstreams:
  - name: orders
    endpoint: http://127.0.0.1:9000/mcp   # a redirect it answers is not followed; the call fails
```

The listener in this build authenticates nobody, so the principal is the one
you configure here and the same for every request on that listener. An
end-user identity bound from a token is not in this build, whatever an approver
claims when answering; [status.md](../status.md) is the inventory.

### 2. Check the configuration before serving

```
guardana-gateway doctor --config gateway.yaml
```

`doctor` prints one line per check, then every bound and where it came from, so
the defaults are read rather than assumed. It serves nothing, and it stops at
the first check it cannot make. A tool no override classifies is printed with
the fingerprint of its definition, which is what step 4 needs. It locks no
approvals directory; that directory's lock and its permissions are checked when
the plane starts. It does open the spool: it locks the evidence directory, cuts
a torn tail and writes a probe file there.

### 3. Run in OBSERVE

```
guardana-gateway run --config gateway.yaml
```

Point the agent at `http://127.0.0.1:8080` instead of the server. In `OBSERVE`
every recordable call runs with the bytes the agent proposed and no obligation
applied: it is how you learn which tools exist before classifying them.

### 4. Classify the tools

A call to a tool the manifest does not classify is blocked with
`ACTION_UNCLASSIFIED` in every mode but `OBSERVE`, so classify each one the
agent needs. Take the fingerprint from `doctor` and add an override:

```yaml
overrides:
  - upstream: orders
    tool: read_order
    fingerprint: <the fingerprint doctor printed>
    effect: READ                # READ, WRITE, DELETE, EXECUTE, COMMUNICATE, TRANSACT, IDENTITY_OR_ACCESS, CONFIGURE, SPAWN_OR_DELEGATE
    resource_type: order
    resource_from: /id          # where in the arguments the resource id sits
```

The fingerprint covers the whole definition: a tool whose description or
schema changes is unclassified again until you look at it.

### 5. Read the health

```
curl -s http://127.0.0.1:8081/healthz | jq
```

The answer carries `mode`, `adapter`, `halted` and a `halt` object, `bundle`
(id, version, digest, serial and when it was last confirmed), `spool` (segments,
bytes, unacknowledged bytes, reserved bytes, open trails, quarantined records
and what a torn write discarded), `exporter` (acknowledged, quarantined, refused
and the retries by class) and `pipeline` (blocks by reason code, pending,
executed, sink failures before and after an effect, unrecorded reads,
mismatches, open and held) and `approvals` (the provider, whether a hold
journal is kept, and what the last reconciliation closed and could not settle)
and `pause`. A plane that takes no material call, or whose pause state is
unknown, answers `503`. `GET /brand` answers what
the product is called.

### 6. Move to ENFORCE

Run `doctor` again with `GUARDANA_CONTROL_MODE=ENFORCE`: outside `OBSERVE` an
unclassified tool is not a pass, so it shows whether the overrides are
complete. Then change the mode and restart.

| Mode | What the plane does with the kernel's decision |
| --- | --- |
| `OBSERVE` | Records; executes every recordable call, unclassified included, unless a pause or halt blocks it; applies no obligation |
| `APPROVE` | Enforces, and holds an allowed material call for an approval. It needs `approvals.provider: file`: nothing outside the process answers a hold kept in memory |
| `ENFORCE` | Enforces the decision: blocks a `DENY` and an `INDETERMINATE`, applies the obligations, holds what needs an approval |
| `LOCKDOWN` | Blocks every material call whatever the policy said, recording a decision; enforces a read with fail-open reads off |
| `SHADOW`, `WARN` | Refused at start: declared in the contract, not built |

### 7. Let a person answer a held call

```yaml
approvals:
  provider: file
  dir: /var/lib/guardana/approvals
  hold_journal_dir: /var/lib/guardana/holds
```

An approver answers with the approval commands
([reference/cli.md](../reference/cli.md)). Write access to `approvals.dir` is
the approval authority, and `--approver-id` is a claim, never authenticated. A
hold lost to a restart is closed on its trail at the next start and never
run, which the journal buys; `doctor` counts them
([concepts/approvals-and-the-held-call.md](../concepts/approvals-and-the-held-call.md)).

### 8. Set the evidence rules on purpose

| Key | What it costs |
| --- | --- |
| `evidence.max_bytes` | The unacknowledged bytes the spool may hold. Reached, an append fails, and a material call is blocked before its effect |
| `evidence.fsync: interval` | A power loss costs the records since the last sync; `evidence.fsync_interval` says how many seconds |
| `evidence.on_unwritable: allow_reads` | A read whose trail the spool will not take runs unrecorded and counted, unless it retries a held call. A material call never does |
| `policy.fail_open_read: true` | A read undecided only because the policy is unavailable runs if the rules would otherwise allow it. Off by default, and forced off under `LOCKDOWN` |
| `policy.max_stale` | Past it, or the bundle's own `maxStaleSeconds`, calls get `POLICY_STALE` until a restart |

### 9. Pause a tool

As the plane's user, before setting `pause.file` and restarting:

```
mkdir -m 700 /srv/pause
guardana-control pause init /srv/pause/pause.json   # a relative path is read from the working directory
```

```yaml
pause:
  file: /srv/pause/pause.json   # a relative path is read from this file's directory
```

```
guardana-control pause add --action tool --provider orders --name refund /srv/pause/pause.json
```

`refund` is blocked with `PAUSED` until `pause remove`
([more](../concepts/enforcement-modes.md#pausing-calls)).

## Verify

- `doctor` ends with `ok upstreams ... 0 unclassified` in the mode you will run.
- A denied call comes back to the agent as a tool result with `isError: true`
  and a `reason_code`, and the upstream never sees it.
- `/healthz` reports `"halted": false` and a spool depth that falls as the
  collector acknowledges records.

## When the plane halts

`"halted": true` and `503` mean the plane takes no material call. The `halt`
object says which rule stopped it:

- `executed_args_mismatch`: something sent bytes that were not the authorized
  ones. This stands until the process is restarted. Read the trail's
  `ACTION_FAILED` record before restarting; it names the digest of what was
  sent.
- `evidence_unwritable`: a closing record could not be written. It lifts when an
  append succeeds, so give the spool room, or let the collector drain it, and
  watch the depth. While a mismatch halt stands it reads `false`, because the
  pipeline reports one flag for both halts and a mismatch needs the restart
  either way; `pipeline.sink_failures_after_effect` counts what happened.

## Roll back

Set `mode: OBSERVE` and restart: no decision blocks a call and the trail
keeps recording. Evidence that cannot be written still blocks a material call,
in any mode. To stop enforcing altogether, point the agent back at the server and
stop the gateway; the spool keeps what it holds, and the exporter drains it on
the next run from the position the collector last acknowledged.
