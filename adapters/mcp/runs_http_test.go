package mcp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

// wantRefusedBody is the one body a refused run token gets on HTTP.
const wantRefusedBody = "run token refused\n"

// send makes one raw HTTP request to the listener and returns the status,
// the response header and the body, or the last event's data for a stream.
func send(t *testing.T, verb, url, version string, headers http.Header, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctxT(t), verb, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	switch verb {
	case http.MethodPost:
		var probe struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
				URI  string `json:"uri"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(body), &probe); err != nil {
			t.Fatalf("the body is not JSON: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Method", probe.Method)
		if name := probe.Params.Name + probe.Params.URI; name != "" {
			req.Header.Set("Mcp-Name", name)
		}
	case http.MethodGet:
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("Mcp-Protocol-Version", version)
	for k, vs := range headers {
		req.Header[k] = vs
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp.StatusCode, resp.Header, raw
	}
	var last []byte
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			last = []byte(data)
		}
	}
	return resp.StatusCode, resp.Header, last
}

func withToken(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		out = http.Header{}
	}
	out.Set("Run-Token", testRunToken)
	return out
}

// TestRefusedRunTokenIsA401BeforeTheLibrary: on both HTTP listeners and on
// every verb, a token that is missing, doubled or refused for any cause is
// a 401 with the challenge and one body, counted by its cause at the
// listener; the resolver is asked once at most, so no message was read, and
// nothing is admitted or sent upstream.
func TestRefusedRunTokenIsA401BeforeTheLibrary(t *testing.T) {
	one := []string{testRunToken}
	cases := []struct {
		name     string
		token    []string
		answer   func(int) (gateway.OpenedRun, error)
		want     gateway.RunCause
		resolves int
	}{
		{"no token", nil, nil, "missing", 0},
		{"an empty token", []string{""}, nil, "missing", 0},
		{"two tokens", []string{testRunToken, testRunToken}, nil, "malformed", 0},
		{"malformed", one, refuse(&gateway.RunRefusal{Cause: gateway.RunMalformed}), "malformed", 1},
		{"unknown", one, refuse(&gateway.RunRefusal{Cause: gateway.RunUnknown}), "unknown", 1},
		{"another identity", one, refuse(&gateway.RunRefusal{Cause: gateway.RunOtherIdentity}), "identity", 1},
		{"closed", one, refuse(&gateway.RunRefusal{Cause: gateway.RunClosed}), "closed", 1},
		{"expired", one, refuse(&gateway.RunRefusal{Cause: gateway.RunExpired}), "expired", 1},
		{"unreadable", one, refuse(&gateway.RunRefusal{Cause: gateway.RunUnreadable}), "unreadable", 1},
		{"a wrapped refusal", one, refuse(fmt.Errorf("resolve: %w", &gateway.RunRefusal{Cause: gateway.RunClosed})), "closed", 1},
		{"an error that is no refusal", one, refuse(errors.New("the directory is gone")), "unreadable", 1},
		{"a refusal naming no known cause", one, refuse(&gateway.RunRefusal{Cause: "elsewhere"}), "unreadable", 1},
		{"a refusal naming no cause", one, refuse(&gateway.RunRefusal{}), "unreadable", 1},
		{"a run without an id", one, func(int) (gateway.OpenedRun, error) { return gateway.OpenedRun{Root: testRunRoot}, nil }, "unreadable", 1},
	}
	verbs := []string{http.MethodPost, http.MethodGet, http.MethodDelete}
	for _, k := range kinds[:2] {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				runs := &fakeRuns{answer: tc.answer}
				r := newRig(t, k.kind, rigOptions{runs: runs, clock: runClock})
				headers := http.Header{}
				if tc.token != nil {
					headers["Run-Token"] = tc.token
				}
				for i, verb := range verbs {
					status, h, body := send(t, verb, r.url, r.version, headers, callBody(1, "read_file", map[string]any{"path": "/x"}))
					if status != http.StatusUnauthorized || h.Get("WWW-Authenticate") != "Run-Token" || string(body) != wantRefusedBody {
						t.Fatalf("%s: HTTP %d, challenge %q, body %q", verb, status, h.Get("WWW-Authenticate"), body)
					}
					assertCounted(t, "request", r.adapter.Stats().RunsRefusedAtRequest, map[gateway.RunCause]uint64{tc.want: uint64(i + 1)})
					if n := len(runs.asked()); n != tc.resolves*(i+1) {
						t.Errorf("%s: the resolver was asked %d times in all, want %d", verb, n, tc.resolves*(i+1))
					}
				}
				assertCounted(t, "message", r.adapter.Stats().RunsRefusedAtMessage, nil)
				if n := len(r.pipe.admitted()); n != 0 {
					t.Errorf("%d admissions behind a refused run token", n)
				}
				if n := upstreamServed(r.victim); n != 0 {
					t.Errorf("the upstream served %d calls behind a refused run token", n)
				}
			})
		}
	}
}

// TestRunRefusedAtTheMessageAfterTheRequest: a token that resolved for the
// request and no longer resolves for the message is the JSON-RPC refusal,
// on every method, with nothing admitted and nothing sent upstream.
func TestRunRefusedAtTheMessageAfterTheRequest(t *testing.T) {
	runs := &fakeRuns{}
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{runs: runs, clock: runClock})
	runs.set(func(n int) (gateway.OpenedRun, error) {
		if n%2 == 1 {
			return servedRun(), nil
		}
		return gateway.OpenedRun{}, &gateway.RunRefusal{Cause: gateway.RunClosed}
	})
	bodies := []string{
		callBody(1, "read_file", map[string]any{"path": "/x"}),
		rawRequest(t, 2, "resources/read", map[string]any{"uri": "file:///r", "_meta": meta()}),
		rawRequest(t, 3, "prompts/get", map[string]any{"name": "p", "_meta": meta()}),
		listBody(4),
		rawRequest(t, 5, "prompts/list", map[string]any{"_meta": meta()}),
	}
	for _, body := range bodies {
		status, _, raw := send(t, http.MethodPost, r.url, r.version, withToken(nil), body)
		w := decodeWire(t, raw)
		if status != http.StatusOK || w.Error == nil || w.Error.Code != -31102 || w.Error.Message != "run token refused" {
			t.Fatalf("%s: HTTP %d %s", body, status, raw)
		}
	}
	if n := len(r.pipe.admitted()); n != 0 {
		t.Errorf("%d admissions for a run closed after the request", n)
	}
	if n := upstreamServed(r.victim); n != 0 {
		t.Errorf("the upstream served %d calls for a run closed after the request", n)
	}
	assertCounted(t, "message", r.adapter.Stats().RunsRefusedAtMessage, map[gateway.RunCause]uint64{"closed": uint64(len(bodies))})
	assertCounted(t, "request", r.adapter.Stats().RunsRefusedAtRequest, nil)
	if n := len(runs.asked()); n != 2*len(bodies) {
		t.Errorf("the resolver was asked %d times, want twice per request", n)
	}
	assertAskedAs(t, runs, serviceWho)
}

// rawRequest is one JSON-RPC request body.
func rawRequest(t *testing.T, id int, method string, params map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatalf("marshalling the body: %v", err)
	}
	return string(b)
}

// TestStatefulSessionNeedsTheRunOnEveryRequest: a session opened under a
// run is no standing grant. A GET or a DELETE on it without the token is
// refused and leaves the session as it was; a call whose run is refused at
// the message after the request is the JSON-RPC refusal.
func TestStatefulSessionNeedsTheRunOnEveryRequest(t *testing.T) {
	runs := &fakeRuns{}
	r := newRig(t, mcp.KindStatefulHTTP, rigOptions{runs: runs, clock: runClock})
	session := http.Header{"Mcp-Session-Id": {initialize(t, r.url, r.version)}}
	for _, verb := range []string{http.MethodGet, http.MethodDelete} {
		if status, _, body := send(t, verb, r.url, r.version, session, ""); status != http.StatusUnauthorized || string(body) != wantRefusedBody {
			t.Fatalf("%s on the session without a token: HTTP %d %q", verb, status, body)
		}
	}
	call := rawRequest(t, 7, "tools/call", map[string]any{"name": "read_file", "arguments": map[string]any{"path": "/x"}})
	status, _, raw := send(t, http.MethodPost, r.url, r.version, withToken(session), call)
	if w := decodeWire(t, raw); status != http.StatusOK || w.Error != nil || !bytes.Contains(w.Result, []byte(upstreamMark)) {
		t.Fatalf("a call on the session the refused DELETE left: HTTP %d %s", status, raw)
	}
	assertRunOnEach(t, r.pipe.admitted(), 1)
	closeAfterTheRequest(runs)
	status, _, raw = send(t, http.MethodPost, r.url, r.version, withToken(session), call)
	if w := decodeWire(t, raw); status != http.StatusOK || w.Error == nil || w.Error.Code != -31102 {
		t.Fatalf("a call whose run closed after the request: HTTP %d %s", status, raw)
	}
	if n, ran := len(r.pipe.admitted()), r.victim.count("read_file"); n != 1 || ran != 1 {
		t.Errorf("%d admissions and %d upstream runs, want still 1 and 1", n, ran)
	}
	assertCounted(t, "request", r.adapter.Stats().RunsRefusedAtRequest, map[gateway.RunCause]uint64{"missing": 2})
	assertCounted(t, "message", r.adapter.Stats().RunsRefusedAtMessage, map[gateway.RunCause]uint64{"closed": 1})
}

// closeAfterTheRequest makes the fake resolve the next token once, for the
// request, and refuse it as closed from then on.
func closeAfterTheRequest(f *fakeRuns) {
	next := len(f.asked()) + 1
	f.set(func(n int) (gateway.OpenedRun, error) {
		if n == next {
			return servedRun(), nil
		}
		return gateway.OpenedRun{}, &gateway.RunRefusal{Cause: gateway.RunClosed}
	})
}

// initialize opens a session by hand under the test's token and returns
// its id; no stream is opened beside it.
func initialize(t *testing.T, url, version string) string {
	t.Helper()
	body := rawRequest(t, 1, "initialize", map[string]any{
		"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "raw-agent", "version": "0"},
	})
	status, h, raw := send(t, http.MethodPost, url, version, withToken(nil), body)
	session := h.Get("Mcp-Session-Id")
	if status != http.StatusOK || session == "" || decodeWire(t, raw).Error != nil {
		t.Fatalf("initialize: HTTP %d session %q %s", status, session, raw)
	}
	notify := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	if status, _, raw := send(t, http.MethodPost, url, version, withToken(http.Header{"Mcp-Session-Id": {session}}), notify); status != http.StatusAccepted {
		t.Fatalf("notifications/initialized: HTTP %d %s", status, raw)
	}
	return session
}

// TestRunIsSelectedByTheTokenAlone: _meta naming a run and headers that
// look like a token change neither what is resolved, nor for whom, nor the
// run the call is admitted under; without the one header they select
// nothing.
func TestRunIsSelectedByTheTokenAlone(t *testing.T) {
	runs := &fakeRuns{}
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{runs: runs, clock: runClock})
	m := meta()
	for k, v := range map[string]any{"run": testRunRoot, "run_id": "run-other", "runToken": "other-token", "Run-Token": "other-token", "principal": "root"} {
		m[k] = v
	}
	m[sdk.MetaKeyClientInfo] = map[string]any{"name": "svc-other", "version": "0"}
	body := rawRequest(t, 1, "tools/call", map[string]any{"name": "read_file", "arguments": map[string]any{"path": "/x"}, "_meta": m})
	decoys := http.Header{
		"X-Run-Token": {"other-token"}, "Run-Id": {"run-other"}, "Mcp-Run-Token": {"other-token"},
		"Run-Tokens": {"other-token"}, "Authorization": {"Bearer other-token"},
	}
	status, _, raw := send(t, http.MethodPost, r.url, r.version, withToken(decoys), body)
	if w := decodeWire(t, raw); status != http.StatusOK || w.Error != nil {
		t.Fatalf("HTTP %d %s", status, raw)
	}
	adm := r.pipe.admitted()
	if len(adm) != 1 || adm[0].a.Run == nil || adm[0].a.Run.ID != testRunID {
		t.Fatalf("admissions %+v", adm)
	}
	if n := len(runs.asked()); n != 2 {
		t.Errorf("the resolver was asked %d times, want 2", n)
	}
	assertAskedAs(t, runs, serviceWho)
	if status, _, _ := send(t, http.MethodPost, r.url, r.version, decoys, body); status != http.StatusUnauthorized {
		t.Errorf("the decoys without the header: HTTP %d, want 401", status)
	}
	if n := len(runs.asked()); n != 2 {
		t.Errorf("the decoys alone reached the resolver: asked %d times", n)
	}
	assertCounted(t, "request", r.adapter.Stats().RunsRefusedAtRequest, map[gateway.RunCause]uint64{"missing": 1})
}

// TestRunIsForTheAuthenticatedUser: the run check sits inside the
// authenticator, so a run is resolved for the user the bearer token names.
func TestRunIsForTheAuthenticatedUser(t *testing.T) {
	runs := &fakeRuns{}
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{auth: bearer(), runs: runs, clock: runClock, headers: withToken(nil)})
	if _, err := callTool(t, r.connect(t, "agent-a"), "read_file", map[string]any{"path": "/x"}); err != nil {
		t.Fatal(err)
	}
	alice := len(runs.asked())
	bob := connectHTTP(t, r.url, "agent-b", r.version, withToken(http.Header{"Authorization": {"Bearer bob-token"}}))
	if _, err := callTool(t, bob, "read_file", map[string]any{"path": "/x"}); err != nil {
		t.Fatal(err)
	}
	got := runs.asked()
	if alice == 0 || len(got) == alice {
		t.Fatalf("the resolver was asked %d times for alice and %d in all", alice, len(got))
	}
	for i, a := range got {
		user := "alice"
		if i >= alice {
			user = "bob"
		}
		want := gateway.RunIdentity{TenantID: "t1", PrincipalType: "user", PrincipalID: user, AgentID: "agent-1"}
		if a.who != want || a.token != testRunToken {
			t.Errorf("Resolve %d got %+v, want %+v", i, a, want)
		}
	}
}

// TestRunNeedsABoundUser: an authenticator that binds nobody leaves nobody a
// run could be for; the request is refused before the resolver is asked.
func TestRunNeedsABoundUser(t *testing.T) {
	runs := &fakeRuns{}
	passThrough := func(next http.Handler) http.Handler { return next }
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{auth: passThrough, runs: runs, clock: runClock})
	status, h, body := send(t, http.MethodPost, r.url, r.version, withToken(nil), callBody(1, "read_file", map[string]any{"path": "/x"}))
	if status != http.StatusUnauthorized || h.Get("WWW-Authenticate") != "Run-Token" || string(body) != wantRefusedBody {
		t.Fatalf("no user bound: HTTP %d %q", status, body)
	}
	if n := len(runs.asked()); n != 0 {
		t.Errorf("the resolver was asked %d times for nobody", n)
	}
	assertCounted(t, "request", r.adapter.Stats().RunsRefusedAtRequest, map[gateway.RunCause]uint64{"identity": 1})
}
