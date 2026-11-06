package runs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Plane is the plane's handle on a runs directory. It resolves tokens, looks
// a run up, and reads and raises a root's state; it has no method that opens,
// closes or lists a run, and nothing it does creates, rewrites or deletes a record. It
// takes no directory lock, so planes side by side share one directory, and a
// root's state is written only under that root's own lock file.
//
// On a signed layout every record counts only once its judge took it, and
// the plane writes in state/ alone: a root's state and lock when a call first
// raises it, and the close of every closed record it took, after which the
// run's open record put back is refused.
//
// The zero value is not a directory. Every method on it fails closed.
type Plane struct {
	// d holds the records: the one directory, or a signed layout's records/.
	d *dir
	// st holds the states and locks: d, or a signed layout's state/.
	st      *dir
	judge   Judge
	signed  bool
	records string
	counts  *signedCounts
}

// OpenPlane opens dir for a plane. It refuses a group- or world-writable
// directory with ErrPermissions, a directory with no marker with
// ErrNotRunsDir, and one holding a file this package does not write with
// ErrForeignFile. It never turns a directory into a runs directory: that is
// the operator's. A signed layout is refused with ErrLayout: only
// OpenSignedPlane, with a judge, reads one.
func OpenPlane(dir string) (*Plane, error) {
	signed, err := signedLayout(dir)
	switch {
	case err != nil:
		return nil, err
	case signed:
		return nil, fmt.Errorf("%w: %s holds signed runs, which only a plane under the signed authority reads", ErrLayout, clip(dir))
	}
	d, err := openDir(dir, roleFlat, false)
	if err != nil {
		return nil, err
	}
	if err := d.checkContents(); err != nil {
		return nil, errors.Join(err, d.close())
	}
	return &Plane{d: d, st: d}, nil
}

// Signed reports whether the handle reads a signed layout.
func (p *Plane) Signed() bool { return p != nil && p.signed }

// Close releases the handle. A later call answers ErrClosed.
func (p *Plane) Close() error {
	if p == nil || p.d == nil {
		return ErrClosed
	}
	if p.st == p.d || p.st == nil {
		return p.d.close()
	}
	return errors.Join(p.d.close(), p.st.close())
}

// begin holds both directories open for one call.
func (p *Plane) begin(ctx context.Context) (func(), error) {
	if p == nil || p.st == nil {
		return nil, ErrClosed
	}
	end, err := p.d.begin(ctx)
	if err != nil || p.st == p.d {
		return end, err
	}
	endState, err := p.st.begin(ctx)
	if err != nil {
		end()
		return nil, err
	}
	return func() { endState(); end() }, nil
}

// Run is an open run as a token resolved it.
type Run struct {
	ID string
	// Root is the run whose state this run shares.
	Root string
	// Who is the identity the run's record names.
	Who Identity
	// Expires is when the run stops resolving.
	Expires time.Time
}

// Resolve returns the run token names when it is open for who at now, its
// project included: planes of two projects that share a directory and a
// listener identity never resolve each other's runs. Every refusal is a
// *Refusal. The causes are judged in an order that tells a caller
// nothing it cannot already prove: the token's shape, then whether its
// secret is right, and only then whose run it is and whether it is still
// open. A zero now is expired, since it would leave every run unexpired.
//
// On a signed layout a record the judge does not take, or one that is not
// signed, is CauseUnreadable after the secret is shown right; a closed record
// taken is carried into state/ before CauseClosed is answered, and an open
// record of a run whose close was carried is CauseClosed with ErrReplayed.
func (p *Plane) Resolve(ctx context.Context, token string, who Identity, now time.Time) (Run, error) {
	if token == "" {
		return Run{}, refuse(CauseMissing, nil)
	}
	id, secret, err := parseToken(token)
	if err != nil {
		return Run{}, refuse(CauseMalformed, err)
	}
	if p == nil {
		return Run{}, refuse(CauseUnreadable, ErrClosed)
	}
	end, err := p.begin(ctx)
	if err != nil {
		return Run{}, refuse(CauseUnreadable, err)
	}
	defer end()
	stored, err := p.d.readStored(id)
	rec := stored.Record
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Run{}, refuse(CauseUnknown, nil)
	case err != nil:
		p.counts.refused(err)
		return Run{}, refuse(CauseUnreadable, err)
	case !secretMatches(secret, rec.SecretSHA256):
		return Run{}, refuse(CauseUnknown, nil)
	}
	if r := p.standing(ctx, stored, who); r != nil {
		return Run{}, r
	}
	if now.IsZero() || !now.Before(rec.ExpiresAt) {
		return Run{}, refuse(CauseExpired, nil)
	}
	return Run{ID: rec.ID, Root: rec.Root, Who: rec.Who, Expires: rec.ExpiresAt}, nil
}

