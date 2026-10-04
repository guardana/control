package otelgenai_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// markers hands out distinct printable strings, each one a value the
// importer could copy if it reached an allowlisted field.
type markers struct {
	n   int
	rng *rand.Rand
}

func (m *markers) next() string { m.n++; return fmt.Sprintf("MK%05d", m.n) }

// value is a marker as one of the forms an attribute value takes.
func (m *markers) value() string {
	switch m.rng.IntN(4) {
	case 0:
		return fmt.Sprintf(`{"arrayValue":{"values":[{"stringValue":%q}]}}`, m.next())
	case 1:
		return fmt.Sprintf(`{"kvlistValue":{"values":[{"key":%q,"value":{"stringValue":%q}}]}}`, m.next(), m.next())
	case 2:
		return fmt.Sprintf(`{"arrayValue":{"values":[{"kvlistValue":{"values":[{"key":"type","value":{"stringValue":%q}}]}}]}}`, m.next())
	}
	return fmt.Sprintf(`{"stringValue":%q}`, m.next())
}

func (m *markers) kv(key string) string {
	return fmt.Sprintf(`{"key":%q,"value":%s}`, key, m.value())
}

// attrs is a list of marked attributes: content keys, a key that is itself
// a marker, and allowlisted keys the span's subject does not read.
func (m *markers) attrs(notRead ...string) []string {
	out := []string{
		m.kv("gen_ai.input.messages"), m.kv("gen_ai.tool.call.arguments"), m.kv("gen_ai." + m.next()),
		m.kv("app." + m.next()), m.kv(m.next()),
		fmt.Sprintf(`{"key":"gen_ai.output.messages","value":{"stringValue":"[{\"parts\":[{\"type\":\"reasoning\",\"content\":\"%s\"}]}]"}}`, m.next()),
	}
	for _, k := range notRead {
		out = append(out, str(k, m.next()))
	}
	m.rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// operation is a mapped, unmapped, marked or missing operation, and the
// allowlisted keys a span of it does not copy.
func (m *markers) operation() ([]string, []string) {
	switch m.rng.IntN(5) {
	case 0:
		return []string{str("gen_ai.operation.name", "execute_tool"), str("gen_ai.tool.name", "t")}, []string{"gen_ai.agent.name", "gen_ai.request.model", "app.run_id"}
	case 1:
		return []string{str("gen_ai.operation.name", "chat")}, []string{"gen_ai.agent.name", "gen_ai.tool.name", "app.run_id"}
	case 2:
		return []string{str("gen_ai.operation.name", "retrieval")}, []string{"gen_ai.tool.name", "gen_ai.provider.name", "server.address", "error.type"}
	case 3:
		return []string{str("gen_ai.operation.name", m.next())}, []string{"gen_ai.request.model", "gen_ai.provider.name"}
	}
	return nil, []string{"gen_ai.tool.name", "error.type"}
}

func (m *markers) span(id int) string {
	op, notRead := m.operation()
	attrs := append(op, m.attrs(notRead...)...)
	return fmt.Sprintf(`{"traceId":%q,"spanId":%q,"traceState":%q,"name":%q,"endTimeUnixNano":"1791108002000000000","attributes":[%s],`+
		`"status":{"code":2,"message":%q},"events":[{"name":%q,"attributes":[%s]}],`+
		`"links":[{"traceId":%q,"spanId":"00f067aa0ba90009","traceState":%q,"attributes":[%s]}]}`,
		traceA, spanID(id), m.next(), m.next(), strings.Join(attrs, ","), m.next(), m.next(), strings.Join(m.attrs(), ","),
		traceB, m.next(), strings.Join(m.attrs(), ","))
}

func (m *markers) line(spans int) string {
	ss := make([]string, spans)
	for i := range ss {
		ss[i] = m.span(i + 1)
	}
	resAttrs := append([]string{str("service.name", "support-agent")}, m.attrs()...)
	return fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[%s],"entityRefs":[{"type":%q,"idKeys":[%q]}]},"schemaUrl":%q,`+
		`"scopeSpans":[{"scope":{"name":%q,"version":%q,"attributes":[%s]},"schemaUrl":%q,"spans":[%s]}]}]}`,
		strings.Join(resAttrs, ","), m.next(), m.next(), m.next(), m.next(), m.next(), strings.Join(m.attrs(), ","), m.next(), strings.Join(ss, ","))
}

// Nothing outside the allowlist reaches a record: every position the
// importer does not copy holds a distinct marker, and no record names one.
func TestNoMarkerOutsideTheAllowlistReachesARecord(t *testing.T) {
	for seed := range uint64(200) {
		m := &markers{rng: rand.New(rand.NewPCG(seed, 1))} //nolint:gosec // G404: seeded to repeat a case, not a secret
		in := m.line(1+m.rng.IntN(4)) + "\n" + probeLeaksLine + "\n"
		b := importBytes(t, []byte(in), descriptor(t))
		if b.Report.GetCounts().GetRefused() != 0 || b.Report.GetCounts().GetContentAttributesDropped() == 0 {
			t.Fatalf("seed %d: the generated line was not read as meant: %v", seed, b.Report.GetCounts())
		}
		if out := checkBatch(t, b); strings.Contains(out, "MK") || strings.Contains(out, "LEAK") {
			t.Fatalf("seed %d: a marker reached a record:\n%s", seed, out)
		}
	}
}

// probeLeaksLine holds a marker in each position an adversarial review
// probed first.
var probeLeaksLine = resource("support-agent", ","+str("gen_ai.input.messages", "LEAK-RES"),
	`"scope":{"name":"LEAK-SCOPE","attributes":[`+str("gen_ai.input.messages", "LEAK-SCOPEATTR")+`]},`,
	spanOf(11, "1791108002000000000", str("gen_ai.operation.name", "execute_tool")+","+str("gen_ai.tool.name", "t")+","+str("gen_ai.tool.call.arguments", "LEAK-ARGS"),
		`,"name":"SPANNAME-LEAK","status":{"code":2,"message":"LEAK-STATUS"}`+links(str("gen_ai.input.messages", "LEAK-LINK"))+
			`,"events":[{"name":"LEAK-EVENTNAME","attributes":[`+str("exception.message", "LEAK-EXC")+`]}]`),
	spanOf(12, "1791108002000000000", str("gen_ai.operation.name", "LEAK-OP")+","+str("gen_ai.input.messages", "x"), ""),
	spanOf(13, "1791108002000000000", str("gen_ai.operation.name", "execute_tool")+`,{"key":"gen_ai.tool.name","value":{"stringValue":"a\ud800b c￾"}}`, ""),
)
