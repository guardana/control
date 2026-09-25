package gateway

import (
	"context"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/evidence"
)

// holdEntrySchemaVersion is the version of the entry shape a journal stores.
const holdEntrySchemaVersion = "1.0"

// defaultReconcileMax bounds one reconciliation where the configuration names
// no bound, so a journal nobody bounded still costs a start a fixed amount of
// work.
const defaultReconcileMax = 256

// HoldState is where a journal entry stands. Its zero value is no state this
// build knows, which is read the same way as an interrupted close: the plane
// cannot say, so it says nothing and writes nothing.
type HoldState int

const (
	// HoldUnknown is an entry in no state this build knows.
	HoldUnknown HoldState = iota
	// HoldHeld is the one state that proves something: the trail stands at
	// its request for approval and can still be closed.
	HoldHeld
	// HoldResuming is an entry whose plane was about to write the approval's
	// answer on the trail.
	HoldResuming
	// HoldClosing is an entry whose plane was about to close the trail.
	HoldClosing
)

// HoldEntry is what a plane records about a request it holds for an approval,
// so that a hold it loses to a restart can still be closed on its own trail.
//
// It carries no envelope and no decision: a lost hold is closed, never
// resumed, and storing what a retry would be compared against would widen
// what the plane keeps on disk for behaviour nobody asked for (ADR-0016).
type HoldEntry struct {
	// SchemaVersion is the version of this shape; a journal refuses a major
	// version it does not know rather than reading past it.
	SchemaVersion string
	// State is where the entry stands.
	State HoldState
	// IDs name the held trail.
	IDs evidence.IDs
	// LastEventID is where that trail stands: its APPROVAL_REQUESTED.
	LastEventID string
	// Binding is what the approval was held under, and all the plane needs to
	// ask a store about it.
	Binding approval.Binding
	// Approval is what the plane minted and the agent was told to retry with.
	Approval *controlv1.Approval
	// Expires is the expiry the plane minted, which an answer may not exceed.
	Expires time.Time
}

// HoldListing is one pass over a journal: the entries that read as held, and
// what the pass could not say about the rest.
type HoldListing struct {
	// Held is the entries in HoldHeld, at most the bound the pass was given.
	Held []HoldEntry
	// Interrupted counts entries in any other state: a close a plane did not
	// finish, whose trail nobody can speak for.
	Interrupted int
	// Unreadable counts entries the journal could not decode. They are
	// unmeasured, never skipped, and a pass that met one is not complete.
	Unreadable int
	// Complete is false when the bound stopped the pass before the journal
	// was read out, or an entry could not be decoded: either way the pass has
	// not seen every hold, and may not report itself done.
	Complete bool
}

// HoldJournal is the plane's own durable record of its own holds, written by
// no other process. It is optional: a pipeline built without one holds and
// resumes exactly as it would otherwise, and never closes a hold it loses,
// which Stats reports as HoldJournal being false.
//
// The write order is what makes an entry's word worth anything, and it is the
// pipeline's to keep: an entry is recorded after APPROVAL_REQUESTED is
// appended and before the agent is told pending, every append that would move
// a trail past APPROVAL_REQUESTED is preceded by a flip out of HoldHeld, and
// the entry is forgotten once that trail can take nothing more. So HoldHeld
// means the trail stands at its request for approval, and a journal that
// refuses a write stops the append rather than being written around.
type HoldJournal interface {
	// Record files entry, which is in HoldHeld. It refuses an entry missing
	// anything its trail's close needs, or in another state, with
	// ErrHoldEntry, and a request id it holds an entry for already with
	// ErrHoldRecorded. It returns only after the entry is durable.
	Record(ctx context.Context, entry HoldEntry) error
	// Mark flips the entry of requestID out of HoldHeld into state, which is
	// HoldResuming or HoldClosing. It refuses an entry it does not hold with
	// ErrNoHoldEntry, and one that is not in HoldHeld, or a state that is
	// neither, with ErrHoldFlip. It returns only after the flip is durable.
	Mark(ctx context.Context, requestID string, state HoldState) error
	// Forget drops the entry of requestID, which its trail can take nothing
	// more after. The plane calls it for every execution it closes, including
	// calls it never held, so forgetting a request it holds no entry for is
	// no error and is worth making cheap.
	Forget(ctx context.Context, requestID string) error
	// List reads at most bound entries and reports what it found. A
	// non-positive bound reads nothing and reports Complete false, because a
	// pass that examined nothing has not found the journal empty, and so does
	// a pass that could not decode an entry.
	List(ctx context.Context, bound int) (HoldListing, error)
}

