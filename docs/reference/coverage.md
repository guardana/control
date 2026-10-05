---
title: Coverage
summary: What guardana-control coverage reads, the inventory it takes, the state it gives each declared path, the join with a plane's evidence, and its exit status.
type: reference
covers: [internal/coverage/**, cmd/guardana-control/coverage.go, internal/observelog/readfile.go]
---

# Coverage

`guardana-control coverage` says, for each path an operator declares, whether
a plane enforces it, decides it without enforcing it, a source observed it,
nobody can tell, or nothing covers it. It reads files and contacts no plane;
no plane reads what it prints
([ADR-0041](../adr/0041-coverage-per-declared-path.md),
[ADR-0044](../adr/0044-coverage-judges-each-plane-and-joins-only-a-whole-export.md)).
It is `experimental`.

```
guardana-control coverage --inventory <file> [--plane <config> [--evidence <export>]]...
    [--source <descriptor> --log <dir>]...
```

## Inputs

| Flag | What it reads |
| --- | --- |
| `--inventory` | the operator's inventory below, once; a file another account or a group can write is refused |
| `--plane` | a plane's configuration, read without the environment of the shell running the command, which is not the plane's |
| `--source`, `--log` | a source descriptor and its observation log's directory, paired in order ([observations](observations.md)) |
| `--evidence` | the evidence export of the `--plane` before it, as `guardana-gateway trail export` writes it ([contracts](../contracts.md#the-evidence-export)); at most one per plane, refused when another account or a group can write it |

A descriptor that is absent turns its paths not covered, never observed. A log
directory without a log is a source never heard. Any other input that cannot
be read or does not parse exits 2, and no map is printed.

## The inventory

A strict JSON document: an unknown member, a repeated member or an empty list
of paths is refused.

```json
{"schema_version": "0.1", "paths": [
  {"id": "orders.read-file", "kind": "mcp_tool", "upstream": "orders", "tool": "read_file",
   "sources": [{"source_id": "agent-runtime", "name": "read_file", "server_address": "files.example"}]}
]}
```

| Member | Meaning |
| --- | --- |
| `id` | unique, 1 to 64 bytes of `a-z`, `0-9`, `.`, `_`, `-` |
| `kind` | `mcp_tool` with `upstream` and `tool`, one path per pair; `egress` with `host`; `http_api` or `process` with `name` |
| `sources` | each a `source_id` that would see the path, the `name` its observation carries and, optionally, its `server_address` |

An observation matches a path when it comes from that source, is of a tool,
and carries that name and, when given, that server address.

## States

Each path gets the first state that holds:

| State | When |
| --- | --- |
| enforced | every plane that counts for the tool is in `ENFORCE`, `APPROVE` or `LOCKDOWN`, and none lets the read fail open |
| decided, not enforced | a plane counts for it in `OBSERVE`, or under `policy.fail_open_read` outside `LOCKDOWN`, where a read runs undecided while the policy is unavailable |
| observed | a source live within its heartbeat holds a matching observation; the weakest trust of the descriptor and the observations is printed |
| unknown | a source the path names is past its heartbeat or was never heard |
| not covered | nothing above |

Only an `mcp_tool` can be enforced or decided. A plane counts for a tool when
it lists the tool's upstream: with an override that classifies the tool, or
without one, when a call to it is unclassified, blocked in a mode that
enforces and let run in `OBSERVE`. A plane in `SHADOW` or `WARN` refuses to
start and counts for nothing. A classification is the configuration's
override; the live definition behind its fingerprint is not checked. A source is last heard at the newest event time an import report
covers, never later than that report's receive time; one heard after the
command's clock is unknown.

## The join

Beside an enforced or decided path, each matching observation is checked
against the exports of the planes that count for it. It is joined when it, or
an observation below it in the same trace, has the span id of a proposal for
that tool on that upstream, in the same tenant and project, recorded in a mode
at least as strong as the path's state. A call every such plane's export could
have shown and none does is printed as a call around the plane. The check is
printed as not checked, with the reason, when:

- a plane that counts has no export, or its export is not whole: cut short,
  not at its end, with a gap, or filtered by a cursor or a query;
- the observation has no trace or span id, or no event time;
- its time is outside the span an export's events cover;
- the path names no source.

## Output and exit status

One line per declared path, its state and what it rests on, then indented
lines for each plane, each source and the joins, and last the line
`undeclared paths: unknown`.

| Exit | When |
| --- | --- |
| 0 | every declared path is at least observed and no call around the plane was found |
| 1 | a path is unknown or not covered, or a call around the plane was found |
| 2 | an input was refused; nothing is printed on standard output |
