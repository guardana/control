package supervise_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/supervise"
)

func v02Doc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/procedure-0.2.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// v02With is the 0.2 fixture with each pair of old and new text replaced once.
func v02With(t *testing.T, pairs ...string) string {
	t.Helper()
	doc := v02Doc(t)
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(doc, pairs[i]) {
			t.Fatalf("0.2 fixture holds no %q", pairs[i])
		}
		doc = strings.Replace(doc, pairs[i], pairs[i+1], 1)
	}
	return doc
}

func TestASchema02DocumentIsReadWithItsMembers(t *testing.T) {
	p, err := supervise.ReadProcedure([]byte(v02Doc(t)))
	if err != nil {
		t.Fatalf("ReadProcedure: %v", err)
	}
	if p.Schema() != "0.2" || p.ID() != "refund" || p.Version() != "2" || p.Children() != supervise.ChildrenInherit {
		t.Fatalf("read %q %q %q %v", p.Schema(), p.ID(), p.Version(), p.Children())
	}
	if p.Digest() != "3bdccd18fb0503f6168a59a8bae712b9b37be7e4435f796b111be409f7d59dd6" {
		t.Fatalf("digest %s", p.Digest())
	}
	wantSteps := []supervise.Step{
		{ID: "lookup", Tool: "get_order", Upstream: "shop", ObservedAs: []string{"get_order"}, Required: true},
		{ID: "refund", Tool: "issue_refund", Upstream: "pay", ObservedAs: []string{"issue_refund"}, Required: true},
		{ID: "notify", Tool: "send_mail", Upstream: "mail", ObservedAs: []string{}},
	}
	if !reflect.DeepEqual(p.Steps(), wantSteps) {
		t.Errorf("steps %+v", p.Steps())
	}
	wantBindings := []supervise.Binding{{Name: "customer", ResourceType: "crm_customer"}, {Name: "order", ResourceType: "shop_order"}}
	if !reflect.DeepEqual(p.Bindings(), wantBindings) {
		t.Errorf("bindings %+v", p.Bindings())
	}
	wantExceptions := []supervise.Exception{
		{ID: "manual_refund_approved", Waives: "STEP_OUTSIDE_PROCEDURE", Tool: "refund_manual", Upstream: "pay",
			When: supervise.WhenApprovalGranted},
		{ID: "no_refund_after_failed_lookup", Waives: "REQUIRED_STEP_SKIPPED", Step: "refund",
			When: supervise.WhenStepFailed, Subject: "lookup"},
		{ID: "early_mail_approved", Waives: "STEP_OUT_OF_ORDER", Step: "notify", When: supervise.WhenApprovalGranted},
		{ID: "lookup_denied", Waives: "CONTINUED_AFTER_FAILURE", Step: "lookup", When: supervise.WhenReason, Subject: "RULE_DENY"},
	}
	if !reflect.DeepEqual(p.Exceptions(), wantExceptions) {
		t.Errorf("exceptions %+v", p.Exceptions())
	}
}

func TestEachEntryBindsWhatItsBindsNames(t *testing.T) {
	p, err := supervise.ReadProcedure([]byte(v02Doc(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tool, upstream, binding string
		binds                   bool
	}{
		{"get_order", "shop", "order", true}, {"issue_refund", "pay", "order", true},
		{"send_mail", "mail", "customer", true}, {"get_customer", "crm", "customer", true},
		{"search_docs", "docs", "", false}, {"get_order", "pay", "", false}, {"refund_manual", "pay", "", false},
	} {
		if b, ok := p.BindingOf(c.tool, c.upstream); b != c.binding || ok != c.binds {
			t.Errorf("BindingOf(%s, %s) = %q, %v", c.tool, c.upstream, b, ok)
		}
	}
}

func TestTheAccessorsHandOutCopies(t *testing.T) {
	p, err := supervise.ReadProcedure([]byte(v02Doc(t)))
	if err != nil {
		t.Fatal(err)
	}
	p.Bindings()[0].Name = "changed"
	p.Exceptions()[0].ID = "changed"
	if p.Bindings()[0].Name != "customer" || p.Exceptions()[0].ID != "manual_refund_approved" {
		t.Fatal("an accessor hands out the procedure's own slice")
	}
}

func TestASchema02DocumentReadsTheSameFromItsCanonicalForm(t *testing.T) {
	doc := []byte(v02Doc(t))
	p, err := supervise.ReadProcedure(doc)
	if err != nil {
		t.Fatal(err)
	}
	form, err := canon.CanonicalizeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	again, err := supervise.ReadProcedure(form)
	if err != nil || !reflect.DeepEqual(*again, *p) {
		t.Fatalf("the canonical form reads otherwise: %v", err)
	}
}

func TestASchema01DocumentStatesNoneOfTheNewMembers(t *testing.T) {
	p, err := supervise.ReadProcedure([]byte(procJSON))
	if err != nil {
		t.Fatal(err)
	}
	if p.Schema() != "0.1" || p.Children() != supervise.ChildrenUnstated || len(p.Bindings()) != 0 || len(p.Exceptions()) != 0 {
		t.Fatalf("read %q %v %+v %+v", p.Schema(), p.Children(), p.Bindings(), p.Exceptions())
	}
	if _, ok := p.BindingOf("get_order", "shop"); ok {
		t.Fatal("a 0.1 step binds")
	}
}

func TestChildrenSeparateIsRead(t *testing.T) {
	p, err := supervise.ReadProcedure([]byte(v02With(t, `"children": "inherit"`, `"children": "separate"`)))
	if err != nil || p.Children() != supervise.ChildrenSeparate {
		t.Fatalf("ReadProcedure = %v, %v", p, err)
	}
}
