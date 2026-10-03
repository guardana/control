package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler returns the agent-facing HTTP handler of a stateless or stateful
// listener: a bound on reading the request body, then the origin check, then
// the authenticator if any, then the run token check if the listener resolves
// runs, then on a stateful listener the cap on live sessions, then the
// library's Streamable HTTP handler over the adapter's server.
// The library refuses a request whose Mcp-Method or Mcp-Name disagrees with
// the body (-32020) and, on a stateful listener, a 2026-07-28 request
// (-32022) before anything of the adapter's runs.
func (a *Adapter) Handler() (http.Handler, error) {
	var stateless bool
	switch a.cfg.Listener.Kind {
	case KindStatelessHTTP:
		stateless = true
	case KindStatefulHTTP:
	default:
		return nil, fmt.Errorf("%w: kind %d serves no HTTP", ErrListener, a.cfg.Listener.Kind)
	}
	idle := a.cfg.Listener.SessionIdle
	if idle <= 0 {
		idle = DefaultSessionIdle
	}
	var h http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.server }, &mcp.StreamableHTTPOptions{
		Stateless:      stateless,
		SessionTimeout: idle,
	})
	if !stateless {
		h = a.capSessions(h)
	}
	if a.cfg.Listener.Runs != nil {
		h = a.runsCheck(h)
	}
	if auth := a.cfg.Listener.Authenticator; auth != nil {
		h = auth(h)
	}
	bound := a.cfg.Listener.BodyTimeout
	if bound <= 0 {
		bound = DefaultBodyTimeout
	}
	return bodyDeadline(bound, originCheck(a.cfg.Listener.Origins, h)), nil
}

// bodyDeadline bounds how long a request may take over sending its body, so a
// client that trickles one cannot hold a connection and a handler, and how
// long its answer may wait on a client that stops reading. The server clears
// the read deadline itself once the body is read, when it starts watching the
// connection for the client leaving, and the write deadline once the request
// is answered; each slice of a write sets its own, so an answer or a
// session's stream runs as long as the client keeps up. A request without a
// body gets no deadline: that watch has already begun, and a deadline would
// end it and the request with it.
func bodyDeadline(bound time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		rc := http.NewResponseController(w)
		if err := rc.SetReadDeadline(time.Now().Add(bound)); err != nil {
			http.Error(w, "the request body cannot be bounded", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(&boundedWriter{ResponseWriter: w, rc: rc, bound: bound}, r)
	})
}

// writeSlice is how much of an answer one deadline covers. The library writes
// a whole answer in one call, so a deadline per call would cut a client that
// reads steadily but more slowly than the answer's size over the bound.
const writeSlice = 64 << 10

// boundedWriter gives each slice of a write, and each flush, the bound to
// reach the client, so the bound measures the client's progress. A deadline
// it cannot set fails the write rather than leaving it unbounded.
type boundedWriter struct {
	http.ResponseWriter
	rc    *http.ResponseController
	bound time.Duration
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if err := b.rc.SetWriteDeadline(time.Now().Add(b.bound)); err != nil {
			return written, err
		}
		chunk := p[:min(len(p), writeSlice)]
		n, err := b.ResponseWriter.Write(chunk)
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
		p = p[n:]
	}
	return written, nil
}

// FlushError is what http.ResponseController.Flush calls.
func (b *boundedWriter) FlushError() error {
	if err := b.rc.SetWriteDeadline(time.Now().Add(b.bound)); err != nil {
		return err
	}
	return b.rc.Flush()
}

// Unwrap lets http.ResponseController reach the server's writer.
func (b *boundedWriter) Unwrap() http.ResponseWriter { return b.ResponseWriter }

// ServeStdio serves one agent over r and w until ctx ends or the agent
// closes, framing as the protocol's stdio transport does.
func (a *Adapter) ServeStdio(ctx context.Context, r io.ReadCloser, w io.WriteCloser) error {
	if a.cfg.Listener.Kind != KindStdio {
		return fmt.Errorf("%w: kind %d is not stdio", ErrListener, a.cfg.Listener.Kind)
	}
	return a.server.Run(ctx, &mcp.IOTransport{Reader: r, Writer: w})
}

// originCheck refuses a request whose Origin header names a site the
// operator did not list, loopback included: a page on the developer's own
// machine is a browser the operator has to name. A request with no Origin is
// not a browser's and passes to the next check.
func originCheck(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(allowed, origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
