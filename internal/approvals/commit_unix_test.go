//go:build unix

package approvals_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
)

// TestEveryFileTheStoreWritesIsItsOwnersAlone: under a umask that narrows
// nothing, the marker, a projection, a held record and an answer are each
// written readable and writable by their owner and nobody else.
func TestEveryFileTheStoreWritesIsItsOwnersAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "approvals")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0)
	err := holdAndAnswer(t, dir)
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"store.meta", "APPROVAL1.view.json", "APPROVAL1.1-answered.rec"} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s: mode %04o, want 0600", name, got)
		}
	}
}

func holdAndAnswer(t *testing.T, dir string) error {
	t.Helper()
	p, err := approvals.OpenPlane(dir)
	if err != nil {
		return err
	}
	defer func() { _ = p.Close() }()
	if err := p.Hold(t.Context(), held(t, firstApproval, "req-1"), minted); err != nil {
		return err
	}
	a, err := approvals.OpenApprover(dir)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	_, err = a.Answer(t.Context(), firstApproval, controlv1.ApprovalState_APPROVAL_STATE_APPROVED, "approver-1", "", minted)
	return err
}

// TestAnAnswerThatCannotBeMadeDurableIsNotFiled: the answer is linked, and
// then the directory cannot be opened to force it to disk. Answer reports the
// failure, and the name it linked is gone with it, so the plane never reads an
// answer the approver was told did not happen, and a second answer is taken.
func TestAnAnswerThatCannotBeMadeDurableIsNotFiled(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the superuser opens a directory whatever its mode")
	}
	p, dir := openPlane(t)
	if err := p.Hold(t.Context(), held(t, firstApproval, "req-1"), minted); err != nil {
		t.Fatal(err)
	}
	var armed atomic.Bool
	armed.Store(true)
	a := openApprover(t, dir, approvals.WithSteps(func(step string) {
		if step == approvals.StepLinked && armed.CompareAndSwap(true, false) {
			if err := os.Chmod(dir, 0o300); err != nil { //nolint:gosec // G302: a directory its owner cannot list, so the sync fails
				t.Error(err)
			}
		}
	}))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: the store's own mode, back
	approved := controlv1.ApprovalState_APPROVAL_STATE_APPROVED
	_, err := a.Answer(t.Context(), firstApproval, approved, "approver-1", "", minted)
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil { //nolint:gosec // G302: the store's own mode, back
		t.Fatal(chmodErr)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Answer with a directory it cannot force to disk = %v, want fs.ErrPermission", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "APPROVAL1.1-answered.rec")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a failed answer left its name: %v", err)
	}
	if _, err := a.Answer(t.Context(), firstApproval, approved, "approver-1", "", minted); err != nil {
		t.Fatalf("the answer after the failed one: %v", err)
	}
}
