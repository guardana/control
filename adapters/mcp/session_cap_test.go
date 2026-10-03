package mcp_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
)

// TestLiveSessionsAreCapped: with the cap live, a request that would open
// one more session is refused with a status and a body that say nothing of
// the plane, while a live session goes on being served; once one closes, a
// session opens again.
func TestLiveSessionsAreCapped(t *testing.T) {
	r := newRig(t, mcp.KindStatefulHTTP, rigOptions{maxSessions: 2})
	first := r.connect(t, "agent-a")
	second := r.connect(t, "agent-b")
	resp := openSession(t, r.url)
	if resp.status != http.StatusServiceUnavailable || resp.body != "Service Unavailable\n" || resp.session != "" {
		t.Fatalf("a third session with two live: %d %q session %q, want 503 and nothing more", resp.status, resp.body, resp.session)
	}
	if s := r.adapter.Stats(); s.SessionsRefused != 1 || s.SessionsLive != 2 {
		t.Errorf("stats after one refused open: %d refused, %d live; want 1 and 2", s.SessionsRefused, s.SessionsLive)
	}
	if _, err := callTool(t, second, "read_file", map[string]any{"path": "/x"}); err != nil {
		t.Fatalf("a live session at the cap: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a session to open once one closed", func() bool {
		return openSession(t, r.url).status == http.StatusOK
	})
}

// TestABodyStillArrivingTakesNoPlace: requests whose bodies are still
// arriving hold no place under the cap, so they cannot keep an agent that
// sends a whole initialize out.
func TestABodyStillArrivingTakesNoPlace(t *testing.T) {
	r := newRig(t, mcp.KindStatefulHTTP, rigOptions{maxSessions: 2})
	trickle(t, r.url)
	trickle(t, r.url)
	time.Sleep(200 * time.Millisecond)
	if got := openSession(t, r.url); got.status != http.StatusOK {
		t.Errorf("an initialize beside two bodies still arriving, under a cap of 2: %d %q", got.status, got.body)
	}
}

// TestOnlyAnOpenIsCounted: at the cap, a sessionless message the library
// reads as a request other than initialize is not refused for it and leaves
// no session behind, and a batch or a body nobody can read is refused, as an
// open would be.
func TestOnlyAnOpenIsCounted(t *testing.T) {
	r := newRig(t, mcp.KindStatefulHTTP, rigOptions{maxSessions: 1})
	r.connect(t, "agent-a")
	ping := `{"jsonrpc":"2.0","id":7,"method":"ping"}`
	discover := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	for _, c := range []struct {
		name, body string
		headers    http.Header
	}{
		{"a ping", ping, nil},
		{"a ping after a space", " \n" + ping, nil},
		{"a notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil},
		{"an initialize renamed", strings.Replace(initializeBody, `"initialize"`, `"Initialize"`, 1), nil},
		{"a server/discover", discover, http.Header{"Mcp-Protocol-Version": {v20260728}, "Mcp-Method": {"server/discover"}}},
	} {
		if got := postWith(t, r.url, c.body, c.headers); got.status == http.StatusServiceUnavailable {
			t.Errorf("%s at the cap was refused as an open: %q", c.name, got.body)
		}
		// Uncounted is safe only while the library closes a session that never
		// initialized, server/discover initializes none, and no session id
		// exists before initialize; an upgrade that changes one fails here.
		waitFor(t, c.name+" to leave no session behind", func() bool { return r.adapter.Stats().SessionsLive == 1 })
	}
	for name, body := range map[string]string{
		"an initialize":                 initializeBody,
		"a batch holding an initialize": "[" + initializeBody + "]",
		"a body that is not JSON":       `{"method":"initialize"`,
		"a response":                    `{"jsonrpc":"2.0","id":7,"result":{}}`,
	} {
		if got := post(t, r.url, body); got.status != http.StatusServiceUnavailable {
			t.Errorf("%s at the cap: %d %q, want it counted and refused", name, got.status, got.body)
		}
	}
}

// TestAStatelessListenerIsNotCapped: a stateless listener keeps no session,
// so a cap forced onto it changes nothing: while a slow call holds the
// request's own session, another call and an initialize are both answered.
func TestAStatelessListenerIsNotCapped(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{forceSessionCap: 1})
	agent := r.connect(t, "agent-a")
	slow := make(chan error, 1)
	go func() {
		_, err := agent.CallTool(ctxT(t), &sdk.CallToolParams{Name: "slow", Arguments: map[string]any{"path": "/x"}})
		slow <- err
	}()
	waitFor(t, "the slow call to reach the upstream", func() bool { return r.victim.count("slow") == 1 })
	if _, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"}); err != nil {
		t.Errorf("a call beside a slow one: %v", err)
	}
	if got := openSession(t, r.url); got.status == http.StatusServiceUnavailable {
		t.Errorf("an initialize beside a slow call was refused at a cap: %q", got.body)
	}
	if err := <-slow; err != nil {
		t.Errorf("the slow call: %v", err)
	}
}

// TestAnIdledOutSessionFreesItsPlace: a session the listener ends for its
// idleness leaves the cap as one closed by its client does.
func TestAnIdledOutSessionFreesItsPlace(t *testing.T) {
	r := newRig(t, mcp.KindStatefulHTTP, rigOptions{maxSessions: 1, sessionIdle: 300 * time.Millisecond})
	if got := openSession(t, r.url).status; got != http.StatusOK {
		t.Fatalf("the one session: %d", got)
	}
	if got := openSession(t, r.url).status; got != http.StatusServiceUnavailable {
		t.Fatalf("a second session at a cap of one: %d", got)
	}
	waitFor(t, "a session to open once the first idled out", func() bool {
		return openSession(t, r.url).status == http.StatusOK
	})
}

type initialized struct {
	status        int
	body, session string
}

// initializeBody opens a session at the revision a stateful listener serves.
const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + v20251125 +
	`","capabilities":{},"clientInfo":{"name":"raw-agent","version":"0"}}}`

// openSession sends one initialize without a session, as a client opening one
// does.
func openSession(t *testing.T, url string) initialized {
	t.Helper()
	return post(t, url, initializeBody)
}

// post sends body without a session.
func post(t *testing.T, url, body string) initialized {
	t.Helper()
	return postWith(t, url, body, nil)
}

// postWith sends body without a session, with headers beside the ones every
// POST carries.
func postWith(t *testing.T, url, body string, headers http.Header) initialized {
	t.Helper()
	got, err := tryPost(ctxT(t), url, body, headers)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func tryPost(ctx context.Context, url, body string, headers http.Header) (initialized, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return initialized{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, vs := range headers {
		req.Header[k] = vs
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return initialized{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return initialized{}, err
	}
	return initialized{status: resp.StatusCode, body: string(raw), session: resp.Header.Get(sessionHeader)}, nil
}

// sessionHeader is the header the library names a session in.
const sessionHeader = "Mcp-Session-Id"
