package contract_test

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// The fixtures under testdata are the reference documents every later session
// reads, so these tests parse those files instead of building envelopes in Go.
const fixtureDir = "../../testdata/contracts/action_envelope"

// Rooted at the fixture directory so a test cannot start reading somewhere
// else, and so failures always name a fixture rather than a long path.
func fixtures() fs.FS { return os.DirFS(fixtureDir) }

func TestFixturesRoundTrip(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			envelope := loadEnvelope(t, name)
			if envelope.GetSchemaVersion() == "" {
				t.Errorf("%s: schema_version is empty", name)
			}

			wire, err := proto.Marshal(envelope)
			if err != nil {
				t.Fatalf("%s: proto.Marshal: %v", name, err)
			}
			decoded := &controlv1.ActionEnvelope{}
			if err := proto.Unmarshal(wire, decoded); err != nil {
				t.Fatalf("%s: proto.Unmarshal: %v", name, err)
			}
			if !proto.Equal(envelope, decoded) {
				t.Errorf("%s: binary round trip changed the message\nbefore: %v\nafter:  %v",
					name, envelope, decoded)
			}
		})
	}
}

// TestFixturesAreValidEnvelopes: each reference document passes the decoder a
// receiver calls, validation included, and not only the codec. A fixture the
// codec accepts and Validate refuses would otherwise pass every test.
func TestFixturesAreValidEnvelopes(t *testing.T) {
	for _, name := range fixtureNames(t) {
		data, err := fs.ReadFile(fixtures(), name)
		if err != nil {
			t.Fatalf("%s: read fixture: %v", name, err)
		}
		if _, err := contract.DecodeJSON(data); err != nil {
			t.Errorf("%s: DecodeJSON refuses it: %v", name, err)
		}
	}
}

// Covers the JSON path. The binary path does not reject on its own: it keeps an
// unknown field as unknown bytes, which pkg/contract's strict decode surfaces,
// and inside a map entry it discards them, which pkg/contract's pre-scan
// refuses before the parse. ADR-0002 states both.
func TestUnknownFieldIsRejected(t *testing.T) {
	const (
		control = `{"schemaVersion":"1.0"}`
		unknown = `{"schemaVersion":"1.0","bogusSecurityField":true}`
	)
	options := protojson.UnmarshalOptions{DiscardUnknown: false}

	// Checked first because the assertion below only looks for an error: a
	// renamed field, a malformed document or the wrong message type would all
	// produce one without the unknown field ever being reached.
	if err := options.Unmarshal([]byte(control), &controlv1.ActionEnvelope{}); err != nil {
		t.Fatalf("control document %s does not parse, so the case below proves nothing: %v",
			control, err)
	}

	err := options.Unmarshal([]byte(unknown), &controlv1.ActionEnvelope{})
	if err == nil {
		t.Fatalf("unmarshalling %s: got no error, want the unknown field rejected", unknown)
	}
	// Silently dropping a field the producer believed was security-relevant is
	// the exact failure this project exists to prevent, so the rejection has to
	// be about that field and not about something else in the document.
	if !strings.Contains(err.Error(), "bogusSecurityField") {
		t.Errorf("unmarshalling %s: rejected with %q, which does not name bogusSecurityField",
			unknown, err)
	}
}

// A fixture that stops representing its case is a false green: the later
// sessions that key off these files would still see three passing documents.
func TestFixturesCoverTheDeniableCases(t *testing.T) {
	crossTenant := loadEnvelope(t, "cross_tenant.json")
	principalTenant := crossTenant.GetPrincipal().GetTenantId()
	resourceTenant := crossTenant.GetResource().GetTenantId()
	switch {
	case principalTenant == "" || resourceTenant == "":
		t.Errorf("cross_tenant.json: principal tenant %q and resource tenant %q must both be set",
			principalTenant, resourceTenant)
	case principalTenant == resourceTenant:
		t.Errorf("cross_tenant.json: principal and resource share tenant %q, so the fixture no longer crosses a tenant boundary",
			principalTenant)
	}

	refund := loadEnvelope(t, "refund_prod.json")
	if got := refund.GetAction().GetEffect(); got != controlv1.EffectClass_EFFECT_CLASS_TRANSACT {
		t.Errorf("refund_prod.json: action effect is %s, want EFFECT_CLASS_TRANSACT", got)
	}
}

// minimal.json is the smallest envelope later sessions can build on, so it is
// pinned in both directions: gutting it and padding it both have to fail.
func TestMinimalFixtureStaysMinimal(t *testing.T) {
	envelope := loadEnvelope(t, "minimal.json")

	for field, value := range map[string]string{
		"request_id":          envelope.GetRequestId(),
		"project_id":          envelope.GetProjectId(),
		"tenant_id":           envelope.GetTenantId(),
		"principal.id":        envelope.GetPrincipal().GetId(),
		"principal.tenant_id": envelope.GetPrincipal().GetTenantId(),
		"action.name":         envelope.GetAction().GetName(),
		"resource.type":       envelope.GetResource().GetType(),
	} {
		if value == "" {
			t.Errorf("minimal.json: %s is empty, so the fixture is no longer a valid envelope", field)
		}
	}
	if envelope.GetOccurredAt() == nil {
		t.Errorf("minimal.json: occurred_at is absent, so the fixture is no longer a valid envelope")
	}
	if envelope.GetAction().GetEffect() == controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED {
		t.Errorf("minimal.json: action effect is unspecified, so no policy can decide on it")
	}

	// Everything richer than the fields above belongs in refund_prod.json.
	for field, present := range map[string]bool{
		"trace_id":    envelope.GetTraceId() != "",
		"span_id":     envelope.GetSpanId() != "",
		"environment": envelope.GetEnvironment() != "",
		"agent":       envelope.GetAgent() != nil,
		"delegation":  len(envelope.GetDelegation()) > 0,
		"destination": envelope.GetDestination() != nil,
		"data":        envelope.GetData() != nil,
		"arguments":   envelope.GetArguments() != nil,
		"context":     envelope.GetContext() != nil,
	} {
		if present {
			t.Errorf("minimal.json: %s is set, so the fixture is no longer the minimal case", field)
		}
	}
}

func fixtureNames(t *testing.T) []string {
	t.Helper()

	names, err := fs.Glob(fixtures(), "*.json")
	if err != nil {
		t.Fatalf("glob %s: %v", fixtureDir, err)
	}
	if len(names) < 3 {
		t.Fatalf("glob %s: found %d fixture(s), want at least 3", fixtureDir, len(names))
	}
	return names
}

func loadEnvelope(t *testing.T, name string) *controlv1.ActionEnvelope {
	t.Helper()

	data, err := fs.ReadFile(fixtures(), name)
	if err != nil {
		t.Fatalf("%s: read fixture: %v", name, err)
	}
	envelope := &controlv1.ActionEnvelope{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, envelope); err != nil {
		t.Fatalf("%s: protojson unmarshal: %v", name, err)
	}
	return envelope
}
