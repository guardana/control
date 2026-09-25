package otel_test

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/adapters/otel"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// bodyLine is one evidence line as the codec writes it, spelled here rather
// than produced by the encoder under test.
const bodyLine = `{"eventId":"evt-1","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"req-1","projectId":"proj-1","tenantId":"tenant-1","schemaVersion":"1.0"}`

// baseRequest is a request of one record, in every member the exporter writes.
// BODY stands for the body's JSON string.
const baseRequest = `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"svc"}}]},` +
	`"scopeLogs":[{"scope":{"name":"scope"},"logRecords":[{"timeUnixNano":"1789812000000000000",` +
	`"severityNumber":"SEVERITY_NUMBER_INFO","severityText":"INFO","body":{"stringValue":BODY},` +
	`"attributes":[{"key":"k","value":{"stringValue":"v"}}]}]}]}]}`

func requestWithBody(body string) string {
	return strings.Replace(baseRequest, "BODY", body, 1)
}

func base() string { return requestWithBody(strconv.Quote(bodyLine)) }

// variant is base with old replaced by new, and fails the test when old is not
// there: a case whose change did not apply would examine the base request.
func variant(t *testing.T, old, replacement string) string {
	t.Helper()
	req := base()
	if !strings.Contains(req, old) {
		t.Fatalf("the base request holds no %q", old)
	}
	return strings.Replace(req, old, replacement, 1)
}

func TestTheBaseRequestReadsAsItsOneEvent(t *testing.T) {
	events, err := otel.ReadRequest([]byte(base()))
	if err != nil {
		t.Fatalf("ReadRequest(base) = %v", err)
	}
	if len(events) != 1 || events[0].GetEventId() != "evt-1" || events[0].GetKind() != controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED ||
		events[0].GetTenantId() != "tenant-1" {
		t.Fatalf("ReadRequest(base) = %v", events)
	}
}

// TestTheGoldenRequestReadsBackToItsEvents pins the receiver to the encoder's
// golden: the request the protocol's own types produced, read here, gives back
// the lines the same run wrote, byte for byte.
func TestTheGoldenRequestReadsBackToItsEvents(t *testing.T) {
	events, err := otel.ReadRequest(readFixture(t, "export_logs_request.json"))
	if err != nil {
		t.Fatalf("ReadRequest(golden) = %v", err)
	}
	var got bytes.Buffer
	if err := evidence.EncodeJSONL(&got, events); err != nil {
		t.Fatal(err)
	}
	if want := readFixture(t, "events.jsonl"); !bytes.Equal(got.Bytes(), want) {
		t.Errorf("the golden request reads back as\n%s\nwant\n%s", got.Bytes(), want)
	}
}

