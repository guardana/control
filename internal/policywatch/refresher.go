package policywatch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
)

// Error is a refusal of this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The refusals no other package names.
const (
	// ErrOptions is Options missing what Start, Check or a refresher needs.
	ErrOptions Error = "policywatch: the options name no bundle id, file, key, holder, floor or clock, or an interval or budget that is not positive"
	// ErrClockBack is a wall clock more than a second below the mark.
	ErrClockBack Error = "policywatch: the wall clock stands more than a second below the highest it has read, so no statement is taken"
	// ErrStatementExpired is a statement whose budget ran out before it was
	// read; it confirms nothing, at start as at a poll.
	ErrStatementExpired Error = "policywatch: the statement's budget has run out"
	// ErrNoSnapshot is a refresher over a holder Start installed nothing in.
	ErrNoSnapshot Error = "policywatch: the holder serves no snapshot; Start installs the first"
)

// Options configure Start, Check and a Refresher.
type Options struct {
	// BundleID is the one bundle id the plane serves.
	BundleID string
	// BundlePath and StatementPath are the two files read at every poll.
	BundlePath, StatementPath string
	// BundleKeys verify a bundle and FreshnessKeys a statement.
	BundleKeys, FreshnessKeys bundle.Keyring
	// Holder is the plane's holder, made by policy.NewFloorHolder over Floor.
	Holder *policy.Holder
	// Floor is the store the holder raises; Start reads it.
	Floor policy.FloorStore
	// Interval is the poll interval and MaxStale the operator's budget.
	Interval, MaxStale time.Duration
	// Wall reads the wall clock and Mono the monotonic one, as a duration
	// since any fixed origin.
	Wall func() time.Time
	Mono func() time.Duration
	// Logger takes each refusal when its cause changes, each install and
	// each confirmation; nil logs nothing.
	Logger *slog.Logger
}

// check refuses options a refresher or Start cannot run on.
func (o Options) check() error {
	if err := o.checkReading(); err != nil {
		return err
	}
	if o.Holder == nil || o.Mono == nil {
		return ErrOptions
	}
	return nil
}

// checkReading refuses options Check cannot judge the files by.
func (o Options) checkReading() error {
	if o.BundleID == "" || o.BundlePath == "" || o.StatementPath == "" || len(o.BundleKeys) == 0 ||
		len(o.FreshnessKeys) == 0 || o.Floor == nil || o.Wall == nil || o.Interval <= 0 || o.MaxStale <= 0 {
		return ErrOptions
	}
	return nil
}

// Stats is what a Refresher counted since New.
type Stats struct {
	// Polls counts the polls made.
	Polls uint64
	// Refused counts the polls that moved nothing because something was
	// refused, by cause.
	Refused map[Cause]uint64
	// AwaitingStatement counts the polls that found a new bundle whose
	// statement still names another, and AwaitingBundle those that found a
	// statement naming a bundle not yet on disk.
	AwaitingStatement uint64
	AwaitingBundle    uint64
	// Confirmations counts the statements accepted, renewals and installs
	// alike, and Installs the bundles that replaced the current one.
	Confirmations uint64
	Installs      uint64
	// Withdrawals counts the confirmations the clock rule took back.
	Withdrawals uint64
}

// Refresher reads the bundle and the statement every interval and moves the
// holder only as the statement and the floor allow.
type Refresher struct {
	o Options

	// mu serializes the reading and judging of the files; the bytes judged
	// are its. The clock mark has its own lock, so a poll held up reading or
	// raising never keeps the next one from judging the clock.
	mu        sync.Mutex
	bundle    *diskBundle
	statement *diskStatement

	clockMu sync.Mutex
	clock   mark

	// statsMu guards the counts and the last outcome logged.
	statsMu sync.Mutex
	stats   Stats
	last    string
}

// New checks o and takes the first reading of both clocks, the clock mark's
// first value.
func New(o Options) (*Refresher, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	r := &Refresher{o: o, stats: Stats{Refused: map[Cause]uint64{}}}
	r.clock.back(o.Wall(), o.Mono())
	return r, nil
}

