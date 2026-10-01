package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

const (
	testRunToken = "run-token-of-the-test"
	testRunID    = "run-00112233445566778899aabbccddeeff"
	testRunRoot  = "run-ffeeddccbbaa99887766554433221100"
)

// runNow is the adapter's clock in every run test, so the reading Resolve
// receives can be told from the wall clock's.
var runNow = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func runClock() time.Time { return runNow }

// servedRun is the run the fake resolves every token to unless told otherwise.
func servedRun() gateway.OpenedRun {
	return gateway.OpenedRun{
		ID: testRunID, Root: testRunRoot,
		Who:     gateway.RunIdentity{TenantID: "t1", PrincipalType: "service", PrincipalID: "svc-agent", AgentID: "agent-1"},
		Expires: time.Date(2030, 1, 2, 4, 0, 0, 0, time.UTC),
	}
}

// asked is one Resolve as the fake saw it.
type asked struct {
	token string
	who   gateway.RunIdentity
	now   time.Time
}

// fakeRuns resolves every token to served, unless answer, given the number
// of this Resolve counting from one, says otherwise.
type fakeRuns struct {
	mu     sync.Mutex
	answer func(n int) (gateway.OpenedRun, error)
	calls  []asked
}

func (f *fakeRuns) Resolve(_ context.Context, token string, who gateway.RunIdentity, now time.Time) (gateway.OpenedRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, asked{token: token, who: who, now: now})
	if f.answer != nil {
		return f.answer(len(f.calls))
	}
	return servedRun(), nil
}

func (f *fakeRuns) set(answer func(n int) (gateway.OpenedRun, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = answer
}

func (f *fakeRuns) asked() []asked {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]asked(nil), f.calls...)
}

// refuse answers every Resolve with err.
func refuse(err error) func(int) (gateway.OpenedRun, error) {
	return func(int) (gateway.OpenedRun, error) { return gateway.OpenedRun{}, err }
}

// serviceWho is who the unauthenticated test listener's runs are for.
var serviceWho = gateway.RunIdentity{TenantID: "t1", PrincipalType: "service", PrincipalID: "svc-agent", AgentID: "agent-1"}

// assertAskedAs checks every Resolve got the test's token, who and the
// adapter's clock, and that there was at least one.
func assertAskedAs(t *testing.T, f *fakeRuns, who gateway.RunIdentity) {
	t.Helper()
	got := f.asked()
	if len(got) == 0 {
		t.Fatal("the resolver was never asked")
	}
	for i, a := range got {
		if a.token != testRunToken || a.who != who || !a.now.Equal(runNow) {
			t.Errorf("Resolve %d got %+v, want %q for %+v at %v", i, a, testRunToken, who, runNow)
		}
	}
}

// assertNoRefusal checks neither counter counted anything.
func assertNoRefusal(t *testing.T, s mcp.Stats) {
	t.Helper()
	for cause, n := range s.RunsRefusedAtRequest {
		if n != 0 {
			t.Errorf("the listener refused %d tokens as %s", n, cause)
		}
	}
	for cause, n := range s.RunsRefusedAtMessage {
		if n != 0 {
			t.Errorf("a message refused %d tokens as %s", n, cause)
		}
	}
}

// assertCounted checks counter holds exactly want, every cause a key.
func assertCounted(t *testing.T, where string, counter mcp.RunRefusals, want map[gateway.RunCause]uint64) {
	t.Helper()
	all := []gateway.RunCause{"missing", "malformed", "unknown", "identity", "closed", "expired", "unreadable"}
	if len(counter) != len(all) {
		t.Errorf("%s counts %d causes, want the %d fixed ones: %v", where, len(counter), len(all), counter)
	}
	for _, cause := range all {
		if n, ok := counter[cause]; !ok || n != want[cause] {
			t.Errorf("%s counted %d (present %v) as %s, want %d", where, n, ok, cause, want[cause])
		}
	}
}

