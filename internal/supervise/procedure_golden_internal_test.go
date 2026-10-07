package supervise

import (
	"os"
	"reflect"
	"testing"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestASchema01DocumentReadsAsItAlwaysHas pins what a 0.1 document reads as,
// member by member, and its digest, computed outside Go from the document's
// sorted, compact form. A 0.1 document states no children, bindings, binds
// or exceptions.
func TestASchema01DocumentReadsAsItAlwaysHas(t *testing.T) {
	raw, err := os.ReadFile("testdata/procedure-0.1.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ReadProcedure(raw)
	if err != nil {
		t.Fatalf("ReadProcedure: %v", err)
	}
	want := &Procedure{
		schema: "0.1", id: "invoice-close", version: "7",
		steps: []Step{
			{ID: "fetch", Tool: "get_invoice", Upstream: "billing", ObservedAs: []string{"get_invoice", "invoice.get"}, Required: true},
			{ID: "check", Tool: "validate_invoice", Upstream: "billing", ObservedAs: []string{}},
			{ID: "close", Tool: "close_invoice", Upstream: "ledger", ObservedAs: []string{"close_invoice"}, Required: true},
		},
		after: map[string][]string{"close": {"fetch", "check"}},
		allow: []Allowed{
			{Tool: "search", Upstream: "kb", ObservedAs: []string{"kb.search"}},
			{Tool: "get_invoice", Upstream: "archive", ObservedAs: []string{}},
		},
		maxDenials: 3,
		rules: map[string]RuleSpec{
			"REPEATED_DENIAL":         {controlv1.FindingSeverity_FINDING_SEVERITY_HIGH, findingv1alpha1.Escalation_ESCALATION_ALERT},
			"STEP_OUTSIDE_PROCEDURE":  {controlv1.FindingSeverity_FINDING_SEVERITY_CRITICAL, findingv1alpha1.Escalation_ESCALATION_ALERT},
			"DEADLINE_EXCEEDED":       {controlv1.FindingSeverity_FINDING_SEVERITY_INFO, findingv1alpha1.Escalation_ESCALATION_INFORM},
			"REQUIRED_STEP_SKIPPED":   {controlv1.FindingSeverity_FINDING_SEVERITY_MEDIUM, findingv1alpha1.Escalation_ESCALATION_INFORM},
			"STEP_OUT_OF_ORDER":       {controlv1.FindingSeverity_FINDING_SEVERITY_LOW, findingv1alpha1.Escalation_ESCALATION_INFORM},
			"CONTINUED_AFTER_FAILURE": {controlv1.FindingSeverity_FINDING_SEVERITY_HIGH, findingv1alpha1.Escalation_ESCALATION_ALERT},
		},
		digest: "2cbf192c20c231f1f3d112fc96e55dc9f97632e40e70852dfce9480e02e677a3",
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("read\n%+v\nwant\n%+v", p, want)
	}
}

func spec(sev controlv1.FindingSeverity, esc findingv1alpha1.Escalation) RuleSpec {
	return RuleSpec{Severity: sev, Escalation: esc}
}

// TestASchema02DocumentReadsEveryMember pins what the 0.2 fixture reads as,
// member by member, and its digest, computed outside Go as for 0.1.
func TestASchema02DocumentReadsEveryMember(t *testing.T) {
	raw, err := os.ReadFile("testdata/procedure-0.2.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ReadProcedure(raw)
	if err != nil {
		t.Fatalf("ReadProcedure: %v", err)
	}
	const (
		info, low, medium = controlv1.FindingSeverity_FINDING_SEVERITY_INFO, controlv1.FindingSeverity_FINDING_SEVERITY_LOW, controlv1.FindingSeverity_FINDING_SEVERITY_MEDIUM
		high, critical    = controlv1.FindingSeverity_FINDING_SEVERITY_HIGH, controlv1.FindingSeverity_FINDING_SEVERITY_CRITICAL
		inform, alert     = findingv1alpha1.Escalation_ESCALATION_INFORM, findingv1alpha1.Escalation_ESCALATION_ALERT
	)
	want := &Procedure{
		schema: "0.2", id: "refund", version: "2",
		bindings: []Binding{{"customer", "crm_customer"}, {"order", "shop_order"}},
		steps: []Step{
			{ID: "lookup", Tool: "get_order", Upstream: "shop", ObservedAs: []string{"get_order"}, Required: true},
			{ID: "refund", Tool: "issue_refund", Upstream: "pay", ObservedAs: []string{"issue_refund"}, Required: true},
			{ID: "notify", Tool: "send_mail", Upstream: "mail", ObservedAs: []string{}},
		},
		stepBinds: []string{"order", "order", "customer"},
		after:     map[string][]string{"refund": {"lookup"}, "notify": {"refund"}},
		allow: []Allowed{
			{Tool: "search_docs", Upstream: "docs", ObservedAs: []string{"search_docs"}},
			{Tool: "get_customer", Upstream: "crm", ObservedAs: []string{}},
		},
		allowBinds: []string{"", "customer"},
		exceptions: []Exception{
			{"manual_refund_approved", "STEP_OUTSIDE_PROCEDURE", "", "refund_manual", "pay", WhenApprovalGranted, ""},
			{"no_refund_after_failed_lookup", "REQUIRED_STEP_SKIPPED", "refund", "", "", WhenStepFailed, "lookup"},
			{"early_mail_approved", "STEP_OUT_OF_ORDER", "notify", "", "", WhenApprovalGranted, ""},
			{"lookup_denied", "CONTINUED_AFTER_FAILURE", "lookup", "", "", WhenReason, "RULE_DENY"},
		},
		children:        ChildrenInherit,
		maxDenials:      4,
		deadlineSeconds: 600,
		rules: map[string]RuleSpec{
			"REPEATED_DENIAL":                 spec(high, alert),
			"STEP_OUTSIDE_PROCEDURE":          spec(medium, alert),
			"DEADLINE_EXCEEDED":               spec(low, inform),
			"REQUIRED_STEP_SKIPPED":           spec(high, alert),
			"STEP_OUT_OF_ORDER":               spec(medium, inform),
			"CONTINUED_AFTER_FAILURE":         spec(critical, alert),
			"RESOURCE_OUTSIDE_RUN":            spec(critical, alert),
			"DENIED_ACTION_RETRIED_ARGUMENTS": spec(medium, inform),
			"DENIED_ACTION_RETRIED_RESOURCE":  spec(high, alert),
			"DENIED_ACTION_RETRIED_AROUND":    spec(low, inform),
			"EXCEPTION_TAKEN":                 spec(info, inform),
		},
		digest: "3bdccd18fb0503f6168a59a8bae712b9b37be7e4435f796b111be409f7d59dd6",
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("read\n%+v\nwant\n%+v", p, want)
	}
}