// Poll reads both files once and moves the holder as they allow. Nothing is
// installed or confirmed while the wall clock stands more than a second
// below the mark; a new bundle is installed only with the statement bound to
// it; and anything refused leaves the snapshot, its confirmation and the
// floor as they were, counted under its cause.
func (r *Refresher) Poll(ctx context.Context) {
	now, mono := r.o.Wall(), r.o.Mono()
	r.count(func(s *Stats) { s.Polls++ })
	r.clockMu.Lock()
	back := r.clock.back(now, mono)
	r.clockMu.Unlock()
	if back {
		r.withdraw()
		r.refuse(CauseClockBack, ErrClockBack)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.o.Holder.Current()
	if cur == nil {
		r.refuse(CauseFloor, ErrNoSnapshot)
		return
	}
	r.bundle = readBundle(r.o, r.bundle)
	if r.bundle.err != nil {
		r.refuse(r.bundle.cause, r.bundle.err)
		return
	}
	r.statement = readStatement(r.o, r.statement)
	if r.statement.err != nil {
		r.refuse(r.statement.cause, r.statement.err)
		return
	}
	st, disk := r.statement.st, r.bundle.snap
	if disk.Ref().GetDigest() == cur.Ref().GetDigest() {
		r.renew(ctx, cur, st, now)
		return
	}
	r.replace(ctx, cur, st, now)
}

// renew judges st against the current bundle, which is the bundle on disk.
func (r *Refresher) renew(ctx context.Context, cur *policy.Snapshot, st policy.Statement, now time.Time) {
	switch {
	case binds(st, cur) && cur.ConfirmedAt().Equal(st.IssuedAt()):
		r.settle("confirmed")
	case binds(st, cur):
		r.confirm(ctx, cur, st, now, nil)
	case st.BundleID() == r.o.BundleID && st.Serial() > cur.Serial():
		r.await(&r.stats.AwaitingBundle, "the statement names a bundle not yet on disk", st)
	default:
		r.refuse(CauseStatementUnbound, fmt.Errorf("%w: it names %s serial %d", policy.ErrStatementUnbound, st.BundleID(), st.Serial()))
	}
}

// replace judges the bundle on disk, which is not the current one, and st.
func (r *Refresher) replace(ctx context.Context, cur *policy.Snapshot, st policy.Statement, now time.Time) {
	disk := r.bundle.snap
	switch {
	case disk.Serial() < cur.Serial():
		r.refuse(CauseRollback, fmt.Errorf("%w: serial %d after %d", policy.ErrRollback, disk.Serial(), cur.Serial()))
		return
	case disk.Serial() == cur.Serial():
		r.refuse(CauseSerialReused, fmt.Errorf("%w: serial %d", policy.ErrSerialReused, disk.Serial()))
		return
	}
	if err := outlastsPoll(disk, r.o); err != nil {
		r.refuse(CauseBundleBudget, err)
		return
	}
	switch {
	case binds(st, disk):
		r.confirm(ctx, disk, st, now, r.bundle)
	case binds(st, cur):
		r.await(&r.stats.AwaitingStatement, "the bundle on disk awaits its statement", st)
	case st.BundleID() == r.o.BundleID && st.Serial() > disk.Serial():
		r.await(&r.stats.AwaitingBundle, "the statement names a bundle not yet on disk", st)
	default:
		r.refuse(CauseStatementUnbound, fmt.Errorf("%w: it names %s serial %d", policy.ErrStatementUnbound, st.BundleID(), st.Serial()))
	}
}

// confirm confirms snap by st: a renewal of the current bundle when install
// is nil, and otherwise the install of the bundle install holds. A statement
// whose budget has run out is refused before the floor is raised.
func (r *Refresher) confirm(ctx context.Context, snap *policy.Snapshot, st policy.Statement, now time.Time, install *diskBundle) {
	if expired(st, snap, r.o.MaxStale, now) {
		r.refuse(CauseStatementExpired, expiry(st, snap, r.o.MaxStale))
		return
	}
	// The holder keeps its lock while the store waits for the directory's,
	// and a withdrawal by the clock rule needs the holder's: the wait is
	// bounded so another process holding the directory cannot hold the clock
	// rule off past one interval.
	raise, cancel := context.WithTimeout(ctx, r.o.Interval)
	defer cancel()
	var err error
	if install == nil {
		err = r.o.Holder.Confirm(raise, st, now)
	} else {
		err = r.o.Holder.InstallConfirmed(raise, install.msg, r.o.BundleKeys, st, now)
	}
	if err != nil {
		r.refuse(causeOf(err), err)
		return
	}
	r.count(func(s *Stats) {
		s.Confirmations++
		if install != nil {
			s.Installs++
		}
	})
	r.settle("")
	if r.o.Logger != nil {
		r.o.Logger.Info("policy confirmed", "serial", st.Serial(), "digest", st.Digest(),
			"issued_at", policy.FormatIssuedAt(st.IssuedAt()), "installed", install != nil)
	}
}

// withdraw unconfirms the snapshot, counting a confirmation it took back.
func (r *Refresher) withdraw() {
	if cur := r.o.Holder.Current(); cur != nil && !cur.ConfirmedAt().IsZero() {
		r.count(func(s *Stats) { s.Withdrawals++ })
		if r.o.Logger != nil {
			r.o.Logger.Warn("the wall clock stepped back past the mark; the policy is unconfirmed until a newer statement",
				"withdrawn", policy.FormatIssuedAt(cur.ConfirmedAt()))
		}
	}
	r.o.Holder.Unconfirm()
}

// refuse counts a poll that moved nothing under cause, and logs it when the
// outcome changed since the poll before.
func (r *Refresher) refuse(cause Cause, err error) {
	r.count(func(s *Stats) { s.Refused[cause]++ })
	if r.changed("refused " + string(cause)) {
		r.o.Logger.Warn("policy refresh refused; the last good snapshot stays", "cause", string(cause), "err", err)
	}
}

// await counts a poll that found one file of a pair, and logs it when the
// outcome changed since the poll before.
func (r *Refresher) await(counter *uint64, what string, st policy.Statement) {
	r.count(func(*Stats) { *counter++ })
	if r.changed(what) {
		r.o.Logger.Info(what, "statement_serial", st.Serial(), "statement_digest", st.Digest())
	}
}

// settle notes a poll that found the snapshot as it should be.
func (r *Refresher) settle(outcome string) { r.changed(outcome) }

// changed records outcome and reports whether it differs from the last one
// and there is a logger to tell.
func (r *Refresher) changed(outcome string) bool {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	differs := outcome != r.last
	r.last = outcome
	return differs && r.o.Logger != nil
}

func (r *Refresher) count(f func(*Stats)) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	f(&r.stats)
}

// Run polls every interval until ctx ends.
func (r *Refresher) Run(ctx context.Context) {
	t := time.NewTicker(r.o.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Poll(ctx)
		}
	}
}

// Stats is what the refresher counted so far.
func (r *Refresher) Stats() Stats {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	out := r.stats
	out.Refused = make(map[Cause]uint64, len(r.stats.Refused))
	for k, v := range r.stats.Refused {
		out.Refused[k] = v
	}
	return out
}
