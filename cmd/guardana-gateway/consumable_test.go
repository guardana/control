package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/gateway"
)

const (
	rowApproval = "apr-row-1"
	rowRequest  = "req-row-1"
	rowDigest   = "sha256:" + "22222222222222222222222222222222222222222222222222222222222222cd"
)

var (
	rowRequestedAt = time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	rowNow         = rowRequestedAt.Add(10 * time.Minute)
)

type rowDecided uint8

const (
	rowDecidedNever rowDecided = iota
	rowDecidedBeforeRequest
	rowDecidedAtRequest
	rowDecidedAtNow
	rowDecidedAfterNow
)

type rowExpires uint8

const (
	rowExpiresNever rowExpires = iota
	rowExpiresBeforeNow
	rowExpiresAtNow
	rowExpiresAfterNow
)

// consumeRow is one combination of what a consuming store reads, and the
// moment it is consumed at.
type consumeRow struct {
	state    controlv1.ApprovalState
	approver string
	multiUse bool
	decided  rowDecided
	expires  rowExpires
	zeroNow  bool
}

func (r consumeRow) String() string {
	return fmt.Sprintf("%s/approver=%q/multi=%t/decided=%d/expires=%d/zero=%t",
		r.state, r.approver, r.multiUse, r.decided, r.expires, r.zeroNow)
}

func (r consumeRow) now() time.Time {
	if r.zeroNow {
		return time.Time{}
	}
	return rowNow
}

// decidedAt is the moment the row's answer is dated, if it has one.
func (r consumeRow) decidedAt() (time.Time, bool) {
	at, ok := map[rowDecided]time.Time{
		rowDecidedBeforeRequest: rowRequestedAt.Add(-time.Nanosecond),
		rowDecidedAtRequest:     rowRequestedAt,
		rowDecidedAtNow:         rowNow,
		rowDecidedAfterNow:      rowNow.Add(time.Nanosecond),
	}[r.decided]
	return at, ok
}

// pending is the row's approval as the plane mints it, before any answer.
func (r consumeRow) pending() *controlv1.Approval {
	a := r.approval()
	a.State, a.ApproverId, a.DecidedAt = controlv1.ApprovalState_APPROVAL_STATE_PENDING, "", nil
	return a
}

// answerable reports whether an approver's route can give the row's answer:
// an approval naming its approver, or a rejection, dated at some moment.
func (r consumeRow) answerable() bool {
	approved := r.state == controlv1.ApprovalState_APPROVAL_STATE_APPROVED && r.approver != ""
	rejected := r.state == controlv1.ApprovalState_APPROVAL_STATE_REJECTED
	return (approved || rejected) && r.decided != rowDecidedNever
}

// asHeld is the row the memory store ends up holding: the row itself where
// its answer can be given, and the pending approval it was held as where it
// cannot.
func (r consumeRow) asHeld() consumeRow {
	if r.answerable() {
		return r
	}
	r.state, r.approver, r.decided = controlv1.ApprovalState_APPROVAL_STATE_PENDING, "", rowDecidedNever
	return r
}

func (r consumeRow) approval() *controlv1.Approval {
	a := &controlv1.Approval{
		SchemaVersion:      "1.0",
		ApprovalId:         rowApproval,
		RequestId:          rowRequest,
		ActionDigest:       rowDigest,
		PolicyBundleDigest: fixtureBundleDigest,
		State:              r.state,
		ApproverId:         r.approver,
		MultiUse:           r.multiUse,
		RequestedAt:        timestamppb.New(rowRequestedAt),
	}
	if at, ok := r.decidedAt(); ok {
		a.DecidedAt = timestamppb.New(at)
	}
	expires := map[rowExpires]time.Time{
		rowExpiresBeforeNow: rowNow.Add(-time.Nanosecond),
		rowExpiresAtNow:     rowNow,
		rowExpiresAfterNow:  rowNow.Add(time.Nanosecond),
	}
	if at, ok := expires[r.expires]; ok {
		a.ExpiresAt = timestamppb.New(at)
	}
	return a
}