// TestTheProtocolsSpellingsAreAcceptedAndNoOther: OTLP/JSON keys are the
// lowerCamelCase names, and the original field names "are not valid"; the
// exporter's answer reader accepts both spellings of the answer, and this
// reader accepts the first only. An enum is accepted by its name, which the
// protobuf JSON mapping and the exporter write, and by its number, which OTLP
// requires; a 64-bit integer as a decimal string or a number. null is an
// absent member, as the mapping says.
func TestTheProtocolsSpellingsAreAcceptedAndNoOther(t *testing.T) {
	accepted := map[string][2]string{
		"an enum by its number":         {`"severityNumber":"SEVERITY_NUMBER_INFO"`, `"severityNumber":9`},
		"the last enum number":          {`"severityNumber":"SEVERITY_NUMBER_INFO"`, `"severityNumber":24`},
		"an enum's zero by its name":    {`"severityNumber":"SEVERITY_NUMBER_INFO"`, `"severityNumber":"SEVERITY_NUMBER_UNSPECIFIED"`},
		"a time as a number":            {`"timeUnixNano":"1789812000000000000"`, `"timeUnixNano":1789812000000000000`},
		"the largest time":              {`"timeUnixNano":"1789812000000000000"`, `"timeUnixNano":"18446744073709551615"`},
		"a null time":                   {`"timeUnixNano":"1789812000000000000"`, `"timeUnixNano":null`},
		"null attributes":               {`"attributes":[{"key":"k","value":{"stringValue":"v"}}]`, `"attributes":null`},
		"a null resource":               {`"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"svc"}}]}`, `"resource":null`},
		"an escaped member name":        {`"severityText"`, `"severity` + esc("0054") + `ext"`},
		"a surrogate pair":              {`"severityText":"INFO"`, `"severityText":"` + esc("d83d") + esc("de00") + ` \` + esc("d800") + ` ` + esc("0041") + `"`},
		"white space around the object": {`{"resourceLogs"`, " \n\t{\"resourceLogs\""},
	}
	for name, c := range accepted {
		events, err := otel.ReadRequest([]byte(variant(t, c[0], c[1])))
		if err != nil || len(events) != 1 {
			t.Errorf("%s: ReadRequest = %d events, %v; want the one event", name, len(events), err)
		}
	}
	for _, empty := range []string{`{}`, `{"resourceLogs":[]}`, `{"resourceLogs":null}`, `{"resourceLogs":[{"scopeLogs":[{"logRecords":[]}]}]}`} {
		if events, err := otel.ReadRequest([]byte(empty)); err != nil || len(events) != 0 {
			t.Errorf("ReadRequest(%s) = %d events, %v; want none and no refusal", empty, len(events), err)
		}
	}
}

// TestEveryRefusalHasItsInput: each case is the base request with one change,
// and the base itself is accepted above, so each case is the input at which
// removing its refusal changes the result.
func TestEveryRefusalHasItsInput(t *testing.T) {
	cases := map[string][2]string{
		"a snake_case request member":    {`"resourceLogs"`, `"resource_logs"`},
		"a snake_case scope member":      {`"scopeLogs"`, `"scope_logs"`},
		"a snake_case record member":     {`"logRecords"`, `"log_records"`},
		"a snake_case time":              {`"timeUnixNano"`, `"time_unix_nano"`},
		"a snake_case severity":          {`"severityNumber"`, `"severity_number"`},
		"a snake_case string value":      {`"body":{"stringValue"`, `"body":{"string_value"`},
		"an unknown request member":      {`{"resourceLogs"`, `{"extra":1,"resourceLogs"`},
		"a protocol member not written":  {`"scopeLogs":[{`, `"scopeLogs":[{"schemaUrl":"x",`},
		"a resource member not written":  {`"resource":{`, `"resource":{"droppedAttributesCount":0,`},
		"a scope member not written":     {`"scope":{"name":"scope"}`, `"scope":{"name":"scope","version":"1"}`},
		"a record member not written":    {`"severityText":"INFO"`, `"severityText":"INFO","traceId":"00"`},
		"a key-value member not written": {`{"key":"k",`, `{"key":"k","extra":1,`},
		"a body that is not a string":    {`"body":{"stringValue"`, `"body":{"intValue":"1","stringValue"`},
		"an attribute not a string":      {`"value":{"stringValue":"v"}`, `"value":{"intValue":"1"}`},
		"an attribute with no value":     {`,"value":{"stringValue":"v"}`, ``},
		"an attribute value with none":   {`"value":{"stringValue":"v"}`, `"value":{}`},
		"a member twice":                 {`"severityText":"INFO"`, `"severityText":"INFO","severityText":"INFO"`},
		"a member twice, once escaped":   {`"severityText":"INFO"`, `"severityText":"INFO","severity` + esc("0054") + `ext":"INFO"`},
		"two documents":                  {base(), base() + `{}`},
		"a list where an object belongs": {`"resource":{`, `"resource":[{`},
		"an object where a list belongs": {`"logRecords":[`, `"logRecords":{"x":[`},
		"an unknown enum name":           {`"SEVERITY_NUMBER_INFO"`, `"SEVERITY_NUMBER_LOUD"`},
		"an enum number past the last":   {`"severityNumber":"SEVERITY_NUMBER_INFO"`, `"severityNumber":25`},
		"a negative enum number":         {`"severityNumber":"SEVERITY_NUMBER_INFO"`, `"severityNumber":-1`},
		"a fractional enum number":       {`"severityNumber":"SEVERITY_NUMBER_INFO"`, `"severityNumber":9.5`},
		"a time that is not a number":    {`"1789812000000000000"`, `"soon"`},
		"a negative time":                {`"1789812000000000000"`, `"-1"`},
		"a time past 64 bits":            {`"1789812000000000000"`, `"18446744073709551616"`},
		"a fractional time":              {`"timeUnixNano":"1789812000000000000"`, `"timeUnixNano":1.5`},
		"a text that is a number":        {`"severityText":"INFO"`, `"severityText":1`},
		"a record with no body":          {`"body":{"stringValue":` + strconv.Quote(bodyLine) + `},`, ``},
		"a null body":                    {`"body":{"stringValue":` + strconv.Quote(bodyLine) + `}`, `"body":null`},
		"a lone surrogate in a string":   {`"severityText":"INFO"`, `"severityText":"\ud800"`},
		"a lone low surrogate":           {`"severityText":"INFO"`, `"severityText":"\udc00x"`},
		"a reversed surrogate pair":      {`"severityText":"INFO"`, `"severityText":"\udc00\ud800"`},
		"a high half before a letter":    {`"severityText":"INFO"`, `"severityText":"` + esc("d800") + esc("0041") + `"`},
		"two high halves":                {`"severityText":"INFO"`, `"severityText":"` + esc("d800") + esc("d800") + esc("dc00") + `"`},
		"bytes that are not UTF-8":       {`"severityText":"INFO"`, "\"severityText\":\"I\xffO\""},
		"not an object":                  {base(), `[]`},
		"not JSON":                       {base(), `resourceLogs`},
		"nothing":                        {base(), ``},
	}
	for name, c := range cases {
		var req string
		if c[0] == base() {
			req = c[1]
		} else {
			req = variant(t, c[0], c[1])
		}
		events, err := otel.ReadRequest([]byte(req))
		if !errors.Is(err, otel.ErrRequest) || events != nil {
			t.Errorf("%s: ReadRequest = %v, %v; want ErrRequest", name, events, err)
		}
	}
}

// TestABodyIsOneEvidenceLine: the body is the record, so a body with a line
// break, one that is not an event, and one the codec would not write back are
// refused, each for its own reason.
func TestABodyIsOneEvidenceLine(t *testing.T) {
	other := strings.Replace(bodyLine, "evt-1", "evt-2", 1)
	cases := map[string]struct {
		body string
		want error
	}{
		"two lines":                  {bodyLine + "\n" + other, evidence.ErrTooManyEvents},
		"a line and its newline":     {bodyLine + "\n", evidence.ErrTooManyEvents},
		"a newline inside the event": {strings.Replace(bodyLine, `,"kind"`, "\n,\"kind\"", 1), evidence.ErrMalformedLine},
		"not an event":               {`{"nope":1}`, evidence.ErrMalformedLine},
		"not JSON":                   {`hello`, evidence.ErrMalformedLine},
		"empty":                      {``, evidence.ErrMalformedLine},
		"a line over the bound":      {padded(bodyLine, evidence.MaxLineBytes+1), evidence.ErrLineTooLong},
		// Raw, a line separator is three bytes; the codec writes it as six.
		// The line fits as sent and not as the file would hold it.
		"a line over the bound once written": {longAsWritten(), evidence.ErrLineTooLong},
	}
	for name, c := range cases {
		events, err := otel.ReadRequest([]byte(requestWithBody(strconv.Quote(c.body))))
		if !errors.Is(err, otel.ErrRequest) || !errors.Is(err, c.want) || events != nil {
			t.Errorf("%s: ReadRequest = %v, %v; want ErrRequest and %v", name, events, err, c.want)
		}
	}
	if _, err := otel.ReadRequest([]byte(requestWithBody(strconv.Quote(padded(bodyLine, evidence.MaxLineBytes))))); err != nil {
		t.Errorf("a line at the bound was refused: %v", err)
	}
}

// padded is line with spaces before its closing brace up to n bytes. JSON
// counts them as white space, so the event is the same.
func padded(line string, n int) string {
	return line[:len(line)-1] + strings.Repeat(" ", n-len(line)) + "}"
}

// esc is the JSON escape of one UTF-16 code unit, built at run time so the
// source holds the escape and not the character it stands for.
func esc(hex string) string { return `\` + "u" + hex }

// longAsWritten is an event line under the bound as sent and over it as the
// codec writes it.
func longAsWritten() string {
	sep := string(rune(0x2028))
	prefix := `{"eventId":"evt-1","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"`
	rest := `","projectId":"proj-1","tenantId":"tenant-1","schemaVersion":"1.0"}`
	n := (evidence.MaxLineBytes - len(prefix) - len(rest)) / len(sep)
	return prefix + strings.Repeat(sep, n) + rest
}

// TestTheRequestBound: a request of MaxRequestBytes is read and one byte more
// is refused before a byte of it is parsed.
func TestTheRequestBound(t *testing.T) {
	req := base()
	at := req + strings.Repeat(" ", otel.MaxRequestBytes-len(req))
	if _, err := otel.ReadRequest([]byte(at)); err != nil {
		t.Errorf("a request of exactly the bound = %v", err)
	}
	over := at + " "
	if _, err := otel.ReadRequest([]byte(over)); !errors.Is(err, otel.ErrRequestTooLarge) {
		t.Errorf("a request one byte over the bound = %v, want ErrRequestTooLarge", err)
	}
}
