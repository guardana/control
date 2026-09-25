package policy_test

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
)

func TestHolderIsEmptyUntilAnInstallSucceeds(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	if h.Current() != nil {
		t.Fatal("a new holder has a snapshot")
	}
	broken := valid()
	broken.editSig = flipFirstBit
	expectOnly(t, h.Install(broken.build(), pinned(), at(0)), policy.ErrSignature)
	if h.Current() != nil {
		t.Fatal("a refused first install left a snapshot")
	}
	install(t, &h, valid().build(), at(1))
	expectCurrent(t, &h, 7, exampleDigest, at(1))
}

func TestInstallTakesAHigherSerial(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	install(t, &h, signedDocument("payments", "v7", 7), at(0))
	higher := signedDocument("payments", "v8", 8)
	install(t, &h, higher, at(1))
	expectCurrent(t, &h, 8, digestOf(higher.GetCanonical()), at(1))
}

func TestInstallRefusesALowerSerial(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	current := signedDocument("payments", "v7", 7)
	install(t, &h, current, at(0))
	before := h.Current()
	expectOnly(t, h.Install(signedDocument("payments", "v6", 6), pinned(), at(1)), policy.ErrRollback)
	if h.Current() != before {
		t.Fatal("the refused install replaced the snapshot")
	}
	expectCurrent(t, &h, 7, digestOf(current.GetCanonical()), at(0))
}

func TestInstallRefusesTheSameSerialWithOtherContent(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	current := signedDocument("payments", "v7", 7)
	install(t, &h, current, at(0))
	before := h.Current()
	expectOnly(t, h.Install(signedDocument("payments", "v7-other", 7), pinned(), at(1)), policy.ErrSerialReused)
	if h.Current() != before {
		t.Fatal("the refused install replaced the snapshot")
	}
	expectCurrent(t, &h, 7, digestOf(current.GetCanonical()), at(0))
}

// TestInstallRefreshesTheSameBundle: the same digest again is a new snapshot
// confirmed at the time Install is given, earlier or later, and the old one
// is left as it was.
func TestInstallRefreshesTheSameBundle(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	b := valid().build()
	install(t, &h, b, at(10))
	first := h.Current()
	install(t, &h, b, at(20))
	if h.Current() == first {
		t.Fatal("a refresh changed the snapshot in place of making a new one")
	}
	if !first.ConfirmedAt().Equal(at(10)) {
		t.Fatalf("the earlier snapshot now says it was confirmed at %v", first.ConfirmedAt())
	}
	expectCurrent(t, &h, 7, exampleDigest, at(20))

	byK2 := valid()
	byK2.signer, byK2.keyID = key(2), "k2"
	install(t, &h, byK2.build(), at(30))
	expectCurrent(t, &h, 7, exampleDigest, at(30))

	install(t, &h, b, at(5))
	expectCurrent(t, &h, 7, exampleDigest, at(5))
}

func TestInstallTakesAnotherBundleAtAnySerial(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	install(t, &h, signedDocument("payments", "v7", 7), at(0))
	other := signedDocument("risk", "v1", 1)
	install(t, &h, other, at(1))
	expectCurrent(t, &h, 1, digestOf(other.GetCanonical()), at(1))
	if got := h.Current().Ref().GetBundleId(); got != "risk" {
		t.Fatalf("bundle id %q, want risk", got)
	}
}

