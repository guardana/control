package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
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

// bigAnswer is longer than the socket buffers of both ends together, so a
// client that does not read leaves the plane's write waiting.
const bigAnswer = 12 << 20

// bigAnswerRig serves a tool whose answer is bigAnswer bytes and a marker,
// and reports on cut each write of an answer that failed, as the library
// sees it.
func bigAnswerRig(t *testing.T) (r *rig, cut <-chan error) {
	t.Helper()
	failed := make(chan error, 16)
	o := rigOptions{bodyTimeout: bodyBound, auth: func(next http.Handler) http.Handler {
		inner := bearer()(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner.ServeHTTP(&writeWatch{ResponseWriter: w, failed: failed}, r)
		})
	}}
	r = newRig(t, mcp.KindStatelessHTTP, o)
	replaceTool(r, "slow", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("a", bigAnswer) + "END-OF-ANSWER"}}}, nil
	})
	return r, failed
}

// writeWatch reports every write that fails.
type writeWatch struct {
	http.ResponseWriter
	failed chan<- error
}

func (w *writeWatch) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		select {
		case w.failed <- err:
		default:
		}
	}
	return n, err
}

func (w *writeWatch) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// TestAnAnswerTheClientDoesNotReadIsCut: a client that sends its call and
// then reads nothing has its answer's write fail once it has waited past the
// bound, and what it reads afterwards is cut, while an agent that reads gets
// the whole answer.
func TestAnAnswerTheClientDoesNotReadIsCut(t *testing.T) {
	r, cut := bigAnswerRig(t)
	res, err := callTool(t, r.connect(t, "agent-a"), "slow", map[string]any{"path": "/x"})
	if err != nil || len(res.Content) != 1 || !strings.HasSuffix(res.Content[0].(*sdk.TextContent).Text, "END-OF-ANSWER") {
		t.Fatalf("an agent that reads did not get the whole answer: %v", err)
	}
	select {
	case err := <-cut:
		t.Fatalf("a write to an agent that reads failed: %v", err)
	default:
	}

	conn := sendCall(t, r.url, "slow", 4096)
	select {
	case err := <-cut:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("the write to a client that reads nothing failed with %v, want its deadline", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the write to a client that reads nothing never failed")
	}
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err == nil && strings.Contains(string(data), "END-OF-ANSWER") {
		t.Error("a client whose answer's write failed still got the whole answer")
	}
}

// TestASlowSteadyReaderGetsTheWholeAnswer: a client that reads the answer
// steadily, at a pace that takes far longer than the bound in all, gets all
// of it: the bound measures progress, not the whole answer.
func TestASlowSteadyReaderGetsTheWholeAnswer(t *testing.T) {
	r, cut := bigAnswerRig(t)
	conn := sendCall(t, r.url, "slow", 0)
	if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&paced{r: conn}), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "END-OF-ANSWER") {
		t.Errorf("a steady reader got %d bytes and %v, want the whole answer", len(data), err)
	}
	select {
	case err := <-cut:
		t.Errorf("a write to a steady reader failed: %v", err)
	default:
	}
}

// pacedRate is how fast paced reads: the whole answer takes some three
// seconds, ten times the bound, and one 64 KiB slice some 16ms.
const pacedRate = 4 << 20

// paced reads no faster than pacedRate bytes a second, however the reads
// above it are sized.
type paced struct {
	r     io.Reader
	start time.Time
	read  int
}

func (p *paced) Read(b []byte) (int, error) {
	if p.start.IsZero() {
		p.start = time.Now()
	}
	time.Sleep(time.Until(p.start.Add(time.Duration(p.read) * time.Second / pacedRate)))
	n, err := p.r.Read(b[:min(len(b), 32<<10)])
	p.read += n
	return n, err
}

// sendCall sends one whole 2026-07-28 tools/call, with the token the bearer
// authenticator takes as alice, and reads nothing back; a readBuffer above
// zero sets the connection's receive buffer.
func sendCall(t *testing.T, raw, tool string, readBuffer int) net.Conn {
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
	if readBuffer > 0 {
		if err := conn.(*net.TCPConn).SetReadBuffer(readBuffer); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": map[string]any{"path": "/x"}, "_meta": meta()}})
	if err != nil {
		t.Fatal(err)
	}
	head := "POST / HTTP/1.1\r\nHost: " + u.Host + "\r\nContent-Type: application/json\r\n" +
		"Accept: application/json, text/event-stream\r\nMcp-Protocol-Version: " + v20260728 + "\r\n" +
		"Authorization: Bearer alice-token\r\nMcp-Method: tools/call\r\nMcp-Name: " + tool + "\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	if _, err := io.WriteString(conn, head+string(body)); err != nil {
		t.Fatal(err)
	}
	return conn
}

// TestAWriteBoundEndsWithItsWrite: on a kept-alive connection, a request
// without a body that follows an answered call well after the bound is still
// answered, so no write deadline outlives the call's own writes.
func TestAWriteBoundEndsWithItsWrite(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{bodyTimeout: bodyBound})
	conn := sendCall(t, r.url, "read_file", 0)
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil || resp.Close || resp.StatusCode != http.StatusOK {
		t.Fatalf("the call was answered %d, read %v, close %v: a kept-alive answer is needed", resp.StatusCode, err, resp.Close)
	}
	time.Sleep(3 * bodyBound)
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: plane\r\nAccept: text/event-stream\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := http.ReadResponse(br, nil); err != nil {
		t.Errorf("a request after the call, on the same connection, got no answer: %v", err)
	}
}
