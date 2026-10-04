package otelgenai_test

import (
	"fmt"
	"strings"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
)

func str(key, value string) string {
	return fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, key, value)
}

// resource is one resource of service svc holding spans, with resExtra
// appended to its attributes and scope set on its one scope.
func resource(svc, resExtra, scope string, spans ...string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[` + str("service.name", svc) + resExtra + `]},` +
		`"scopeSpans":[{` + scope + `"spans":[` + strings.Join(spans, ",") + `]}]}]}`
}

// spanOf is a span of traceA with the given id, end time, attributes and extra members.
func spanOf(id int, end, attrs, extra string) string {
	return fmt.Sprintf(`{"traceId":%q,"spanId":%q,"endTimeUnixNano":%q,"attributes":[%s]%s}`, traceA, spanID(id), end, attrs, extra)
}

func events(attrs ...string) string {
	return `,"events":[{"name":"e","attributes":[` + strings.Join(attrs, ",") + `]}]`
}

func links(attrs ...string) string {
	return `,"links":[{"traceId":"` + traceB + `","spanId":"00f067aa0ba90009","attributes":[` + strings.Join(attrs, ",") + `]}]`
}

const (
	reasoningJSON = `[{"parts":[{"type":"reasoning"}]}]`
	thinkingArray = `{"key":"gen_ai.input.messages","value":{"arrayValue":{"values":[{"kvlistValue":{"values":[` +
		`{"key":"parts","value":{"arrayValue":{"values":[{"kvlistValue":{"values":[{"key":"type","value":{"stringValue":"thinking"}}]}}]}}}]}}]}}}`
	endEarly = "1791108001000000000"
	endLate  = "1791108009000000000"
)

// Content the importer drops is counted wherever the selected resource holds
// it, outside a refused span; an observation counts only its own span's.
func TestContentOutsideAnObservedSpanIsCounted(t *testing.T) {
	observed := spanOf(1, endEarly, str("gen_ai.operation.name", "execute_tool")+","+str("gen_ai.tool.name", "t")+","+str("gen_ai.tool.call.arguments", "x"),
		events(str("gen_ai.output.messages", "not json"))+links(str("gen_ai.input.messages", "[]")))
	retrieval := spanOf(2, endEarly, str("gen_ai.operation.name", "retrieval")+","+str("gen_ai.request.model", "m")+","+thinkingArray,
		events(str("gen_ai.tool.call.result", "x")))
	noOperation := spanOf(3, endEarly, str("gen_ai.output.messages", reasoningJSON), "")
	refused := strings.Replace(spanOf(4, endEarly, str("gen_ai.prompt", "x"), ""), traceA, strings.ToUpper(traceA), 1)
	in := resource("support-agent", ","+str("gen_ai.input.messages", reasoningJSON),
		`"scope":{"name":"s","attributes":[`+str("gen_ai.system_instructions", "x")+`]},`,
		observed, retrieval, noOperation, refused) + "\n" +
		resource("billing-agent", ","+str("gen_ai.prompt", "x"), "", spanOf(5, endEarly, str("gen_ai.completion", "x"), "")) + "\n"
	b := importBytes(t, []byte(in), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{
		Read: 5, Observed: 1, OtherResource: 1, Refused: 1,
		Skipped:                  map[string]uint64{"operation:retrieval": 1, "no_operation": 1},
		ContentAttributesDropped: 9, ReasoningPartsDropped: 3, ContentUnparsed: 1,
	})
	if got := b.Observations[0].GetContentAttributesDropped(); got != 2 {
		t.Errorf("observation counts %d content attributes, want its span's own 2", got)
	}
}

// A source that sends only operations the importer skips is still heard.
func TestSkippedSpansSetTheEventTimes(t *testing.T) {
	in := resource("support-agent", "", "",
		spanOf(1, endLate, str("gen_ai.operation.name", "retrieval"), ""),
		spanOf(2, endEarly, "", ""),
		strings.Replace(spanOf(3, "1791108000000000000", "", ""), traceA, strings.ToUpper(traceA), 1),
		spanOf(4, "1791108010000000000", `{"key":"gen_ai.operation.name","value":{"intValue":1}}`, ""),
	) + "\n" + resource("billing-agent", "", "", spanOf(5, "1791108020000000000", str("gen_ai.operation.name", "chat"), "")) + "\n"
	b := importBytes(t, []byte(in), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{
		Read: 5, OtherResource: 1, Refused: 1,
		Skipped: map[string]uint64{"operation:retrieval": 1, "no_operation": 1, "unmapped_operation": 1},
	})
	equal(t, b.Report.GetEarliestEventTime(), ts(t, "2026-10-04T10:00:01Z"))
	equal(t, b.Report.GetLatestEventTime(), ts(t, "2026-10-04T10:00:10Z"))
}

func TestMessagesStringIsReadStrictly(t *testing.T) {
	cases := map[string]struct {
		messages            string
		reasoning, unparsed uint64
	}{
		"type repeated":             {`[{"parts":[{"type":"reasoning","type":"text"}]}]`, 0, 1},
		"type repeated as escape":   {`[{"parts":[{"type":"text","type":"reasoning"}]}]`, 0, 1},
		"parts repeated":            {`[{"parts":[{"type":"reasoning"}],"parts":[]}]`, 0, 1},
		"type differing in case":    {`[{"parts":[{"Type":"text","type":"reasoning"}]}]`, 1, 0},
		"other members of any type": {`[{"role":"x","n":[1,{"a":null}],"parts":[{"type":"thinking","content":{"x":[true]}}]}]`, 1, 0},
		"no messages":               {`[]`, 0, 0},
		"trailing value":            {`[] []`, 0, 1},
		"null type":                 {`[{"parts":[{"type":null}]}]`, 0, 1},
		"no type":                   {`[{"parts":[{"content":"x"}]}]`, 0, 1},
		"no parts":                  {`[{"role":"x"}]`, 0, 1},
		"null parts":                {`[{"parts":null}]`, 0, 1},
		"a part that is no object":  {`[{"parts":["reasoning"]}]`, 0, 1},
		"nested past the depth":     {`[{"parts":[{"type":"reasoning","c":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}]}]`, 0, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := importBytes(t, []byte(oneChat(","+str("gen_ai.output.messages", c.messages))+"\n"), descriptor(t))
			wantCounts(t, b, &observev1.ImportCounts{Read: 1, Observed: 1, ContentAttributesDropped: 1, ReasoningPartsDropped: c.reasoning, ContentUnparsed: c.unparsed})
		})
	}
}

func TestUnprintableRunesAreDropped(t *testing.T) {
	refused := map[string]string{
		"line separator":            ` `,
		"paragraph separator":       ` `,
		"private use":               ``,
		"private use, plane 15":     `󰀀`,
		"private use, plane 16 end": `􏿽`,
		"noncharacter FFFE":         `￾`,
		"noncharacter FFFF":         `￿`,
		"noncharacter FDD0":         `﷐`,
		"noncharacter FDEF":         `﷯`,
		"noncharacter 1FFFE":        `🿾`,
		"noncharacter 10FFFF":       `􏿿`,
		"replacement character":     `�`,
		"lone surrogate":            `\ud800`,
	}
	kept := map[string]string{
		"before the noncharacter block": `﷏`,
		"after the noncharacter block":  `ﷰ`,
		"before FFFE":                   `￼`,
		"plane 1 before its last two":   `🿽`,
	}
	run := func(cases map[string]string, wantKept bool) {
		for name, r := range cases {
			t.Run(name, func(t *testing.T) {
				b := importBytes(t, []byte(oneSpan("", `,{"key":"gen_ai.tool.name","value":{"stringValue":"a`+r+`b"}}`)+"\n"), descriptor(t))
				name := b.Observations[0].GetSubject().GetName()
				dropped := b.Report.GetCounts().GetStringsDropped()
				if wantKept != (name != "") || wantKept != (dropped == 0) {
					t.Errorf("name %q, strings dropped %d; want kept %v", name, dropped, wantKept)
				}
			})
		}
	}
	run(refused, false)
	run(kept, true)
}

func TestServiceNameMatchesExactly(t *testing.T) {
	for _, svc := range []string{"Support-agent", "support-agent ", " support-agent", "support-agent2", "support-agen", "support_agent"} {
		t.Run(svc, func(t *testing.T) {
			b := importBytes(t, []byte(resource(svc, "", "", spanOf(1, endEarly, str("gen_ai.operation.name", "chat"), ""))+"\n"), descriptor(t))
			wantCounts(t, b, &observev1.ImportCounts{Read: 1, OtherResource: 1})
		})
	}
}
