package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/internal/spool"
)

// sessionUpstream is the fixture upstream with a session per client. With
// dropEnd, the request that ends a session, the DELETE a client sends as it
// closes, loses its connection instead of an answer, so the plane's close of
// its upstream fails.
func sessionUpstream(t *testing.T, dropEnd bool) string {
	t.Helper()
	h := upstreamOver(false, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dropEnd && r.Method == http.MethodDelete {
			if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// TestRunSaysWhetherThePlaneClosed: a run whose plane cannot be released, its
// upstream's session here, exits non-zero although it served and stopped as
// asked; the same run whose upstream lets go exits zero.
func TestRunSaysWhetherThePlaneClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dropEnd bool
		want    int
	}{{"released", false, 0}, {"not released", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, "upstreams.0.endpoint", sessionUpstream(t, tc.dropEnd))
			setEnv(t, "listener.address", "127.0.0.1:0")
			setEnv(t, "health.address", "")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stdout, stderr syncBuffer
			done := make(chan int, 1)
			go func() { done <- serve(ctx, tr.config, "", &stdout, &stderr) }()
			waitFor(t, &stdout, "listening for agents on ")
			cancel()
			if status := <-done; status != tc.want {
				t.Errorf("run exited %d, want %d; stderr:\n%s", status, tc.want, stderr.String())
			}
		})
	}
}

// TestDoctorSaysWhetherTheCheckedPlaneClosed: every check can pass and the
// plane they checked still fail to close; doctor then reports a failure.
func TestDoctorSaysWhetherTheCheckedPlaneClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dropEnd bool
		want    int
	}{{"released", false, 0}, {"not released", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, "upstreams.0.endpoint", sessionUpstream(t, tc.dropEnd))
			var stdout, stderr bytes.Buffer
			if status := doctor(context.Background(), tr.config, &stdout, &stderr); status != tc.want {
				t.Errorf("doctor exited %d, want %d:\n%s", status, tc.want, stdout.String())
			}
			if failed := strings.Contains(stdout.String(), "fail    close"); failed != tc.dropEnd {
				t.Errorf("a failed close reported: %t, want %t:\n%s", failed, tc.dropEnd, stdout.String())
			}
		})
	}
}

// heldUpstream holds a call for order ord-1 or ord-2 until that order is
// freed or the call's context ends, and says on entered that a call arrived.
type heldUpstream struct {
	url     string
	entered chan struct{}
	release map[string]chan struct{}
	once    map[string]*sync.Once
}

