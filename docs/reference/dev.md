---
title: Dev mode
summary: What guardana-gateway dev lays out, refuses, prints and stops, and how --scenario runs each scenario on a plane of its own.
type: reference
covers: [cmd/guardana-gateway/dev*.go, internal/loopback/**]
---

# Dev mode

`guardana-gateway dev` starts one plane on your machine from a demo's
configuration and policy document, with the approvals page beside it, and
stops all of it when you interrupt it. It is `experimental`, and it is for
trying the plane out, not for serving anyone
([ADR-0023](../adr/0023-a-local-page-answers-through-the-directory.md)).

```
guardana-gateway dev --config <file> --policy <file> [--state <dir>] [--scenario <file>]...
```

It needs `guardana-control` of the same version in the same directory as
itself, as `go install ./cmd/...` puts them; it never looks one up on the
search path. It builds the plane the way `run` does: no key and no branch
exists for dev alone.

## What it refuses

Before it creates or binds anything, dev refuses, with status 1:

- any `GUARDANA_CONTROL_*` variable in its environment, named without its
  value: dev reads none of them, and refuses one left set so that nobody
  believes it applies;
- a `--state` that exists;
- a policy document it cannot read or sign;
- a configuration that sets a key dev owns: `listener.address`,
  `health.address`, `policy.bundle_file`, `policy.key_id`,
  `policy.public_key`, `pause.file`, `evidence.dir`, `export.endpoint`,
  `export.allow_plaintext` and every `approvals` key;
- a `stdio` listener, and any address that is not an IP literal on the
  loopback, a host name included: its own listeners and collector, every
  `upstreams[].endpoint`, and `pdp.identifier`, `pdp.evaluation_endpoint`
  and `pdp.proxy`;
- a `guardana-control` beside it that is missing or of another version.

Dev's own values reach the loader the way environment variables do, over
the file, and nothing else parses the file. A key the file sets to the value
it has anyway may not be seen, and dev's own value applies.

## What it lays out

`--state`, or a new directory under the system's temporary directory, mode
`0700`, holding everything the plane writes: `approvals/`, `holds/` and
`spool/`, `trail.jsonl`, which a collector inside dev appends to,
`policy.bundle`, `pause.json`, made by `guardana-control pause init`, and
`settings.txt`, every scalar key the plane resolved. The directory is left
behind when dev stops.

The bundle is signed under an Ed25519 key made in dev's memory for that one
bundle. The key is never written, and dev clears its own copy once the
bundle is signed; copies the standard library makes while signing are not
cleared. The plane pins the public half.

Dev binds `127.0.0.1` at ports the system picks, for the collector, the
agents and the health answers, and hands the bound sockets to the plane, so
no port is chosen and then taken by someone else. The agents' and the health
listeners take no credential, as `run`'s do: any account on the machine, and
any tool behind the plane, can call tools as the listener's principal.

A demo's `upstreams[].command` runs as you. Run only demos you trust.

## What it prints

One `name: value` line each, on stdout: `mcp`, the address an agent calls;
`healthz` and `metrics`; `page`, the line `guardana-control console` printed
([console.md](console.md)); every path above; `mode`, `bundle`, `digest` and
`key_id`; `stale_at`; and the commands that read the trail, list the
approvals, pause every call and run a scenario. The page's token reaches
nothing but that one line: no argument, variable, file or log of dev's.

The plane installs its bundle once. From `stale_at`, the smaller of the
document's `maxStaleSeconds` and `policy.max_stale` after the start, every
decision carries `POLICY_STALE` and the plane blocks the call as its mode
blocks an undecided one, until dev is started again; nothing reloads the
bundle.

The plane's own log, and the page's, go to stderr.

## How it stops

An interrupt, or any part stopping on its own, stops every part in order:
the plane's listeners, then up to five seconds for the exporter to ship what
the spool holds, then the collector, then the page, whose input dev closes.
Dev then prints `unshipped:`, the bytes the collector never acknowledged,
which the trail file lacks; it never says the trail is complete. It exits 0
after an interrupt and 1 when a part stopped on its own, the page included,
whatever the page's status. Killed outright, dev leaves the page to stop
within two seconds, when its input ends.

## Scenarios

With `--scenario`, once per file, dev reads every file first and then runs
each on a plane of its own: a new state directory (a numbered one under
`--state` when given), a new plane, no page, stopped once the scenario ends.
Each line is the runner's ([scenario-format.md](scenario-format.md)), after
one line naming the scenario's state directory. It exits 1 when any
scenario differed, else 2 when any could not run or its plane stopped, else
0.
