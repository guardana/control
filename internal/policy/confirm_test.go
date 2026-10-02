package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
)

// floorHolder is a holder of "payments" confirmed through store.
func floorHolder(t tb, store policy.FloorStore) *policy.Holder {
	t.Helper()
	h, err := policy.NewFloorHolder("payments", store)
	if err != nil {
		t.Fatalf("NewFloorHolder: %v", err)
	}
	return h
}

// expectConfirmed fails unless the holder's snapshot has this serial and
// digest and is confirmed at the time of day given, or unconfirmed for "".
func expectConfirmed(t tb, h *policy.Holder, serial int64, digest, confirmed string) {
	t.Helper()
	cur := h.Current()
	if cur == nil {
		t.Fatalf("no snapshot, want serial %d", serial)
	}
	want := time.Time{}
	if confirmed != "" {
		want = utc(confirmed)
	}
	if cur.Serial() != serial || cur.Ref().GetDigest() != digest || !cur.ConfirmedAt().Equal(want) {
		t.Fatalf("snapshot serial %d, digest %s, confirmed %v; want %d, %s, %v", cur.Serial(), cur.Ref().GetDigest(), cur.ConfirmedAt(), serial, digest, want)
	}
}

func TestNewFloorHolderRefusesNoPinAndNoStore(t *testing.T) {
	t.Parallel()
	if h, err := policy.NewFloorHolder("", newMemStore()); !errors.Is(err, policy.ErrBundlePin) || h != nil {
		t.Errorf("NewFloorHolder with no bundle id = %v, %v; want ErrBundlePin", h, err)
	}
	if h, err := policy.NewFloorHolder("payments", nil); !errors.Is(err, policy.ErrNoFloorStore) || h != nil {
		t.Errorf("NewFloorHolder with no store = %v, %v; want ErrNoFloorStore", h, err)
	}
}

// A holder confirmed by statements never takes the clock's word: Install,
// which confirms at the time it is handed, refuses on it.
func TestAFloorHolderRefusesInstall(t *testing.T) {
	t.Parallel()
	h := floorHolder(t, newMemStore(emptyFloor(t, "payments")))
	expectOnly(t, h.Install(valid().build(), pinned(), at(0)), policy.ErrStatementOnly)
	if h.Current() != nil {
		t.Fatal("a refused Install left a snapshot")
	}
}

// The zero Holder has no store, so none of the confirming entry points runs
// on it; Install still does.
func TestTheZeroHolderHasNoConfirmingEntryPoints(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	ctx := context.Background()
	st := statementFor(t, "payments", 7, d7, "12:00:00")
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); !errors.Is(err, policy.ErrNoFloorStore) {
		t.Errorf("InstallUnconfirmed = %v", err)
	}
	if err := h.InstallConfirmed(ctx, valid().build(), pinned(), st, utc("13:00:00")); !errors.Is(err, policy.ErrNoFloorStore) {
		t.Errorf("InstallConfirmed = %v", err)
	}
	if err := h.Confirm(ctx, st, utc("13:00:00")); !errors.Is(err, policy.ErrNoFloorStore) {
		t.Errorf("Confirm = %v", err)
	}
	if h.Current() != nil {
		t.Fatal("a refused entry point left a snapshot")
	}
	install(t, &h, valid().build(), at(0))
	expectCurrent(t, &h, 7, exampleDigest, at(0))
}

// An install moves no confirmation: a bundle starts unconfirmed, a statement
// confirms it at its issuedAt, and the same bundle installed again leaves
// that confirmation where it was.
func TestInstallingTheSameDigestLeavesTheConfirmation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(emptyFloor(t, "payments"))
	h := floorHolder(t, store)
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	expectConfirmed(t, h, 7, d7, "")
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:00:00"), utc("12:30:00")); err != nil {
		t.Fatal(err)
	}
	expectConfirmed(t, h, 7, d7, "12:00:00")
	expectFloor(t, "the stored floor", store.stored("payments"), 7, d7, "12:00:00", "12:00:00")
	before := h.Current()
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	if h.Current() != before {
		t.Fatal("installing the same digest again replaced the snapshot")
	}
	expectConfirmed(t, h, 7, d7, "12:00:00")
}

