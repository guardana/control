package otel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// MaxRequestBytes bounds one request the receiver reads. It admits one record
// at evidence.MaxLineBytes as the exporter spells it: the body escapes the line
// again, and encoding/json writes each of "<", ">" and "&" as six bytes, so the
// body reaches six times the line, and an identifier holding them reaches that
// again in its attribute. A batch over the bound is refused, and the exporter
// halves it down to records that fit.
const MaxRequestBytes = 16 * evidence.MaxLineBytes

const (
	// ErrRequest is a request the receiver refuses: not the JSON of an
	// ExportLogsServiceRequest as the exporter writes it, or a log record whose
	// body is not one evidence line.
	ErrRequest Error = "otel: the request is not an export of evidence lines"
	// ErrRequestTooLarge is a request over MaxRequestBytes.
	ErrRequestTooLarge Error = "otel: the request is over the bound"
)

// maxSeverityNumber is SEVERITY_NUMBER_FATAL4, the last value OTLP declares.
const maxSeverityNumber = 24

// severityNames are the names of SeverityNumber, each at its number.
var severityNames = func() map[string]bool {
	names := map[string]bool{"SEVERITY_NUMBER_UNSPECIFIED": true}
	for _, level := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"} {
		names["SEVERITY_NUMBER_"+level] = true
		for i := 2; i <= 4; i++ {
			names["SEVERITY_NUMBER_"+level+strconv.Itoa(i)] = true
		}
	}
	return names
}()

// ReadRequest reads one request and returns the event each log record's body
// holds, in the request's order.
//
// The request is ExportLogsServiceRequest in OTLP's JSON encoding, holding the
// members encode writes and no other: a member the protocol has and the
// exporter never writes, such as a trace id or a schema URL, is refused like
// one it does not have, because this receiver would otherwise drop it unread.
// OTLP asks a receiver to ignore unknown members; this one reads its own
// exporter's requests and ignores nothing. Every member appears once, under
// its lowerCamelCase name only: OTLP says the original field names are not
// valid keys, so "resource_logs" is refused, although the protobuf JSON mapping
// and the exporter's answer reader both accept that spelling. An enum is read
// by its name, as the mapping and the exporter write it, or by its number, as
// OTLP requires; a 64-bit integer as a decimal string or a number; null as an
// absent member. The request is UTF-8 and no string in it escapes half of a
// surrogate pair, which encoding/json would otherwise turn into U+FFFD without
// a word.
//
// Every log record's body is a string holding one evidence line, which decodes
// as exactly one event under the evidence codec's own rules and bound, and
// which the codec writes back within its bound. The attributes are read for
// their shape and not their meaning: the body is the record.
func ReadRequest(body []byte) ([]*controlv1.Event, error) {
	if len(body) > MaxRequestBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrRequestTooLarge, len(body), MaxRequestBytes)
	}
	if !utf8.Valid(body) {
		return nil, refuse("the request is not UTF-8")
	}
	r := &requestReader{}
	if err := r.request(body); err != nil {
		return nil, err
	}
	if r.events == nil {
		r.events = []*controlv1.Event{}
	}
	return r.events, nil
}

type requestReader struct {
	events []*controlv1.Event
	line   bytes.Buffer
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRequest}, args...)...)
}

func (r *requestReader) request(raw []byte) error {
	m, err := object(raw, "request", "resourceLogs")
	if err != nil {
		return err
	}
	return each(m["resourceLogs"], "resourceLogs", r.resourceLogs)
}

func (r *requestReader) resourceLogs(raw json.RawMessage) error {
	m, err := object(raw, "resourceLogs", "resource", "scopeLogs")
	if err != nil {
		return err
	}
	if res, ok := m["resource"]; ok {
		inner, err := object(res, "resource", "attributes")
		if err != nil {
			return err
		}
		if err := each(inner["attributes"], "attributes", readAttribute); err != nil {
			return err
		}
	}
	return each(m["scopeLogs"], "scopeLogs", r.scopeLogs)
}

func (r *requestReader) scopeLogs(raw json.RawMessage) error {
	m, err := object(raw, "scopeLogs", "scope", "logRecords")
	if err != nil {
		return err
	}
	if scope, ok := m["scope"]; ok {
		inner, err := object(scope, "scope", "name")
		if err != nil {
			return err
		}
		if _, _, err := text(inner["name"], "the scope's name"); err != nil {
			return err
		}
	}
	return each(m["logRecords"], "logRecords", r.logRecord)
}

func (r *requestReader) logRecord(raw json.RawMessage) error {
	number := len(r.events) + 1
	m, err := object(raw, "log record", "timeUnixNano", "severityNumber", "severityText", "body", "attributes")
	if err != nil {
		return err
	}
	if err := timeUnixNano(m["timeUnixNano"]); err != nil {
		return err
	}
	if err := severityNumber(m["severityNumber"]); err != nil {
		return err
	}
	if _, _, err := text(m["severityText"], "a severity text"); err != nil {
		return err
	}
	if err := each(m["attributes"], "attributes", readAttribute); err != nil {
		return err
	}
	// An absent body, like an absent attribute value, reaches stringValue as
	// nil, which is not an object and is refused there.
	line, err := stringValue(m["body"], "a log record's body")
	if err != nil {
		return err
	}
	event, err := r.evidenceLine(line)
	if err != nil {
		return fmt.Errorf("%w: log record %d: %w", ErrRequest, number, err)
	}
	r.events = append(r.events, event)
	return nil
}

// evidenceLine decodes line as the one event it has to be, and checks the
// codec writes that event back within its bound: the file the records go to
// holds the codec's line, and a record that fits as sent and not as written
// would be refused there, after the request was accepted.
func (r *requestReader) evidenceLine(line string) (*controlv1.Event, error) {
	events, err := evidence.DecodeJSONL(strings.NewReader(line+"\n"), 1)
	if err != nil {
		return nil, err
	}
	r.line.Reset()
	if err := evidence.EncodeJSONL(&r.line, events); err != nil {
		return nil, err
	}
	return events[0], nil
}

func readAttribute(raw json.RawMessage) error {
	m, err := object(raw, "attribute", "key", "value")
	if err != nil {
		return err
	}
	if _, _, err := text(m["key"], "an attribute's key"); err != nil {
		return err
	}
	_, err = stringValue(m["value"], "an attribute's value")
	return err
}

// stringValue reads an AnyValue that has to hold a string, and nothing else.
func stringValue(raw json.RawMessage, what string) (string, error) {
	m, err := object(raw, what, "stringValue")
	if err != nil {
		return "", err
	}
	s, present, err := text(m["stringValue"], what)
	switch {
	case err != nil:
		return "", err
	case !present:
		return "", refuse("%s holds no string", what)
	}
	return s, nil
}

func timeUnixNano(raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	digits := string(raw)
	if raw[0] == '"' {
		s, _, err := text(raw, "a time")
		if err != nil {
			return err
		}
		digits = s
	}
	if _, err := strconv.ParseUint(digits, 10, 64); err != nil {
		return refuse("a time is not an unsigned 64-bit count of nanoseconds")
	}
	return nil
}

func severityNumber(raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	if raw[0] == '"' {
		name, _, err := text(raw, "a severity number")
		if err != nil {
			return err
		}
		if !severityNames[name] {
			return refuse("a severity number names no value OTLP declares")
		}
		return nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 32)
	if err != nil || n < 0 || n > maxSeverityNumber {
		return refuse("a severity number is no value OTLP declares")
	}
	return nil
}
