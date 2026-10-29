---
title: Egress access log
summary: The descriptor of an egress proxy's access log, how its lines map to connection observations, what is never read, each refusal, and the trust it claims.
type: reference
covers: [internal/ingest/egresslog/**, internal/observe/kinds.go, internal/observe/connection.go, internal/ingest/ingest.go]
---

# Egress access log

A source of kind `egress-access-log` is an egress proxy's access log, written
as JSON lines. Each line becomes one connection observation: the host an agent
reached, as the proxy saw it, not as the agent reported it. It is
`experimental`; [status.md](../status.md) is the inventory and
[ADR-0069](../adr/0069-an-egress-proxys-access-log-is-an-independent-sensor-on-a-kind-neutral-descriptor.md)
the record. [Watch an egress proxy](../guides/watch-an-egress-proxy.md) sets
one up.

## The descriptor

A descriptor of this kind is schema `0.2` and carries the members every kind
shares ([observations](observations.md#the-source-descriptor)) and one of its
own, `egress_access_log`. It may not carry `convention`, `otlp_version`,
`select` or `run_attribute`.

```json
{"schema_version": "0.2", "source_id": "egress-envoy", "kind": "egress-access-log",
 "trust": "TRUST_INDEPENDENT", "sampling": "SAMPLING_COMPLETE", "heartbeat_seconds": 300,
 "tenant_id": "tenant-a", "project_id": "project-a",
 "egress_access_log": {"format": "json-lines",
   "fields": {"time": "start_time", "time_format": "TIME_FORMAT_RFC3339",
              "destination": "server_name", "port": "upstream_remote_port"}}}
```

| Member | Required | Value |
| --- | --- | --- |
| `format` | yes | `json-lines`, the one format |
| `fields.time`, `fields.destination` | yes | the member of a line that holds the time and the host |
| `fields.time_format` | yes | `TIME_FORMAT_RFC3339`, `TIME_FORMAT_UNIX_SECONDS` or `TIME_FORMAT_UNIX_MILLISECONDS` |
| `fields.port`, `method`, `status`, `bytes_sent`, `bytes_received`, `caller` | no | the member that holds each value |
| `caller_basis` | when `caller` is mapped, and only then | `CALLER_BASIS_NONE`, `CALLER_BASIS_ADDRESS` (the address the connection came from) or `CALLER_BASIS_AUTHENTICATED` (the proxy authenticated the caller) |

A mapped name is a top-level member of the line, never a path into it. The
names are distinct, at most 128 bytes (`MaxMemberNameBytes` in
`internal/ingest/egresslog/descriptor.go`), and hold the text rules every
descriptor string keeps. The caller's basis comes from the descriptor, never
from the data.

## What a line becomes

| Observation field | From the line |
| --- | --- |
| `subject` | kind `SUBJECT_KIND_CONNECTION`, name the destination host |
| `event_time` | the mapped time; a time after the import's clock is kept as read |
| `connection.port` | 1 to 65535; absent or unset is 0, read as unknown |
| `connection.method` | kept only when it is one of `CONNECT`, `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `TRACE`, exactly as spelled |
| `connection.http_status` | 100 to 599, or 0, which the proxy writes when it has none |
| `connection.bytes_sent`, `bytes_received` | an unsigned 64-bit count |
| `connection.caller`, `caller_basis` | the caller as the proxy wrote it, at most 256 bytes (`observe.MaxTextBytes`), and the descriptor's basis; with no caller the basis is `CALLER_BASIS_NONE` |
| `stage` | `STAGE_INDIRECT` |
| `correlation` | basis `BASIS_NONE`, with no trace, span or run: a connection names no run |

A port, status or count may be written as a JSON number or a decimal string,
never with a sign, a fraction or an exponent. `null`, `""` and `-` say nothing: an unset
optional value is left empty and counts as nothing. An RFC 3339 time needs its
zone. Unix seconds take at most nine fractional digits; Unix milliseconds take
none.

The host is lowercased. A name is held to labels of 1 to 63 letters, digits
and hyphens, not starting or ending with a hyphen, at most 253 bytes in all
(`observe.MaxHostBytes`), with no trailing dot. An A-label (`xn--`) is taken as
written; a name in Unicode is refused, since this build carries no IDNA
tables. An IPv4 address is a dotted quad; an IPv6 address is written in its
canonical form, without brackets or a zone.

## What is never read

A line is checked to be valid JSON, and only the mapped members are decoded;
every other value is stepped over unread. A path, query, header, user agent or
body the proxy also logged is never copied unless the descriptor maps its
member to one of the fields above (invariant 9). A destination that is a URL,
or holds a port, a path, a query, a fragment, user information or whitespace, is
refused whole and never parsed down to its host: mapping a URL member is the
operator's mistake, and guessing the host from it would hide that.

## Refusals and drops

A refused line becomes no observation. It is counted in `refused`, and under
its reason in the report's `refused_by`. `observe import` prints `lines_read`
and the total refused, not each reason, and any refusal makes it exit 1.

| `refused_by` key | The line |
| --- | --- |
| `line_too_long` | is over 64 KiB (`MaxLineBytes` in `internal/ingest/egresslog/lines.go`); it is never held in memory, and the next line is read whole |
| `not_an_object` | is not one JSON object, or not UTF-8 |
| `duplicate_member` | names a member twice, mapped or not, however the name is escaped |
| `time` | has no time, or one its format cannot read |
| `destination` | has no destination, or one that is not a host alone |
| `port` | has a port that is not a number from 1 to 65535 |

A malformed optional value, such as a method off the list, a status outside
its range or a caller with a control character, is dropped and counted in
`strings_dropped`, and the line is kept. Bytes after the last newline are not
a line: the proxy may still be writing them, so they are neither read nor
refused, and the next import reads them.

A refused line names no run, so the report sets `every_run_doubted`. That
weighs on nothing: `supervise` refuses a source of this kind
([supervision](supervision.md)).

## Identity and the log

An observation's id is `obs-` and the first 32 hex digits of SHA-256 under
the domain `observation-id:connection:1`, over the tenant, project, source,
event time, host, port, method, status, both byte counts and the caller, each
prefixed by its length (`ConnectionID` in `internal/observe/connection.go`).
Importing a file again, grown or rotated, writes only what is new; two
connections alike in all of these are one duplicate. Trust and the caller's
basis are not in the id, so a connection imported again after the descriptor
changed either is a conflict. Every connection observation is record schema
`0.3`, and so is a report with `refused_by`
([versions](versions.md)).

## Trust

This kind may declare `TRUST_INDEPENDENT` with an unsigned descriptor. It is
the operator's statement that the agent can write neither the log nor the
descriptor; nothing here checks which account writes either. Under it,
`observe import` refuses an input the group or others can write, as it
refuses such a descriptor under any trust. `coverage` prints the trust as
`independent (unsigned descriptor)`.

A forged line can make a destination look reached. A deleted line makes one
look unreached, which shows as not observed, never as covered. A finding that
rests on this trust needs a signed descriptor before continuous supervision
uses it.