// A renewal moves the confirmation to its issuedAt and raises the floor; an
// older statement replayed after it is refused and moves nothing.
func TestARenewalRaisesAndAReplayIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(floorOf(t, 7, d7, "12:00:00", "12:00:00"))
	h := floorHolder(t, store)
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:05:00"), utc("12:06:00")); err != nil {
		t.Fatal(err)
	}
	expectConfirmed(t, h, 7, d7, "12:05:00")
	expectFloor(t, "after the renewal", store.stored("payments"), 7, d7, "12:05:00", "12:05:00")

	before := h.Current()
	for _, old := range []string{"12:00:00", "12:04:59"} {
		err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, old), utc("12:07:00"))
		if !errors.Is(err, policy.ErrBelowFloor) || !errors.Is(err, policy.ErrFloorRaise) {
			t.Errorf("a replayed statement issued %s: %v, want ErrBelowFloor through ErrFloorRaise", old, err)
		}
		if h.Current() != before {
			t.Fatalf("a replayed statement issued %s replaced the snapshot", old)
		}
	}
	expectConfirmed(t, h, 7, d7, "12:05:00")
	expectFloor(t, "after the replays", store.stored("payments"), 7, d7, "12:05:00", "12:05:00")
}

// A statement for another bundle id, serial or digest confirms nothing and
// reaches no store.
func TestAStatementForAnotherBundleConfirmsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(emptyFloor(t, "payments"), emptyFloor(t, "risk"))
	h := floorHolder(t, store)
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	before := h.Current()
	for name, st := range map[string]policy.Statement{
		"another bundle id": statementFor(t, "risk", 7, d7, "12:00:00"),
		"another serial":    statementFor(t, "payments", 8, d7, "12:00:00"),
		"another digest":    statementFor(t, "payments", 7, d7other, "12:00:00"),
	} {
		if err := h.Confirm(ctx, st, utc("13:00:00")); !errors.Is(err, policy.ErrStatementUnbound) {
			t.Errorf("%s: Confirm = %v, want ErrStatementUnbound", name, err)
		}
		if h.Current() != before {
			t.Fatalf("%s: the snapshot was replaced", name)
		}
	}
	if store.writes() != 0 || store.stored("payments").HasSerial() || store.stored("risk").HasSerial() {
		t.Fatalf("an unbound statement reached the store: %d writes", store.writes())
	}
	expectConfirmed(t, h, 7, d7, "")
}

// The floor is raised before the confirmed snapshot is published: when the
// store's write fails, nothing is published, a renewal and a new bundle alike.
func TestAFailedRaisePublishesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(floorOf(t, 7, d7, "12:00:00", "12:00:00"))
	h := floorHolder(t, store)
	if err := h.InstallConfirmed(ctx, valid().build(), pinned(), statementFor(t, "payments", 7, d7, "12:00:00"), utc("12:01:00")); err != nil {
		t.Fatal(err)
	}
	expectConfirmed(t, h, 7, d7, "12:00:00")
	before := h.Current()
	store.writeErr = errWriteFailed

	err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:05:00"), utc("12:06:00"))
	if !errors.Is(err, policy.ErrFloorRaise) || !errors.Is(err, errWriteFailed) {
		t.Errorf("a renewal the store failed to write: %v", err)
	}
	if h.Current() != before {
		t.Fatal("a renewal whose raise failed was published")
	}

	higher := signedDocument("payments", "v8", 8)
	st8 := statementFor(t, "payments", 8, digestOf(higher.GetCanonical()), "12:05:00")
	err = h.InstallConfirmed(ctx, higher, pinned(), st8, utc("12:06:00"))
	if !errors.Is(err, policy.ErrFloorRaise) || !errors.Is(err, errWriteFailed) {
		t.Errorf("a new bundle the store failed to raise for: %v", err)
	}
	if h.Current() != before {
		t.Fatal("a bundle whose raise failed was published")
	}
	expectConfirmed(t, h, 7, d7, "12:00:00")
	expectFloor(t, "the stored floor", store.stored("payments"), 7, d7, "12:00:00", "12:00:00")

	store.writeErr = nil
	if err := h.InstallConfirmed(ctx, higher, pinned(), st8, utc("12:06:00")); err != nil {
		t.Fatalf("the same install once the store writes: %v", err)
	}
	expectConfirmed(t, h, 8, digestOf(higher.GetCanonical()), "12:05:00")
}

// A store that answers with a floor other than the statement's is not
// believed.
func TestAStoreThatReturnsAnotherFloorIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := floorHolder(t, liarStore{floor: floorOf(t, 7, d7, "12:00:00", "12:00:00")})
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	before := h.Current()
	err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:05:00"), utc("12:06:00"))
	if !errors.Is(err, policy.ErrFloorRaise) {
		t.Fatalf("Confirm through a store that did not raise: %v", err)
	}
	if h.Current() != before {
		t.Fatal("the snapshot was replaced")
	}
}

