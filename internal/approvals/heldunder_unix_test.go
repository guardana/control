//go:build unix

package approvals_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/guardana/control/internal/approvals"
)

// TestAReaderOpensAnotherAccountsDirectory: a directory another account
// owns is refused to an approver and read by a reader, which says it does
// not run as the directory's owner; a group-writable one is refused to both.
func TestAReaderOpensAnotherAccountsDirectory(t *testing.T) {
	p, dir := openPlane(t, approvals.WithAuthority(approvals.HeldUnderSigned))
	if err := p.Hold(t.Context(), held(t, "APPROVAL1", "req-1"), minted); err != nil {
		t.Fatal(err)
	}
	if !openReader(t, dir).PlaneAccount() {
		t.Error("a reader running as the directory's owner says it does not")
	}
	restore := approvals.SetEffectiveUID(func() int { return os.Geteuid() + 1 })
	defer restore()
	if _, err := approvals.OpenApprover(dir); !errors.Is(err, approvals.ErrOwner) {
		t.Errorf("OpenApprover of another account's directory = %v, want ErrOwner", err)
	}
	r := openReader(t, dir)
	if r.PlaneAccount() {
		t.Error("a reader under another account says it is the directory's owner")
	}
	rec, err := r.Held(t.Context(), "APPROVAL1")
	if err != nil || rec.ApprovalID != "APPROVAL1" || rec.Tenant != "tenant-1" || rec.Project != "proj-1" {
		t.Fatalf("Held = %+v, %v", rec, err)
	}
	if _, err := r.Held(t.Context(), "NOSUCH"); !errors.Is(err, approvals.ErrNoApproval) {
		t.Errorf("Held of an unknown id = %v, want ErrNoApproval", err)
	}
	if err := os.Chmod(dir, 0o770); err != nil { //nolint:gosec // G302: the mode under test is one the store refuses
		t.Fatal(err)
	}
	if _, err := approvals.OpenReader(dir); !errors.Is(err, approvals.ErrPermissions) {
		t.Errorf("OpenReader of a group-writable directory = %v, want ErrPermissions", err)
	}
}

// TestAnAnswerFileIsNeverReplaced: File links an answer into the inbox mode
// 0644 whatever the umask, refuses a second file of the same name and leaves
// the first as it was, and refuses a name the inbox would not read.
func TestAnAnswerFileIsNeverReplaced(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	in, err := approvals.OpenInbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	name, err := approvals.AnswerFileName("APPROVAL1", "ed25519-0011223344556677-approve")
	if err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0o077)
	err = in.File(name, []byte("first"))
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := in.File(name, []byte("second")); !errors.Is(err, approvals.ErrAnswerFiled) {
		t.Errorf("a second file of one name = %v, want ErrAnswerFiled", err)
	}
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the answer file: %v, %v; want mode 0644", info, err)
	}
	if got := readFile(t, dir, name); string(got) != "first" {
		t.Errorf("the answer file reads %q, want the first", got)
	}
	if err := in.File("APPROVAL1.answer", []byte("x")); !errors.Is(err, approvals.ErrRecordName) {
		t.Errorf("a name with no label = %v, want ErrRecordName", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("the inbox holds %d entries (%v), want the one answer", len(entries), err)
	}
}
