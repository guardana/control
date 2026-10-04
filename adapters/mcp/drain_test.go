package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
)

// holdTool makes the victim's tool name wait until free is closed, and says
// on entered that a call reached it.
func holdTool(r *rig, name string) (entered chan struct{}, free chan struct{}) {
	entered, free = make(chan struct{}, 1), make(chan struct{})
	r.victim.mu.Lock()
	r.victim.replaced[name] = func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		entered <- struct{}{}
		select {
		case <-free:
		case <-ctx.Done():
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "held " + upstreamMark}}}, nil
	}
	r.victim.mu.Unlock()
	return entered, free
}

// TestACallIsInFlightUntilItsClosingRecord: a drain waits for a call until
// the pipeline has taken its closing record, not until the upstream answered,
// on every listener kind.
func TestACallIsInFlightUntilItsClosingRecord(t *testing.T) {
	forEachKind(t, rigOptions{}, func(t *testing.T, r *rig) {
		entered, free := holdTool(r, "read_file")
		cs := r.connect(t, "agent")
		answered := make(chan error, 1)
		go func() {
			_, err := callTool(t, cs, "read_file", map[string]any{"path": "/a"})
			answered <- err
		}()
		<-entered
		if n := r.adapter.InFlight(); n != 1 {
			t.Errorf("a call at its upstream counts %d in flight, want 1", n)
		}
		r.pipe.mu.Lock()
		close(free)
		time.Sleep(200 * time.Millisecond)
		left := r.adapter.Drain(100 * time.Millisecond)
		r.pipe.mu.Unlock()
		if left != 1 {
			t.Errorf("a drain while the closing record was not yet taken left %d in flight, want 1", left)
		}
		if left := r.adapter.Drain(10 * time.Second); left != 0 {
			t.Errorf("a drain once the record was taken left %d in flight, want 0", left)
		}
		if n := len(r.pipe.closed()); n != 1 {
			t.Errorf("the pipeline took %d closing record(s), want 1", n)
		}
		if err := <-answered; err != nil {
			t.Errorf("the call admitted before the drain was answered %v", err)
		}
	})
}

// TestADrainAdmitsNoFurtherCall: once a stop began, a call is refused before
// the pipeline is asked, so nothing is admitted that the stop would not wait
// for, and a drain with nothing in flight returns at once.
func TestADrainAdmitsNoFurtherCall(t *testing.T) {
	forEachKind(t, rigOptions{}, func(t *testing.T, r *rig) {
		cs := r.connect(t, "agent")
		start := time.Now()
		if left := r.adapter.Drain(time.Minute); left != 0 {
			t.Fatalf("a drain with no call left %d in flight", left)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("a drain with no call took %v", took)
		}
		_, err := callTool(t, cs, "read_file", map[string]any{"path": "/a"})
		if err == nil || !strings.Contains(err.Error(), string(mcp.ErrDraining)) {
			t.Errorf("a call after the drain began returned %v, want it refused as draining", err)
		}
		if n := len(r.pipe.admitted()); n != 0 {
			t.Errorf("the pipeline admitted %d call(s) after the drain began, want none", n)
		}
		if n := r.victim.count("read_file"); n != 0 {
			t.Errorf("the upstream ran %d call(s) after the drain began, want none", n)
		}
	})
}