func consumeRows() []consumeRow {
	var out []consumeRow
	for _, state := range []controlv1.ApprovalState{
		controlv1.ApprovalState_APPROVAL_STATE_APPROVED,
		controlv1.ApprovalState_APPROVAL_STATE_REJECTED,
		controlv1.ApprovalState_APPROVAL_STATE_PENDING,
		controlv1.ApprovalState_APPROVAL_STATE_EXPIRED,
		controlv1.ApprovalState_APPROVAL_STATE_UNSPECIFIED,
		controlv1.ApprovalState(99),
	} {
		for _, approver := range []string{"", "duty-officer"} {
			for _, multiUse := range []bool{false, true} {
				for decided := rowDecidedNever; decided <= rowDecidedAfterNow; decided++ {
					for expires := rowExpiresNever; expires <= rowExpiresAfterNow; expires++ {
						for _, zero := range []bool{false, true} {
							out = append(out, consumeRow{state, approver, multiUse, decided, expires, zero})
						}
					}
				}
			}
		}
	}
	return out
}

// The classes a Consume answer falls in, as the pipeline tells them apart.
const (
	classSpent      = "spent"
	classRejected   = "rejected"
	classExpired    = "expired"
	classMultiUse   = "multi-use"
	classNoApproval = "no approval"
	classZeroTime   = "zero time"
	classConsumed   = "consumed already"
	classNotHeld    = "not held"
)

// classOf names what Consume answered. Anything else is spelled out, so it
// can match no expected class.
func classOf(a *controlv1.Approval, err error) string {
	switch {
	case err == nil && a != nil:
		return classSpent
	case errors.Is(err, gateway.ErrApprovalRejected) && a != nil:
		return classRejected
	case a != nil:
		return fmt.Sprintf("an approval beside %v", err)
	case errors.Is(err, gateway.ErrApprovalExpired):
		return classExpired
	case errors.Is(err, gateway.ErrMultiUse):
		return classMultiUse
	case errors.Is(err, gateway.ErrNoApproval):
		return classNoApproval
	case errors.Is(err, gateway.ErrZeroTime):
		return classZeroTime
	case errors.Is(err, gateway.ErrApprovalConsumed):
		return classConsumed
	}
	return fmt.Sprintf("refused: %v", err)
}

// wantClass is the order restated over the row's names: no time and no
// store in it.
func wantClass(r consumeRow) string {
	switch {
	case r.zeroNow:
		return classZeroTime
	case r.multiUse:
		return classMultiUse
	case r.expires != rowExpiresAfterNow:
		return classExpired
	case r.decided != rowDecidedAtRequest && r.decided != rowDecidedAtNow:
		return classNoApproval
	case r.state == controlv1.ApprovalState_APPROVAL_STATE_REJECTED:
		return classRejected
	case r.state == controlv1.ApprovalState_APPROVAL_STATE_APPROVED && r.approver != "":
		return classSpent
	}
	return classNoApproval
}

// wantAgain is what a second Consume of the same record answers: a spent
// record is consumed, a dropped one is gone, and anything else stands.
func wantAgain(first string) string {
	switch first {
	case classSpent:
		return classConsumed
	case classRejected, classExpired:
		return classNoApproval
	}
	return first
}

// unreadable is a row the directory refuses to read at all: no expiry, an
// answer dated before the request, or one dated past the expiry.
func unreadable(r consumeRow) bool {
	switch {
	case r.expires == rowExpiresNever, r.decided == rowDecidedBeforeRequest:
		return true
	case r.decided == rowDecidedAtNow:
		return r.expires == rowExpiresBeforeNow
	case r.decided == rowDecidedAfterNow:
		return r.expires != rowExpiresAfterNow
	}
	return false
}

func rowBinding(t *testing.T) approval.Binding {
	t.Helper()
	b, err := canon.ApprovalBinding(rowDigest, fixtureBundleDigest)
	if err != nil {
		t.Fatalf("ApprovalBinding: %v", err)
	}
	return approval.Binding(b)
}

// later is a moment past every expiry a row can carry.
var later = rowNow.Add(time.Hour)

// storeAnswer is what one store answered about one row: the class of a
// Consume, of the same Consume again, and of one at a later moment, with the
// errors the directory's two Consumes came back with and the names it holds
// afterwards.
type storeAnswer struct {
	first, again, later string
	firstErr, againErr  error
	names               []string
	answered            bool
}

