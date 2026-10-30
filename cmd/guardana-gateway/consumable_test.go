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
	decided := map[rowDecided]time.Time{
		rowDecidedBeforeRequest: rowRequestedAt.Add(-time.Nanosecond),
		rowDecidedAtRequest:     rowRequestedAt,
		rowDecidedAtNow:         rowNow,
		rowDecidedAfterNow:      rowNow.Add(time.Nanosecond),
	}
	if at, ok := decided[r.decided]; ok {
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

// consumeInMemory holds the row's approval in the memory store and consumes
// it twice.
func consumeInMemory(t *testing.T, r consumeRow, binding approval.Binding) (first, again string) {
	t.Helper()
	var store gateway.MemoryApprovals
	err := store.Hold(context.Background(), gateway.Held{
		Approval: r.approval(),
		Envelope: &controlv1.ActionEnvelope{},
		Decision: &controlv1.Decision{},
		Binding:  binding,
	}, rowRequestedAt)
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	first = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now()))
	again = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now()))
	return first, again
}

// directoryAnswer is what the approvals directory answered about one row:
// the class of each Consume, the names it holds afterwards and the first
// error as it came back.
type directoryAnswer struct {
	first, again string
	names        []string
	err          error
}

// consumeInDirectory files the row's approval as a pending record, as a writer
// of the directory would, and consumes it twice through the seam.
func consumeInDirectory(t *testing.T, r consumeRow, binding approval.Binding) directoryAnswer {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "records")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("making %s: %v", dir, err)
	}
	made, err := approvals.OpenPlane(dir)
	if err != nil {
		t.Fatalf("OpenPlane: %v", err)
	}
	if err := made.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	writeRecord(t, filepath.Join(dir, rowApproval+".0-held.rec"), r.approval(), binding)
	plane, err := approvals.OpenPlane(dir)
	if err != nil {
		t.Fatalf("OpenPlane: %v", err)
	}
	store := &approvalStore{plane: plane}
	var out directoryAnswer
	a, firstErr := store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now())
	out.first, out.err = classOf(a, firstErr), firstErr
	out.again = classOf(store.Consume(context.Background(), binding, rowRequest, rowApproval, r.now()))
	if err := plane.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	out.names = namesUnder(t, dir)
	return out
}

// writeRecord frames one pending record as the store's format states it.
func writeRecord(t *testing.T, path string, a *controlv1.Approval, binding approval.Binding) {
	t.Helper()
	raw, err := protojson.Marshal(a)
	if err != nil {
		t.Fatalf("protojson: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"schema_version": "1.0",
		"approval_id":    rowApproval,
		"request_id":     rowRequest,
		"binding":        string(binding),
		"resolution":     "pending",
		"approval":       json.RawMessage(raw),
	})
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	out := make([]byte, recordHeader, recordHeader+len(body))
	copy(out, recordMagic)
	binary.BigEndian.PutUint32(out[len(recordMagic):], uint32(len(body))) //nolint:gosec // G115: a test record is a few hundred bytes
	binary.BigEndian.PutUint32(out[len(recordMagic)+4:], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
	if err := os.WriteFile(path, append(out, body...), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestBothStoresConsumeEveryRowAlike: the memory store and the approvals
// directory answer the same class for every row the directory can read, a
// second Consume included, and each answers the class the order names. A row
// the directory refuses to read spends nothing in either store.
func TestBothStoresConsumeEveryRowAlike(t *testing.T) {
	binding := rowBinding(t)
	unread := 0
	for _, r := range consumeRows() {
		if consumeRowAlike(t, r, binding) {
			unread++
		}
	}
	if unread != 176 {
		t.Errorf("%d rows were refused at read, want 176", unread)
	}
}

// consumeRowAlike consumes one row in both stores and holds them to the class
// the order names. It reports whether the directory refused the row at read.
func consumeRowAlike(t *testing.T, r consumeRow, binding approval.Binding) bool {
	t.Helper()
	memFirst, memAgain := consumeInMemory(t, r, binding)
	dir := consumeInDirectory(t, r, binding)
	want := wantClass(r)
	if memFirst != want || memAgain != wantAgain(want) {
		t.Errorf("%s: the memory store answered %q then %q, want %q then %q", r, memFirst, memAgain, want, wantAgain(want))
	}
	consumed := slices.Contains(dir.names, rowApproval+".3-consumed.rec")
	if !r.zeroNow && unreadable(r) {
		refusedAtRead(t, r, memFirst, dir, consumed)
		return true
	}
	if dir.first != memFirst || dir.again != memAgain {
		t.Errorf("%s: the directory answered %q then %q, the memory store %q then %q", r, dir.first, dir.again, memFirst, memAgain)
	}
	dropped := !slices.ContainsFunc(dir.names, func(n string) bool { return strings.HasPrefix(n, rowApproval+".") })
	if consumed != (want == classSpent) || dropped != (want == classRejected || want == classExpired) {
		t.Errorf("%s: answered %q and the directory holds %v", r, dir.first, dir.names)
	}
	return false
}

// refusedAtRead holds a row the directory cannot read to spending nothing in
// either store.
func refusedAtRead(t *testing.T, r consumeRow, memFirst string, dir directoryAnswer, consumed bool) {
	t.Helper()
	if !errors.Is(dir.err, approvals.ErrInvalidHold) && !errors.Is(dir.err, approvals.ErrDecidedAt) {
		t.Errorf("%s: the directory answered %v, want the record refused at read", r, dir.err)
	}
	if memFirst == classSpent || consumed {
		t.Errorf("%s: a record the directory refuses to read was spent: memory %q, directory names %v", r, memFirst, dir.names)
	}
}