// Two planes on one floor: a floor another plane raised past a renewal
// refuses that renewal, and neither plane ever lowers it.
func TestAFloorRaisedByAnotherPlaneRefusesTheRenewal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(floorOf(t, 7, d7, "12:00:00", "12:00:00"))
	a, b := floorHolder(t, store), floorHolder(t, store)
	for _, h := range []*policy.Holder{a, b} {
		if err := h.InstallConfirmed(ctx, valid().build(), pinned(), statementFor(t, "payments", 7, d7, "12:00:00"), utc("12:01:00")); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:10:00"), utc("12:11:00")); err != nil {
		t.Fatal(err)
	}
	err := b.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:05:00"), utc("12:11:00"))
	if !errors.Is(err, policy.ErrBelowFloor) {
		t.Fatalf("plane b's renewal under a's: %v, want ErrBelowFloor", err)
	}
	expectConfirmed(t, b, 7, d7, "12:00:00")

	higher := signedDocument("payments", "v8", 8)
	d8 := digestOf(higher.GetCanonical())
	if err := a.InstallConfirmed(ctx, higher, pinned(), statementFor(t, "payments", 8, d8, "12:20:00"), utc("12:21:00")); err != nil {
		t.Fatal(err)
	}
	err = b.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:30:00"), utc("12:31:00"))
	if !errors.Is(err, policy.ErrBelowFloor) {
		t.Fatalf("plane b's renewal of serial 7 under a's serial 8: %v, want ErrBelowFloor", err)
	}
	expectConfirmed(t, b, 7, d7, "12:00:00")
	expectFloor(t, "the shared floor", store.stored("payments"), 8, d8, "12:20:00", "12:20:00")
}

// InstallUnconfirmed starts only what a floor lets start without its
// statement: anything while it holds no serial, and its own bundle.
func TestInstallUnconfirmedHoldsToTheFloor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := floorHolder(t, newMemStore(floorOf(t, 7, d7, "12:00:00", "12:00:00")))
	for _, c := range []struct {
		name string
		b    func() error
		want error
	}{
		{"a lower bundle", func() error { return h.InstallUnconfirmed(ctx, signedDocument("payments", "v6", 6), pinned()) }, policy.ErrBelowFloor},
		{"the floor's serial, another digest", func() error {
			return h.InstallUnconfirmed(ctx, signedDocument("payments", "v7-other", 7), pinned())
		}, policy.ErrFloorSerialReused},
		{"a higher bundle", func() error { return h.InstallUnconfirmed(ctx, signedDocument("payments", "v8", 8), pinned()) }, policy.ErrAboveFloorUnbound},
		{"another bundle id", func() error { return h.InstallUnconfirmed(ctx, signedDocument("risk", "v7", 7), pinned()) }, policy.ErrBundlePin},
		{"a broken signature", func() error {
			broken := valid()
			broken.editSig = flipFirstBit
			return h.InstallUnconfirmed(ctx, broken.build(), pinned())
		}, policy.ErrSignature},
	} {
		if err := c.b(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if h.Current() != nil {
			t.Fatalf("%s: a refused install left a snapshot", c.name)
		}
	}
	err := h.InstallUnconfirmed(ctx, signedDocument("payments", "v8", 8), pinned())
	if errors.Is(err, policy.ErrClockBehindFloor) || errors.Is(err, policy.ErrStatementFuture) || !errors.Is(err, policy.ErrStatementMissing) {
		t.Errorf("a higher bundle names a clock InstallUnconfirmed was never given: %v", err)
	}
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatalf("the floor's own bundle: %v", err)
	}
	expectConfirmed(t, h, 7, d7, "")

	missing := floorHolder(t, newMemStore())
	if err := missing.InstallUnconfirmed(ctx, valid().build(), pinned()); err == nil || missing.Current() != nil {
		t.Fatalf("a store with no floor for the id: %v", err)
	}
}