// TestRunTokenSourceFitsTheListener: the token comes from the header on
// HTTP and from RunToken on stdio, so New refuses a token without a
// resolver, a token on HTTP and a stdio resolver without a token; the
// listener presents runs exactly when it has a resolver.
func TestRunTokenSourceFitsTheListener(t *testing.T) {
	v := newVictim()
	ct, _ := sdk.NewInMemoryTransports()
	cases := []struct {
		name     string
		kind     mcp.Kind
		runs     bool
		token    string
		refused  bool
		presents bool
	}{
		{"stdio token without a resolver", mcp.KindStdio, false, testRunToken, true, false},
		{"stateless token without a resolver", mcp.KindStatelessHTTP, false, testRunToken, true, false},
		{"stateless token beside a resolver", mcp.KindStatelessHTTP, true, testRunToken, true, false},
		{"stateful token beside a resolver", mcp.KindStatefulHTTP, true, testRunToken, true, false},
		{"stdio resolver without a token", mcp.KindStdio, true, "", true, false},
		{"stdio resolver and token", mcp.KindStdio, true, testRunToken, false, true},
		{"stateless resolver", mcp.KindStatelessHTTP, true, "", false, true},
		{"stateful resolver", mcp.KindStatefulHTTP, true, "", false, true},
		{"stdio without runs", mcp.KindStdio, false, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := rigOptions{runToken: tc.token}
			if tc.runs {
				o.runs = &fakeRuns{}
			}
			a, err := mcp.New(newConfig(t, v, tc.kind, ct, o))
			if tc.refused {
				if a != nil || !errors.Is(err, mcp.ErrRunsSource) {
					t.Fatalf("New = %v, %v; want nil, ErrRunsSource", a, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New = %v, want an adapter", err)
			}
			if got := a.Capabilities().PresentsRuns; got != tc.presents {
				t.Errorf("PresentsRuns = %v, want %v", got, tc.presents)
			}
		})
	}
}

// TestRunTokenReachesTheAdmission: PresentsRuns. A token that resolves puts
// the run on the admission of every tools/call, resources/read and
// prompts/get, on every listener kind, resolved for the listener's identity
// at the adapter's clock.
func TestRunTokenReachesTheAdmission(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			runs := &fakeRuns{}
			o := rigOptions{runs: runs, clock: runClock, headers: http.Header{"Run-Token": {testRunToken}}}
			if k.kind == mcp.KindStdio {
				o.runToken, o.headers = testRunToken, nil
			}
			r := newRig(t, k.kind, o)
			agent := r.connect(t, "agent-a")
			for _, m := range stdioMethods()[:3] {
				if err := m.do(t, agent); err != nil {
					t.Fatalf("%s: %v", m.name, err)
				}
			}
			assertRunOnEach(t, r.pipe.admitted(), 3)
			assertAskedAs(t, runs, serviceWho)
			assertNoRefusal(t, r.adapter.Stats())
		})
	}
}

// assertRunOnEach checks there are want admissions and each carries the
// run the fake resolves to.
func assertRunOnEach(t *testing.T, adm []admitted, want int) {
	t.Helper()
	if len(adm) != want {
		t.Fatalf("admissions %d, want %d", len(adm), want)
	}
	for i, a := range adm {
		run := a.a.Run
		if run == nil || run.ID != testRunID || run.Root != testRunRoot || run.Who != serviceWho || !run.Expires.Equal(time.Date(2030, 1, 2, 4, 0, 0, 0, time.UTC)) {
			t.Errorf("admission %d carries the run %+v", i, run)
		}
	}
}

// TestStdioResolvesEveryMessage: on stdio the operator's token is resolved
// again at every message, whatever its method, the lists and a ping the
// library answers itself included; once it stops resolving, each is the one
// refusal, nothing is admitted and nothing reaches the upstream, and a new
// agent cannot even initialize.
func TestStdioResolvesEveryMessage(t *testing.T) {
	runs := &fakeRuns{}
	r := newRig(t, mcp.KindStdio, rigOptions{runs: runs, runToken: testRunToken, clock: runClock})
	agent := r.connect(t, "agent-a")
	methods := stdioMethods()
	for _, m := range methods {
		if err := m.do(t, agent); err != nil {
			t.Fatalf("%s while the run resolves: %v", m.name, err)
		}
	}
	assertRunOnEach(t, r.pipe.admitted(), 3)
	before := upstreamServed(r.victim)
	runs.set(refuse(&gateway.RunRefusal{Cause: gateway.RunExpired}))
	for _, m := range methods {
		assertRunRefused(t, m.name, m.do(t, agent))
	}
	if adm := r.pipe.admitted(); len(adm) != 3 {
		t.Errorf("admissions %d after the run stopped resolving, want still 3", len(adm))
	}
	if got := upstreamServed(r.victim); got != before {
		t.Errorf("the upstream served %d calls, %d before the run stopped resolving", got, before)
	}
	assertCounted(t, "message", r.adapter.Stats().RunsRefusedAtMessage, map[gateway.RunCause]uint64{"expired": uint64(len(methods))})
	assertCounted(t, "request", r.adapter.Stats().RunsRefusedAtRequest, nil)
	assertAskedAs(t, runs, serviceWho)
	answer := initializeStdio(t, r.adapter)
	if answer.Error == nil || answer.Error.Code != -31102 || answer.Error.Message != "run token refused" {
		t.Errorf("a new agent's initialize under a refused run: %+v", answer)
	}
	if n := r.adapter.Stats().RunsRefusedAtMessage["expired"]; n != uint64(len(methods))+1 {
		t.Errorf("the refused initialize left the count at %d, want %d", n, len(methods)+1)
	}
}

