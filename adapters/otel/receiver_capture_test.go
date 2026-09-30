package otel_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/adapters/otel"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/trailfile"
)

// previewMarker stands for content a producer captured. The collector keeps
// no preview whatever the producer's capture setting was.
const previewMarker = "captured-by-the-producer"

// previewedEvents are a proposed action and its result as a producer that
// captured content sends them: each with its preview and the profile naming it.
func previewedEvents() []*controlv1.Event {
	const hash = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	head := func(id string, kind controlv1.EventKind) *controlv1.Event {
		return &controlv1.Event{EventId: id, Kind: kind, RequestId: "req-1", ProjectId: "proj-1", TenantId: "tenant-1", SchemaVersion: "1.0"}
	}
	proposed := head("evt-1", controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED)
	proposed.Payload = &controlv1.Event_Proposed{Proposed: &controlv1.ActionEnvelope{
		Arguments: &controlv1.Arguments{
			CanonicalHash:    hash,
			RedactedPreview:  `{"note": "` + previewMarker + `"}`,
			RedactionProfile: "profile-" + previewMarker,
		},
	}}
	result := head("evt-2", controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED)
	result.Payload = &controlv1.Event_Result{Result: &controlv1.ActionResult{
		SchemaVersion:         "1.0",
		Status:                controlv1.ResultStatus_RESULT_STATUS_SUCCESS,
		ResultHash:            hash,
		RedactedResultPreview: "the tool said " + previewMarker,
		RedactionProfile:      "profile-" + previewMarker,
	}}
	return []*controlv1.Event{proposed, result}
}

// postEvent sends ev to h as the one record of a request and returns the status.
func postEvent(t *testing.T, h http.Handler, ev *controlv1.Event) int {
	t.Helper()
	var line bytes.Buffer
	if err := evidence.EncodeJSONL(&line, []*controlv1.Event{ev}); err != nil {
		t.Fatalf("encoding %s: %v", ev.GetEventId(), err)
	}
	body := requestWithBody(strconv.Quote(strings.TrimSuffix(line.String(), "\n")))
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestAReceivedPreviewIsNotKept: previews and their profiles a producer sent
// are cleared before the trail file holds the event, and the rest of the
// record is kept. The same events sent again are the lines the file holds
// already, so nothing is written twice.
func TestAReceivedPreviewIsNotKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trail.jsonl")
	w, err := trailfile.Open(path)
	if err != nil {
		t.Fatalf("opening the trail file: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	h, err := otel.NewReceiver(w, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	postAll(t, h)
	first := readTrail(t, path)
	assertUncaptured(t, first)

	postAll(t, h)
	if again := readTrail(t, path); !bytes.Equal(again, first) {
		t.Errorf("sending the same events again changed the trail file:\n%s\nwas:\n%s", again, first)
	}
}

// assertUncaptured fails the test unless trail holds the two previewed events
// with their hashes and without a preview or a profile.
func assertUncaptured(t *testing.T, trail []byte) {
	t.Helper()
	if bytes.Contains(trail, []byte(previewMarker)) {
		t.Errorf("the trail file holds what the producer captured:\n%s", trail)
	}
	kept, err := evidence.DecodeJSONL(bytes.NewReader(trail), 10)
	if err != nil {
		t.Fatalf("reading the trail file back: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("the trail file holds %d events, want 2", len(kept))
	}
	if args := kept[0].GetProposed().GetArguments(); args.GetCanonicalHash() == "" || args.GetRedactedPreview() != "" || args.GetRedactionProfile() != "" {
		t.Errorf("the proposed event's arguments read back as %v, want the hash and no preview", args)
	}
	if res := kept[1].GetResult(); res.GetResultHash() == "" || res.GetRedactedResultPreview() != "" || res.GetRedactionProfile() != "" {
		t.Errorf("the result reads back as %v, want the hash and no preview", res)
	}
}

// postAll sends every previewed event to h, one request each, and fails the
// test on any answer but 200.
func postAll(t *testing.T, h http.Handler) {
	t.Helper()
	for _, ev := range previewedEvents() {
		if got := postEvent(t, h, ev); got != http.StatusOK {
			t.Fatalf("posting %s answered %d, want 200", ev.GetEventId(), got)
		}
	}
}

func readTrail(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
