package policy_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
)

// openHolder is a holder of "payments" over a floor that holds no serial yet,
// so the holder's own rules alone decide what InstallUnconfirmed takes.
func openHolder(t tb) *policy.Holder {
	t.Helper()
	return floorHolder(t, newMemStore(emptyFloor(t, "payments")))
}

// installUnconfirmed installs b with no confirmation, or fails.
func installUnconfirmed(t tb, h *policy.Holder, b *controlv1.PolicyBundle) {
	t.Helper()
	if err := h.InstallUnconfirmed(context.Background(), b, pinned()); err != nil {
		t.Fatalf("InstallUnconfirmed refused a bundle this test expects it to take: %v", err)
	}
}

// The zero Holder holds nothing and installs nothing: every entry point that
// would change it is refused, so a holder nobody made with a floor store
// never serves a snapshot.
func TestTheZeroHolderRefusesEveryEntryPoint(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	ctx := context.Background()
	st := statementFor(t, "payments", 7, d7, "12:00:00")
	for name, err := range map[string]error{
		"InstallUnconfirmed": h.InstallUnconfirmed(ctx, valid().build(), pinned()),
		"InstallConfirmed":   h.InstallConfirmed(ctx, valid().build(), pinned(), st, utc("13:00:00")),
		"Confirm":            h.Confirm(ctx, st, utc("13:00:00")),
	} {
		if !errors.Is(err, policy.ErrNoFloorStore) {
			t.Errorf("%s on the zero Holder = %v, want ErrNoFloorStore", name, err)
		}
	}
	h.Unconfirm()
	if h.Current() != nil {
		t.Fatal("the zero Holder serves a snapshot")
	}
}

func TestHolderIsEmptyUntilAnInstallSucceeds(t *testing.T) {
	t.Parallel()
	h := openHolder(t)
	if h.Current() != nil {
		t.Fatal("a new holder has a snapshot")
	}
	broken := valid()
	broken.editSig = flipFirstBit
	expectOnly(t, h.InstallUnconfirmed(context.Background(), broken.build(), pinned()), policy.ErrSignature)
	if h.Current() != nil {
		t.Fatal("a refused first install left a snapshot")
	}
	installUnconfirmed(t, h, valid().build())
	expectConfirmed(t, h, 7, exampleDigest, "")
}

func TestInstallTakesAHigherSerial(t *testing.T) {
	t.Parallel()
	h := openHolder(t)
	installUnconfirmed(t, h, signedDocument("payments", "v7", 7))
	higher := signedDocument("payments", "v8", 8)
	installUnconfirmed(t, h, higher)
	expectConfirmed(t, h, 8, digestOf(higher.GetCanonical()), "")
}

func TestInstallRefusesALowerSerial(t *testing.T) {
	t.Parallel()
	h := openHolder(t)
	current := signedDocument("payments", "v7", 7)
	installUnconfirmed(t, h, current)
	before := h.Current()
	expectOnly(t, h.InstallUnconfirmed(context.Background(), signedDocument("payments", "v6", 6), pinned()), policy.ErrRollback)
	if h.Current() != before {
		t.Fatal("the refused install replaced the snapshot")
	}
}

func TestInstallRefusesTheSameSerialWithOtherContent(t *testing.T) {
	t.Parallel()
	h := openHolder(t)
	current := signedDocument("payments", "v7", 7)
	installUnconfirmed(t, h, current)
	before := h.Current()
	expectOnly(t, h.InstallUnconfirmed(context.Background(), signedDocument("payments", "v7-other", 7), pinned()), policy.ErrSerialReused)
	if h.Current() != before {
		t.Fatal("the refused install replaced the snapshot")
	}
}

// TestAFailedInstallChangesNothing offers every change Load refuses to a
// holder whose snapshot is the unchanged example. Most carry the current
// digest in their ref, so a holder that took the same digest as the same
// bundle before checking would take them.
func TestAFailedInstallChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := openHolder(t)
	installUnconfirmed(t, h, valid().build())
	before := h.Current()
	for _, c := range changes() {
		expectOnly(t, h.InstallUnconfirmed(ctx, c.edit(valid()).build(), pinned()), c.want)
		if h.Current() != before {
			t.Fatalf("%s/%s: the refused install replaced the snapshot", c.field, c.name)
		}
	}
	expectOnly(t, h.InstallUnconfirmed(ctx, nil, pinned()), policy.ErrNoBundle)
	if h.Current() != before {
		t.Fatal("a refused install changed the snapshot")
	}
}

// TestInstallRunsEveryLoadCheckFirst: a bundle that fails a check of Load is
// refused by that check, whatever its serial would have said.
func TestInstallRunsEveryLoadCheckFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := openHolder(t)
	installUnconfirmed(t, h, signedDocument("payments", "v7", 7))

	lower := valid().withDoc(document("payments", "v6", 6, 300))
	lower.bundleID, lower.version, lower.editSig = "payments", "v6", flipFirstBit
	expectOnly(t, h.InstallUnconfirmed(ctx, lower.build(), pinned()), policy.ErrSignature)

	reused := valid().withDoc(document("payments", "v7-other", 7, 300))
	reused.bundleID, reused.version, reused.keyID = "payments", "v7-other", "k3"
	expectOnly(t, h.InstallUnconfirmed(ctx, reused.build(), pinned()), policy.ErrKey)
}

