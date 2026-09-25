package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAnUpstreamRedirectIsNotFollowed: an upstream that lists its tools and
// answers the call with a 307 to a second server fails the call, and the
// second server, which would have run it, sees no request.
func TestAnUpstreamRedirectIsNotFollowed(t *testing.T) {
	var reached atomic.Int32
	elsewhere := upstreamHandler()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		elsewhere.ServeHTTP(w, r)
	}))
	t.Cleanup(target.Close)

	front := upstreamHandler()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var msg struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(raw, &msg) == nil && msg.Method == "tools/call" {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		front.ServeHTTP(w, r)
	}))
	t.Cleanup(redirector.Close)

	tr := newTree(t)
	collector := newAcceptingCollector(t)
	setEnv(t, "upstreams.0.endpoint", redirector.URL)
	setEnv(t, "export.endpoint", collector.url)
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "")
	agent, _ := serveInProcess(t, tr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := agent.CallTool(ctx, &sdk.CallToolParams{Name: "read_order", Arguments: map[string]any{"id": "ord-1"}})
	if err == nil && (!res.IsError || strings.Contains(resultText(res), upstreamAnswer)) {
		t.Errorf("the call through a redirecting upstream came back as %+v, want a failed call", res)
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("the server the upstream redirected to saw %d request(s), want none", n)
	}
}

// resultText joins the text content of a result.
func resultText(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
