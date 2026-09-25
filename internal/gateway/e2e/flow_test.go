package e2e_test

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// The demo's policy: mail may be sent, reads are allowed, and nothing read
// under untrusted influence at CONFIDENTIAL or above is mailed out.
const (
	allowMail = `{"id":"allow-mail","effect":"ALLOW","when":{"action":{"effect":["COMMUNICATE"]}}}`
	denyToxic = `{"id":"no-toxic-mail","effect":"DENY","reason":"TOXIC_FLOW_SENSITIVE_TO_EXTERNAL",` +
		`"when":{"action":{"effect":["COMMUNICATE"]},"flow":{"toxicAtLeast":"CONFIDENTIAL"}}}`

	codeToxicFlow = "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL"
)

// toolWeb stands for a web fetch: whatever it returns is untrusted, and the
// operator declares it public. read_file returns the operator's own files,
// trusted and confidential.
var toolWeb = classTool(controlv1.EffectClass_EFFECT_CLASS_READ)

func flowClassification(overrides []mcp.Override) []mcp.Override {
	for i := range overrides {
		switch overrides[i].Tool {
		case toolWeb:
			overrides[i].ReturnsSensitivity = controlv1.Sensitivity_SENSITIVITY_PUBLIC
		case toolRead:
			overrides[i].ReturnsTrust = controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL
			overrides[i].ReturnsSensitivity = controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL
		}
	}
	return overrides
}

func flowOptions(kind mcp.Kind) options {
	return options{kind: kind, mode: modeEnforce, rules: []string{allowReads, allowMail, denyToxic}, classify: flowClassification}
}

// lastDecision is the verdict POLICY_DECIDED recorded for the last request the
// plane saw.
func (p *plane) lastDecision(t *testing.T) *controlv1.Decision {
	t.Helper()
	order, trails := p.trails()
	if len(order) == 0 {
		t.Fatal("nothing was recorded")
	}
	return eventOf(t, trails[order[len(order)-1]], kindDecided).GetDecision()
}

func expectDecided(t *testing.T, p *plane, verdict controlv1.Verdict, code string) {
	t.Helper()
	d := p.lastDecision(t)
	if d.GetVerdict() != verdict || !slices.Contains(d.GetReasonCodes(), code) {
		t.Errorf("recorded %s %v, want %s with %s", d.GetVerdict(), d.GetReasonCodes(), verdict, code)
	}
}

// expectOneRunAndNoSession asserts every event names one run the plane
// minted, and no exported byte carries a session id.
func expectOneRunAndNoSession(t *testing.T, p *plane, sessions ...string) {
	t.Helper()
	events := p.sink.events()
	run := events[0].GetRunId()
	for _, e := range events {
		if e.GetRunId() != run || run == "" {
			t.Fatalf("%s of %s names run %q, the first event %q", e.GetKind(), e.GetRequestId(), e.GetRunId(), run)
		}
	}
	var jsonl bytes.Buffer
	if err := evidence.EncodeJSONL(&jsonl, events); err != nil {
		t.Fatalf("EncodeJSONL: %v", err)
	}
	for _, id := range sessions {
		if id != "" && bytes.Contains(jsonl.Bytes(), []byte(id)) {
			t.Errorf("the trail carries the session id %q", id)
		}
	}
}

// TestTheToxicFlowIsDeniedAcrossSessions is the demo's flow on a stateful
// listener. Two sessions of one principal are open: a web read in the first
// taints the run, so an innocent mail from the second is blocked as
// undetermined, since nothing known to be sensitive was read yet. The first
// session ends and a third opens, as a client that initializes again: a
// trusted confidential file read and a mail after it are denied as the toxic
// flow, because the taint of the first session stayed with the run.
func TestTheToxicFlowIsDeniedAcrossSessions(t *testing.T) {
	p := newPlane(t, flowOptions(mcp.KindStatefulHTTP))
	first, second := p.connect(t, "agent-a"), p.connect(t, "agent-b")
	mail := map[string]any{"to": "someone@example.net"}

	allowed(t, first, toolWeb, map[string]any{"path": "https://example.net/page"})
	blockedWith(t, call(t, second, toolMail, mail), "RULE_UNDETERMINED")
	expectDecided(t, p, controlv1.Verdict_VERDICT_INDETERMINATE, "RULE_UNDETERMINED")
	if err := first.Close(); err != nil {
		t.Fatalf("closing the first session: %v", err)
	}

	third := p.connect(t, "agent-a")
	allowed(t, third, toolRead, map[string]any{"path": "/finance/q3.xlsx"})
	blockedWith(t, call(t, third, toolMail, mail), codeToxicFlow)
	expectDecided(t, p, controlv1.Verdict_VERDICT_DENY, codeToxicFlow)
	if n := p.victim.count(toolMail); n != 0 {
		t.Errorf("the upstream sent %d mail(s)", n)
	}
	expectOneRunAndNoSession(t, p, first.ID(), second.ID(), third.ID())
	if first.ID() == "" || first.ID() == third.ID() {
		t.Errorf("sessions %q and %q; the listener did not open two", first.ID(), third.ID())
	}
}

