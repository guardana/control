package mcp_test

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
)

// bodyBound is the body bound these tests run the listener with: far below
// the slow tool's two seconds, so an answer that outlives it shows the bound
// is off once the body is read.
const bodyBound = 300 * time.Millisecond

// TestATrickledBodyIsRefused: a client that sends its headers and then the
// body a byte at a time is answered with a refusal soon after the bound,
// while another agent's call goes on being served.
func TestATrickledBodyIsRefused(t *testing.T) {
	for _, k := range []mcp.Kind{mcp.KindStatelessHTTP, mcp.KindStatefulHTTP} {
		r := newRig(t, k, rigOptions{bodyTimeout: bodyBound})
		conn := trickle(t, r.url)
		if _, err := callTool(t, r.connect(t, "agent-b"), "read_file", map[string]any{"path": "/x"}); err != nil {
			t.Fatalf("kind %d: another agent's call while a body trickles: %v", k, err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("kind %d: no answer to a body trickled past the bound: %v", k, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("kind %d: a trickled body is answered %d, want a refusal", k, resp.StatusCode)
		}
	}
}

// trickle opens a connection to the listener at raw, sends a POST's headers
// and the first byte of a 4096-byte body, and goes on sending one byte every
// 50ms until the test ends: the whole body would take over three minutes.
func trickle(t *testing.T, raw string) net.Conn {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	head := "POST / HTTP/1.1\r\nHost: " + u.Host + "\r\nContent-Type: application/json\r\n" +
		"Accept: application/json, text/event-stream\r\nContent-Length: 4096\r\n\r\n{"
	if _, err := io.WriteString(conn, head); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if _, err := io.WriteString(conn, " "); err != nil {
					return
				}
			}
		}
	}()
	return conn
}

// TestABodyThatCannotBeBoundedIsRefused: a request with a body, served
// through a writer that cannot set a read deadline, is refused before the
// library reads it, and a request without a body is not.
func TestABodyThatCannotBeBoundedIsRefused(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{bodyTimeout: bodyBound})
	h, err := r.adapter.Handler()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"/x"}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "cannot be bounded") {
		t.Errorf("an unbounded body is answered %d %q", rec.Code, rec.Body.String())
	}
	if n := r.victim.count("read_file"); n != 0 {
		t.Errorf("the upstream ran %d call(s) behind a body nothing bounded", n)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code == http.StatusInternalServerError {
		t.Errorf("a request without a body was refused as unbounded: %q", rec.Body.String())
	}
}

// TestAnAnswerAndAStreamOutliveTheBodyBound: a call whose answer takes longer
// than the body bound is answered with its request still live to every
// handler around the library, and a stateful session's GET stream, which has
// no body, stays open past the bound.
func TestAnAnswerAndAStreamOutliveTheBodyBound(t *testing.T) {
	var cut atomic.Int64
	o := rigOptions{bodyTimeout: bodyBound, auth: func(next http.Handler) http.Handler {
		inner := bearer()(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner.ServeHTTP(w, r)
			if r.Context().Err() != nil {
				cut.Add(1)
			}
		})
	}}
	r := newRig(t, mcp.KindStatelessHTTP, o)
	if _, err := callTool(t, r.connect(t, "alice"), "slow", map[string]any{"path": "/x"}); err != nil {
		t.Errorf("stateless: a call answered after the body bound: %v", err)
	}
	if n := cut.Load(); n != 0 {
		t.Errorf("stateless: %d request(s) ended before their answer was written", n)
	}

	r = newRig(t, mcp.KindStatefulHTTP, o)
	watch := &streamWatch{next: headerTransport{headers: http.Header{"Authorization": {"Bearer alice-token"}}, next: http.DefaultTransport}}
	c := sdk.NewClient(&sdk.Implementation{Name: "agent-a", Version: "0"}, nil)
	cs, err := c.Connect(ctxT(t), &sdk.StreamableClientTransport{Endpoint: r.url, HTTPClient: &http.Client{Transport: watch}},
		&sdk.ClientSessionOptions{ProtocolVersion: r.version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	for deadline := time.Now().Add(2 * time.Second); watch.opened.Load() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the client opened no GET stream, so its survival proves nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := callTool(t, cs, "slow", map[string]any{"path": "/x"}); err != nil {
		t.Errorf("stateful: a call answered after the body bound: %v", err)
	}
	if n := cut.Load(); n != 0 {
		t.Errorf("stateful: %d request(s) ended before their answer was written", n)
	}
	if n := watch.ended.Load(); n != 0 {
		t.Errorf("the session's GET stream ended %d time(s) within the slow call", n)
	}
}

// streamWatch counts the GET streams an agent opens and those that end.
type streamWatch struct {
	next          http.RoundTripper
	opened, ended atomic.Int64
}

func (s *streamWatch) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := s.next.RoundTrip(r)
	if err != nil || r.Method != http.MethodGet || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp, err
	}
	s.opened.Add(1)
	resp.Body = &endWatch{ReadCloser: resp.Body, ended: &s.ended}
	return resp, nil
}

// endWatch counts a stream's end the first time a read fails.
type endWatch struct {
	io.ReadCloser
	ended *atomic.Int64
	once  atomic.Bool
}

func (e *endWatch) Read(p []byte) (int, error) {
	n, err := e.ReadCloser.Read(p)
	if err != nil && e.once.CompareAndSwap(false, true) {
		e.ended.Add(1)
	}
	return n, err
}
