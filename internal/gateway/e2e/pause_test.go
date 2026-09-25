package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/pause"
)

const (
	codePaused   = "PAUSED"
	pauseEvery   = 20 * time.Millisecond
	pauseEntryID = "stop-deletes"
)

// TestAnOperatorsPauseBitesAndLifts: against a serving plane, a pause written
// through the writer blocks the next call to its tool once the poller has
// read it, leaves another tool of the same upstream running, and its removal
// lets the call after through. Every trail validates.
func TestAnOperatorsPauseBitesAndLifts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pause")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pause.json")
	ctx := context.Background()
	if err := pause.Init(ctx, path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	poller, err := pause.Open(pause.Options{Path: path, Interval: pauseEvery, Clock: time.Now})
	if err != nil {
		t.Fatalf("pause.Open: %v", err)
	}
	running, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		poller.Run(running)
		close(done)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes}, pause: poller})
	agent := p.connect(t, "agent-a")
	allowed(t, agent, toolDelete, map[string]any{"path": "/a"})

	scope := pause.Scope{Kind: pause.ScopeAction, Action: pause.ActionTool, Provider: upstreamName, Name: toolDelete}
	if err := pause.Add(ctx, path, pause.Entry{ID: pauseEntryID, Scope: scope, CreatedAt: time.Now(), Reason: "deletes misbehave"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	eventually(t, func() bool { return poller.Current().State() == pause.Paused })
	blockedWith(t, call(t, agent, toolDelete, map[string]any{"path": "/b"}), codePaused)
	allowed(t, agent, toolRead, map[string]any{"path": "/c"})
	if got := p.victim.args(toolDelete); len(got) != 1 {
		t.Fatalf("the upstream ran the delete %d time(s), want once, before the pause", len(got))
	}

	if err := pause.Remove(ctx, path, pauseEntryID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	eventually(t, func() bool { return poller.Current().State() == pause.Clear })
	allowed(t, agent, toolDelete, map[string]any{"path": "/d"})
	if got := p.victim.args(toolDelete); len(got) != 2 {
		t.Errorf("after the lift the upstream ran the delete %d time(s) in all, want twice", len(got))
	}

	order, trails := p.trails()
	if len(order) != 4 {
		t.Fatalf("the plane recorded %d trails, want 4", len(order))
	}
	for _, id := range order {
		if err := evidence.ValidateChain(trails[id]); err != nil {
			t.Errorf("trail %s: %v", id, err)
		}
	}
	expectTrail(t, trails[order[1]], kindProposed, kindDecided, kindBlocked)
	if got := p.pipeline.Stats().Blocks[codePaused]; got != 1 {
		t.Errorf("Stats.Blocks[PAUSED] = %d, want 1", got)
	}
}
