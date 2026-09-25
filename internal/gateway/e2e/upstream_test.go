package e2e_test

import (
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
