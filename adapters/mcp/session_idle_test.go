package mcp_test

import (
	"testing"
	"time"

	"github.com/guardana/control/adapters/mcp"
)

// TestAnIdleStatefulSessionEnds: a session left idle past the listener's bound
// is gone, so sessions a client opens and abandons do not pile up; one in use
// within the bound goes on.
func TestAnIdleStatefulSessionEnds(t *testing.T) {
	r := newRig(t, mcp.KindStatefulHTTP, rigOptions{sessionIdle: 300 * time.Millisecond})
	agent := r.connect(t, "agent-a")
	if _, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"}); err != nil {
		t.Fatalf("a call in a fresh session: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"}); err != nil {
		t.Fatalf("a call within the idle bound: %v", err)
	}
	time.Sleep(time.Second)
	if _, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"}); err == nil {
		t.Fatal("a session idle past its bound still answered")
	}
}
