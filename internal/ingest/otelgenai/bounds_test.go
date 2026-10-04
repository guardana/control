package otelgenai_test

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/ingest/otelgenai"
)

// maxAllocPerByte bounds what importing one line may allocate in all, as a
// multiple of its length, whatever the line holds.
const maxAllocPerByte = 8

// repeated is prefix, n copies of unit joined by commas, suffix and a newline.
func repeated(prefix, unit, suffix string, n int) []byte {
	var b bytes.Buffer
	b.Grow(len(prefix) + n*(len(unit)+1) + len(suffix) + 1)
	b.WriteString(prefix)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(unit)
	}
	b.WriteString(suffix)
	b.WriteByte('\n')
	return b.Bytes()
}

// fill is how many units fit a line of MaxLineBytes around prefix and suffix.
func fill(prefix, unit, suffix string) int {
	return (otelgenai.MaxLineBytes - len(prefix) - len(suffix)) / (len(unit) + 1)
}

// distinctKeys is a resource whose attribute list fills a line with
// attributes of distinct keys.
func distinctKeys() []byte {
	const prefix, suffix = `{"resourceSpans":[{"resource":{"attributes":[`, `]}}]}`
	var b bytes.Buffer
	b.WriteString(prefix)
	for i := 0; b.Len() < otelgenai.MaxLineBytes-len(suffix)-32; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"key":"k%d"}`, i)
	}
	b.WriteString(suffix + "\n")
	return b.Bytes()
}

// allocated imports in and returns the batch and the bytes allocated meanwhile.
func allocated(t *testing.T, in []byte) (otelgenai.Batch, uint64) {
	t.Helper()
	desc := descriptor(t)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b := importBytes(t, in, desc)
	runtime.ReadMemStats(&after)
	return b, after.TotalAlloc - before.TotalAlloc
}

func TestALongLineCostsAFixedMultipleOfItsLength(t *testing.T) {
	const (
		emptySpans   = `{"resourceSpans":[{"scopeSpans":[{"spans":[`
		refusedSpans = `{"x":1,"resourceSpans":[{"scopeSpans":[{"spans":[`
		spansEnd     = `]}]}]}`
		emptyValues  = `{"resourceSpans":[{"resource":{"attributes":[{"key":"k","value":{"arrayValue":{"values":[`
		valuesEnd    = `]}}}]}}]}`
		parts        = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"support-agent"}}]},"scopeSpans":[{"spans":[{"traceId":"` + traceA + `","spanId":"00f067aa0ba90001","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.input.messages","value":{"stringValue":"[{\"parts\":[`
		partsEnd     = `]}]"}}]}]}]}]}`
		part         = `{\"type\":\"a\"}`
		resources    = `{"resourceSpans":[`
		resourcesEnd = `]}`
		events       = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"support-agent"}}]},"scopeSpans":[{"spans":[{"traceId":"` + traceA + `","spanId":"00f067aa0ba90001","events":[`
		event        = `{"attributes":[{"key":"gen_ai.prompt"}]}`
	)
	const manyMembers = `{"resourceSpans":[{"scopeSpans":[{"spans":[{`
	spans := fill(emptySpans, "{}", spansEnd)
	refused := fill(refusedSpans, "0", spansEnd)
	cases := map[string]struct {
		in   []byte
		want *observev1.ImportCounts
	}{
		"empty spans over the element bound": {
			repeated(emptySpans, "{}", spansEnd, spans),
			&observev1.ImportCounts{Read: uint64(spans), Refused: uint64(spans)}, //nolint:gosec // G115: a positive count this test chose
		},
		"a refused line of many spans": {
			repeated(refusedSpans, "0", spansEnd, refused),
			&observev1.ImportCounts{Read: uint64(refused), Refused: uint64(refused)}, //nolint:gosec // G115: a positive count this test chose
		},
		"empty values over the element bound": {
			repeated(emptyValues, "{}", valuesEnd, fill(emptyValues, "{}", valuesEnd)),
			&observev1.ImportCounts{Read: 1, Refused: 1},
		},
		"a refused line of many members": {
			repeated(manyMembers, `"a":0`, `}]}]}]}`, fill(manyMembers, `"a":0`, `}]}]}]}`)),
			&observev1.ImportCounts{Read: 1, Refused: 1},
		},
		"empty resources over the element bound": {
			repeated(resources, "{}", resourcesEnd, fill(resources, "{}", resourcesEnd)),
			&observev1.ImportCounts{Read: 1, Refused: 1},
		},
		"distinct attributes over the element bound": {distinctKeys(), &observev1.ImportCounts{Read: 1, Refused: 1}},
		"events over the element bound": {
			repeated(events, event, spansEnd, fill(events, event, spansEnd)),
			&observev1.ImportCounts{Read: 1, Refused: 1},
		},
		"message parts over the element bound": {
			repeated(parts, part, partsEnd, fill(parts, part, partsEnd)),
			&observev1.ImportCounts{Read: 1, Observed: 1, ContentAttributesDropped: 1, ContentUnparsed: 1},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if len(c.in) > otelgenai.MaxLineBytes+1 || len(c.in) < otelgenai.MaxLineBytes-64 {
				t.Fatalf("line of %d bytes, want just under %d", len(c.in), otelgenai.MaxLineBytes)
			}
			b, total := allocated(t, c.in)
			wantCounts(t, b, c.want)
			if limit := uint64(maxAllocPerByte * len(c.in)); total > limit {
				t.Errorf("a %d MiB line allocated %d MiB, over %d MiB", len(c.in)>>20, total>>20, limit>>20)
			}
		})
	}
}

// arrayLine is oneSpan with one more attribute whose array holds n empty
// values: n+6 array elements in all.
func arrayLine(n int) []byte {
	return []byte(oneSpan("", attr(`{"arrayValue":{"values":[`+strings.TrimSuffix(strings.Repeat("{},", n), ",")+`]}}`)) + "\n")
}

func TestElementBound(t *testing.T) {
	desc := descriptor(t)
	at := importBytes(t, arrayLine(otelgenai.MaxLineElements-6), desc)
	wantCounts(t, at, &observev1.ImportCounts{Read: 1, Observed: 1})
	over := importBytes(t, arrayLine(otelgenai.MaxLineElements-5), desc)
	wantCounts(t, over, &observev1.ImportCounts{Read: 1, Refused: 1})
}

// messagesLine is a chat span whose input messages, a JSON string, hold one
// message of n parts, each with its type: 2n+2 elements of the line's parts
// budget, and one more with role.
func messagesLine(n int, role bool) []byte {
	parts := strings.TrimSuffix(strings.Repeat(`{\"type\":\"reasoning\"},`, n), ",")
	message := `{\"parts\":[` + parts + `]}`
	if role {
		message = `{\"role\":\"user\",` + message[1:]
	}
	return []byte(oneChat(`,{"key":"gen_ai.input.messages","value":{"stringValue":"[`+message+`]"}}`) + "\n")
}