// standing judges a record whose secret was shown right: taken by the judge
// on a signed layout, for who, not closed, and not put back after a close
// the plane carried. A closed record taken is carried first.
func (p *Plane) standing(ctx context.Context, stored SignedRecord, who Identity) *Refusal {
	rec := stored.Record
	if p.signed {
		if err := p.take(ctx, stored); err != nil {
			return refuse(CauseUnreadable, err)
		}
	}
	switch {
	case rec.Who != who:
		return refuse(CauseIdentity, nil)
	case rec.Closed():
		if p.signed {
			p.carry(rec.ID)
		}
		return refuse(CauseClosed, nil)
	}
	switch err := p.notReplayed(rec); {
	case errors.Is(err, ErrReplayed):
		return refuse(CauseClosed, err)
	case err != nil:
		return refuse(CauseUnreadable, err)
	}
	return nil
}

// Lookup reads the record of run id, with its secret's hash left out, for a
// caller that needs to know whether a run is open, whose it is and what it
// shares state with. It refuses a run no record names with ErrNoRun, and it
// locks, creates and rewrites nothing. On a signed layout it refuses a record
// as Resolve does, and an open record of a run whose close was carried with
// ErrReplayed.
func (p *Plane) Lookup(ctx context.Context, id string) (Record, error) {
	if p == nil {
		return Record{}, ErrClosed
	}
	if err := checkRunID(id); err != nil {
		return Record{}, err
	}
	end, err := p.begin(ctx)
	if err != nil {
		return Record{}, err
	}
	defer end()
	if !p.signed {
		return p.d.lookup(id)
	}
	stored, err := p.d.readStored(id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Record{}, fmt.Errorf("%w: %s", ErrNoRun, id)
	case err != nil:
		p.counts.refused(err)
		return Record{}, err
	}
	if err := p.take(ctx, stored); err != nil {
		return Record{}, err
	}
	if err := p.notReplayed(stored.Record); err != nil {
		return Record{}, err
	}
	rec := stored.Record
	rec.SecretSHA256 = ""
	return rec, nil
}

// State reads root's state. A missing or unreadable state file is an error:
// the operator wrote it when the root opened, so a missing one was removed,
// and reading it as a clean start would wash what the run took in. On a
// signed layout the plane writes a root's state when it first raises it, so
// a missing one there is the state a root starts with.
func (p *Plane) State(ctx context.Context, root string) (State, error) {
	if p == nil {
		return State{}, ErrClosed
	}
	if err := checkRunID(root); err != nil {
		return State{}, err
	}
	end, err := p.begin(ctx)
	if err != nil {
		return State{}, err
	}
	defer end()
	return p.readState(root)
}

// readState is root's state, the initial one for a root of a signed layout
// the plane has not raised yet.
func (p *Plane) readState(root string) (State, error) {
	s, err := p.st.readState(root)
	if p.signed && errors.Is(err, fs.ErrNotExist) {
		return initialState, nil
	}
	return s, err
}

// Raise reads root's state under the root's exclusive lock, applies join to
// it, and when the result differs writes it whole and forces it to disk before
// it returns. The lock is taken through root's lock file, which Raise never
// creates: a missing one is an error. On a signed layout Raise creates a
// root's lock file and state where they are missing. While another writer
// holds the lock, Raise tries again until ctx ends.
//
// join decides alone whether the state rises; nothing else writes it.
func (p *Plane) Raise(ctx context.Context, root string, join func(State) State) (err error) {
	if p == nil {
		return ErrClosed
	}
	if join == nil {
		return fmt.Errorf("%w: no join", ErrState)
	}
	if err := checkRunID(root); err != nil {
		return err
	}
	end, err := p.begin(ctx)
	if err != nil {
		return err
	}
	defer end()
	lock, err := p.openLock(root)
	if err != nil {
		return fmt.Errorf("the lock of %s: %w", root, err)
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if err := lockWaiting(ctx, lock); err != nil {
		return err
	}
	cur, err := p.readState(root)
	if err != nil {
		return err
	}
	next := join(cur)
	if next == cur {
		return nil
	}
	body, err := encodeState(root, next)
	if err != nil {
		return err
	}
	if err := p.st.stillLocked(lock, root); err != nil {
		return err
	}
	return p.st.replace(root+stateTemp, root+stateSuffix, body)
}

// openLock opens root's lock file, which on a signed layout it creates where
// it is missing.
func (p *Plane) openLock(root string) (*os.File, error) {
	if p.signed {
		if err := p.st.createEmpty(root + lockSuffix); err != nil {
			return nil, err
		}
	}
	return p.st.openRegular(root+lockSuffix, os.O_RDWR)
}

// stillLocked refuses a raise whose lock file no longer stands at its name:
// another writer that opened the new file holds a lock of its own, so neither
// would keep the other out.
func (d *dir) stillLocked(lock *os.File, root string) error {
	held, err := lock.Stat()
	if err != nil {
		return err
	}
	now, err := d.root.Lstat(root + lockSuffix)
	if err != nil || !os.SameFile(held, now) {
		return fmt.Errorf("%w: %s", ErrLockChanged, root)
	}
	return nil
}
