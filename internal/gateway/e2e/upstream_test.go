package e2e_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

	"github.com/guardana/control/adapters/mcp"
)

// TestUpstreamTimeoutIsRecordedAsOne: a call the upstream does not answer in
// time ends as a timeout, and neither the agent nor the record is told it
// succeeded.
func TestUpstreamTimeoutIsRecordedAsOne(t *testing.T) {
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce,
		rules: []string{allowReads}, callTimeout: 150 * time.Millisecond,
	})
	agent := p.connect(t, "agent-a")
	res, err := callTool(t, agent, toolSlow, map[string]any{"path": "/x"})
	if err == nil {
		t.Fatalf("a call the upstream never answered came back as %+v", res)
	}
	if n := p.victim.count(toolSlow); n != 1 {
		t.Fatalf("the upstream saw the call %d times", n)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindFailed)
	result := eventOf(t, trail, kindFailed).GetResult()
	if result.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_TIMEOUT {
		t.Errorf("the closing record says %v, want TIMEOUT", result.GetStatus())
	}
	if result.GetToolProtocolStatus() != "timeout" {
		t.Errorf("the closing record's protocol status is %q", result.GetToolProtocolStatus())
	}
	// The digest of what was sent is still the authorized one: the call ran, it
	// only did not answer.
	if result.GetExecutedActionDigest() != eventOf(t, trail, kindDecided).GetDecision().GetActionDigest() {
		t.Errorf("a timeout was recorded as a mismatch: %q", result.GetExecutedActionDigest())
	}
	if s := p.pipeline.Stats(); s.Halted || s.Mismatches != 0 {
		t.Errorf("a timeout halted the plane: %+v", s)
	}
}

// TestMalformedUpstreamAnswerIsNotASuccess: an upstream answer no client can
// read is recorded as a failure with the protocol's own word for it, and the
// agent gets an error rather than an empty success.
func TestMalformedUpstreamAnswerIsNotASuccess(t *testing.T) {
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads}})
	agent := p.connect(t, "agent-a")
	p.gate.garbage.Store(true)
	res, err := callTool(t, agent, toolRead, map[string]any{"path": "/x"})
	if err == nil {
		t.Fatalf("a malformed upstream answer came back as %+v", res)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindFailed)
	result := eventOf(t, trail, kindFailed).GetResult()
	switch result.GetStatus() {
	case controlv1.ResultStatus_RESULT_STATUS_FAILURE, controlv1.ResultStatus_RESULT_STATUS_UNKNOWN:
	default:
		t.Errorf("the closing record says %v for an answer nothing could read", result.GetStatus())
	}
	if result.GetToolProtocolStatus() == "ok" || result.GetToolProtocolStatus() == "" {
		t.Errorf("the closing record's protocol status is %q", result.GetToolProtocolStatus())
	}
	if result.GetResultHash() != "" {
		t.Errorf("the record hashes a result the gateway never received: %q", result.GetResultHash())
	}
}

// materialClasses are the effect classes that change something upstream, which
// a call is never sent twice for however it ended.
var materialClasses = []controlv1.EffectClass{
	controlv1.EffectClass_EFFECT_CLASS_WRITE,
	effectTransact,
}

// allowEffect is a rule that lets every call of class through.
func allowEffect(class controlv1.EffectClass) string {
	return `{"id":"allow-material","effect":"ALLOW","when":{"action":{"effect":["` + effectSpelling(class) + `"]}}}`
}

// slowAs classifies the slow tool as class, so a material call is one the
// upstream does not answer in time.
func slowAs(class controlv1.EffectClass) func([]mcp.Override) []mcp.Override {
	return func(overrides []mcp.Override) []mcp.Override {
		for i := range overrides {
			if overrides[i].Tool == toolSlow {
				overrides[i].Effect = class
			}
		}
		return overrides
	}
}

// TestAMaterialCallThatTimesOutReachesTheUpstreamOnce: a material call the
// upstream does not answer in time may have taken effect, so it is recorded
// as a timeout and never sent again.
func TestAMaterialCallThatTimesOutReachesTheUpstreamOnce(t *testing.T) {
	for _, class := range materialClasses {
		t.Run(effectSpelling(class), func(t *testing.T) {
			p := newPlane(t, options{
				kind: mcp.KindStatelessHTTP, mode: modeEnforce,
				rules: []string{allowEffect(class)}, callTimeout: 150 * time.Millisecond,
				classify: slowAs(class),
			})
			agent := p.connect(t, "agent-a")
			res, err := callTool(t, agent, toolSlow, map[string]any{"path": "/x"})
			if err == nil {
				t.Fatalf("a call the upstream never answered came back as %+v", res)
			}
			if n := p.victim.count(toolSlow); n != 1 {
				t.Fatalf("the upstream saw the call %d times", n)
			}
			trail := p.oneTrail(t)
			expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindFailed)
			result := eventOf(t, trail, kindFailed).GetResult()
			if result.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_TIMEOUT {
				t.Errorf("the closing record says %v, want TIMEOUT", result.GetStatus())
			}
			if result.GetToolProtocolStatus() != "timeout" {
				t.Errorf("the closing record's protocol status is %q", result.GetToolProtocolStatus())
			}
		})
	}
}

// garbleCalls lets every request reach the victim and answers a tools/call
// with bytes no client can read: the effect happened, and nobody on the
// gateway's side can say how it ended. Every other request is answered as the
// victim answers it, so a fresh session could still reach the upstream.
func garbleCalls(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !bytes.Contains(body, []byte(`"method":"tools/call"`)) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(httptest.NewRecorder(), r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":`))
	})
}

// TestAMaterialCallAnsweredUnreadablyReachesTheUpstreamOnce: the upstream ran
// the call and its answer cannot be read. The record does not say it
// succeeded, and the call is not sent again to find out. The client closes a
// session whose answer it cannot decode, so a retry on that session cannot
// reach the upstream: the count here stands against a retry on a fresh
// connection, which garbleCalls lets through, and the timeout test, whose
// session stays open, against a retry on the same session.
func TestAMaterialCallAnsweredUnreadablyReachesTheUpstreamOnce(t *testing.T) {
	for _, class := range materialClasses {
		t.Run(effectSpelling(class), func(t *testing.T) {
			p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowEffect(class)}})
			agent := p.connect(t, "agent-a")
			// Start read the manifest through the gate already; only the call
			// is answered unreadably.
			p.gate.wrap(garbleCalls)
			tool := classTool(class)
			res, err := callTool(t, agent, tool, map[string]any{"path": "/x"})
			if err == nil {
				t.Fatalf("an unreadable upstream answer came back as %+v", res)
			}
			if n := p.victim.count(tool); n != 1 {
				t.Fatalf("the upstream saw the call %d times", n)
			}
			trail := p.oneTrail(t)
			expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindFailed)
			result := eventOf(t, trail, kindFailed).GetResult()
			if result.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN {
				t.Errorf("the closing record says %v for an effect whose end nobody read, want UNKNOWN", result.GetStatus())
			}
			if result.GetResultHash() != "" {
				t.Errorf("the record hashes a result the gateway never received: %q", result.GetResultHash())
			}
		})
	}
}