// holderModel is what the holder's rules say it holds, written from ADR-0012
// and ADR-0038: a bundle of another id than the pin is refused; the current
// bundle's digest again changes nothing; a lower serial, or the same serial
// with other content, is refused; anything else replaces the current bundle,
// unconfirmed.
type holderModel struct {
	digest    string // the current bundle's, "" before any
	serial    int64
	installed map[int64]string
}

func (m *holderModel) install(id string, serial int64, digest string) error {
	switch {
	case id != "payments":
		return policy.ErrBundlePin
	case m.digest != "" && digest == m.digest:
		return nil
	case serial < m.highest():
		return policy.ErrRollback
	case m.installed[serial] != "" && m.installed[serial] != digest:
		return policy.ErrSerialReused
	}
	m.installed[serial] = digest
	m.digest, m.serial = digest, serial
	return nil
}

func (m *holderModel) highest() int64 {
	var top int64
	for s := range m.installed {
		top = max(top, s)
	}
	return top
}

// TestHolderAgreesWithItsRules runs random sequences of installs over the
// pinned id and another, four serials, two contents per serial, and now and
// then a bundle whose signature is broken.
func TestHolderAgreesWithItsRules(t *testing.T) {
	t.Parallel()
	cache := map[string]*controlv1.PolicyBundle{}
	signed := func(id string, serial int64, content int) *controlv1.PolicyBundle {
		version := fmt.Sprintf("%d.%d", serial, content)
		if b, ok := cache[id+"/"+version]; ok {
			return b
		}
		b := signedDocument(id, version, serial)
		cache[id+"/"+version] = b
		return b
	}
	rapid.Check(t, func(rt *rapid.T) {
		h := openHolder(rt)
		m := holderModel{installed: map[int64]string{}}
		for range rapid.IntRange(1, 25).Draw(rt, "steps") {
			b := signed(rapid.SampledFrom([]string{"payments", "risk"}).Draw(rt, "id"),
				rapid.Int64Range(1, 4).Draw(rt, "serial"), rapid.IntRange(0, 1).Draw(rt, "content"))
			before := h.Current()
			if rapid.IntRange(0, 9).Draw(rt, "broken") == 0 {
				broken := proto.CloneOf(b)
				broken.Signature = flipFirstBit(broken.GetSignature())
				expectOnly(rt, h.InstallUnconfirmed(context.Background(), broken, pinned()), policy.ErrSignature)
				if h.Current() != before {
					rt.Fatalf("a bundle with a broken signature changed the holder")
				}
				continue
			}
			want := m.install(b.GetRef().GetBundleId(), serialOf(b), digestOf(b.GetCanonical()))
			compareStep(rt, h, &m, before, h.InstallUnconfirmed(context.Background(), b, pinned()), want)
		}
	})
}

// serialOf reads the serial the test wrote into the version, "serial.content".
func serialOf(b *controlv1.PolicyBundle) int64 {
	var serial, content int64
	if _, err := fmt.Sscanf(b.GetRef().GetVersion(), "%d.%d", &serial, &content); err != nil {
		return -1
	}
	return serial
}

func compareStep(t tb, h *policy.Holder, m *holderModel, before *policy.Snapshot, err, want error) {
	t.Helper()
	if want == nil && err != nil {
		t.Fatalf("InstallUnconfirmed refused what the rules take: %v", err)
	}
	if want != nil {
		expectOnly(t, err, want)
		if h.Current() != before {
			t.Fatalf("a refused install replaced the snapshot")
		}
	}
	cur := h.Current()
	if m.digest == "" {
		if cur != nil {
			t.Fatalf("the holder serves serial %d; the rules say nothing was installed", cur.Serial())
		}
		return
	}
	if cur == nil || cur.Serial() != m.serial || cur.Ref().GetDigest() != m.digest || !cur.ConfirmedAt().IsZero() {
		t.Fatalf("Current is %v; the rules say serial %d, digest %s, unconfirmed", cur, m.serial, m.digest)
	}
}

// TestPinnedHolderRefusesAnotherBundleID: a holder pinned to a bundle id takes
// no other id at any serial, first or later, and the refusal leaves the
// snapshot as it was.
func TestPinnedHolderRefusesAnotherBundleID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := openHolder(t)
	expectOnly(t, h.InstallUnconfirmed(ctx, signedDocument("risk", "v1", 1), pinned()), policy.ErrBundlePin)
	if h.Current() != nil {
		t.Fatal("a refused first install left a snapshot")
	}
	own := signedDocument("payments", "v7", 7)
	installUnconfirmed(t, h, own)
	before := h.Current()
	for _, serial := range []int64{1, 7, 8, 100} {
		expectOnly(t, h.InstallUnconfirmed(ctx, signedDocument("risk", "v", serial), pinned()), policy.ErrBundlePin)
		if h.Current() != before {
			t.Fatalf("a bundle of another id at serial %d replaced the snapshot", serial)
		}
	}
	expectConfirmed(t, h, 7, digestOf(own.GetCanonical()), "")
}
