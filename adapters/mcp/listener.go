package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler returns the agent-facing HTTP handler of a stateless or stateful
// listener: origin check, then the authenticator if any, then the library's
// Streamable HTTP handler over the adapter's server. The library refuses a
// request whose Mcp-Method or Mcp-Name disagrees with the body (-32020) and,
// on a stateful listener, a 2026-07-28 request (-32022) before anything of
// the adapter's runs.
func (a *Adapter) Handler() (http.Handler, error) {
	var stateless bool
	switch a.cfg.Listener.Kind {
	case KindStatelessHTTP:
		stateless = true
	case KindStatefulHTTP:
	default:
		return nil, fmt.Errorf("%w: kind %d serves no HTTP", ErrListener, a.cfg.Listener.Kind)
	}
	var h http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.server }, &mcp.StreamableHTTPOptions{
		Stateless: stateless,
	})
	if auth := a.cfg.Listener.Authenticator; auth != nil {
		h = auth(h)
	}
	return originCheck(a.cfg.Listener.Origins, h), nil
}

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