// InstallConfirmed refuses a statement that is not the bundle's, and a bundle
// the pin or the in-memory rollback rule refuses, before the store is asked.
func TestInstallConfirmedRefusesBeforeTheStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(emptyFloor(t, "payments"))
	h := floorHolder(t, store)
	higher := signedDocument("payments", "v8", 8)
	err := h.InstallConfirmed(ctx, higher, pinned(), statementFor(t, "payments", 7, d7, "12:00:00"), utc("13:00:00"))
	if !errors.Is(err, policy.ErrStatementUnbound) {
		t.Errorf("the old bundle's statement for a new bundle: %v", err)
	}
	other := signedDocument("risk", "v1", 1)
	err = h.InstallConfirmed(ctx, other, pinned(), statementFor(t, "risk", 1, digestOf(other.GetCanonical()), "12:00:00"), utc("13:00:00"))
	if !errors.Is(err, policy.ErrBundlePin) {
		t.Errorf("another bundle id: %v", err)
	}
	if h.Current() != nil || store.writes() != 0 {
		t.Fatalf("a refused install published %v or wrote %d floors", h.Current(), store.writes())
	}
	if err := h.InstallConfirmed(ctx, higher, pinned(), statementFor(t, "payments", 8, digestOf(higher.GetCanonical()), "12:00:00"), utc("13:00:00")); err != nil {
		t.Fatal(err)
	}
	err = h.InstallConfirmed(ctx, valid().build(), pinned(), statementFor(t, "payments", 7, d7, "12:30:00"), utc("13:00:00"))
	if !errors.Is(err, policy.ErrRollback) {
		t.Errorf("a lower bundle after a higher one: %v, want ErrRollback", err)
	}
	if store.writes() != 1 {
		t.Errorf("%d floor writes, want 1", store.writes())
	}
}

// A statement dated after the clock, or a clock behind the floor's latest
// issuedAt, confirms nothing.
func TestConfirmHoldsToTheClock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := floorHolder(t, newMemStore(floorOf(t, 7, d7, "11:00:00", "12:00:00")))
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:30:01"), utc("12:30:00")); !errors.Is(err, policy.ErrStatementFuture) {
		t.Errorf("a statement dated ahead: %v", err)
	}
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "11:30:00"), utc("11:59:59")); !errors.Is(err, policy.ErrClockBehindFloor) {
		t.Errorf("a clock behind the floor's latest issuedAt: %v", err)
	}
	expectConfirmed(t, h, 7, d7, "")
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:30:00"), utc("12:30:00")); err != nil {
		t.Fatalf("a statement dated at the clock: %v", err)
	}
	expectConfirmed(t, h, 7, d7, "12:30:00")
}

// Unconfirm publishes the same bundle with no confirmation, and leaves the
// snapshot it replaced as it was; on an empty holder it does nothing. The
// statement that confirmed it does not confirm it again, at any clock; a
// statement issued after it does.
func TestUnconfirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := floorHolder(t, newMemStore(emptyFloor(t, "payments")))
	h.Unconfirm()
	if h.Current() != nil {
		t.Fatal("Unconfirm on an empty holder made a snapshot")
	}
	if err := h.InstallConfirmed(ctx, valid().build(), pinned(), statementFor(t, "payments", 7, d7, "12:00:00"), utc("12:01:00")); err != nil {
		t.Fatal(err)
	}
	confirmed := h.Current()
	h.Unconfirm()
	expectConfirmed(t, h, 7, d7, "")
	if h.Current() == confirmed || !confirmed.ConfirmedAt().Equal(utc("12:00:00")) {
		t.Fatal("Unconfirm changed the published snapshot in place")
	}
	for _, clock := range []string{"12:02:00", "13:00:00"} {
		err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:00:00"), utc(clock))
		if !errors.Is(err, policy.ErrConfirmationWithdrawn) {
			t.Errorf("the withdrawn statement again at %s: %v, want ErrConfirmationWithdrawn", clock, err)
		}
		expectConfirmed(t, h, 7, d7, "")
	}
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "12:00:01"), utc("13:00:00")); err != nil {
		t.Fatalf("a statement issued one second after the withdrawn one: %v", err)
	}
	expectConfirmed(t, h, 7, d7, "12:00:01")
	higher := signedDocument("payments", "v8", 8)
	early := statementFor(t, "payments", 8, digestOf(higher.GetCanonical()), "11:30:00")
	if err := h.InstallConfirmed(ctx, higher, pinned(), early, utc("13:00:01")); err != nil {
		t.Fatalf("once a newer statement is accepted, the mark is gone, and a higher serial issued before it is taken: %v", err)
	}
	expectConfirmed(t, h, 8, digestOf(higher.GetCanonical()), "11:30:00")
}

