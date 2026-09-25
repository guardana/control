package trailfile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

const (
	kindProposed  = controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED
	kindDecided   = controlv1.EventKind_EVENT_KIND_POLICY_DECIDED
	kindRequested = controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED
	kindStarted   = controlv1.EventKind_EVENT_KIND_ACTION_STARTED
	kindCompleted = controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED
	kindBlocked   = controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED
)

// firstLine is the line the codec writes for event(1): spelled out here so a
// test of the file does not take its expected bytes from the writer.
const firstLine = `{"eventId":"e1","kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r1","projectId":"p1","tenantId":"t1","schemaVersion":"1.0","enforcementMode":"ENFORCEMENT_MODE_OBSERVE"}` + "\n"

// event is a proposal on a request of its own.
func event(n int) *controlv1.Event {
	id := strconv.Itoa(n)
	return &controlv1.Event{EventId: "e" + id, Kind: kindProposed, RequestId: "r" + id, ProjectId: "p1", TenantId: "t1",
		SchemaVersion: evidence.SchemaVersion, EnforcementMode: controlv1.EnforcementMode_ENFORCEMENT_MODE_OBSERVE}
}

// chain is one request's events of the kinds given, each linked to the one
// before, with an execution where a kind needs one.
func chain(project, request string, kinds ...controlv1.EventKind) []*controlv1.Event {
	out := make([]*controlv1.Event, 0, len(kinds))
	prev := ""
	for i, kind := range kinds {
		ev := &controlv1.Event{EventId: project + "-" + request + "-" + strconv.Itoa(i), Kind: kind, RequestId: request,
			ProjectId: project, TenantId: "t1", SchemaVersion: evidence.SchemaVersion, PrevEventId: prev,
			EnforcementMode: controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE}
		if kind == kindStarted || kind == kindCompleted || kind == controlv1.EventKind_EVENT_KIND_ACTION_FAILED {
			ev.ExecutionId = "x-" + request
		}
		prev = ev.EventId
		out = append(out, ev)
	}
	return out
}

func lines(t *testing.T, events ...*controlv1.Event) string {
	t.Helper()
	var b bytes.Buffer
	if err := evidence.EncodeJSONL(&b, events); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func trailPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "trail.jsonl")
}

func openWriter(t *testing.T, path string) *Writer {
	t.Helper()
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s) = %v", path, err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func mustAppend(t *testing.T, w *Writer, events ...*controlv1.Event) {
	t.Helper()
	if err := w.Append(context.Background(), events); err != nil {
		t.Fatalf("Append = %v", err)
	}
}

func contents(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