// consumeInMemory holds the row's approval pending, answers it through
// Answer where an approver could, and consumes it three times. A hold the
// store refuses is held nowhere.
func consumeInMemory(t *testing.T, r consumeRow, binding approval.Binding) storeAnswer {
	t.Helper()
	var store gateway.MemoryApprovals
	err := store.Hold(context.Background(), gateway.Held{
		Approval: r.pending(),
		Envelope: &controlv1.ActionEnvelope{},
		Decision: &controlv1.Decision{},
		Binding:  binding,
	}, rowRequestedAt)
	if errors.Is(err, gateway.ErrInvalidHold) {
		return storeAnswer{first: classNotHeld, again: classNotHeld, later: classNotHeld}
	}
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	var out storeAnswer
	if at, ok := r.decidedAt(); ok {
		out.answered = store.Answer(rowApproval, r.state, r.approver, "", at) == nil
	}
	out.first = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now()))
	out.again = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now()))
	out.later = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, later))
	return out
}

// consumeInDirectory holds the row's approval pending through the plane,
// answers it as an approver would where the approvers' command takes the
// answer, and otherwise writes the row's record by hand, as any writer of the
// directory can. It then consumes it three times through the seam.
func consumeInDirectory(t *testing.T, r consumeRow, binding approval.Binding) storeAnswer {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "records")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("making %s: %v", dir, err)
	}
	made, err := approvals.OpenPlane(dir)
	if err != nil {
		t.Fatalf("OpenPlane: %v", err)
	}
	held := made.Hold(context.Background(), approvals.Hold{
		Approval: r.pending(), Binding: binding,
		Envelope: &controlv1.ActionEnvelope{}, Decision: &controlv1.Decision{},
	}, rowRequestedAt)
	if err := made.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var out storeAnswer
	if held == nil {
		out.answered = answerInDirectory(t, dir, r)
	}
	if !out.answered {
		writeRecord(t, filepath.Join(dir, rowApproval+".0-held.rec"), r.approval(), binding, "pending")
	}
	plane, err := approvals.OpenPlane(dir)
	if err != nil {
		t.Fatalf("OpenPlane: %v", err)
	}
	store := &approvalStore{plane: plane}
	a, err := store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now())
	out.first, out.firstErr = classOf(a, err), err
	a, err = store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now())
	out.again, out.againErr = classOf(a, err), err
	out.names = namesUnder(t, dir)
	out.later = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, later))
	if err := plane.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out
}

// answerInDirectory gives the row's answer through the approvers' command,
// and reports whether the command took it.
func answerInDirectory(t *testing.T, dir string, r consumeRow) bool {
	t.Helper()
	at, ok := r.decidedAt()
	if !ok || !r.answerable() {
		return false
	}
	approver, err := approvals.OpenApprover(dir)
	if err != nil {
		t.Fatalf("OpenApprover: %v", err)
	}
	_, err = approver.Answer(context.Background(), rowApproval, r.state, r.approver, "", at)
	if closeErr := approver.Close(); closeErr != nil {
		t.Fatalf("closing the approver: %v", closeErr)
	}
	return err == nil
}

// writeRecord frames one record as the store's format states it.
func writeRecord(t *testing.T, path string, a *controlv1.Approval, binding approval.Binding, resolution string) {
	t.Helper()
	raw, err := protojson.Marshal(a)
	if err != nil {
		t.Fatalf("protojson: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"schema_version": "1.0",
		"approval_id":    a.GetApprovalId(),
		"request_id":     a.GetRequestId(),
		"binding":        string(binding),
		"resolution":     resolution,
		"approval":       json.RawMessage(raw),
	})
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	writeFramed(t, path, body)
}

