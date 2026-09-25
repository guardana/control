package policy_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/internal/policy/reasons"
)

func matchInputs() match.Inputs { return match.Inputs{} }

func TestSnapshotReportsItsDocument(t *testing.T) {
	t.Parallel()
	snap := load(t, valid().build())
	want := &controlv1.PolicyBundleRef{BundleId: "payments", Version: "2026-09-10.1", Digest: exampleDigest}
	if !proto.Equal(snap.Ref(), want) {
		t.Errorf("Ref() = %v, want %v", snap.Ref(), want)
	}
	if got := snap.Serial(); got != 7 {
		t.Errorf("Serial() = %d, want 7", got)
	}
	if got := snap.MaxStale(); got != 5*time.Minute {
		t.Errorf("MaxStale() = %v, want 5m0s", got)
	}
	if got := snap.ConfirmedAt(); !got.Equal(loadTime()) {
		t.Errorf("ConfirmedAt() = %v, want %v", got, loadTime())
	}
}

// TestMaxStaleIsTheBudgetInSeconds runs up to the largest budget the format
// takes, the most whole seconds a time.Duration holds, where a conversion that
// wrapped would show.
func TestMaxStaleIsTheBudgetInSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		seconds int64
		want    time.Duration
	}{
		{1, time.Second},
		{900, 15 * time.Minute},
		{9223372036, time.Duration(9223372036000000000)},
	}
	for _, tc := range cases {
		if got := load(t, signedWithBudget("payments", "v1", 1, tc.seconds)).MaxStale(); got != tc.want {
			t.Errorf("maxStaleSeconds %d: MaxStale() = %v, want %v", tc.seconds, got, tc.want)
		}
	}
	_, err := policy.Load(signedWithBudget("payments", "v1", 1, 9223372037), pinned(), loadTime())
	expectOnly(t, err, policy.ErrDocument)
}

// TestConfirmedAtIsTheTimeLoadIsGiven: Load reads no clock, so whatever time
// it is handed is the confirmation time, to the nanosecond, however far it is
// from today.
func TestConfirmedAtIsTheTimeLoadIsGiven(t *testing.T) {
	t.Parallel()
	b := valid().build()
	for _, now := range []time.Time{
		{},
		time.Unix(0, 0).UTC(),
		loadTime(),
		loadTime().Add(time.Nanosecond),
		time.Date(2999, time.December, 31, 23, 59, 59, 999999999, time.UTC),
		time.Date(2026, time.September, 11, 14, 0, 0, 1, time.FixedZone("UTC+2", 2*60*60)),
	} {
		snap, err := policy.Load(b, pinned(), now)
		if err != nil {
			t.Fatalf("Load at %v: %v", now, err)
		}
		if got := snap.ConfirmedAt(); !got.Equal(now) {
			t.Errorf("loaded at %v, ConfirmedAt() = %v", now, got)
		}
	}
}

func TestRefIsAFreshCopy(t *testing.T) {
	t.Parallel()
	snap := load(t, valid().build())
	first := snap.Ref()
	first.BundleId, first.Version, first.Digest = "x", "y", "z"
	first.CreatedAt = timestamppb.New(loadTime())
	second := snap.Ref()
	if second == first {
		t.Fatal("Ref() returned the same message twice")
	}
	want := &controlv1.PolicyBundleRef{BundleId: "payments", Version: "2026-09-10.1", Digest: exampleDigest}
	if !proto.Equal(second, want) {
		t.Errorf("after the first copy was changed, Ref() = %v, want %v", second, want)
	}
}

// TestEvaluateDecidesWithTheSignedRules: each expected result is typed from
// the example document.
func TestEvaluateDecidesWithTheSignedRules(t *testing.T) {
	t.Parallel()
	snap := load(t, valid().build())
	expectResult(t, snap.Evaluate(refund(), matchInputs()), approvalForRefund())
	expectResult(t, snap.Evaluate(readCall(), matchInputs()), match.Result{
		Verdict:     controlv1.Verdict_VERDICT_DENY,
		Determinate: controlv1.Verdict_VERDICT_DENY,
		ReasonCodes: []string{"NO_MATCHING_RULE"},
	})
	expectResult(t, snap.Evaluate(nil, matchInputs()), match.Result{
		Verdict:       controlv1.Verdict_VERDICT_INDETERMINATE,
		Determinate:   controlv1.Verdict_VERDICT_DENY,
		Indeterminate: []string{"refunds-in-prod-need-approval"},
		ReasonCodes:   []string{"RULE_UNDETERMINED"},
	})
}

// TestASnapshotNobodyLoadedHoldsNoPolicy: a nil snapshot, and the zero value
// any package can write, report nothing and evaluate as a program nobody
// compiled does. This is also where unavailableCode is shown to be the code
// that path emits, so the next test's search for it cannot pass vacuously.
func TestASnapshotNobodyLoadedHoldsNoPolicy(t *testing.T) {
	t.Parallel()
	if _, ok := reasons.Lookup(unavailableCode); !ok {
		t.Fatalf("%s is not a registered reason code", unavailableCode)
	}
	for name, snap := range map[string]*policy.Snapshot{"nil": nil, "zero": {}} {
		if snap.Ref() != nil || !snap.ConfirmedAt().IsZero() || snap.MaxStale() != 0 || snap.Serial() != 0 {
			t.Errorf("%s: Ref %v, ConfirmedAt %v, MaxStale %v, Serial %d; want nothing",
				name, snap.Ref(), snap.ConfirmedAt(), snap.MaxStale(), snap.Serial())
		}
		expectResult(t, snap.Evaluate(refund(), matchInputs()), match.Result{
			Verdict:     controlv1.Verdict_VERDICT_INDETERMINATE,
			Determinate: controlv1.Verdict_VERDICT_DENY,
			ReasonCodes: []string{unavailableCode},
		})
	}
}

// TestEveryPathToASnapshotWrapsACompiledProgram takes a snapshot from each way
// the package makes one: Load, a first Install, a refresh and a replacement.
// Each decides with the signed rules, and none answers as a program nobody
// compiled, whatever the envelope.
func TestEveryPathToASnapshotWrapsACompiledProgram(t *testing.T) {
	t.Parallel()
	higher, err := policy.Sign([]byte(strings.Replace(exampleRaw, `"serial": 7`, `"serial": 8`, 1)), key(1), "k1")
	if err != nil {
		t.Fatal(err)
	}
	var h policy.Holder
	paths := map[string]*policy.Snapshot{"Load": load(t, valid().build())}
	install(t, &h, valid().build(), at(0))
	paths["the first Install"] = h.Current()
	install(t, &h, valid().build(), at(1))
	paths["a refresh"] = h.Current()
	install(t, &h, higher, at(2))
	paths["a replacement"] = h.Current()
	for name, snap := range paths {
		expectResult(t, snap.Evaluate(refund(), matchInputs()), approvalForRefund())
		for _, env := range []*controlv1.ActionEnvelope{nil, {}, readCall()} {
			if codes := snap.Evaluate(env, matchInputs()).ReasonCodes; slices.Contains(codes, unavailableCode) {
				t.Errorf("%s: a snapshot answered %q", name, codes)
			}
		}
	}
}
