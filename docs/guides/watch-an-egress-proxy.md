---
title: Watch an egress proxy
summary: Have Envoy or Squid log each connection as a JSON line, import the log as observations, and see in the coverage map which hosts agents reached.
type: how-to
covers: [internal/ingest/egresslog/**, cmd/guardana-control/observe.go, cmd/guardana-control/coverage.go, internal/coverage/undeclared.go]
---

# Watch an egress proxy

An agent's own traces say what it chose to report. When its outbound traffic
goes through a proxy it cannot reconfigure, the proxy's access log says where
that traffic went. This page has the proxy write that log as JSON lines,
imports it, and runs `guardana-control coverage` over it, so each host agents
reached is either a declared path or a row of its own. The importer is
`experimental`; [status.md](../status.md) is the inventory and
[egress access log](../reference/egress-access-log.md) the reference.

## Prerequisites

- `guardana-control` on your `PATH`, built with `go build ./cmd/guardana-control`,
  and a shell at the repository root, where the fixtures below live.
- For a real proxy: an account the agent does not run under that owns the
  proxy's log and the descriptor. Nothing checks this; declaring the source
  `TRUST_INDEPENDENT` states it.

## Steps

### 1. Have the proxy write JSON lines

The member names are yours to choose; the descriptor in step 2 names them.
These are the formats the fixtures in `internal/ingest/egresslog/testdata/`
were written under.

Envoy, with a file access logger and `json_format`:

```yaml
access_log:
- name: envoy.access_loggers.file
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
    path: /var/log/envoy/egress.jsonl
    log_format:
      json_format:
        start_time: "%START_TIME%"
        server_name: "%REQUESTED_SERVER_NAME%"
        upstream_remote_port: "%UPSTREAM_REMOTE_PORT%"
        method: "%REQ(:METHOD)%"
        response_code: "%RESPONSE_CODE%"
        bytes_sent: "%BYTES_SENT%"
        bytes_received: "%BYTES_RECEIVED%"
        downstream_remote_address: "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%"
        path: "%REQ(:PATH)%"
        user_agent: "%REQ(USER-AGENT)%"
```

Do not map `%REQ(:AUTHORITY)%` as the destination: it carries a port, and a
destination that is not a host alone is refused.

Squid has no JSON encoding, so the `logformat` writes the braces and quotes:

```
logformat egress_json {"time":%ts.%03tu,"client":"%>a","user":"%ul","method":"%rm","host":"%>rd","port":"%>rP","status":"%>Hs","reply_bytes":"%<st","request_bytes":"%>st","url":"%ru"}
access_log daemon:/var/log/squid/egress.jsonl logformat=egress_json
```

Every value but the time is quoted, because Squid writes `-` for a value it
does not have, and a bare `-` is not JSON. A value that holds a quote or a
backslash still breaks its line, which the import then refuses. Both formats
also log a path or a URL, which no mapping names and the import never reads.

### 2. Describe the source

The descriptor maps each value to its member. The Envoy fixture's:

```json
"egress_access_log": {
  "format": "json-lines",
  "fields": {"time": "start_time", "time_format": "TIME_FORMAT_RFC3339",
             "destination": "server_name", "port": "upstream_remote_port",
             "method": "method", "status": "response_code",
             "bytes_sent": "bytes_sent", "bytes_received": "bytes_received",
             "caller": "downstream_remote_address"},
  "caller_basis": "CALLER_BASIS_ADDRESS"}
```

`envoy.descriptor.json` holds the whole file, with `trust: TRUST_INDEPENDENT`.
`squid.descriptor.json` reads `%ts.%03tu` as `TIME_FORMAT_UNIX_SECONDS` and
the authenticated user `%ul` as a caller with `CALLER_BASIS_AUTHENTICATED`.
The descriptor must be a file of this account that the group and others
cannot write. Copy the fixtures into a directory of their own:

```sh
mkdir -m 700 egress egress/log
cp internal/ingest/egresslog/testdata/envoy.descriptor.json internal/ingest/egresslog/testdata/envoy.jsonl egress/
chmod 600 egress/envoy.descriptor.json egress/envoy.jsonl
```

### 3. Import the log

```sh
guardana-control observe import --source egress/envoy.descriptor.json --log egress/log egress/envoy.jsonl
```

```text
lines_read: 3
observed: 2
written: 2
duplicate: 0
conflict: 0
refused: 1
strings_dropped: 0
guardana-control: observe import: 1 span(s) or line(s) refused and 0 observation(s) conflicting with the log; the import report holds the counts
```

It exits 1: the third line's `server_name` is `-`, so it has no destination.
The same command again writes nothing new and counts the two connections as
duplicates, so importing a growing or rotated log again is safe.
Under `TRUST_INDEPENDENT`, an input the group or others can write is refused.

### 4. Map coverage

Declare the hosts agents may reach as `egress` paths, each naming the source
that sees it:

```sh
cat > egress/inventory.json <<'EOF'
{"schema_version": "0.1", "paths": [
  {"id": "egress.api", "kind": "egress", "host": "api.example.com",
   "sources": [{"source_id": "egress-envoy", "name": "api.example.com"}]}
]}
EOF
guardana-control coverage --inventory egress/inventory.json --source egress/envoy.descriptor.json --log egress/log
```

```text
egress.api unknown: sources egress-envoy; no plane decides egress
  source egress-envoy: unknown: past its heartbeat: last heard 2026-10-04T10:00:01Z, heartbeat 300 s
undeclared egress registry.example.org: observed by egress-envoy: independent (unsigned descriptor), past its heartbeat: last heard 2026-10-04T10:00:01Z, heartbeat 300 s; 1 connection, port 80; no plane decides egress
undeclared paths: unknown
```

The fixture's lines are from 2026-10-04, past the 300-second heartbeat, so the
declared path is unknown; over a log the proxy is still writing, it reads
observed. `registry.example.org` was reached and declared by no path, so it
gets a row, and the command exits 1. No plane decides a connection, so an
egress path is never decided or enforced.

## Verify

```sh
guardana-control observe export egress/log/observations.jsonl
```

It writes the two connection observations and the import report, whose
`refusedBy` holds `"destination":"1"`. A line refused for another reason is
counted under its own key ([the reference](../reference/egress-access-log.md#refusals-and-drops)).

## Roll back

Leave `--source` and `--log` off the `coverage` command line and nothing reads
the log again. To start the log afresh, with no import running, move
`egress/log/observations.jsonl` out of its directory
([observations](../reference/observations.md#the-log)). A log that holds a
connection cannot be read by 0.14.
