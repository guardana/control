package policycompare

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/rules"
	"github.com/guardana/control/pkg/contract"
)

// emptyArgumentsHash is the arguments hash of {}, the authorized arguments
// every evidence case holds, computed apart: SHA-256 over the domain tag and "{}".
const emptyArgumentsHash = "sha256:6fdd0e8a870307187078a787323b1237e1ebb5d9d1998f9a5a937001920da409"

func proposed(env *controlv1.ActionEnvelope) *controlv1.Event {
	return &controlv1.Event{Kind: controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, RequestId: env.GetRequestId(),
		Payload: &controlv1.Event_Proposed{Proposed: env}}
}

// recorded is a fresh decision of digest, loaded at t0 and made a minute later.
func recorded(req, digest string, v controlv1.Verdict, codes ...string) *controlv1.Decision {
	return &controlv1.Decision{RequestId: req, PolicyBundleDigest: digest, Verdict: v, ReasonCodes: codes,
		PolicyFreshness: controlv1.PolicyFreshness_POLICY_FRESHNESS_FRESH,
		PolicyLoadedAt:  timestamppb.New(t0), DecidedAt: timestamppb.New(t0.Add(time.Minute))}
}

func decidedEvent(d *controlv1.Decision) *controlv1.Event {
	return &controlv1.Event{Kind: controlv1.EventKind_EVENT_KIND_POLICY_DECIDED, RequestId: d.GetRequestId(),
		Payload: &controlv1.Event_Decision{Decision: d}}
}

func otherEvent(req string, kind controlv1.EventKind) *controlv1.Event {
	return &controlv1.Event{Kind: kind, RequestId: req}
}

func TestEvidenceCasesBeforeEvaluation(t *testing.T) {
	const digest = "sha256:" + "ab"
	allow := controlv1.Verdict_VERDICT_ALLOW
	stale := recorded("stale", digest, controlv1.Verdict_VERDICT_INDETERMINATE, "POLICY_STALE")
	stale.PolicyFreshness = controlv1.PolicyFreshness_POLICY_FRESHNESS_STALE
	staleCode := recorded("stale-code", digest, controlv1.Verdict_VERDICT_INDETERMINATE, "POLICY_STALE")
	noTime := recorded("no-time", digest, allow, "RULE_ALLOW")
	noTime.DecidedAt = nil
	events := []*controlv1.Event{
		{Kind: controlv1.EventKind_EVENT_KIND_POLICY_RELOADED},
		proposed(envelope("ok", "orders.read", read, "prod")), decidedEvent(recorded("ok", digest, allow, "RULE_ALLOW")),
		proposed(envelope("twice", "orders.read", read, "prod")), decidedEvent(recorded("twice", digest, allow, "RULE_ALLOW")),
		decidedEvent(recorded("twice", digest, allow, "RULE_ALLOW")),
		proposed(envelope("stale", "orders.read", read, "prod")), decidedEvent(stale),
		proposed(envelope("stale-code", "orders.read", read, "prod")), decidedEvent(staleCode),
		proposed(envelope("historic", "orders.read", read, "prod")), decidedEvent(recorded("historic", "sha256:cd", allow, "RULE_ALLOW")),
		proposed(envelope("no-decision", "orders.read", read, "prod")), otherEvent("no-decision", controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED),
		otherEvent("no-proposal", controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED),
		proposed(envelope("two-proposals", "orders.read", read, "prod")), proposed(envelope("two-proposals", "orders.read", read, "prod")),
		decidedEvent(recorded("two-proposals", digest, allow, "RULE_ALLOW")),
		proposed(envelope("no-time", "orders.read", read, "prod")), decidedEvent(noTime),
		otherEvent("finding-only", controlv1.EventKind_EVENT_KIND_FINDING_RAISED),
		otherEvent("empty", controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED), decidedEvent(recorded("empty", digest, allow, "RULE_ALLOW")),
	}
	want := []wantEvidence{
		{"request:ok", ""}, {"request:twice", ReasonSeveralDecisions}, {"request:stale", ReasonRecordedStale},
		{"request:stale-code", ReasonRecordedStale}, {"request:historic", ReasonHistoricPolicyMissing},
		{"request:no-decision", ReasonIncompleteRequest}, {"request:no-proposal", ReasonIncompleteRequest},
		{"request:two-proposals", ReasonIncompleteRequest}, {"request:no-time", ReasonIncompleteRequest},
		{"request:empty", ReasonIncompleteRequest},
	}
	checkEvidenceCases(t, "whole", EvidenceCases(Export{Events: events, Whole: true}, digest), want, "")
	checkEvidenceCases(t, "not whole", EvidenceCases(Export{Events: events, NotWhole: "it holds a gap"}, digest), want, ReasonExportNotWhole)
}

