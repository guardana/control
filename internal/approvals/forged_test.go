package approvals_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
)

// The approval id a writer of the directory files under, which is every id the
// plane did not mint.
const forgedApproval = "MALLORY1"

// forgeUnder writes a record of forgedApproval by taking the plane's own
// answered record and renaming it inside and out. Everything else — the
// binding, the request id, the answer — is what the plane wrote, so the only
// thing that tells the two apart is the approval id.
func forgeUnder(t *testing.T, dir string, st string) {
	t.Helper()
	framed := readFile(t, dir, firstApproval+st)
	body := replaceInBody(t, framed, `"approval_id":"`+firstApproval+`"`, `"approval_id":"`+forgedApproval+`"`)
	body = bytes.Replace(body, []byte(`"approvalId":"`+firstApproval+`"`), []byte(`"approvalId":"`+forgedApproval+`"`), 1)
	writeFile(t, dir, forgedApproval+st, reframe(t, body))
}

// answeredAndForged leaves the plane's own answered record for req-1 beside a
// forged one under the same binding and the same request id.
func answeredAndForged(t *testing.T) (*approvals.Plane, string, approvals.Hold) {
	t.Helper()
	p, dir := openPlane(t)
	h := held(t, firstApproval, "req-1")
	if err := p.Hold(t.Context(), h, minted); err != nil {
		t.Fatalf("holding: %v", err)
	}
	answerAt(t, dir, minted.Add(time.Minute))
	forgeUnder(t, dir, ".1-answered.rec")
	return p, dir, h
}

// TestConsumeSpendsARecordThePlaneDidNotMintNeverAtAll is the finding: the
// compare-and-swap is keyed by the approval id the plane minted, so a record
// filed under the binding and the request id of a held call, with any other
// approval id, is not the plane's and is never spent by it.
//
// The plane's own record is unlinked here because a writer of the directory
// may unlink as well as write, and the approval id the plane passes is the one
// it holds in its own record, not one it reads back from the directory.
func TestConsumeSpendsARecordThePlaneDidNotMintNeverAtAll(t *testing.T) {
	p, dir, h := answeredAndForged(t)
	if err := os.Remove(filepath.Join(dir, firstApproval+".1-answered.rec")); err != nil {
		t.Fatalf("unlinking the plane's own record: %v", err)
	}

	got, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, minted.Add(2*time.Minute))
	if !errors.Is(err, approvals.ErrNoApproval) {
		t.Fatalf("consuming a record the plane did not mint = %v, want ErrNoApproval", err)
	}
	if got != nil {
		t.Fatalf("a refused consume handed back %v", got)
	}
	if names := recordNames(t, dir); !slices.Contains(names, forgedApproval+".1-answered.rec") ||
		slices.Contains(names, forgedApproval+".3-consumed.rec") {
		t.Fatalf("the forged record was spent by the plane: %v", names)
	}
}

// TestConsumeSpendsThePlanesOwnRecordBesideAForgedOne: the same directory, the
// plane's own record still there. It is consumed, and the forged one is left
// exactly as it was.
func TestConsumeSpendsThePlanesOwnRecordBesideAForgedOne(t *testing.T) {
	p, dir, h := answeredAndForged(t)

	got, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, minted.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("consuming the plane's own record beside a forged one: %v", err)
	}
	if got.GetApprovalId() != firstApproval {
		t.Fatalf("consume returned approval %q", got.GetApprovalId())
	}
	names := recordNames(t, dir)
	if !slices.Contains(names, firstApproval+".3-consumed.rec") {
		t.Fatalf("the plane's own record was not spent: %v", names)
	}
	if !slices.Contains(names, forgedApproval+".1-answered.rec") ||
		slices.Contains(names, forgedApproval+".3-consumed.rec") {
		t.Fatalf("the forged record was touched: %v", names)
	}
}

// TestConsumeRefusesAHeldRecordBesideAForgedOne is the shape the review
// reported: a live hold under the plane's own approval id, a forged record
// under the same binding and request id. Nobody has answered the plane's
// record, so the answer is that there is no approval, and nothing is spent.
func TestConsumeRefusesAHeldRecordBesideAForgedOne(t *testing.T) {
	p, dir := openPlane(t)
	h := held(t, firstApproval, "req-1")
	if err := p.Hold(t.Context(), h, minted); err != nil {
		t.Fatalf("holding: %v", err)
	}
	forgeUnder(t, dir, ".0-held.rec")

	_, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, minted.Add(time.Minute))
	if !errors.Is(err, approvals.ErrNoApproval) {
		t.Fatalf("consuming beside a forged record = %v, want ErrNoApproval", err)
	}
	for _, name := range recordNames(t, dir) {
		if filepath.Ext(name) == ".rec" && slices.Contains([]string{
			firstApproval + ".3-consumed.rec", forgedApproval + ".3-consumed.rec",
		}, name) {
			t.Fatalf("a refused consume spent %s", name)
		}
	}
}

// TestConsumeRefusesAnApprovalIDItCannotName: an empty id is not "any record
// under this binding", which is what the call meant before it took one.
func TestConsumeRefusesAnApprovalIDItCannotName(t *testing.T) {
	p, dir, h := answeredAndForged(t)
	for name, id := range map[string]string{
		"an empty approval id": "",
		"a path separator":     "../APPROVAL1",
	} {
		_, err := p.Consume(t.Context(), h.Binding, "req-1", id, minted.Add(2*time.Minute))
		if !errors.Is(err, approvals.ErrRecordName) {
			t.Errorf("consuming with %s = %v, want ErrRecordName", name, err)
		}
	}
	if names := recordNames(t, dir); slices.Contains(names, firstApproval+".3-consumed.rec") {
		t.Fatalf("a refused consume spent a record: %v", names)
	}
}

// TestAnAnswerIsStillReadBackFromTheRecordThePlaneMinted holds the ordinary
// path against the check above: the plane mints, an approver answers, and the
// plane spends its own record once.
func TestAnAnswerIsStillReadBackFromTheRecordThePlaneMinted(t *testing.T) {
	p, dir := openPlane(t)
	h := held(t, firstApproval, "req-1")
	if err := p.Hold(t.Context(), h, minted); err != nil {
		t.Fatalf("holding: %v", err)
	}
	answerAt(t, dir, minted.Add(time.Minute))
	got, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, minted.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("consuming: %v", err)
	}
	if got.GetState() != controlv1.ApprovalState_APPROVAL_STATE_APPROVED {
		t.Fatalf("consume returned state %s", got.GetState())
	}
}
