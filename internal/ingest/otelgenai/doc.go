// Package otelgenai imports OpenTelemetry GenAI spans, written as OTLP/JSON
// TracesData lines by a collector's file exporter, as observations.
//
// The reader is strict: an unknown or repeated member, or an attribute key
// that appears twice in one list, refuses the whole line, because a reader
// that guesses which of two values counts can be steered by whoever writes
// the second. Only an allowlist of attributes is copied; every other GenAI
// attribute, content among them, is dropped and counted, and reasoning parts
// are counted, never kept. A line longer than MaxLineBytes, or holding more
// than MaxLineElements array elements, is refused, so one line costs a small
// multiple of its length.
//
// It is pure: it reads only the reader it is given and takes the receive time
// as an argument. See ADR-0040.
package otelgenai
