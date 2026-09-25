package otel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// TestTheWorstRecordFitsInOneRequest: a record at the line bound whose
// identifier encoding/json escapes the most, as the exporter sends it alone,
// is under MaxRequestBytes and reads back as itself. A smaller bound would
// refuse it on every attempt and send it to the quarantine.
func TestTheWorstRecordFitsInOneRequest(t *testing.T) {
	ev := &controlv1.Event{EventId: "evt-1", Kind: controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED,
		RequestId: "req-1", ProjectId: "proj-1", TenantId: "tenant-1", SchemaVersion: evidence.SchemaVersion}
	var line bytes.Buffer
	if err := evidence.EncodeJSONL(&line, []*controlv1.Event{ev}); err != nil {
		t.Fatal(err)
	}
	// "runId":"" and the newline are not part of the line's room for the id.
	room := evidence.MaxLineBytes - (line.Len() - 1) - len(`,"runId":""`)
	ev.RunId = strings.Repeat("<", room)
	line.Reset()
	if err := evidence.EncodeJSONL(&line, []*controlv1.Event{ev}); err != nil {
		t.Fatalf("the record is not at the line bound: %v", err)
	}
	if line.Len()-1 != evidence.MaxLineBytes {
		t.Fatalf("the line is %d bytes, want exactly %d", line.Len()-1, evidence.MaxLineBytes)
	}
	body, err := encode([]*controlv1.Event{ev})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 8*evidence.MaxLineBytes {
		t.Fatalf("the request is %d bytes; this case is meant to be the worst one", len(body))
	}
	if len(body) > MaxRequestBytes {
		t.Fatalf("one record at the line bound makes a request of %d bytes, over the bound of %d", len(body), MaxRequestBytes)
	}
	got, err := ReadRequest(body)
	if err != nil || len(got) != 1 || !proto.Equal(got[0], ev) {
		t.Fatalf("the worst record did not read back as itself: %v", err)
	}
}

// FuzzReadRequest: nothing the reader is handed makes it panic, and whatever
// it accepts the exporter's encoder writes back as a request it reads as the
// same events.
func FuzzReadRequest(f *testing.F) {
	golden, err := os.ReadFile(filepath.Join("..", "..", "testdata", "otlp", "export_logs_request.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(golden)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"{\"eventId\":\"e\"}"},"severityNumber":9}]}]}]}`))
	f.Add([]byte(`{"resource_logs":[]}`))
	f.Add([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"\ud800"}}]}]}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		events, err := ReadRequest(body)
		if err != nil {
			if events != nil {
				t.Fatalf("a refusal returned %d events", len(events))
			}
			return
		}
		again, err := encode(events)
		if err != nil {
			t.Fatalf("an accepted request's events do not encode: %v", err)
		}
		if len(again) > MaxRequestBytes {
			return
		}
		back, err := ReadRequest(again)
		if err != nil || len(back) != len(events) {
			t.Fatalf("the encoder's request of accepted events reads as %d events, %v", len(back), err)
		}
		for i := range events {
			if !proto.Equal(events[i], back[i]) {
				t.Fatalf("event %d changed on the way round", i)
			}
		}
	})
}
