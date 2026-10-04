// Package tracereads holds every read of a trace field the source walk must
// find, and names that only resemble one, which it must not.
package tracereads

type span struct{ TraceId, SpanId string }

func (span) GetTraceId() string { return "" }

func (span) GetSpanId() string { return "" }

func reads(s span) []string {
	return []string{s.TraceId, s.SpanId, s.GetTraceId(), s.GetSpanId(), "trace_id", "span_id", "traceId", "spanId", `trace_id`}
}

func decoys(s span) []string {
	return []string{s.TraceIdx(), "trace", "TraceId", "span_id ", "traceid"}
}

func (span) TraceIdx() string { return "" }
