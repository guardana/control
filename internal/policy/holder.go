package policy

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/match"
)

// Holder keeps the current snapshot. Install is serialized; Current is a read
// that takes no lock. The zero Holder holds nothing, is ready to use and takes
// any bundle id; NewHolder pins one.
type Holder struct {
	mu      sync.Mutex
	current atomic.Pointer[Snapshot]
	// pin is the one bundle id Install takes, or "" for any.
	pin string
	// installed holds, per bundle id, the highest serial installed and its
	// digest. ADR-0012 says rollback protection holds per bundle id, so a
	// detour through another bundle id must not reset it. Guarded by mu.
	installed map[string]installed
}

type installed struct {
	serial int64
	digest string
}

// NewHolder returns a Holder that serves bundleID and no other: Install
// refuses a bundle of any other id with ErrBundlePin, whatever its serial. An
// empty bundleID pins nothing, which is the zero Holder.
func NewHolder(bundleID string) *Holder {
	return &Holder{pin: bundleID}
}

// Install loads b, and only if every check of Load passed, refreshes or
// replaces the current snapshot:
//
//   - a pinned Holder refuses a bundle of another id (ErrBundlePin);
//   - the current snapshot's digest again gives a new snapshot of the same
//     program, confirmed at now;
//   - for a bundle id installed before, a lower serial is refused
//     (ErrRollback), and so is the same serial with another digest
//     (ErrSerialReused);
//   - anything else replaces the current snapshot.
//
// A refused install changes nothing. The protection lasts as long as the
// Holder: a new one accepts any serial. now is taken as given in both
// directions: a refresh with an earlier now moves the confirmation back, and
// a later one keeps a superseded bundle fresh, so the caller's clock
// discipline is the only guard.
func (h *Holder) Install(b *controlv1.PolicyBundle, keys bundle.Keyring, now time.Time) error {
	return h.install(b, keys, now, match.Compile)
}

// install is Install with the compiler as a parameter, so that a test can hold
// one install inside Load and see whether a second one waits for it.
func (h *Holder) install(b *controlv1.PolicyBundle, keys bundle.Keyring, now time.Time, compile compiler) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	next, err := load(b, keys, now, compile)
	if err != nil {
		return err
	}
	if id := next.ref.GetBundleId(); h.pin != "" && id != h.pin {
		return fmt.Errorf("%w: %q", ErrBundlePin, id)
	}
	if cur := h.current.Load(); cur != nil && cur.ref.GetDigest() == next.ref.GetDigest() {
		refreshed := *cur
		refreshed.confirmedAt = now
		h.current.Store(&refreshed)
		return nil
	}
	if err := h.admit(next); err != nil {
		return err
	}
	h.current.Store(next)
	return nil
}

// admit applies the rollback rules to next and records it. mu is held.
func (h *Holder) admit(next *Snapshot) error {
	id, digest := next.ref.GetBundleId(), next.ref.GetDigest()
	if last, ok := h.installed[id]; ok {
		switch {
		case next.serial < last.serial:
			return fmt.Errorf("%w: serial %d after %d", ErrRollback, next.serial, last.serial)
		case next.serial == last.serial && digest != last.digest:
			return fmt.Errorf("%w: serial %d", ErrSerialReused, next.serial)
		}
	}
	if h.installed == nil {
		h.installed = make(map[string]installed)
	}
	h.installed[id] = installed{serial: next.serial, digest: digest}
	return nil
}

// Current returns the current snapshot, or nil before the first successful
// Install.
func (h *Holder) Current() *Snapshot {
	return h.current.Load()
}