func oneChat(attrExtra string) string {
	return strings.Replace(oneSpan("", attrExtra), `"execute_tool"`, `"chat"`, 1)
}

func TestMessagesBound(t *testing.T) {
	desc := descriptor(t)
	n := (otelgenai.MaxLineElements - 2) / 2
	if 2*n+2 != otelgenai.MaxLineElements {
		t.Fatalf("%d parts do not meet the bound", n)
	}
	at := importBytes(t, messagesLine(n, false), desc)
	wantCounts(t, at, &observev1.ImportCounts{Read: 1, Observed: 1, ContentAttributesDropped: 1, ReasoningPartsDropped: uint64(n)})
	over := importBytes(t, messagesLine(n, true), desc)
	wantCounts(t, over, &observev1.ImportCounts{Read: 1, Observed: 1, ContentAttributesDropped: 1, ContentUnparsed: 1})
}

// The parts budget is the line's, not each string's: two strings that fit
// alone do not fit together.
func TestMessagesShareOneBudgetPerLine(t *testing.T) {
	quarter := strings.TrimSuffix(strings.Repeat(`{\"type\":\"reasoning\"},`, otelgenai.MaxLineElements/4), ",")
	value := `{"stringValue":"[{\"parts\":[` + quarter + `]}]"}`
	line := oneChat(`,{"key":"gen_ai.input.messages","value":` + value + `},{"key":"gen_ai.output.messages","value":` + value + `}`)
	b := importBytes(t, []byte(line+"\n"), descriptor(t))
	wantCounts(t, b, &observev1.ImportCounts{
		Read: 1, Observed: 1, ContentAttributesDropped: 2,
		ReasoningPartsDropped: otelgenai.MaxLineElements / 4, ContentUnparsed: 1,
	})
}

// A refused line counts its spans as written, escaped names included, and a
// line in which none can be counted counts once.
func TestRefusedLineCountsItsSpans(t *testing.T) {
	cases := map[string]struct {
		line string
		want uint64
	}{
		"plain":                   {`{"x":1,"resourceSpans":[{"scopeSpans":[{"spans":[0,{},[]]},{"spans":["a"]}]},{"scopeSpans":[{"spans":[null]}]}]}`, 5},
		"escaped names":           {`{"x":1,"resource\u0053pans":[{"scope\u0053pans":[{"spa\u006es":[0,0,0]}]}]}`, 3},
		"other members skipped":   {`{"x":{"spans":[0,0]},"resourceSpans":[{"r":"\"]","scopeSpans":[{"s":[{"spans":[0]}],"spans":[1, 2]}]}]}`, 2},
		"names that differ":       {`{"x":1,"resourcespans":[{"scopeSpans":[{"spans":[0,0]}]}],"resourceSpans":[{"scopeSpans":[{"spa\ns":[0,0],"spans ":[0,0]}]}]}`, 1},
		"escape of a non-ASCII":   {`{"x":1,"resourceSpans":[{"scopeSpans":[{"\u00e9spans":[0,0]}]}]}`, 1},
		"not an object":           {`[{"resourceSpans":[{"scopeSpans":[{"spans":[0,0]}]}]}]`, 1},
		"not valid JSON":          {`{"resourceSpans":[{"scopeSpans":[{"spans":[0,0]}]}]`, 1},
		"spans that are no array": {`{"x":1,"resourceSpans":[{"scopeSpans":[{"spans":{"a":0}}]}]}`, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := importBytes(t, []byte(c.line+"\n"), descriptor(t))
			wantCounts(t, b, &observev1.ImportCounts{Read: c.want, Refused: c.want})
		})
	}
}
