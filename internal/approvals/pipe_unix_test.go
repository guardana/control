//go:build unix

package approvals_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
)

// The entries anyone who may write the directory can leave behind under a name
// that reads like a record. A pipe blocks an open until a writer arrives, a
// directory is not a file at all, and a symbolic link reads bytes from outside
// the directory the store owns.
var notRegularFiles = map[string]func(t *testing.T, path string){
	"a named pipe": func(t *testing.T, path string) {
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatalf("making a pipe: %v", err)
		}
	},
	"a directory": func(t *testing.T, path string) {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("making a directory: %v", err)
		}
	},
	"a symbolic link out of the directory": func(t *testing.T, path string) {
		outside := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.WriteFile(outside, []byte("not this store's bytes"), 0o600); err != nil {
			t.Fatalf("writing outside the store: %v", err)
		}
		if err := os.Symlink(outside, path); err != nil {
			t.Fatalf("linking: %v", err)
		}
	},
}

// answerFast is how long a call over a directory of a handful of files may
// take. A call that blocks holds the store's only mutex, so every later call
// queues behind it; the deadline is what tells a refusal apart from that.
const answerFast = time.Second

// answerWithin fails unless call answers inside answerFast. The goroutine is
// left where it is on that path: it holds the store's only mutex for good, so
// waiting for it would turn a named failure into a stuck run.
func answerWithin(t *testing.T, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(answerFast):
		t.Fatalf("the call did not answer within %s: an entry that is not a regular file blocks the directory's only mutex", answerFast)
		return nil
	}
}

// The handles in this file are opened without a closing cleanup, and closed by
// hand where every call answered. Close takes the store's only mutex, which is
// the mutex a call this file left blocked in open(2) holds for good, so a
// handle registered for closing with the test would turn the failure these
// tests name into a run that hangs until the binary's timeout and reports
// nothing. Do not hand these back to the shared helpers.
func openPlaneUnclosed(t *testing.T) (*approvals.Plane, string) {
	t.Helper()
	dir := t.TempDir()
	p, err := approvals.OpenPlane(dir)
	if err != nil {
		t.Fatalf("opening the plane: %v", err)
	}
	return p, dir
}

func openApproverUnclosed(t *testing.T, dir string) *approvals.Approver {
	t.Helper()
	a, err := approvals.OpenApprover(dir)
	if err != nil {
		t.Fatalf("opening the approver: %v", err)
	}
	return a
}

// TestAnEntryThatIsNotARegularFileRefusesTheListing. What an entry is comes
// from the directory, not from its name, and the listing is where that is
// decided: a name alone would send the read into an open that never returns.
//
// context.Background: the call has to answer on its own, and a context the
// test cancels would hide a block behind a cancellation.
func TestAnEntryThatIsNotARegularFileRefusesTheListing(t *testing.T) {
	for name, create := range notRegularFiles {
		t.Run(name, func(t *testing.T) {
			p, dir := openPlaneUnclosed(t)
			h := held(t, firstApproval, "req-1")
			if err := p.Hold(t.Context(), h, minted); err != nil {
				t.Fatalf("holding: %v", err)
			}
			create(t, filepath.Join(dir, forgedApproval+".0-held.rec"))

			err := answerWithin(t, func() error {
				_, err := p.Find(context.Background(), h.Binding, minted)
				return err
			})
			if !errors.Is(err, approvals.ErrForeignFile) {
				t.Fatalf("finding beside %s = %v, want ErrForeignFile", name, err)
			}
			if err := p.Close(); err != nil {
				t.Fatalf("closing the plane: %v", err)
			}
		})
	}
}

// TestAnEntryThatIsNotARegularFileRefusesTheOpen: the same entries at the
// other end, where a plane that opened one would hang before it bound
// anything and a doctor would hang printing a listing.
func TestAnEntryThatIsNotARegularFileRefusesTheOpen(t *testing.T) {
	for name, create := range notRegularFiles {
		t.Run(name, func(t *testing.T) {
			p, dir := openPlaneUnclosed(t)
			if err := p.Close(); err != nil {
				t.Fatalf("closing the maker of the store: %v", err)
			}
			create(t, filepath.Join(dir, firstApproval+".0-held.rec"))

			err := answerWithin(t, func() error {
				opened, err := approvals.OpenPlane(dir)
				if err == nil {
					return opened.Close()
				}
				return err
			})
			if !errors.Is(err, approvals.ErrForeignFile) {
				t.Fatalf("opening over %s = %v, want ErrForeignFile", name, err)
			}
		})
	}
}

// TestAPipeNamedLikeTheMarkerRefusesTheOpen. The marker is read by name and
// before anything else, so a listing that took a name for a file would open it
// before a plane had bound anything. The marker decides first here, and that
// precedence is what names the refusal.
func TestAPipeNamedLikeTheMarkerRefusesTheOpen(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "store.meta"), 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}
	err := answerWithin(t, func() error {
		opened, err := approvals.OpenPlane(dir)
		if err == nil {
			return opened.Close()
		}
		return err
	})
	if !errors.Is(err, approvals.ErrNotAStore) {
		t.Fatalf("opening over a pipe named like the marker = %v, want ErrNotAStore", err)
	}
}

// TestAPipeNamedLikeARecordRefusesAReadByIdRatherThanBlocking. An answer is
// read by the approval id an operator typed, not from a listing, so the
// listing's refusal never runs: this is the one input that the guard at the
// open answers on its own. The entry appears after the handle is open, which
// is the only way such a read is reached at all.
func TestAPipeNamedLikeARecordRefusesAReadByIdRatherThanBlocking(t *testing.T) {
	p, dir := openPlaneUnclosed(t)
	if err := p.Hold(t.Context(), held(t, firstApproval, "req-1"), minted); err != nil {
		t.Fatalf("holding: %v", err)
	}
	a := openApproverUnclosed(t, dir)
	const piped = "PIPED1"
	if err := syscall.Mkfifo(filepath.Join(dir, piped+".0-held.rec"), 0o600); err != nil {
		t.Fatalf("making a pipe: %v", err)
	}

	err := answerWithin(t, func() error {
		_, err := a.Answer(context.Background(), piped,
			controlv1.ApprovalState_APPROVAL_STATE_APPROVED, "approver-1", "signed off", minted.Add(time.Minute))
		return err
	})
	if !errors.Is(err, approvals.ErrForeignFile) {
		t.Fatalf("answering an id a pipe is filed under = %v, want ErrForeignFile", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("closing the approver: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("closing the plane: %v", err)
	}
}
