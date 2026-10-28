# Egress access-log fixtures

Each `*.jsonl` file is what a proxy writes under the access-log format shown
here, read by the descriptor beside it. Member names in both formats are the
operator's choice; the values come from the proxies' own format codes. The
hosts are reserved example names, and the token in an Envoy path is a
placeholder that the importer must never copy.

## Envoy: `envoy.jsonl`, `envoy.descriptor.json`

Sources: the Envoy documentation for 1.41.0-dev, "Access logging"
(`configuration/observability/access_log/usage`, the `json_format` section)
and "Substitution formatter" (`configuration/advanced/substitution_formatter`),
for `%START_TIME%`, `%REQUESTED_SERVER_NAME%`, `%UPSTREAM_REMOTE_PORT%`,
`%RESPONSE_CODE%`, `%BYTES_SENT%`, `%BYTES_RECEIVED%`,
`%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%` and `%REQ(X)%`.

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

The documentation says `%RESPONSE_CODE%` and `%BYTES_RECEIVED%` render as
numbers and `%START_TIME%` as a string in typed JSON, and that an unset value
is `null` there and `"-"` in text. It does not say how `%BYTES_SENT%` and
`%UPSTREAM_REMOTE_PORT%` render, so the importer reads a number either as a
JSON number or as a decimal string. `%REQ(:AUTHORITY)%` is not used for the
destination: it carries a port, and the importer refuses a destination that is
not a host alone.

## Squid: `squid.jsonl`, `squid.descriptor.json`

Source: the Squid configuration reference for the `logformat` directive
(`Doc/config/logformat`, versions 6 and 7), for `%ts`, `%03tu`, `%>a`, `%ul`,
`%rm`, `%>rd`, `%>rP`, `%>Hs`, `%<st`, `%>st` and `%ru`. Squid has no JSON
encoding, so the operator writes the braces and quotes:

```
logformat egress_json {"time":%ts.%03tu,"client":"%>a","user":"%ul","method":"%rm","host":"%>rd","port":"%>rP","status":"%>Hs","reply_bytes":"%<st","request_bytes":"%>st","url":"%ru"}
access_log daemon:/var/log/squid/egress.jsonl logformat=egress_json
```

Every value but the time is quoted, because Squid writes `-` for a value it
does not have, and a bare `-` is not JSON. A value holding a quote or a
backslash still breaks the line, which the importer then refuses. `%<st` is the
reply sent to the client and `%>st` the request received from it, which is the
sense of Envoy's `%BYTES_SENT%` and `%BYTES_RECEIVED%`.