func newHeldUpstream(t *testing.T) *heldUpstream {
	t.Helper()
	up := &heldUpstream{entered: make(chan struct{}, 2), release: map[string]chan struct{}{}, once: map[string]*sync.Once{}}
	for _, id := range []string{"ord-1", "ord-2"} {
		up.release[id], up.once[id] = make(chan struct{}), &sync.Once{}
	}
	ts := httptest.NewServer(upstreamOver(true, func(ctx context.Context, req *sdk.CallToolRequest) {
		var args struct{ ID string }
		if json.Unmarshal(req.Params.Arguments, &args) != nil || up.release[args.ID] == nil {
			return
		}
		up.entered <- struct{}{}
		select {
		case <-up.release[args.ID]:
		case <-ctx.Done():
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { up.free("ord-1"); up.free("ord-2") })
	up.url = ts.URL
	return up
}

func (up *heldUpstream) free(id string) { up.once[id].Do(func() { close(up.release[id]) }) }

// arrived waits for n calls to reach the upstream.
func (up *heldUpstream) arrived(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-up.entered:
		case <-time.After(20 * time.Second):
			t.Fatal("a call never reached the upstream")
		}
	}
}

// runningPlane is the tree's plane serving in this process, with an agent
// connected to it.
type runningPlane struct {
	p     *plane
	stop  context.CancelFunc
	done  chan struct{}
	err   error
	agent *sdk.ClientSession
	url   string
	// released is set by a test that released the plane itself.
	released bool
}

// runPlane serves the tree's plane with grace for its listeners' shutdown and
// bound for the calls in flight at a stop.
func runPlane(t *testing.T, tr tree, grace, bound time.Duration) *runningPlane {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	p, err := startPlane(ctx, tr.load(t), io.Discard, "")
	if err != nil {
		stop()
		t.Fatalf("starting the plane: %v", err)
	}
	p.grace, p.callBound = grace, bound
	r := &runningPlane{p: p, stop: stop, done: make(chan struct{})}
	stdout := &syncBuffer{}
	go func() {
		defer close(r.done)
		r.err = p.run(ctx, stdout, listenTCP, 0)
	}()
	t.Cleanup(func() {
		stop()
		<-r.done
		if r.released {
			return
		}
		if err := p.close(); err != nil && !errors.Is(err, spool.ErrClosed) {
			t.Errorf("closing the plane: %v", err)
		}
	})
	const says = "listening for agents on "
	line := waitFor(t, stdout, says)
	addr := strings.TrimSpace(line[strings.Index(line, says)+len(says):])
	r.agent, r.url = connectAgent(t, addr), "http://"+addr
	return r
}

// call asks for order id in the background and hands back the answer.
func (r *runningPlane) call(id string) <-chan *sdk.CallToolResult {
	out := make(chan *sdk.CallToolResult, 1)
	go func() {
		res, err := r.agent.CallTool(context.Background(), &sdk.CallToolParams{Name: "read_order", Arguments: map[string]any{"id": id}})
		if err != nil {
			res = nil
		}
		out <- res
	}()
	return out
}

// stopped asks the plane to stop and returns what its run returned.
func (r *runningPlane) stopped(t *testing.T) error {
	t.Helper()
	r.stop()
	select {
	case <-r.done:
		return r.err
	case <-time.After(30 * time.Second):
		t.Fatal("the plane did not stop")
		return nil
	}
}

// TestAStopWaitsForTheCallInFlightToClose: two calls are still at their
// upstream when the plane is asked to stop, past the listeners' grace; the
// one that ends first does not end the wait, and both are answered and their
// trails closed before run returns, which is what lets the spool close after.
func TestAStopWaitsForTheCallInFlightToClose(t *testing.T) {
	tr := newTree(t)
	up := newHeldUpstream(t)
	setEnv(t, "upstreams.0.endpoint", up.url)
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, 50*time.Millisecond, time.Minute)
	slow, quick := r.call("ord-1"), r.call("ord-2")
	up.arrived(t, 2)
	time.AfterFunc(200*time.Millisecond, func() { up.free("ord-2") })
	time.AfterFunc(time.Second, func() { up.free("ord-1") })
	if err := r.stopped(t); err != nil {
		t.Errorf("a stop whose calls closed within their bound failed the run: %v", err)
	}
	stats, err := r.p.spool.Stats()
	if err != nil || stats.OpenTrails != 0 {
		t.Errorf("when run returned the spool held %d open trail(s), %v; want both calls' closed", stats.OpenTrails, err)
	}
	for _, answered := range []<-chan *sdk.CallToolResult{slow, quick} {
		if res := <-answered; res == nil || resultText(res) != upstreamAnswer {
			t.Errorf("a call in flight at the stop was answered %+v", res)
		}
	}
}

// TestACallOutlivingTheStopBoundFailsTheRun: a call still running when the
// stop's bound runs out is cut, and the run says so rather than returning as
// if every trail had closed.
func TestACallOutlivingTheStopBoundFailsTheRun(t *testing.T) {
	tr := newTree(t)
	up := newHeldUpstream(t)
	setEnv(t, "upstreams.0.endpoint", up.url)
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, 50*time.Millisecond, 300*time.Millisecond)
	r.call("ord-1")
	up.arrived(t, 1)
	if err := r.stopped(t); err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Errorf("a stop that gave up on a call returned %v, want the call named in flight", err)
	}
	up.free("ord-1")
}

// TestALostClosingRecordFailsTheRun: a call whose closing record the spool
// would not take leaves its trail open, and the run that served it does not
// end as a clean one.
func TestALostClosingRecordFailsTheRun(t *testing.T) {
	tr := newTree(t)
	up := newHeldUpstream(t)
	setEnv(t, "upstreams.0.endpoint", up.url)
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, time.Second, time.Minute)
	answered := r.call("ord-1")
	up.arrived(t, 1)
	if err := r.p.spool.Close(); err != nil {
		t.Fatalf("closing the spool under the call: %v", err)
	}
	up.free("ord-1")
	<-answered
	if err := r.stopped(t); err == nil || !strings.Contains(err.Error(), "closing record") {
		t.Errorf("a run that lost a closing record returned %v, want it named", err)
	}
}

