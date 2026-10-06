package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/core"
)

// TestReconcileMaxReachesThePipeline: with two lost holds in the journal, a
// pass bounded by approvals.reconcile_max of one closes one and says it did
// not read every entry, and a bound of two closes both.
func TestReconcileMaxReachesThePipeline(t *testing.T) {
	for _, c := range []struct {
		max      string
		closed   int
		complete bool
	}{{"1", 1, false}, {"2", 2, true}} {
		t.Run(c.max, func(t *testing.T) {
			tr := newTree(t)
			records, holds := tr.fileProvider(t)
			expires := time.Now().Add(10 * time.Minute)
			for _, id := range []string{"1", "2"} {
				env := readEnvelope()
				env.Resource.Id = "ord-lost-" + id
				lostHoldOf(t, records, holds, expires, lostCall{"apr-lost-" + id, "req-lost-" + id, "evt-lost-" + id, env})
			}
			setEnv(t, "approvals.reconcile_max", c.max)
			r := tr.plane(t).pipeline.Reconcile(context.Background())
			if r.Closed != c.closed || r.Complete != c.complete {
				t.Errorf("under approvals.reconcile_max %s the pass closed %d, complete %t; want %d, complete %t",
					c.max, r.Closed, r.Complete, c.closed, c.complete)
			}
		})
	}
}

// TestRetryAfterReachesThePipeline: a held call tells the agent to wait the
// approvals.retry_after the configuration names, a value no default has.
func TestRetryAfterReachesThePipeline(t *testing.T) {
	_, p := holdingPlane(t, provider{name: "memory", configure: func(t *testing.T, _ tree) {
		t.Helper()
		setEnv(t, "approvals.retry_after", "7s")
	}}, 15*time.Minute)
	held := admitTransfer(t, p, "req-retry-1")
	if held.Action != core.AwaitApproval || held.Pending == nil {
		t.Fatalf("the call is %v, want a hold: %v", held.Action, held.Decision.GetReasonCodes())
	}
	if held.Pending.RetryAfter != 7*time.Second {
		t.Errorf("the held call asks for a retry after %v, want the configured 7s", held.Pending.RetryAfter)
	}
}

// TestListTTLReachesTheAdapter: the tool list an agent reads carries the
// list.ttl the configuration names, a value no default has, in milliseconds.
func TestListTTLReachesTheAdapter(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "list.ttl", "7s")
	p := tr.plane(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.startUpstreams(ctx); err != nil {
		t.Fatalf("connecting the upstream: %v", err)
	}
	h, err := p.adapter.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	res, err := connectAgent(t, strings.TrimPrefix(ts.URL, "http://")).ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("listing the tools: %v", err)
	}
	if len(res.Tools) != 1 || res.TTLMs != 7000 {
		t.Errorf("the list holds %d tool(s) and a ttl of %dms, want one and the configured 7000ms", len(res.Tools), res.TTLMs)
	}
}