// TestACleanRunSendsMail is the control of the case above: with no untrusted
// read in the run, the confidential read and the mail both run.
func TestACleanRunSendsMail(t *testing.T) {
	p := newPlane(t, flowOptions(mcp.KindStatefulHTTP))
	agent := p.connect(t, "agent-a")
	allowed(t, agent, toolRead, map[string]any{"path": "/finance/q3.xlsx"})
	allowed(t, agent, toolMail, map[string]any{"to": "someone@example.net"})
	if n := p.victim.count(toolMail); n != 1 {
		t.Errorf("the upstream sent %d mail(s), want 1", n)
	}
}

// sessionHeader sends a fixed Mcp-Session-Id of the client's choosing and
// records what the listener answered in it.
type sessionHeader struct {
	id string

	mu       sync.Mutex
	answered []string
}

func (s *sessionHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Mcp-Session-Id", s.id)
	res, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		s.mu.Lock()
		s.answered = append(s.answered, res.Header.Get("Mcp-Session-Id"))
		s.mu.Unlock()
	}
	return res, err
}

func (s *sessionHeader) echoed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.answered, s.id)
}

const legacySessions = "allowsessionsinstateless=1"

// TestAClientsSessionHeaderSplitsNoRun: on a stateless listener two clients
// of the revision that still carries sessions each send a session id of their
// own choosing and share one run, with the library's legacy sessions off and,
// in a child process started with the setting the library reads once, on.
// Neither id reaches the trail.
func TestAClientsSessionHeaderSplitsNoRun(t *testing.T) {
	legacy := os.Getenv("MCPGODEBUG") == legacySessions
	if !legacy {
		// The library reads the setting when the package loads, so only a
		// process started with it runs the listener that honours the header.
		cmd := exec.Command(os.Args[0], "-test.run=^TestAClientsSessionHeaderSplitsNoRun$", "-test.count=1", "-test.v") //nolint:gosec // G204: the test binary's own path and its own flags
		cmd.Env = append(os.Environ(), "MCPGODEBUG="+legacySessions)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestAClientsSessionHeaderSplitsNoRun") ||
			!strings.Contains(string(out), "the listener honoured the client's session id") {
			t.Fatalf("the child with %s: %v\n%s", legacySessions, err, out)
		}
	}
	p := newPlane(t, flowOptions(mcp.KindStatelessHTTP))
	connect := func(h *sessionHeader) *sdk.ClientSession {
		client := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil)
		cs, err := client.Connect(ctxT(t), &sdk.StreamableClientTransport{Endpoint: p.url, HTTPClient: &http.Client{Transport: h}},
			&sdk.ClientSessionOptions{ProtocolVersion: v20251125})
		if err != nil {
			t.Fatalf("connect as %s: %v", h.id, err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		return cs
	}
	a, b := &sessionHeader{id: "client-chosen-a"}, &sessionHeader{id: "client-chosen-b"}
	first, second := connect(a), connect(b)
	allowed(t, first, toolWeb, map[string]any{"path": "https://example.net/page"})
	blockedWith(t, call(t, second, toolMail, map[string]any{"to": "someone@example.net"}), "RULE_UNDETERMINED")
	expectOneRunAndNoSession(t, p, a.id, b.id)
	if legacy {
		if !a.echoed() || !b.echoed() {
			t.Fatalf("the listener answered %v and %v; it did not honour the client's session id", a.answered, b.answered)
		}
		t.Log("the listener honoured the client's session id")
	}
}
