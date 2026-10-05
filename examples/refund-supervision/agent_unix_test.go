//go:build unix

package refundsupervision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/brand"
)

const protocolVersion = "2026-07-28"

// span is one tool call as the agent's runtime reports it: the span id the
// call's traceparent carried, or one no call carried for a tool the plane
// never saw, and when it began and ended by the agent's clock.
type span struct {
	id, tool   string
	server     string
	start, end time.Time
	failed     bool
}

// refundAgent calls the plane over HTTP as a runtime does, presenting the
// run's token and one trace, and keeps the span of each call it made.
type refundAgent struct {
	url, token, trace string
	spans             []span
}

func newAgent(t *testing.T, url, token string) *refundAgent {
	t.Helper()
	trace := make([]byte, 16)
	if _, err := rand.Read(trace); err != nil {
		t.Fatal(err)
	}
	return &refundAgent{url: url, token: token, trace: hex.EncodeToString(trace)}
}

// answer is the JSON-RPC result of one tools/call.
type answer struct {
	Result struct {
		Meta    map[string]any `json:"_meta"`
		IsError bool           `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (a answer) meta(name string) string {
	s, _ := a.Result.Meta[brand.OTelNamespace+"/"+name].(string)
	return s
}

func (a answer) codes() string {
	raw, _ := json.Marshal(a.Result.Meta[brand.OTelNamespace+"/reason_codes"])
	return string(raw)
}

func (a answer) text() string {
	if len(a.Result.Content) != 1 {
		return ""
	}
	return a.Result.Content[0].Text
}

// newSpan is the n-th span id of this agent's trace.
func newSpan(n int) string { return fmt.Sprintf("a1b2c3d4%08x", n) }

// call makes one tools/call with the traceparent of span id and returns the
// answer; a transport or protocol error fails the test.
func (ra *refundAgent) call(t *testing.T, id, tool string, args map[string]string) answer {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": tool, "arguments": args, "_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    protocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"traceparent": "00-" + ra.trace + "-" + id + "-01",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ra.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
		"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": tool,
		adaptermcp.RunTokenHeader: ra.token,
	} {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("calling %s: %v", tool, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("calling %s: status %d, %v: %s", tool, resp.StatusCode, err, raw)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			raw = []byte(data)
		}
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil || a.Error != nil {
		t.Fatalf("calling %s: the answer %s is no result: %v", tool, raw, err)
	}
	return a
}

// otlpSpans renders the spans as one line of OTLP/JSON trace data from
// service, each an execute_tool span carrying runOf(span) in attribute.
func otlpSpans(t *testing.T, trace, service, attribute string, spans []span, runOf func(span) string) string {
	t.Helper()
	str := func(k, v string) map[string]any {
		return map[string]any{"key": k, "value": map[string]any{"stringValue": v}}
	}
	var out []map[string]any
	for _, s := range spans {
		attrs := []map[string]any{str("gen_ai.operation.name", "execute_tool"), str("gen_ai.tool.name", s.tool), str(attribute, runOf(s))}
		if s.server != "" {
			attrs = append(attrs, str("server.address", s.server))
		}
		status := 1
		if s.failed {
			status = 2
		}
		out = append(out, map[string]any{
			"traceId": trace, "spanId": s.id, "name": "execute_tool " + s.tool, "kind": 3,
			"startTimeUnixNano": strconv.FormatInt(s.start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(s.end.UnixNano(), 10),
			"attributes":        attrs, "status": map[string]any{"code": status},
		})
	}
	raw, err := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
		"resource":   map[string]any{"attributes": []any{str("service.name", service)}},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": service}, "spans": out}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}
