package mcp_test

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

// TestACallItsAgentGaveUpOnIsAbortedUnsent: an execution handed out after the
// agent cancelled its call is not sent. The adapter aborts it, so the trail
// says nothing was sent rather than that the result is unknown, and the
// upstream receives nothing.
func TestACallItsAgentGaveUpOnIsAbortedUnsent(t *testing.T) {
	r := newRig(t, mcp.KindStdio, rigOptions{})
	admitting := make(chan struct{})
	r.pipe.onAdmit = func(ctx context.Context) {
		close(admitting)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			t.Error("the agent's cancel never reached the adapter's call")
		}
	}
	cs := r.connect(t, "agent-a")
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		_, _ = cs.CallTool(ctx, &sdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "/x"}})
	}()
	<-admitting
	cancel()
	<-answered
	waitFor(t, "the call to be closed or aborted", func() bool { return len(r.pipe.aborted())+len(r.pipe.closed()) > 0 })
	if n := r.victim.count("read_file"); n != 0 {
		t.Errorf("the upstream received %d call(s) its agent gave up on, want 0", n)
	}
	if closes := r.pipe.closed(); len(closes) != 0 {
		t.Errorf("the call was closed as sent, with %v", closes[0].result)
	}
	if aborts := r.pipe.aborted(); len(aborts) != 1 || aborts[0].cause != gateway.AbortCancelled {
		t.Errorf("aborts = %+v, want one with the cause of a cancelled call", aborts)
	}
}
