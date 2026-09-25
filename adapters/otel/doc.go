// Package otel exports evidence to an OpenTelemetry collector: it drains the
// spool from a cursor, sends each event as one OTLP log record over HTTP in
// the protocol's JSON encoding, and acknowledges what the collector accepted
// (ADR-0014). It never touches the request path; a collector outage fills the
// spool and nothing else.
package otel