// journalRecord files own as a held entry, and reports whether the hold may
// stand. A pipeline with no journal keeps no entry and says so.
func (p *Pipeline) journalRecord(ctx context.Context, own *heldRequest) bool {
	if p.cfg.Journal == nil {
		return true
	}
	entry := HoldEntry{
		SchemaVersion: holdEntrySchemaVersion,
		State:         HoldHeld,
		IDs:           own.ids,
		LastEventID:   own.lastEventID,
		Binding:       own.binding,
		Approval:      proto.CloneOf(own.approval),
		Expires:       own.expires,
	}
	if err := p.cfg.Journal.Record(ctx, entry); err != nil {
		p.counts.add(func(s *Stats) *uint64 { return &s.JournalRefusals })
		return false
	}
	return true
}

// journalMark flips the entry of requestID into state and reports whether the
// caller may now append past that trail's APPROVAL_REQUESTED. A journal that
// refuses the flip blocks the append: the entry still says the trail stands
// where it stood, and only that statement lets a later plane close it.
func (p *Pipeline) journalMark(ctx context.Context, requestID string, state HoldState) bool {
	if p.cfg.Journal == nil {
		return true
	}
	if err := p.cfg.Journal.Mark(ctx, requestID, state); err != nil {
		p.counts.add(func(s *Stats) *uint64 { return &s.JournalRefusals })
		return false
	}
	return true
}

// journalForget drops the entry of requestID once its trail can take nothing
// more. An entry that will not go stays in the state the flip left it, which
// a later reconciliation reports rather than acts on, so nothing appends to
// that trail twice.
func (p *Pipeline) journalForget(ctx context.Context, requestID string) {
	if p.cfg.Journal == nil {
		return
	}
	if err := p.cfg.Journal.Forget(ctx, requestID); err != nil {
		p.counts.add(func(s *Stats) *uint64 { return &s.JournalRefusals })
	}
}

// MemoryHoldJournal is a HoldJournal in process memory, which is what the
// write order is driven against in a test. It keeps a plane's holds no longer
// than the plane itself, so it closes no hold a restart loses: only a journal
// that outlives the process does that.
type MemoryHoldJournal struct {
	mu      sync.Mutex
	entries map[string]HoldEntry
	order   []string
}

var _ HoldJournal = (*MemoryHoldJournal)(nil)

// Record files entry under its request id.
func (j *MemoryHoldJournal) Record(ctx context.Context, entry HoldEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !recordable(entry) {
		return ErrHoldEntry
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	id := entry.IDs.RequestID
	if _, taken := j.entries[id]; taken {
		return ErrHoldRecorded
	}
	if j.entries == nil {
		j.entries = make(map[string]HoldEntry)
	}
	j.entries[id] = cloneEntry(entry)
	j.order = append(j.order, id)
	return nil
}

// recordable reports whether entry holds what closing its trail needs.
func recordable(entry HoldEntry) bool {
	return entry.State == HoldHeld &&
		entry.SchemaVersion != "" &&
		entry.IDs.RequestID != "" &&
		entry.LastEventID != "" &&
		entry.Binding != "" &&
		entry.Approval.GetApprovalId() != "" &&
		entry.Approval.GetRequestId() != "" &&
		!entry.Expires.IsZero()
}

// Mark flips the entry of requestID out of HoldHeld.
func (j *MemoryHoldJournal) Mark(ctx context.Context, requestID string, state HoldState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state != HoldResuming && state != HoldClosing {
		return ErrHoldFlip
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	entry, kept := j.entries[requestID]
	switch {
	case !kept:
		return ErrNoHoldEntry
	case entry.State != HoldHeld:
		return ErrHoldFlip
	}
	entry.State = state
	j.entries[requestID] = entry
	return nil
}

// Forget drops the entry of requestID.
func (j *MemoryHoldJournal) Forget(ctx context.Context, requestID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.entries, requestID)
	j.order = slices.DeleteFunc(j.order, func(id string) bool { return id == requestID })
	return nil
}

// List reads the entries in the order they were recorded, at most bound of
// them.
func (j *MemoryHoldJournal) List(ctx context.Context, bound int) (HoldListing, error) {
	if err := ctx.Err(); err != nil {
		return HoldListing{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var out HoldListing
	read := 0
	for _, id := range j.order {
		if read >= bound {
			return out, nil
		}
		read++
		entry := j.entries[id]
		if entry.State == HoldHeld {
			out.Held = append(out.Held, cloneEntry(entry))
			continue
		}
		out.Interrupted++
	}
	out.Complete = true
	return out, nil
}

// Entries returns what the journal holds, by request id, so a test can read
// the state an entry stands in without reading it out of a listing.
func (j *MemoryHoldJournal) Entries() map[string]HoldEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make(map[string]HoldEntry, len(j.entries))
	for id, entry := range j.entries {
		out[id] = cloneEntry(entry)
	}
	return out
}

// cloneEntry deep-copies the approval, so neither side's later writes reach
// the other.
func cloneEntry(entry HoldEntry) HoldEntry {
	entry.Approval = proto.CloneOf(entry.Approval)
	return entry
}
