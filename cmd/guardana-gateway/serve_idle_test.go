package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestAnIdleKeptAliveConnectionIsClosed: once a kept-alive connection has
// been answered and sends nothing more, the server closes it after its idle
// bound instead of waiting for a next request without end.
func TestAnIdleKeptAliveConnectionIsClosed(t *testing.T) {
	const idle = 200 * time.Millisecond
	srv := newServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), idle, 0)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: plane\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil || resp.Close {
		t.Fatalf("the first answer did not keep the connection alive: %v, close %v", err, resp.Close)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = br.ReadByte()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("an idle kept-alive connection: %v, want it closed by the server", err)
	}
	if waited := time.Since(started); waited < idle/2 {
		t.Errorf("the connection was closed after %v, before its idle bound of %v", waited, idle)
	}
}

// TestBoundServersCarryTheIdleBound: every server a plane binds closes a
// connection idle for two minutes.
func TestBoundServersCarryTheIdleBound(t *testing.T) {
	tr := newTree(t)
	p := tr.plane(t)
	bound, err := p.bind(io.Discard, func(string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeListeners(bound) })
	if len(bound) != 2 {
		t.Fatalf("bound %d server(s), want the agents' and the plane's own", len(bound))
	}
	for _, b := range bound {
		if b.server.IdleTimeout != 2*time.Minute {
			t.Errorf("the server on %s closes an idle connection after %v, want 2m0s", b.listener.Addr(), b.server.IdleTimeout)
		}
	}
	// The agents' listener bounds each body in the adapter, and a stream it
	// serves outlives any bound on the whole request; the plane's own
	// answers read a request in thirty seconds or drop it.
	if got := bound[0].server.ReadTimeout; got != 0 {
		t.Errorf("the agents' server bounds a whole request at %v, want no bound of the server's", got)
	}
	if got := bound[1].server.ReadTimeout; got != 30*time.Second {
		t.Errorf("the plane's own server reads a request for %v, want 30s", got)
	}
}

// TestATrickledRequestToTheHealthServerIsDropped: a client that sends its
// headers and then a body a byte at a time, to a handler that reads no body,
// has its connection ended soon after the read bound, not once the whole
// body has arrived.
func TestATrickledRequestToTheHealthServerIsDropped(t *testing.T) {
	const read = 300 * time.Millisecond
	srv := newServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), idleTimeout, read)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, "POST /healthz HTTP/1.1\r\nHost: plane\r\nContent-Length: 4096\r\n\r\n{"); err != nil {
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
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = io.Copy(io.Discard, conn)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the connection was still held after %v, the whole body would take over three minutes", time.Since(started))
	}
	if waited := time.Since(started); waited < read/2 {
		t.Errorf("the connection ended after %v, before its read bound of %v", waited, read)
	}
}
