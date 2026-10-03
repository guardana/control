package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
)

// expectNotBefore fails unless h's snapshot is confirmed at confirmed and not
// before notBefore, each a time of day or "" for the zero time.
func expectNotBefore(t tb, h *policy.Holder, when, confirmed, notBefore string) {
	t.Helper()
	at := func(clock string) time.Time {
		if clock == "" {
			return time.Time{}
		}
		return utc(clock)
	}
	cur := h.Current()
	if !cur.ConfirmedAt().Equal(at(confirmed)) || !cur.NotBefore().Equal(at(notBefore)) {
		t.Fatalf("%s: confirmed %v, not before %v; want %v and %v", when, cur.ConfirmedAt(), cur.NotBefore(), at(confirmed), at(notBefore))
	}
}

// TestNotBeforeIsTheLatestVerifiedTimeAndNeverFalls: a holder's snapshot is
// not before the latest issuedAt the holder verified, a floor's latest
// included, and an Unconfirm or an unconfirmed install never lowers it.
func TestNotBeforeIsTheLatestVerifiedTimeAndNeverFalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b7, b8 := signedDocument("payments", "v7", 7), signedDocument("payments", "v8", 8)
	d7, d8 := digestOf(b7.GetCanonical()), digestOf(b8.GetCanonical())
	store := newMemStore(floorOf(t, 7, d7, "11:00:00", "11:30:00"))
	h := floorHolder(t, store)
	installUnconfirmed(t, h, b7)
	expectNotBefore(t, h, "the floor's own bundle, started unconfirmed", "", "11:30:00")

	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "11:20:00"), utc("12:00:00")); err != nil {
		t.Fatal(err)
	}
	expectNotBefore(t, h, "confirmed by a statement older than the floor's latest", "11:20:00", "11:30:00")

	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "11:40:00"), utc("12:00:00")); err != nil {
		t.Fatal(err)
	}
	expectNotBefore(t, h, "renewed", "11:40:00", "11:40:00")

	store.mu.Lock()
	store.floors["payments"] = floorOf(t, 7, d7, "11:00:00", "11:45:00")
	store.mu.Unlock()
	if err := h.Confirm(ctx, statementFor(t, "payments", 7, d7, "11:42:00"), utc("12:00:00")); err != nil {
		t.Fatal(err)
	}
	expectNotBefore(t, h, "renewed over a floor another plane raised", "11:42:00", "11:45:00")

	h.Unconfirm()
	expectNotBefore(t, h, "unconfirmed", "", "11:45:00")

	if err := h.InstallConfirmed(ctx, b8, pinned(), statementFor(t, "payments", 8, d8, "11:50:00"), utc("12:00:00")); err != nil {
		t.Fatal(err)
	}
	expectNotBefore(t, h, "a new bundle confirmed", "11:50:00", "11:50:00")
	h.Unconfirm()
	expectNotBefore(t, h, "unconfirmed again", "", "11:50:00")
}

// TestAnEmptyFloorVerifiesNothing: a bundle started unconfirmed over a floor
// with no serial has no verified time to be held to.
func TestAnEmptyFloorVerifiesNothing(t *testing.T) {
	t.Parallel()
	h := floorHolder(t, newMemStore(emptyFloor(t, "payments")))
	installUnconfirmed(t, h, signedDocument("payments", "v7", 7))
	expectNotBefore(t, h, "over an empty floor", "", "")
}

// TestALoadedSnapshotIsNotBeforeItsConfirmation: Load's time is both the
// confirmation and the not-before, and a nil snapshot has neither.
func TestALoadedSnapshotIsNotBeforeItsConfirmation(t *testing.T) {
	t.Parallel()
	snap := load(t, signedDocument("payments", "v7", 7))
	if !snap.NotBefore().Equal(loadTime()) || !snap.ConfirmedAt().Equal(loadTime()) {
		t.Fatalf("confirmed %v, not before %v; want both %v", snap.ConfirmedAt(), snap.NotBefore(), loadTime())
	}
	var none *policy.Snapshot
	if !none.NotBefore().IsZero() {
		t.Fatalf("a nil snapshot is not before %v", none.NotBefore())
	}
}