// TestRollbackProtectionHoldsPerBundleID: ADR-0012 says the protection holds
// per bundle id and per process, so a detour through another bundle id does
// not reset what was installed before under the first.
func TestRollbackProtectionHoldsPerBundleID(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	latest := signedDocument("payments", "v7", 7)
	install(t, &h, latest, at(0))
	other := signedDocument("risk", "v1", 1)
	install(t, &h, other, at(1))

	expectOnly(t, h.Install(signedDocument("payments", "v6", 6), pinned(), at(2)), policy.ErrRollback)
	expectOnly(t, h.Install(signedDocument("payments", "v7-other", 7), pinned(), at(3)), policy.ErrSerialReused)
	expectCurrent(t, &h, 1, digestOf(other.GetCanonical()), at(1))

	install(t, &h, latest, at(4))
	expectCurrent(t, &h, 7, digestOf(latest.GetCanonical()), at(4))
	higher := signedDocument("payments", "v8", 8)
	install(t, &h, higher, at(5))
	expectCurrent(t, &h, 8, digestOf(higher.GetCanonical()), at(5))
}

// TestAFailedInstallChangesNothing offers every change Load refuses to a
// holder whose snapshot is the unchanged example. Most carry the current
// digest in their ref, so a holder that refreshed on that claim before
// checking would take them.
func TestAFailedInstallChangesNothing(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	install(t, &h, valid().build(), at(0))
	before := h.Current()
	for _, c := range changes() {
		expectOnly(t, h.Install(c.edit(valid()).build(), pinned(), at(60)), c.want)
		if h.Current() != before {
			t.Fatalf("%s/%s: the refused install replaced the snapshot", c.field, c.name)
		}
	}
	expectOnly(t, h.Install(nil, pinned(), at(60)), policy.ErrNoBundle)
	if h.Current() != before || !before.ConfirmedAt().Equal(at(0)) {
		t.Fatal("a refused install changed the snapshot")
	}
}

// TestInstallRunsEveryLoadCheckFirst: a bundle that fails a check of Load is
// refused by that check, whatever its serial would have said.
func TestInstallRunsEveryLoadCheckFirst(t *testing.T) {
	t.Parallel()
	var h policy.Holder
	install(t, &h, signedDocument("payments", "v7", 7), at(0))

	lower := valid().withDoc(document("payments", "v6", 6, 300))
	lower.bundleID, lower.version, lower.editSig = "payments", "v6", flipFirstBit
	expectOnly(t, h.Install(lower.build(), pinned(), at(1)), policy.ErrSignature)

	reused := valid().withDoc(document("payments", "v7-other", 7, 300))
	reused.bundleID, reused.version, reused.keyID = "payments", "v7-other", "k3"
	expectOnly(t, h.Install(reused.build(), pinned(), at(2)), policy.ErrKey)
}

// holderModel is what the holder's rules say it holds, written from ADR-0012
// and the rules of Install: the current bundle's digest again is confirmed at
// the new time; for a bundle id installed before, a lower serial, or the same
// serial with other content, is refused; anything else replaces the current
// bundle.
type holderModel struct {
	digest    string // the current bundle's, "" before any
	serial    int64
	confirmed time.Time
	installed map[string]installed
}

type installed struct {
	serial int64
	digest string
}

func (m *holderModel) install(id string, serial int64, digest string, when time.Time) error {
	if m.digest != "" && digest == m.digest {
		m.confirmed = when
		return nil
	}
	if last, ok := m.installed[id]; ok {
		if serial < last.serial {
			return policy.ErrRollback
		}
		if serial == last.serial && digest != last.digest {
			return policy.ErrSerialReused
		}
	}
	m.installed[id] = installed{serial: serial, digest: digest}
	m.digest, m.serial, m.confirmed = digest, serial, when
	return nil
}

