package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestAnIdleKeptAliveConnectionIsClosed: once a kept-alive connection has
// been answered and sends nothing more, the server closes it after its idle
// bound instead of waiting for a next request without end.
func TestAnIdleKeptAliveConnectionIsClosed(t *testing.T) {
	const idle = 200 * time.Millisecond
	srv := newServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), idle)
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
}