// writeFramed writes body in the record frame.
func writeFramed(t *testing.T, path string, body []byte) {
	t.Helper()
	out := make([]byte, recordHeader, recordHeader+len(body))
	copy(out, recordMagic)
	binary.BigEndian.PutUint32(out[len(recordMagic):], uint32(len(body))) //nolint:gosec // G115: a test record is a few hundred bytes
	binary.BigEndian.PutUint32(out[len(recordMagic)+4:], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
	if err := os.WriteFile(path, append(out, body...), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestBothStoresConsumeEveryRowAlike: each store holds the row pending and
// takes its answer through its own route, and answers the class the order
// names; where both hold the row itself, they answer alike, a second Consume
// included. A record spent and since expired still answers spent. A row the
// directory refuses to read spends nothing in either store.
func TestBothStoresConsumeEveryRowAlike(t *testing.T) {
	binding := rowBinding(t)
	unread, alike := 0, 0
	for _, r := range consumeRows() {
		read, compared := consumeRowAlike(t, r, binding)
		if !read {
			unread++
		}
		if compared {
			alike++
		}
	}
	if unread != 264 || alike != 36 {
		t.Errorf("%d rows were refused at read and %d compared across the stores, want 264 and 36", unread, alike)
	}
}

// consumeRowAlike consumes one row in both stores and holds each to the class
// the order names for what it holds. It reports whether the directory read
// the row rather than refusing it at read, and whether both stores held the
// row itself and were compared.
func consumeRowAlike(t *testing.T, r consumeRow, binding approval.Binding) (read, compared bool) {
	t.Helper()
	mem := consumeInMemory(t, r, binding)
	dir := consumeInDirectory(t, r, binding)
	memoryAsHeld(t, r, mem)
	if unreadable(r) && (!r.zeroNow || r.expires == rowExpiresNever) {
		refusedByOne(t, r, mem, dir)
		return r.zeroNow, false
	}
	want := wantClass(r)
	if dir.first != want || dir.again != wantAgain(want) {
		t.Errorf("%s: the directory answered %q then %q, want %q then %q", r, dir.first, dir.again, want, wantAgain(want))
	}
	if r.answerable() && (dir.first != mem.first || dir.again != mem.again) {
		t.Errorf("%s: the directory answered %q then %q, the memory store %q then %q", r, dir.first, dir.again, mem.first, mem.again)
	}
	keptAlike(t, r, want, mem, dir)
	return true, r.answerable() && !r.zeroNow
}

// memoryAsHeld holds the memory store to the class the order names for what
// it holds: nothing for a row with no expiry, the row itself where its answer
// could be given, and the pending approval otherwise.
func memoryAsHeld(t *testing.T, r consumeRow, mem storeAnswer) {
	t.Helper()
	want := wantClass(r.asHeld())
	if r.expires == rowExpiresNever {
		want = classNotHeld
	} else if mem.answered != r.answerable() {
		t.Errorf("%s: the memory store took the answer: %t, want %t", r, mem.answered, r.answerable())
	}
	if mem.first != want || mem.again != wantAgain(want) {
		t.Errorf("%s: the memory store answered %q then %q, want %q then %q", r, mem.first, mem.again, want, wantAgain(want))
	}
}

// keptAlike holds what each store keeps of a row it read: a spent record
// still answers spent past its expiry, and the directory holds the spent
// name, or nothing once it dropped the record.
func keptAlike(t *testing.T, r consumeRow, want string, mem, dir storeAnswer) {
	t.Helper()
	if want == classSpent && (mem.later != classConsumed || dir.later != classConsumed) {
		t.Errorf("%s: past its expiry the spent record answered %q in memory and %q in the directory, want %q", r, mem.later, dir.later, classConsumed)
	}
	consumed := slices.Contains(dir.names, rowApproval+".3-consumed.rec")
	dropped := !slices.ContainsFunc(dir.names, func(n string) bool {
		return strings.HasPrefix(n, rowApproval+".") && !strings.HasSuffix(n, ".view.json")
	})
	if consumed != (want == classSpent) || dropped != (want == classRejected || want == classExpired) {
		t.Errorf("%s: answered %q and the directory holds %v", r, dir.first, dir.names)
	}
}

// refusedByOne holds a row one store refuses to keep to spending nothing in
// either: the directory refuses it at read, or at the zero time before it
// reads, both times, and leaves the record where it was.
func refusedByOne(t *testing.T, r consumeRow, mem, dir storeAnswer) {
	t.Helper()
	readRefusal := func(err error) bool {
		return errors.Is(err, approvals.ErrInvalidHold) || errors.Is(err, approvals.ErrDecidedAt)
	}
	switch {
	case r.zeroNow:
		if dir.first != classZeroTime || dir.again != classZeroTime {
			t.Errorf("%s: the directory answered %q then %q, want %q twice", r, dir.first, dir.again, classZeroTime)
		}
	case !readRefusal(dir.firstErr) || !readRefusal(dir.againErr):
		t.Errorf("%s: the directory answered %v then %v, want the record refused at read twice", r, dir.firstErr, dir.againErr)
	}
	if !slices.Contains(dir.names, rowApproval+".0-held.rec") || slices.Contains(dir.names, rowApproval+".3-consumed.rec") {
		t.Errorf("%s: a record the directory refused leaves %v, want it in place and unspent", r, dir.names)
	}
	if mem.first == classSpent {
		t.Errorf("%s: a record the directory refuses to read was spent in memory", r)
	}
}
