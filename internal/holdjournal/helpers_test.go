// Helpers shared by this package's tests. Every fixture here builds an input;
// no expected value in any test is read back from one of these functions.
package holdjournal_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/holdjournal"
)

// base is the clock every fixture is minted against.
func base() time.Time { return time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC) }

// newDir is a directory a journal may own: a fresh one, readable and writable
// by its owner alone.
func newDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 is owner-only
		t.Fatalf("setting the directory's mode: %v", err)
	}
	return dir
}

// openJournal opens dir and closes it when the test ends.
func openJournal(t *testing.T, dir string, opts ...holdjournal.Option) *holdjournal.Journal {
	t.Helper()
	j, err := holdjournal.Open(dir, opts...)
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	t.Cleanup(func() {
		if err := j.Close(); err != nil && !errors.Is(err, holdjournal.ErrClosed) {
			t.Errorf("closing the journal: %v", err)
		}
	})
	return j
}

// heldEntry is one complete entry in held, under requestID.
func heldEntry(requestID string) holdjournal.Entry {
	return holdjournal.Entry{
		SchemaVersion: holdjournal.SchemaVersion,
		State:         holdjournal.StateHeld,
		IDs: evidence.IDs{
			RequestID: requestID, RunID: "run-7",
			ProjectID: "proj-2", TenantID: "tenant-3",
		},
		LastEventID: "evt-" + requestID,
		Binding:     approval.Binding("bind-" + requestID),
		Approval: &controlv1.Approval{
			SchemaVersion: "1.0",
			ApprovalId:    "appr-" + requestID,
			RequestId:     requestID,
			State:         controlv1.ApprovalState_APPROVAL_STATE_PENDING,
			RequestedAt:   timestamppb.New(base()),
			ExpiresAt:     timestamppb.New(base().Add(15 * time.Minute)),
		},
		Expires: base().Add(15 * time.Minute),
	}
}

// record files one held entry and fails the test if the journal refuses it.
func record(t *testing.T, j *holdjournal.Journal, requestID string) {
	t.Helper()
	if err := j.Record(context.Background(), heldEntry(requestID)); err != nil {
		t.Fatalf("recording %q: %v", requestID, err)
	}
}

// mark flips one entry and fails the test if the journal refuses the flip.
func mark(t *testing.T, j *holdjournal.Journal, requestID string, state holdjournal.State) {
	t.Helper()
	if err := j.Mark(context.Background(), requestID, state); err != nil {
		t.Fatalf("flipping %q into %v: %v", requestID, state, err)
	}
}

// entryFiles is the names under dir that are neither the marker nor a
// temporary file, read from the directory itself rather than from the
// package's own name encoding.
func entryFiles(t *testing.T, dir string) []string {
	t.Helper()
	listed, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	var names []string
	for _, e := range listed {
		if name := e.Name(); filepath.Ext(name) == ".hold" {
			names = append(names, name)
		}
	}
	return names
}

// readFile and writeFile move bytes under a journal's directory the way a
// corrupted disk or another writer would.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path this test composed under its own temporary directory
	if err != nil {
		t.Fatalf("reading %q: %v", path, err)
	}
	return raw
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}
}

// entryFileOf is the path of the file holding requestID, found by reading the
// directory rather than by asking the package how it names a file.
func entryFileOf(t *testing.T, dir, requestID string) string {
	t.Helper()
	for _, name := range entryFiles(t, dir) {
		path := filepath.Join(dir, name)
		if bytes.Contains(readFile(t, path), []byte(requestID)) {
			return path
		}
	}
	t.Fatalf("no file under the directory holds %q", requestID)
	return ""
}
