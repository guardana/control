package authzen

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const askedPath = "/access/v1/evaluation"

// envelope is a call that names everything mapping version 1 reads, and
// something in every field it never sends.
func envelope() *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     "req-0001",
		TraceId:       "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanId:        "00f067aa0ba902b7",
		OccurredAt:    timestamppb.New(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)),
		ProjectId:     "proj-a",
		TenantId:      "tenant-a",
		Environment:   "prod",
		Principal: &controlv1.Principal{
			Id: "user-7", Type: "user", AuthnStrength: "mfa", TenantId: "tenant-a",
			Attributes: map[string]string{"team": "billing", "level": "3"},
		},
		Agent: &controlv1.Agent{
			Id: "agent-1", InstanceId: "inst-9", Framework: "langgraph", Version: "0.4.1", ModelRef: "model-x",
		},
		Delegation: []*controlv1.Delegation{{
			From: "user-7", To: "agent-1", Scopes: []string{"invoices.read"}, Reason: "month end",
			IssuedAt:  timestamppb.New(time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)),
			ExpiresAt: timestamppb.New(time.Date(2026, 3, 4, 6, 0, 0, 0, time.UTC)),
		}},
		Action: &controlv1.Action{
			Kind: "tool_call", Name: "invoices.export", Protocol: "mcp",
			Effect: controlv1.EffectClass_EFFECT_CLASS_COMMUNICATE, Provider: "billing-server",
		},
		Resource: &controlv1.Resource{
			Type: "invoice", Id: "inv-42", TenantId: "tenant-a", Environment: "prod",
			Labels: map[string]string{"region": "eu", "owner": "finance"},
		},
		Destination: &controlv1.Destination{
			TrustZone: controlv1.TrustZone_TRUST_ZONE_PARTNER, Host: "api.partner.example",
		},
		Data: &controlv1.DataLabels{
			Sensitivities:   []controlv1.Sensitivity{controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL, controlv1.Sensitivity_SENSITIVITY_INTERNAL},
			Sources:         []string{"crm"},
			ContainsSecrets: true,
		},
		Arguments: &controlv1.Arguments{
			CanonicalHash:    "sha256:" + "ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12",
			RedactedPreview:  `{"month":"03"}`,
			SchemaRef:        "schema-1",
			RedactionProfile: "default",
		},
		Context: &controlv1.RunContext{
			SessionId: "sess-1", RunId: "run-1", StepId: "step-1", Risk: "low",
			Budgets: map[string]int64{"calls": 10}, Tags: []string{"client:ide"},
		},
	}
}

// pdp is an in-process decision point: it records what it was asked and
// answers with the handler it holds.
type pdp struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	reqs  []*http.Request
	body  [][]byte
}

func (p *pdp) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, r.URL.Path)
	p.reqs = append(p.reqs, r)
	p.body = append(p.body, b)
}

func (p *pdp) hits() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.paths)
}

func (p *pdp) lastBody(t *testing.T) []byte {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.body) == 0 {
		t.Fatal("the decision point was never asked")
	}
	return p.body[len(p.body)-1]
}

func (p *pdp) lastRequest(t *testing.T) *http.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.reqs) == 0 {
		t.Fatal("the decision point was never asked")
	}
	return p.reqs[len(p.reqs)-1]
}

// newPDP starts a TLS decision point that records each request and then runs
// handle.
func newPDP(t *testing.T, handle http.HandlerFunc) *pdp {
	t.Helper()
	p := &pdp{}
	p.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.record(r)
		handle(w, r)
	}))
	t.Cleanup(p.Close)
	return p
}

// newPlainPDP is newPDP over plaintext on the loopback.
func newPlainPDP(t *testing.T, handle http.HandlerFunc) *pdp {
	t.Helper()
	p := &pdp{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.record(r)
		handle(w, r)
	}))
	t.Cleanup(p.Close)
	return p
}

// answer is a conformant reply carrying body: status 200, JSON, and the
// request identifier returned.
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		_, _ = io.WriteString(w, body)
	}
}

func options(p *pdp) Options {
	pool := x509.NewCertPool()
	if cert := p.Certificate(); cert != nil {
		pool.AddCert(cert)
	}
	return Options{Identifier: p.URL, Timeout: 5 * time.Second, InFlight: 4, RootCAs: pool}
}

func client(t *testing.T, opts Options) *Client {
	t.Helper()
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func within(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
