package core_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/pkg/contract"
)

// FuzzDecide hands Decide whatever a receiver could pass it: an envelope as
// bytes, arguments, a document, a flow, options and an age, seeded from the
// fixtures. Decide has no error return, so the only refusals it can make are
// decisions: it must not panic, must answer one of the five verdicts, must
// never enforce INDETERMINATE as anything but Block or a marked fail-open
// READ, and must answer the same twice, whatever external answer comes with
// it, named as answers() names it; any other name is none. Where the answer
// was needed, one that is no answer or a denial blocks and never allows. The
// envelope goes in both ways, as the strict decoder's result with its refusal
// beside it and as the decoded message alone, which makes Decide validate it
// itself.
func FuzzDecide(f *testing.F) {
	for _, c := range readFixtures(f) {
		f.Add(c.envelope, []byte(c.args), c.document, c.flow.UntrustedInfluence, int32(c.flow.MaxSensitivityRead),
			c.opts.FailOpenRead, int64(c.opts.MaxStale/time.Second), c.decidedAt.Sub(c.loadedAt).Nanoseconds(), true, c.answer.name)
	}
	f.Fuzz(func(t *testing.T, envelope, args, document []byte, untrusted bool, floor int32, failOpenRead bool, maxStaleSeconds, ageNanos int64, withBundle bool, answerName string) {
		opts := core.Options{
			Mode:         controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE,
			FailOpenRead: failOpenRead,
			MaxStale:     time.Duration(max(1, maxStaleSeconds%86_400)) * time.Second,
			Applicable:   []string{"cap_amount", "redact_fields"},
		}
		k, err := core.New(opts, fixedClock(base()), func() string { return "decision" })
		if err != nil {
			t.Fatalf("New refused options built as valid: %v", err)
		}
		snap := fuzzSnapshot(document, base().Add(-time.Duration(ageNanos)), withBundle)
		flow := contract.NewFlowState(untrusted, controlv1.Sensitivity(floor))
		env, refusal := contract.DecodeJSON(envelope)
		given := answers()[0]
		if i := slices.IndexFunc(answers(), func(a answer) bool { return a.name == answerName }); i >= 0 {
			given = answers()[i]
		}
		for _, req := range []core.Request{
			{Envelope: env, Refusal: refusal, AuthorizedArgs: args, Flow: flow, External: given.external},
			{Envelope: env, AuthorizedArgs: args, Flow: flow, External: given.external},
		} {
			first := k.Decide(context.Background(), req, snap)
			checkFuzzOutcome(t, req, opts, given, first)
			second := k.Decide(context.Background(), req, snap)
			if first.Action != second.Action || !proto.Equal(first.Decision, second.Decision) {
				t.Fatalf("decided differently: %v then %v", first, second)
			}
		}
	})
}

// fuzzSnapshot signs and loads document, or hands back no snapshot when the
// fuzzer asked for none or wrote a document the loader refuses: either way
// Decide has to answer.
func fuzzSnapshot(document []byte, loadedAt time.Time, withBundle bool) *policy.Snapshot {
	if !withBundle {
		return nil
	}
	b, err := policy.Sign(document, key(), "k1")
	if err != nil {
		return nil
	}
	snap, err := policy.Load(b, pinned(), loadedAt)
	if err != nil {
		return nil
	}
	return snap
}

// checkFuzzOutcome is what a decision on unknown input has to satisfy: it
// exists, it names a cause, it is enforced as ADR-0012 says, and an answer it
// needed that is no answer or a denial blocks it.
func checkFuzzOutcome(t *testing.T, req core.Request, opts core.Options, given answer, out core.Outcome) {
	t.Helper()
	if out.Decision == nil {
		t.Fatal("Decision is nil")
	}
	if len(out.Decision.GetReasonCodes()) == 0 {
		t.Fatalf("a decision with no reason code: %v", out.Decision)
	}
	checkEnforcement(t, opts.FailOpenRead, req.Envelope.GetAction().GetEffect() == effectRead, out)
	denial := given.external == core.ExternalDenied() || given.external == core.ExternalDeniedObligations()
	if out.NeedsExternal && (given.unanswered || denial) && (out.Action != core.Block || out.Decision.GetVerdict() == verdictAllow) {
		t.Fatalf("%s where an answer was needed, yet %s enforced as %d with codes %q",
			given.name, out.Decision.GetVerdict(), out.Action, out.Decision.GetReasonCodes())
	}
}