type wantEvidence struct {
	id     string
	reason Reason
}

// expected is the reason a case is unknown for; in an export that is not
// whole, a request missing its proposal or its decision is notWhole instead
// of incomplete.
func (w wantEvidence) expected(notWhole Reason) Reason {
	if w.reason == ReasonIncompleteRequest && notWhole != "" && w.id != "request:two-proposals" && w.id != "request:no-time" {
		return notWhole
	}
	return w.reason
}

func checkEvidenceCases(t *testing.T, name string, cases []Case, want []wantEvidence, notWhole Reason) {
	t.Helper()
	if len(cases) != len(want) {
		t.Fatalf("%s: %d cases, want %d: %+v", name, len(cases), len(want), cases)
	}
	for i, w := range want {
		c := cases[i]
		reason := w.expected(notWhole)
		if c.ID != w.id || c.Source != SourceEvidence || c.Unknown != reason {
			t.Errorf("%s: case %d is %s %s %q, want %s %q", name, i, c.ID, c.Source, c.Unknown, w.id, reason)
		}
		if c.Unknown != "" && (c.Detail == "" || c.Input.Envelope != nil) {
			t.Errorf("%s: %s unknown with detail %q and an input", name, c.ID, c.Detail)
		}
	}
}

// An evidence case holds {} for its arguments, with the hash of {}, no flow,
// no answer from a decision point, every obligation type applicable and the
// times the decision recorded.
func TestEvidenceCaseHoldsWhatTheEvidenceHolds(t *testing.T) {
	env := envelope("r1", "orders.read", read, "prod")
	env.Arguments = &controlv1.Arguments{CanonicalHash: "sha256:" + strings.Repeat("1", 64), RedactedPreview: `{"order":"x"}`}
	sent := proto.CloneOf(env)
	cases := EvidenceCases(Export{Whole: true, Events: []*controlv1.Event{proposed(env),
		decidedEvent(recorded("r1", "sha256:ab", controlv1.Verdict_VERDICT_ALLOW, "RULE_ALLOW"))}}, "sha256:ab")
	if len(cases) != 1 || cases[0].Unknown != "" {
		t.Fatalf("%+v", cases)
	}
	in := cases[0].Input
	if string(in.AuthorizedArgs) != "{}" || in.Envelope.GetArguments().GetCanonicalHash() != emptyArgumentsHash ||
		in.Envelope.GetArguments().GetRedactedPreview() != "" {
		t.Errorf("arguments %q %v", in.AuthorizedArgs, in.Envelope.GetArguments())
	}
	if !proto.Equal(env, sent) {
		t.Error("building the case changed the export's envelope")
	}
	if _, err := contract.ToxicFlow(in.Flow, env, controlv1.Sensitivity_SENSITIVITY_PUBLIC); err == nil {
		t.Error("an evidence case holds a flow the evidence never recorded")
	}
	if in.External != (Input{}).External || in.Refusal != nil {
		t.Errorf("external %v refusal %v", in.External, in.Refusal)
	}
	checkEvidenceTimesAndOptions(t, in)
}