// initializeStdio sends one initialize to a over a new pipe, as the protocol
// frames it, and returns the one answer.
func initializeStdio(t *testing.T, a *mcp.Adapter) wireResponse {
	t.Helper()
	toServer, fromAgent := io.Pipe()
	toAgent, fromServer := io.Pipe()
	ctx, cancel := context.WithTimeout(ctxT(t), 5*time.Second)
	t.Cleanup(cancel)
	go func() { _ = a.ServeStdio(ctx, toServer, fromServer) }()
	t.Cleanup(func() { _ = fromAgent.Close(); _ = toAgent.Close() })
	line := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent-b","version":"0"}}}` + "\n"
	if _, err := io.WriteString(fromAgent, line); err != nil {
		t.Fatalf("writing initialize: %v", err)
	}
	got := make(chan []byte, 1)
	go func() {
		sc := bufio.NewScanner(toAgent)
		if sc.Scan() {
			got <- bytes.Clone(sc.Bytes())
		}
		close(got)
	}()
	select {
	case raw := <-got:
		return decodeWire(t, raw)
	case <-ctx.Done():
		t.Fatal("no answer to initialize")
	}
	return wireResponse{}
}

type stdioMethod struct {
	name string
	do   func(t *testing.T, cs *sdk.ClientSession) error
}

// stdioMethods are every method an agent sends that the adapter answers or
// passes to the library: three it admits, four lists and a ping.
func stdioMethods() []stdioMethod {
	return []stdioMethod{
		{"tools/call", func(t *testing.T, cs *sdk.ClientSession) error {
			res, err := callTool(t, cs, "read_file", map[string]any{"path": "/x"})
			if err == nil && res.IsError {
				return errors.New("tools/call answered isError")
			}
			return err
		}},
		{"resources/read", func(t *testing.T, cs *sdk.ClientSession) error {
			_, err := cs.ReadResource(ctxT(t), &sdk.ReadResourceParams{URI: "file:///r"})
			return err
		}},
		{"prompts/get", func(t *testing.T, cs *sdk.ClientSession) error {
			_, err := cs.GetPrompt(ctxT(t), &sdk.GetPromptParams{Name: "p"})
			return err
		}},
		{"tools/list", func(t *testing.T, cs *sdk.ClientSession) error {
			_, err := cs.ListTools(ctxT(t), nil)
			return err
		}},
		{"resources/list", func(t *testing.T, cs *sdk.ClientSession) error {
			_, err := cs.ListResources(ctxT(t), nil)
			return err
		}},
		{"resources/templates/list", func(t *testing.T, cs *sdk.ClientSession) error {
			_, err := cs.ListResourceTemplates(ctxT(t), nil)
			return err
		}},
		{"prompts/list", func(t *testing.T, cs *sdk.ClientSession) error {
			_, err := cs.ListPrompts(ctxT(t), nil)
			return err
		}},
		{"ping", func(t *testing.T, cs *sdk.ClientSession) error {
			return cs.Ping(ctxT(t), nil)
		}},
	}
}

// upstreamServed is every call, read and prompt the victim served.
func upstreamServed(v *victim) int {
	n := len(v.received())
	for _, name := range []string{"read_file", "delete_file", "transfer", "send_mail", "slow", "unlisted"} {
		n += v.count(name)
	}
	return n
}

// assertRunRefused checks err is the one refusal: the adapter's code and a
// message that names no cause.
func assertRunRefused(t *testing.T, what string, err error) {
	t.Helper()
	var werr *jsonrpc.Error
	if !errors.As(err, &werr) || werr.Code != -31102 {
		t.Errorf("%s under a refused run: %v, want code -31102", what, err)
		return
	}
	if werr.Message != "run token refused" || len(werr.Data) != 0 {
		t.Errorf("%s: the refusal says %q with data %s", what, werr.Message, werr.Data)
	}
	for _, cause := range []string{"missing", "malformed", "unknown", "identity", "closed", "expired", "unreadable"} {
		if strings.Contains(werr.Message, cause) {
			t.Errorf("%s: the refusal names the cause %s", what, cause)
		}
	}
}
