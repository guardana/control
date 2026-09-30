package approvals_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
)

// The names a resolved record of firstApproval takes, spelled as the package
// documentation spells them.
const (
	heldName       = firstApproval + ".0-held.rec"
	answeredName   = firstApproval + ".1-answered.rec"
	notResumedName = firstApproval + ".2-not-resumed.rec"
	consumedName   = firstApproval + ".3-consumed.rec"
)

// afterLink returns an option whose step runs plant the first time a name is
// linked after arm: between a transition's link and the re-read behind it,
// which is where a plane that crashed between its own link and unlink leaves
// its mark.
func afterLink(t *testing.T, plant func()) (opt approvals.Option, arm func()) {
	t.Helper()
	var armed, planted atomic.Bool
	steps := func(step string) {
		if step != approvals.StepLinked || !armed.Load() || !planted.CompareAndSwap(false, true) {
			return
		}
		plant()
	}
	t.Cleanup(func() {
		if armed.Load() && !planted.Load() {
			t.Error("the plant never ran: nothing was linked")
		}
	})
	return approvals.WithSteps(steps), func() { armed.Store(true) }
}

// resolvedRecord runs firstApproval for req-1 to name in a directory of its
// own and returns the record the plane wrote there, so the record planted
// elsewhere is one the plane itself would write.
func resolvedRecord(t *testing.T, name string) []byte {
	t.Helper()
	p, dir, h := heldOne(t)
	switch name {
	case consumedName:
		answerAt(t, dir, minted.Add(time.Minute))
		if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, minted.Add(2*time.Minute)); err != nil {
			t.Fatalf("consuming: %v", err)
		}
	case notResumedName:
		if err := p.Resolve(t.Context(), h.Binding, "req-1", approvals.ResolutionNotResumed, minted.Add(time.Minute)); err != nil {
			t.Fatalf("resolving: %v", err)
		}
	default:
		t.Fatalf("no plane resolves a record to %s", name)
	}
	return readFile(t, dir, name)
}

// heldWith holds firstApproval for req-1 on a plane opened with opts.
func heldWith(t *testing.T, opts ...approvals.Option) (*approvals.Plane, string, approvals.Hold) {
	t.Helper()
	p, dir := openPlane(t, opts...)
	h := held(t, firstApproval, "req-1")
	if err := p.Hold(t.Context(), h, minted); err != nil {
		t.Fatalf("holding: %v", err)
	}
	return p, dir, h
}

func assertNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := recordNames(t, dir)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the directory holds %v, want %v", got, want)
	}
}

// TestAnAnswerLinkedBesideAResolvedRecordIsTakenBack: the answer's link wins
// the name, and a record the plane resolved is already there. The answer is
// refused and its record unlinked, so no answer stands beside a record an
// execution spent or the plane gave up on.
func TestAnAnswerLinkedBesideAResolvedRecordIsTakenBack(t *testing.T) {
	cases := []struct {
		resolved string
		want     approvals.Error
	}{
		{consumedName, approvals.ErrApprovalConsumed},
		{notResumedName, approvals.ErrResolved},
	}
	for _, c := range cases {
		t.Run(c.resolved, func(t *testing.T) {
			body := resolvedRecord(t, c.resolved)
			_, dir, _ := heldOne(t)
			opt, arm := afterLink(t, func() { writeFile(t, dir, c.resolved, body) })
			a := openApprover(t, dir, opt)
			arm()
			_, err := a.Answer(t.Context(), firstApproval, controlv1.ApprovalState_APPROVAL_STATE_APPROVED, "approver-1", "", minted.Add(time.Minute))
			if !errors.Is(err, c.want) {
				t.Fatalf("answering beside %s = %v, want %v", c.resolved, err, c.want)
			}
			assertNames(t, dir, heldName, c.resolved)
		})
	}
}

// unreadable are the ways a resolved name can exist without being a record
// the package reads: a directory, and a relative symbolic link to a name that
// is not there, which a read that follows it takes for no name at all.
var unreadable = []struct {
	name  string
	plant func(t *testing.T, path string)
}{
	{"directory", func(t *testing.T, path string) {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Errorf("planting a directory: %v", err)
		}
	}},
	{"dangling link", func(t *testing.T, path string) {
		if err := os.Symlink("absent", path); err != nil {
			t.Errorf("planting a link: %v", err)
		}
	}},
}

// TestAnAnswerBesideAResolvedNameItCannotReadIsRefused: a name the answer
// cannot read is not a name that is absent. Anything but a regular file at
// either resolved name refuses the answer, and the answered record is taken
// back.
func TestAnAnswerBesideAResolvedNameItCannotReadIsRefused(t *testing.T) {
	for _, resolved := range []string{consumedName, notResumedName} {
		for _, u := range unreadable {
			t.Run(resolved+"/"+u.name, func(t *testing.T) {
				_, dir, _ := heldOne(t)
				opt, arm := afterLink(t, func() { u.plant(t, filepath.Join(dir, resolved)) })
				a := openApprover(t, dir, opt)
				arm()
				_, err := a.Answer(t.Context(), firstApproval, controlv1.ApprovalState_APPROVAL_STATE_APPROVED, "approver-1", "", minted.Add(time.Minute))
				if !errors.Is(err, approvals.ErrForeignFile) {
					t.Fatalf("answering beside a %s at %s = %v, want ErrForeignFile", u.name, resolved, err)
				}
				assertNames(t, dir, heldName, resolved)
			})
		}
	}
}

// TestNotResumedLinkedBesideAConsumedRecordIsTakenBack: the total order of the
// two terminal names. A plane that links not-resumed while an execution's
// consumed name is there reads it back, unlinks its own and says the approval
// was consumed, because a record read as not-resumed would let a later
// identical call be held and run anew.
func TestNotResumedLinkedBesideAConsumedRecordIsTakenBack(t *testing.T) {
	body := resolvedRecord(t, consumedName)
	var dir string
	opt, arm := afterLink(t, func() { writeFile(t, dir, consumedName, body) })
	p, dir, h := heldWith(t, opt)
	arm()
	err := p.Resolve(t.Context(), h.Binding, "req-1", approvals.ResolutionNotResumed, minted.Add(time.Minute))
	if !errors.Is(err, approvals.ErrApprovalConsumed) {
		t.Fatalf("resolving beside a consumed record = %v, want ErrApprovalConsumed", err)
	}
	assertNames(t, dir, heldName, consumedName)
}

// TestNotResumedBesideAConsumedNameItCannotReadIsRefused: the same re-read,
// with anything but a regular file at the consumed name. The plane cannot
// tell whether an execution spent the approval, so it neither answers that
// nobody did nor leaves its not-resumed name standing.
func TestNotResumedBesideAConsumedNameItCannotReadIsRefused(t *testing.T) {
	for _, u := range unreadable {
		t.Run(u.name, func(t *testing.T) {
			var dir string
			opt, arm := afterLink(t, func() { u.plant(t, filepath.Join(dir, consumedName)) })
			p, dir, h := heldWith(t, opt)
			arm()
			err := p.Resolve(t.Context(), h.Binding, "req-1", approvals.ResolutionNotResumed, minted.Add(time.Minute))
			if !errors.Is(err, approvals.ErrForeignFile) {
				t.Fatalf("resolving beside a %s at the consumed name = %v, want ErrForeignFile", u.name, err)
			}
			assertNames(t, dir, heldName, consumedName)
		})
	}
}