// TestHolderAgreesWithItsRules runs random sequences of installs over two
// bundle ids, four serials, two contents per serial, a clock that also steps
// back, and now and then a bundle whose signature is broken.
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
		var h policy.Holder
		m := holderModel{installed: map[string]installed{}}
		now := loadTime()
		for range rapid.IntRange(1, 25).Draw(rt, "steps") {
			b := signed(rapid.SampledFrom([]string{"payments", "risk"}).Draw(rt, "id"),
				rapid.Int64Range(1, 4).Draw(rt, "serial"), rapid.IntRange(0, 1).Draw(rt, "content"))
			now = now.Add(time.Duration(rapid.Int64Range(-10, 60).Draw(rt, "seconds")) * time.Second)
			before := h.Current()
			if rapid.IntRange(0, 9).Draw(rt, "broken") == 0 {
				broken := proto.CloneOf(b)
				broken.Signature = flipFirstBit(broken.GetSignature())
				expectOnly(rt, h.Install(broken, pinned(), now), policy.ErrSignature)
				if h.Current() != before {
					rt.Fatalf("a bundle with a broken signature changed the holder")
				}
				continue
			}
			want := m.install(b.GetRef().GetBundleId(), serialOf(b), digestOf(b.GetCanonical()), now)
			compareStep(rt, &h, &m, before, h.Install(b, pinned(), now), want)
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
		t.Fatalf("Install refused what the rules take: %v", err)
	}
	if want != nil {
		expectOnly(t, err, want)
		if h.Current() != before {
			t.Fatalf("a refused install replaced the snapshot")
		}
	}
	cur := h.Current()
	if cur == nil || cur.Serial() != m.serial || cur.Ref().GetDigest() != m.digest || !cur.ConfirmedAt().Equal(m.confirmed) {
		t.Fatalf("Current has serial %d, digest %s, confirmed %v; the rules say %d, %s, %v",
			cur.Serial(), cur.Ref().GetDigest(), cur.ConfirmedAt(), m.serial, m.digest, m.confirmed)
	}
}

// TestPinnedHolderRefusesAnotherBundleID: a holder pinned to a bundle id takes
// no other id at any serial, first or later, and the refusal leaves the
// snapshot as it was.
func TestPinnedHolderRefusesAnotherBundleID(t *testing.T) {
	t.Parallel()
	h := policy.NewHolder("payments")
	if h.Current() != nil {
		t.Fatal("a new pinned holder has a snapshot")
	}
	expectOnly(t, h.Install(signedDocument("risk", "v1", 1), pinned(), at(0)), policy.ErrBundlePin)
	if h.Current() != nil {
		t.Fatal("a refused first install left a snapshot")
	}
	own := signedDocument("payments", "v7", 7)
	install(t, h, own, at(1))
	before := h.Current()
	for _, serial := range []int64{1, 7, 8, 100} {
		expectOnly(t, h.Install(signedDocument("risk", "v", serial), pinned(), at(2)), policy.ErrBundlePin)
		if h.Current() != before {
			t.Fatalf("a bundle of another id at serial %d replaced the snapshot", serial)
		}
	}
	expectCurrent(t, h, 7, digestOf(own.GetCanonical()), at(1))
}

// TestPinnedHolderTakesItsOwnBundle: the pin changes nothing for the pinned
// id, so a higher serial still replaces and a lower one is still a rollback.
func TestPinnedHolderTakesItsOwnBundle(t *testing.T) {
	t.Parallel()
	h := policy.NewHolder("payments")
	install(t, h, signedDocument("payments", "v7", 7), at(0))
	higher := signedDocument("payments", "v8", 8)
	install(t, h, higher, at(1))
	expectCurrent(t, h, 8, digestOf(higher.GetCanonical()), at(1))
	expectOnly(t, h.Install(signedDocument("payments", "v7", 7), pinned(), at(2)), policy.ErrRollback)
	install(t, h, higher, at(3))
	expectCurrent(t, h, 8, digestOf(higher.GetCanonical()), at(3))
}

// TestAnEmptyPinPinsNothing: NewHolder("") is the zero Holder, which takes
// any bundle id.
func TestAnEmptyPinPinsNothing(t *testing.T) {
	t.Parallel()
	h := policy.NewHolder("")
	install(t, h, signedDocument("payments", "v7", 7), at(0))
	other := signedDocument("risk", "v1", 1)
	install(t, h, other, at(1))
	expectCurrent(t, h, 1, digestOf(other.GetCanonical()), at(1))
}