func checkEvidenceTimesAndOptions(t *testing.T, in Input) {
	t.Helper()
	if !in.LoadedAt.Equal(t0) || !in.DecidedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("loaded %v decided %v", in.LoadedAt, in.DecidedAt)
	}
	var every []string
	for _, spec := range rules.Obligations() {
		every = append(every, spec.Type)
	}
	if !slices.Equal(in.Options.Applicable, every) || in.Options.FailOpenRead || in.Options.MaxStale < 100*365*24*time.Hour {
		t.Errorf("options %+v", in.Options)
	}
}

// Through Compare: the recorded decision is a cross-check, a rule reading
// what the evidence lacks, a side stale only because the case is old, and an
// obligation the plane may not apply are each unknown.
func TestEvidenceCasesThroughCompare(t *testing.T) {
	current := signed(t, currentDoc).Bundle.GetRef().GetDigest()
	allow, deny := controlv1.Verdict_VERDICT_ALLOW, controlv1.Verdict_VERDICT_DENY
	withArgs := envelope("args", "orders.read", read, "prod")
	withArgs.Arguments = &controlv1.Arguments{CanonicalHash: "sha256:" + strings.Repeat("1", 64)}
	late := recorded("late", current, allow, "RULE_ALLOW")
	late.DecidedAt = timestamppb.New(t0.Add(10 * time.Minute))
	events := []*controlv1.Event{
		proposed(envelope("agrees", "orders.read", read, "prod")), decidedEvent(recorded("agrees", current, allow, "RULE_ALLOW")),
		proposed(withArgs), decidedEvent(recorded("args", current, allow, "RULE_ALLOW")),
		proposed(envelope("write", "orders.update", write, "prod")), decidedEvent(recorded("write", current, deny, "NO_MATCHING_RULE")),
		proposed(envelope("disagrees", "orders.read", read, "prod")), decidedEvent(recorded("disagrees", current, deny, "RULE_DENY")),
		proposed(envelope("codes", "orders.read", read, "prod")), decidedEvent(recorded("codes", current, allow, "RULE_ALLOW", "PDP_ALLOW")),
		proposed(envelope("send", "send", send, "prod")), decidedEvent(recorded("send", current, allow, "RULE_ALLOW")),
		proposed(envelope("pay", "pay", pay, "prod")), decidedEvent(recorded("pay", current, allow, "RULE_ALLOW", "PDP_ALLOW")),
		proposed(envelope("late", "orders.read", read, "prod")), decidedEvent(late),
		proposed(envelope("wire", "wire", pay, "prod")), decidedEvent(recorded("wire", current, controlv1.Verdict_VERDICT_REQUIRE_APPROVAL, "APPROVAL_REQUIRED")),
	}
	r := compareOrFail(t, EvidenceCases(Export{Whole: true, Events: events}, current), nil)
	for id, want := range map[string]struct {
		change Change
		reason Reason
	}{
		"request:agrees":    {Unchanged, ""},
		"request:args":      {Unchanged, ""},
		"request:write":     {Looser, ""},
		"request:disagrees": {Unknown, ReasonDisagreesWithRecord},
		"request:codes":     {Unknown, ReasonDisagreesWithRecord},
		"request:send":      {Unknown, ReasonMissingFacts},
		"request:pay":       {Unknown, ReasonExternalAnswerNeeded},
		"request:late":      {Unknown, ReasonFreshnessNotHeld},
		"request:wire":      {Unknown, ReasonObligationApplicability},
	} {
		row := rowOf(t, r, id)
		if row.Change != want.change || reasonOf(row) != want.reason || row.Source != SourceEvidence {
			t.Errorf("%s: %s %q, want %s %q (%v)", id, row.Change, reasonOf(row), want.change, want.reason, row.UnknownDetail)
		}
	}
	if got := rowOf(t, r, "request:send").Before.MissingFacts; !slices.Equal(got, []string{"flow"}) {
		t.Errorf("send's missing facts %q, want flow", got)
	}
	if d := rowOf(t, r, "request:disagrees").UnknownDetail; d == nil || !strings.Contains(*d, "DENY") || !strings.Contains(*d, "ALLOW") {
		t.Errorf("disagreement detail %v", d)
	}
}
