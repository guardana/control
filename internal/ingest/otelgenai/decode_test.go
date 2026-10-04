package otelgenai_test

import (
	"strings"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
)

// oneSpan is a line holding one execute_tool span of the selected service,
// with spanExtra appended to the span's members and attrExtra to its
// attributes.
func oneSpan(spanExtra, attrExtra string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"support-agent"}}]},` +
		`"scopeSpans":[{"spans":[{"traceId":"` + traceA + `","spanId":"00f067aa0ba90001"` + spanExtra +
		`,"attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"execute_tool"}}` + attrExtra + `]}]}]}]}`
}

func attr(value string) string { return `,{"key":"app.v","value":` + value + `}` }

func nested(depth int, wrap func(string) string) string {
	v := `{"stringValue":"x"}`
	for range depth - 1 {
		v = wrap(v)
	}
	return v
}

func inArray(v string) string  { return `{"arrayValue":{"values":[` + v + `]}}` }
func inKVList(v string) string { return `{"kvlistValue":{"values":[{"key":"k","value":` + v + `}]}}` }

func TestDecoderAcceptsEveryOTLPForm(t *testing.T) {
	cases := map[string]string{
		"end time as a number":   oneSpan(`,"endTimeUnixNano":1791108000250000000`, ""),
		"end time as a string":   oneSpan(`,"endTimeUnixNano":"1791108000250000000"`, ""),
		"largest end time":       oneSpan(`,"endTimeUnixNano":"18446744073709551615"`, ""),
		"all span members":       oneSpan(`,"traceState":"a=1","parentSpanId":"","flags":257,"name":"n","kind":3,"startTimeUnixNano":"1","droppedAttributesCount":0,"events":[],"droppedEventsCount":2,"links":[],"droppedLinksCount":0,"status":{"message":"m","code":0}`, ""),
		"event and link":         oneSpan(`,"events":[{"timeUnixNano":"1","name":"e","attributes":[],"droppedAttributesCount":1}],"links":[{"traceId":"x","spanId":"y","traceState":"","attributes":[],"droppedAttributesCount":0,"flags":1}]`, ""),
		"int as a string":        oneSpan("", attr(`{"intValue":"-9223372036854775808"}`)),
		"int as a number":        oneSpan("", attr(`{"intValue":12}`)),
		"double":                 oneSpan("", attr(`{"doubleValue":1.5e3}`)),
		"double NaN":             oneSpan("", attr(`{"doubleValue":"NaN"}`)),
		"double infinity":        oneSpan("", attr(`{"doubleValue":"-Infinity"}`)),
		"bool":                   oneSpan("", attr(`{"boolValue":false}`)),
		"bytes":                  oneSpan("", attr(`{"bytesValue":"aGk="}`)),
		"bytes unpadded":         oneSpan("", attr(`{"bytesValue":"aGk"}`)),
		"empty value":            oneSpan("", attr(`{}`)),
		"no value":               oneSpan("", `,{"key":"app.v"}`),
		"array nested 32 deep":   oneSpan("", attr(nested(32, inArray))),
		"kvlist nested 32 deep":  oneSpan("", attr(nested(32, inKVList))),
		"scope and schema urls":  strings.Replace(oneSpan("", ""), `"scopeSpans":[{"spans"`, `"schemaUrl":"u","scopeSpans":[{"schemaUrl":"u","scope":{"name":"s","version":"1","attributes":[],"droppedAttributesCount":0},"spans"`, 1),
		"entity refs":            strings.Replace(oneSpan("", ""), `"resource":{`, `"resource":{"droppedAttributesCount":0,"entityRefs":[{"schemaUrl":"u","type":"service","idKeys":["service.name"],"descriptionKeys":[]}],`, 1),
		"whitespace and CR":      " " + oneSpan("", "") + " \r",
		"escaped member name":    strings.Replace(oneSpan("", ""), `"spanId"`, `"spanId"`, 1),
		"parent span id present": oneSpan(`,"parentSpanId":"b7ad6b7169203331"`, ""),
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			b := importBytes(t, []byte(line+"\n"), descriptor(t))
			wantCounts(t, b, &observev1.ImportCounts{Read: 1, Observed: 1})
		})
	}
}

func TestDecoderRefusesTheLine(t *testing.T) {
	cases := map[string]string{
		"null member":            oneSpan(`,"name":null`, ""),
		"enum as a name":         oneSpan(`,"kind":"SPAN_KIND_CLIENT"`, ""),
		"status code as string":  oneSpan(`,"status":{"code":"2"}`, ""),
		"end time with fraction": oneSpan(`,"endTimeUnixNano":1.5`, ""),
		"end time exponent":      oneSpan(`,"endTimeUnixNano":1e18`, ""),
		"end time negative":      oneSpan(`,"endTimeUnixNano":"-1"`, ""),
		"end time hex":           oneSpan(`,"endTimeUnixNano":"0x10"`, ""),
		"end time plus":          oneSpan(`,"endTimeUnixNano":"+1"`, ""),
		"end time overflow":      oneSpan(`,"endTimeUnixNano":"18446744073709551616"`, ""),
		"flags negative":         oneSpan(`,"flags":-1`, ""),
		"flags over 32 bits":     oneSpan(`,"flags":4294967296`, ""),
		"int not a number":       oneSpan("", attr(`{"intValue":"abc"}`)),
		"int overflow":           oneSpan("", attr(`{"intValue":"9223372036854775808"}`)),
		"int with a plus sign":   oneSpan("", attr(`{"intValue":"+5"}`)),
		"double as a word":       oneSpan("", attr(`{"doubleValue":"nan"}`)),
		"bool as a string":       oneSpan("", attr(`{"boolValue":"true"}`)),
		"bytes not base64":       oneSpan("", attr(`{"bytesValue":"%%%"}`)),
		"two value members":      oneSpan("", attr(`{"stringValue":"a","intValue":1}`)),
		"array nested 33 deep":   oneSpan("", attr(nested(33, inArray))),
		"kvlist nested 33 deep":  oneSpan("", attr(nested(33, inKVList))),
		"array unknown member":   oneSpan("", attr(`{"arrayValue":{"items":[]}}`)),
		"event unknown member":   oneSpan(`,"events":[{"body":"x"}]`, ""),
		"link unknown member":    oneSpan(`,"links":[{"spanID":"x"}]`, ""),
		"status unknown member":  oneSpan(`,"status":{"description":"x"}`, ""),
		"scope unknown member":   strings.Replace(oneSpan("", ""), `"scopeSpans":[{"spans"`, `"scopeSpans":[{"scope":{"id":1},"spans"`, 1),
		"entity ref unknown":     strings.Replace(oneSpan("", ""), `"resource":{`, `"resource":{"entityRefs":[{"id":"x"}],`, 1),
		"key value unknown":      oneSpan("", `,{"key":"a","value":{},"type":1}`),
		"spans not an array":     strings.Replace(oneSpan("", ""), `"spans":[{`, `"spans":{"x":[{`, 1),
		"second value on line":   oneSpan("", "") + ` {}`,
		"truncated":              strings.TrimSuffix(oneSpan("", ""), "}"),
		"invalid UTF-8":          oneSpan(`,"name":"`+"\xff"+`"`, ""),
		"top-level array":        "[" + oneSpan("", "") + "]",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			b := importBytes(t, []byte(line+"\n"), descriptor(t))
			wantCounts(t, b, &observev1.ImportCounts{Read: 1, Refused: 1})
		})
	}
}

