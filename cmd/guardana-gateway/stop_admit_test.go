package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/adapters/mcp"
)

// TestACallArrivingDuringTheStopIsNotAdmitted: a request whose body is still
// arriving when the stop begins is served by a listener that is shutting
// down; its call is refused as draining, never admitted and never sent.
func TestACallArrivingDuringTheStopIsNotAdmitted(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	r := runPlane(t, tr, 10*time.Second, time.Minute)
	addr := strings.TrimPrefix(r.url, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // the answer is read before; nothing reads a close error
	revision := r.agent.InitializeResult().ProtocolVersion
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read_order","arguments":{"id":"ord-1"},` +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"` + revision + `","io.modelcontextprotocol/clientCapabilities":{}}}}`
	head := "POST / HTTP/1.1\r\nHost: " + addr + "\r\nContent-Type: application/json\r\nAccept: application/json, text/event-stream\r\n" +
		"Mcp-Protocol-Version: " + revision + "\r\nMcp-Method: tools/call\r\nMcp-Name: read_order\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	if _, err := io.WriteString(conn, head+body[:10]); err != nil {
		t.Fatal(err)
	}
	waitHandled(t, r.p)
	r.stop()
	waitClosed(t, addr)
	if _, err := io.WriteString(conn, body[10:]); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("the request that arrived during the stop was not answered: %v", err)
	}
	answer, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if !strings.Contains(string(answer), string(mcp.ErrDraining)) || strings.Contains(string(answer), upstreamAnswer) {
		t.Errorf("the call that arrived during the stop was answered %s %q, want it refused as draining", resp.Status, answer)
	}
	if n := r.p.adapter.Stats().Admitted; n != 0 {
		t.Errorf("%d call(s) admitted during the stop, want none", n)
	}
	if err := r.stopped(t); err != nil {
		t.Errorf("the stop returned %v", err)
	}
}

// waitHandled waits until a handler of p's listener is reading a request.
func waitHandled(t *testing.T, p *plane) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for p.calls.n.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no handler took the request")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestALostClosingRecordIsReportedOnce: the run that lost a closing record
// names it, and the command that releases the plane after it does not name
// the same record again.
func TestALostClosingRecordIsReportedOnce(t *testing.T) {
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
	ran := r.stopped(t)
	r.released = true
	var stderr syncBuffer
	if status := r.p.finish(ran, &stderr); status != exitFail {
		t.Errorf("the command exited %d, want %d", status, exitFail)
	}
	const lost = "1 call(s) could not append their closing record"
	if n := strings.Count(stderr.String(), lost); n != 1 {
		t.Errorf("the command named the lost closing record %d time(s), want once:\n%s", n, stderr.String())
	}
}