// A wall clock set back: confirmed at 12:00 with the clock at 12:30, then
// unconfirmed, the same statement does not confirm again with the clock at
// 12:10, nor through InstallConfirmed, nor once the clock runs on.
func TestAWithdrawnConfirmationNeedsANewerStatement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(emptyFloor(t, "payments"))
	h := floorHolder(t, store)
	st := statementFor(t, "payments", 7, d7, "12:00:00")
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); err != nil {
		t.Fatal(err)
	}
	if err := h.Confirm(ctx, st, utc("12:30:00")); err != nil {
		t.Fatal(err)
	}
	h.Unconfirm()
	h.Unconfirm()
	writes := store.writes()
	if err := h.Confirm(ctx, st, utc("12:10:00")); !errors.Is(err, policy.ErrConfirmationWithdrawn) {
		t.Errorf("the same statement after the step back: %v", err)
	}
	if err := h.InstallConfirmed(ctx, valid().build(), pinned(), st, utc("12:40:00")); !errors.Is(err, policy.ErrConfirmationWithdrawn) {
		t.Errorf("the same statement through InstallConfirmed: %v", err)
	}
	if err := h.Confirm(ctx, st, utc("23:00:00")); !errors.Is(err, policy.ErrConfirmationWithdrawn) {
		t.Errorf("the same statement with the clock run on: %v", err)
	}
	expectConfirmed(t, h, 7, d7, "")
	if store.writes() != writes {
		t.Errorf("a refused confirmation reached the store")
	}
}

// A store whose floor cannot be read refuses the install with a cause of its
// own, never as a floor that is not one.
func TestAFloorTheStoreCannotReadIsItsOwnCause(t *testing.T) {
	t.Parallel()
	store := newMemStore(emptyFloor(t, "payments"))
	store.readErr = errReadFailed
	h := floorHolder(t, store)
	err := h.InstallUnconfirmed(context.Background(), valid().build(), pinned())
	if !errors.Is(err, policy.ErrFloorRead) || !errors.Is(err, errReadFailed) || errors.Is(err, policy.ErrFloorInvalid) {
		t.Fatalf("InstallUnconfirmed over a store that cannot read: %v, want ErrFloorRead wrapping the store's error", err)
	}
	if h.Current() != nil {
		t.Fatal("a refused install left a snapshot")
	}
	missing := floorHolder(t, newMemStore())
	if err := missing.InstallUnconfirmed(context.Background(), valid().build(), pinned()); !errors.Is(err, policy.ErrFloorRead) {
		t.Fatalf("a store holding no floor for the id: %v, want ErrFloorRead", err)
	}
}

// While the floor holds no serial yet, the in-memory rollback rule still
// holds: serial 9 installed unconfirmed, serial 7 after it is refused.
func TestInstallUnconfirmedHoldsToTheRollbackRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := floorHolder(t, newMemStore(emptyFloor(t, "payments")))
	nine := signedDocument("payments", "v9", 9)
	if err := h.InstallUnconfirmed(ctx, nine, pinned()); err != nil {
		t.Fatal(err)
	}
	before := h.Current()
	if err := h.InstallUnconfirmed(ctx, valid().build(), pinned()); !errors.Is(err, policy.ErrRollback) {
		t.Fatalf("serial 7 after serial 9: %v, want ErrRollback", err)
	}
	if h.Current() != before {
		t.Fatal("the refused install replaced the snapshot")
	}
	expectConfirmed(t, h, 9, digestOf(nine.GetCanonical()), "")
}

// A store that wrote the floor and then reported a failure: the holder
// publishes nothing, and the floor's own statement, read again, recovers.
func TestAStoreThatWroteAndFailedPublishesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMemStore(floorOf(t, 7, d7, "12:00:00", "12:00:00"))
	h := floorHolder(t, store)
	if err := h.InstallConfirmed(ctx, valid().build(), pinned(), statementFor(t, "payments", 7, d7, "12:00:00"), utc("12:01:00")); err != nil {
		t.Fatal(err)
	}
	before := h.Current()
	renewal := statementFor(t, "payments", 7, d7, "12:05:00")
	store.afterWrite = errSyncFailed
	err := h.Confirm(ctx, renewal, utc("12:06:00"))
	if !errors.Is(err, policy.ErrFloorRaise) || !errors.Is(err, errSyncFailed) {
		t.Fatalf("a raise written and then failed: %v", err)
	}
	if h.Current() != before {
		t.Fatal("a raise the store reported failed was published")
	}
	expectFloor(t, "the floor written before the failure", store.stored("payments"), 7, d7, "12:05:00", "12:05:00")
	store.afterWrite = nil
	if err := h.Confirm(ctx, renewal, utc("12:07:00")); err != nil {
		t.Fatalf("the floor's own statement after the failure: %v", err)
	}
	expectConfirmed(t, h, 7, d7, "12:05:00")
}

func TestConfirmNeedsASnapshot(t *testing.T) {
	t.Parallel()
	h := floorHolder(t, newMemStore(emptyFloor(t, "payments")))
	if err := h.Confirm(context.Background(), statementFor(t, "payments", 7, d7, "12:00:00"), utc("13:00:00")); !errors.Is(err, policy.ErrNoBundle) {
		t.Fatalf("Confirm with nothing installed: %v", err)
	}
}
