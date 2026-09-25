//go:build unix

package console

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
)

// freshState reads one record's state through an approver opened now, not
// through the page's handle.
func freshState(t *testing.T, dir, approvalID string) controlv1.ApprovalState {
	t.Helper()
	store, err := approvals.OpenApprover(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	l, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Record.ApprovalID == approvalID {
			return e.Record.Approval.GetState()
		}
	}
	t.Fatalf("%s holds no record %s", dir, approvalID)
	return 0
}

// TestAPageRefusesADirectoryLoosenedUnderIt: the approvals command refuses a
// directory others may write at every run, and so does the page, which was
// started before the change. The answer and the listing carry the store's
// sentence and the record stays pending.
func TestAPageRefusesADirectoryLoosenedUnderIt(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "HELD1")
	s := serve(t, dir, "")
	body := s.answering(t, "HELD1", "")
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: the mode under test
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: a directory needs its execute bit; 0700 is owner-only
	a := s.do(t, s.write("/api/approve", body))
	if a.status != http.StatusConflict || !strings.Contains(a.body, approvals.ErrDirectoryChanged.Error()) {
		t.Errorf("an answer in a loosened directory: %d %q, want 409 saying the directory changed", a.status, a.body)
	}
	if l := readListing(t, s); !strings.Contains(l.Error, approvals.ErrDirectoryChanged.Error()) || l.PlaneRunning != nil {
		t.Errorf("the listing of a loosened directory reads %+v", l)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 is owner-only
		t.Fatal(err)
	}
	if got := freshState(t, dir, "HELD1"); got != pending {
		t.Errorf("the record is %s", got)
	}
}

// TestAPageRefusesADirectorySwappedForALink: the directory is renamed and a
// link to another store put at its name. The page answers nothing there and
// the other store's record stays pending.
func TestAPageRefusesADirectorySwappedForALink(t *testing.T) {
	dir, _ := newPlane(t)
	s := serve(t, dir, "")
	other, plane2 := newPlane(t)
	hold(t, plane2, "ELSEWHERE")
	digest := freshDigest(t, other, "ELSEWHERE")
	if err := os.Rename(dir, filepath.Join(filepath.Dir(dir), "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, dir); err != nil {
		t.Fatal(err)
	}
	a := s.do(t, s.write("/api/approve", `{"id":"ELSEWHERE","reason":"","action_digest":"`+digest+`"}`))
	if a.status != http.StatusConflict || !strings.Contains(a.body, approvals.ErrDirectoryChanged.Error()) {
		t.Errorf("an answer through the link: %d %q, want 409 saying the directory changed", a.status, a.body)
	}
	if got := freshState(t, other, "ELSEWHERE"); got != pending {
		t.Errorf("the other store's record is %s", got)
	}
}

// TestAPageSaysADirectoryRemovedUnderItIsNotEmpty: the listing fails, and
// does not read as an empty directory.
func TestAPageSaysADirectoryRemovedUnderItIsNotEmpty(t *testing.T) {
	dir, _ := newPlane(t)
	s := serve(t, dir, "")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if l := readListing(t, s); l.Error == "" || l.Complete || l.Empty != "" {
		t.Errorf("a removed directory reads as %+v", l)
	}
}

func freshDigest(t *testing.T, dir, approvalID string) string {
	t.Helper()
	store, err := approvals.OpenApprover(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	l, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Record.ApprovalID == approvalID {
			return e.Record.Approval.GetActionDigest()
		}
	}
	t.Fatalf("%s holds no record %s", dir, approvalID)
	return ""
}
