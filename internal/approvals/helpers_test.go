// Helpers shared by this package's tests. Every fixture here builds an input;
// no expected value in any test is read back from one of these functions.
package approvals_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core/approval"
)

// firstApproval is the approval id every one-record fixture takes.
const firstApproval = "APPROVAL1"

// bundle is the policy bundle every fixture binds under.
const bundle approval.BundleDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// The clock every fixture is minted against, and the expiry the plane mints.
var (
	minted  = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	expires = time.Date(2026, time.March, 1, 12, 15, 0, 0, time.UTC)
)

// authorizedArgs is the argument document the digest is taken over.
var authorizedArgs = []byte(`{"amount_minor":1250}`)

// envelopeOf builds a held request's envelope. It carries both of the
// contract's free-text fields, so a test can prove the projection leaves them
// behind rather than assume it.
func envelopeOf(requestID string) *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     requestID,
		ProjectId:     "proj-1",
		TenantId:      "tenant-1",
		Environment:   "prod",
		Principal:     &controlv1.Principal{Id: "user-1", Type: "user", TenantId: "tenant-1"},
		Agent:         &controlv1.Agent{Id: "agent-1", Framework: "cli"},
		Delegation: []*controlv1.Delegation{{
			From:      "user-1",
			To:        "agent-1",
			Scopes:    []string{"refund"},
			Reason:    freeTextReason,
			IssuedAt:  timestamppb.New(minted),
			ExpiresAt: timestamppb.New(expires),
		}},
		Action: &controlv1.Action{
			Kind: "tool.call", Name: "refund", Protocol: "mcp",
			Effect: controlv1.EffectClass_EFFECT_CLASS_TRANSACT, Provider: "payments",
		},
		Resource:  &controlv1.Resource{Type: "payment", Id: "pay-9", TenantId: "tenant-1", Environment: "prod"},
		Arguments: &controlv1.Arguments{RedactedPreview: freeTextPreview, RedactionProfile: "default"},
	}
}

// The two free-text strings a projection may never carry. They are distinctive
// so that a test can search a file for them.
const (
	freeTextReason  = "the customer asked on the phone"
	freeTextPreview = "amount_minor=1250 card=****"
)

func decisionOf(requestID string) *controlv1.Decision {
	return &controlv1.Decision{
		SchemaVersion: "1.0",
		DecisionId:    "dec-" + requestID,
		RequestId:     requestID,
		PolicyRuleIds: []string{"rule-a", "rule-b"},
		Verdict:       controlv1.Verdict_VERDICT_REQUIRE_APPROVAL,
	}
}

// held builds the hold a plane would record for one request, with the digest
// and the binding the kernel computes for it.
func held(t testing.TB, approvalID, requestID string) approvals.Hold {
	t.Helper()
	env := envelopeOf(requestID)
	digest, binding, err := approval.Bind(env, authorizedArgs, bundle)
	if err != nil {
		t.Fatalf("binding the fixture: %v", err)
	}
	return approvals.Hold{
		Approval: &controlv1.Approval{
			SchemaVersion:      "1.0",
			ApprovalId:         approvalID,
			RequestId:          requestID,
			ActionDigest:       string(digest),
			PolicyBundleDigest: string(bundle),
			State:              controlv1.ApprovalState_APPROVAL_STATE_PENDING,
			RequestedAt:        timestamppb.New(minted),
			ExpiresAt:          timestamppb.New(expires),
		},
		Binding:  binding,
		Envelope: env,
		Decision: decisionOf(requestID),
	}
}

// bindingOf is the binding of one request's fixture.
func bindingOf(t testing.TB, requestID string) approval.Binding {
	t.Helper()
	_, binding, err := approval.Bind(envelopeOf(requestID), authorizedArgs, bundle)
	if err != nil {
		t.Fatalf("binding the fixture: %v", err)
	}
	return binding
}

// otherBinding is the binding of another action. Two requests for one action
// share a binding, because the canonical action digest leaves identifiers out;
// what makes another binding is another action, here another resource.
func otherBinding(t testing.TB) approval.Binding {
	t.Helper()
	env := envelopeOf("req-1")
	env.Resource.Id = "pay-8"
	_, binding, err := approval.Bind(env, authorizedArgs, bundle)
	if err != nil {
		t.Fatalf("binding the fixture: %v", err)
	}
	return binding
}

// openPlane opens a plane over a fresh directory and closes it with the test.
func openPlane(t *testing.T, opts ...approvals.Option) (*approvals.Plane, string) {
	t.Helper()
	dir := t.TempDir()
	p, err := approvals.OpenPlane(dir, opts...)
	if err != nil {
		t.Fatalf("opening the plane: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, dir
}

// openApprover opens an approver over dir and closes it with the test.
func openApprover(t *testing.T, dir string, opts ...approvals.Option) *approvals.Approver {
	t.Helper()
	a, err := approvals.OpenApprover(dir, opts...)
	if err != nil {
		t.Fatalf("opening the approver: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// recordNames lists the record files under dir, so a test can see which names
// a transition left behind.
func recordNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".rec" {
			names = append(names, e.Name())
		}
	}
	return names
}

// readFile reads one file under dir.
func readFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return raw
}

// writeFile replaces one file under dir, as a writer of the directory that is
// not this package would.
func writeFile(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}
