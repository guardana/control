package mcp

import (
	"regexp"
	"strings"
	"testing"
)

func TestTraceContextReadsOnlyAWellFormedVersion00(t *testing.T) {
	const (
		trace = "4bf92f3577b34da6a3ce929d0e0e4736"
		span  = "00f067aa0ba902b7"
		valid = "00-" + trace + "-" + span + "-01"
	)
	for _, tc := range []struct {
		name  string
		value any
		ok    bool
	}{
		{"well formed", valid, true},
		{"flags not sampled", "00-" + trace + "-" + span + "-00", true},
		{"absent", nil, false},
		{"not a string", map[string]any{"traceparent": valid}, false},
		{"upper case", strings.ToUpper(valid), false},
		{"one character short", valid[:54], false},
		{"one character long", valid + "0", false},
		{"version ff", "ff" + valid[2:], false},
		{"version 01", "01" + valid[2:], false},
		{"version 01 with a field after", "01" + valid[2:] + "-ab", false},
		{"trace id all zeros", "00-" + strings.Repeat("0", 32) + "-" + span + "-01", false},
		{"parent id all zeros", "00-" + trace + "-" + strings.Repeat("0", 16) + "-01", false},
		{"not hex in the trace id", "00-" + trace[:31] + "g-" + span + "-01", false},
		{"not hex in the flags", "00-" + trace + "-" + span + "-0x", false},
		{"dashes moved", "00-" + trace[:31] + "-" + trace[31:] + span + "-01", false},
		{"spaces", " " + valid[1:], false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]any{"other": "x"}
			if tc.value != nil {
				meta[metaKeyTraceparent] = tc.value
			}
			gotTrace, gotSpan := traceContext(meta)
			switch {
			case tc.ok && (gotTrace != trace || gotSpan != span):
				t.Fatalf("traceContext(%v) = %q, %q; want %q, %q", tc.value, gotTrace, gotSpan, trace, span)
			case !tc.ok && (gotTrace != "" || gotSpan != ""):
				t.Fatalf("traceContext(%v) = %q, %q; want both empty", tc.value, gotTrace, gotSpan)
			}
		})
	}
	if gotTrace, gotSpan := traceContext(nil); gotTrace != "" || gotSpan != "" {
		t.Fatalf("traceContext(nil) = %q, %q", gotTrace, gotSpan)
	}
}

var (
	w3cTraceID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	w3cSpanID  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// FuzzTraceContext: whatever the value, traceContext returns both fields or
// neither, and what it returns is the value it was given, field by field.
func FuzzTraceContext(f *testing.F) {
	f.Add("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("00-00000000000000000000000000000000-00f067aa0ba902b7-01")
	f.Add("ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("")
	f.Fuzz(func(t *testing.T, v string) {
		trace, span := traceContext(map[string]any{metaKeyTraceparent: v})
		if (trace == "") != (span == "") {
			t.Fatalf("traceContext(%q) = %q, %q: one field without the other", v, trace, span)
		}
		if trace == "" {
			return
		}
		if !w3cTraceID.MatchString(trace) || !w3cSpanID.MatchString(span) {
			t.Fatalf("traceContext(%q) = %q, %q: not the W3C shape", v, trace, span)
		}
		if !strings.HasPrefix(v, "00-"+trace+"-"+span+"-") || len(v) != 55 {
			t.Fatalf("traceContext(%q) = %q, %q: not the value's own fields", v, trace, span)
		}
	})
}