// TestAStopWaitsAsLongAsACallMayTake: the bound is the call's own timeout,
// the decision point's when one is asked, and the grace to append the
// closing record.
func TestAStopWaitsAsLongAsACallMayTake(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstream.call_timeout", "7s")
	if got := tr.plane(t).callBound; got != 17*time.Second {
		t.Errorf("without a decision point a stop waits %v, want 17s", got)
	}
	veto := vetoTree(t, "http://127.0.0.1:1", "  timeout: 1500ms\n")
	setEnv(t, "upstream.call_timeout", "7s")
	if got := veto.plane(t).callBound; got != 18500*time.Millisecond {
		t.Errorf("with a decision point a stop waits %v, want 18.5s", got)
	}
}

// TestASessionStreamDoesNotHoldTheStop: on a stateful listener an agent holds
// a stream open for as long as it is connected; that stream carries no call,
// so a stop with no call in flight returns cleanly well within the bound.
func TestASessionStreamDoesNotHoldTheStop(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "listener.kind", "stateful_http")
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, 50*time.Millisecond, 300*time.Millisecond)
	if res := <-r.call("ord-1"); res == nil {
		t.Fatal("the call before the stop was not answered")
	}
	if err := r.stopped(t); err != nil {
		t.Errorf("a stop with only a session's stream open returned %v", err)
	}
}

// leaveCall sends a tools/call for order id on the agent's session and drops
// the connection once the call reached the upstream, the way an agent that
// gives up on an answer does.
func leaveCall(t *testing.T, r *runningPlane, up *heldUpstream, id string) {
	t.Helper()
	ctx, drop := context.WithCancel(context.Background())
	defer drop()
	body := `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"read_order","arguments":{"id":"` + id + `"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", r.agent.ID())
	req.Header.Set("Mcp-Protocol-Version", r.agent.InitializeResult().ProtocolVersion)
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	up.arrived(t, 1)
}

// TestACallItsAgentLeftStillHoldsTheStop: on a stateful listener a call runs
// on after its agent dropped the connection that carried it. A stop waits for
// that call's closing record rather than for the connection, so when run
// returns the trail is closed and nothing fails after the plane is released.
func TestACallItsAgentLeftStillHoldsTheStop(t *testing.T) {
	tr := newTree(t)
	up := newHeldUpstream(t)
	setEnv(t, "upstreams.0.endpoint", up.url)
	setEnv(t, "listener.kind", "stateful_http")
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, 50*time.Millisecond, time.Minute)
	leaveCall(t, r, up, "ord-1")
	time.Sleep(200 * time.Millisecond)
	time.AfterFunc(300*time.Millisecond, func() { up.free("ord-1") })
	if err := r.stopped(t); err != nil {
		t.Errorf("a stop whose call closed within its bound failed the run: %v", err)
	}
	stats, err := r.p.spool.Stats()
	if err != nil || stats.OpenTrails != 0 {
		t.Errorf("when run returned the spool held %d open trail(s), %v; want the left call's closed", stats.OpenTrails, err)
	}
	if n := r.p.adapter.Stats().CloseFailures; n != 0 {
		t.Errorf("%d closing record(s) failed, want none", n)
	}
}

// TestAClosingRecordLostAfterRunReturnedFailsTheCommand: a call cut at the
// stop goes on running, and its closing record fails once the spool is shut.
// The command reads the lost record after it released the plane and names it,
// although run returned before the record was lost.
func TestAClosingRecordLostAfterRunReturnedFailsTheCommand(t *testing.T) {
	tr := newTree(t)
	up := newHeldUpstream(t)
	setEnv(t, "upstreams.0.endpoint", up.url)
	setEnv(t, "listener.kind", "stateful_http")
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, 50*time.Millisecond, 300*time.Millisecond)
	r.call("ord-1")
	up.arrived(t, 1)
	ran := r.stopped(t)
	if ran == nil || strings.Contains(ran.Error(), "could not append") {
		t.Fatalf("the stop that cut the call returned %v, want the cut alone", ran)
	}
	if err := r.p.spool.Close(); err != nil {
		t.Fatalf("closing the spool under the cut call: %v", err)
	}
	up.free("ord-1")
	deadline := time.Now().Add(10 * time.Second)
	for r.p.adapter.InFlight() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	r.released = true
	var stderr syncBuffer
	if status := r.p.finish(ran, &stderr); status != exitFail {
		t.Errorf("the command exited %d, want %d", status, exitFail)
	}
	if !strings.Contains(stderr.String(), "1 call(s) could not append their closing record") {
		t.Errorf("the command did not name the closing record lost after run returned:\n%s", stderr.String())
	}
}