func TestSpanRefusalLeavesTheLine(t *testing.T) {
	refused := strings.Replace(oneSpan(`,"status":{"code":3}`, ""), `"spans":[`, `"spans":[`+
		`{"traceId":"`+traceA+`","spanId":"00f067aa0ba90002","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}}]},`, 1)
	b := importBytes(t, []byte(refused+"\n"), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{Read: 2, Observed: 1, Refused: 1})
	for _, code := range []string{"-1", "2147483648"} {
		b := importBytes(t, []byte(oneSpan(`,"status":{"code":`+code+`}`, "")+"\n"), descriptor(t))
		wantCounts(t, b, &observev1.ImportCounts{Read: 1, Refused: 1})
	}
}

func TestOperationsMapToSubjects(t *testing.T) {
	cases := map[string]observev1.SubjectKind{
		"execute_tool": observev1.SubjectKind_SUBJECT_KIND_TOOL, "invoke_agent": observev1.SubjectKind_SUBJECT_KIND_AGENT,
		"create_agent": observev1.SubjectKind_SUBJECT_KIND_AGENT, "chat": observev1.SubjectKind_SUBJECT_KIND_MODEL,
		"generate_content": observev1.SubjectKind_SUBJECT_KIND_MODEL, "text_completion": observev1.SubjectKind_SUBJECT_KIND_MODEL,
		"embeddings": observev1.SubjectKind_SUBJECT_KIND_MODEL,
	}
	names := `,{"key":"gen_ai.tool.name","value":{"stringValue":"tool-n"}},{"key":"gen_ai.agent.name","value":{"stringValue":"agent-n"}},{"key":"gen_ai.request.model","value":{"stringValue":"model-n"}}`
	wantName := map[observev1.SubjectKind]string{
		observev1.SubjectKind_SUBJECT_KIND_TOOL: "tool-n", observev1.SubjectKind_SUBJECT_KIND_AGENT: "agent-n", observev1.SubjectKind_SUBJECT_KIND_MODEL: "model-n",
	}
	for op, kind := range cases {
		line := strings.Replace(oneSpan("", names), `"execute_tool"`, `"`+op+`"`, 1)
		b := importBytes(t, []byte(line+"\n"), descriptor(t))
		wantCounts(t, b, &observev1.ImportCounts{Read: 1, Observed: 1, ContentAttributesDropped: 2})
		if s := b.Observations[0].GetSubject(); s.GetKind() != kind || s.GetName() != wantName[kind] || s.GetOperation() != op {
			t.Errorf("%s: subject %v, want %v %s", op, s, kind, wantName[kind])
		}
	}
	for op, reason := range map[string]string{
		`"invoke_workflow"`: "operation:invoke_workflow", `"Chat"`: "unmapped_operation", `""`: "unmapped_operation",
	} {
		line := strings.Replace(oneSpan("", ""), `"execute_tool"`, op, 1)
		wantCounts(t, importBytes(t, []byte(line+"\n"), descriptor(t)), &observev1.ImportCounts{Read: 1, Skipped: map[string]uint64{reason: 1}})
	}
	line := strings.Replace(oneSpan("", ""), `{"stringValue":"execute_tool"}`, `{"intValue":1}`, 1)
	wantCounts(t, importBytes(t, []byte(line+"\n"), descriptor(t)), &observev1.ImportCounts{Read: 1, Skipped: map[string]uint64{"unmapped_operation": 1}})
}
