package mcp

import "strings"

// metaKeyTraceparent is the _meta key W3C trace context travels under,
// unprefixed as SEP-414 and OpenTelemetry's MCP conventions write it.
const metaKeyTraceparent = "traceparent"

// traceContext returns the trace id and the caller's span id a traceparent
// of version 00 names, or two empty strings for anything else. The value is
// the client's claim: it correlates a call with a trace and decides nothing.
func traceContext(meta map[string]any) (traceID, spanID string) {
	v, ok := meta[metaKeyTraceparent].(string)
	if !ok || len(v) != 55 {
		return "", ""
	}
	parts := strings.Split(v, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return "", ""
	}
	for _, p := range parts[1:] {
		if !lowerHex(p) {
			return "", ""
		}
	}
	if allZero(parts[1]) || allZero(parts[2]) {
		return "", ""
	}
	return parts[1], parts[2]
}

func lowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func allZero(s string) bool {
	return strings.Trim(s, "0") == ""
}
