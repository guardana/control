package mcp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/secretscan"
)

// The fixed answers the agent is shown in place of one that quoted a
// credential, pinned here as the agent reads them.
const (
	textRan        = "The call ran, and its answer was withheld because it quoted a credential the gateway holds. Calling again repeats its effect."
	textErrored    = "The tool answered with an error, which was withheld because it quoted a credential the gateway holds."
	messageError   = "the upstream's error was withheld because it quoted a credential the gateway holds"
	messageAnswer  = "the upstream's answer was withheld because it quoted a credential the gateway holds"
	upstreamCode   = -32042
	answerWithheld = "withheld"
)

// leak is one configured secret an upstream echoes. The values are built at
// run time so that no fixture reads as a credential.
type leak struct {
	name, key, value string
	kind             secretscan.Kind
}

func leaks() []leak {
	password := "echoed" + "-password-" + "value"
	return []leak{
		{"password", "upstreams.victim.endpoint.password", password, secretscan.Credential},
		{"basic", "upstreams.victim.endpoint.basic", base64.StdEncoding.EncodeToString([]byte("svc:" + password)), secretscan.Credential},
		{"query", "upstreams.victim.endpoint.query.token", "echoed" + "-query-" + "value", secretscan.MaybeCredential},
		{"env", "upstreams.victim.env.API_KEY", "echoed" + "-env-" + "value", secretscan.MaybeCredential},
	}
}

func leakSet(t *testing.T) *secretscan.Set {
	t.Helper()
	var secrets []secretscan.Secret
	for _, l := range leaks() {
		secrets = append(secrets, secretscan.Secret{Key: l.key, Value: l.value, Kind: l.kind})
	}
	set, err := secretscan.New(secrets)
	if err != nil {
		t.Fatalf("secretscan.New refused the test's secrets: %v", err)
	}
	return set
}

// blobPrefix puts the secret at an offset no multiple of three, so its
// base64 shares no alignment with the blob's.
const blobPrefix = "cfg: "

// shown is what the agent is shown in place of a withheld answer.
type shown int

const (
	ranResult    shown = iota // a tools/call result that says the call ran
	errorResult               // a tools/call isError result
	keptCode                  // a wire error with the upstream's code
	withheldCode              // a wire error with CodeWithheld
)

// place is one spot of one method's answer an upstream can echo a secret
// in, the answer the agent is shown instead, and the record's protocol
// status without the prefix.
type place struct {
	name   string
	method string
	answer func(secret string) (sdk.Result, error)
	shown  shown
	status string
}

// wireError is the upstream's error with message and, unless nil, data. A
// value that does not encode is answered as a failure of no wire error,
// which every assertion on the error refuses.
func wireError(message string, data any) error {
	werr := &jsonrpc.Error{Code: upstreamCode, Message: upstreamMark + " " + message}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		werr.Data = raw
	}
	return werr
}

func toolText(text string) []sdk.Content {
	return []sdk.Content{&sdk.TextContent{Text: text}}
}

func places() []place {
	call, read, prompt := "tools/call", "resources/read", "prompts/get"
	return []place{
		{"tool text", call, func(s string) (sdk.Result, error) {
			return &sdk.CallToolResult{Content: toolText(upstreamMark + " token " + s)}, nil
		}, ranResult, "ok"},
		{"structured content", call, func(s string) (sdk.Result, error) {
			return &sdk.CallToolResult{Content: toolText(upstreamMark), StructuredContent: map[string]any{"token": s}}, nil
		}, ranResult, "ok"},
		{"structured key", call, func(s string) (sdk.Result, error) {
			return &sdk.CallToolResult{Content: toolText(upstreamMark), StructuredContent: map[string]any{s: true}}, nil
		}, ranResult, "ok"},
		{"result meta", call, func(s string) (sdk.Result, error) {
			out := &sdk.CallToolResult{Content: toolText(upstreamMark)}
			out.Meta = sdk.Meta{"debug": s}
			return out, nil
		}, ranResult, "ok"},
		{"embedded blob", call, func(s string) (sdk.Result, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: upstreamMark},
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///cfg", MIMEType: "application/octet-stream", Blob: []byte(blobPrefix + s + "\n")}},
			}}, nil
		}, ranResult, "ok"},
		{"error result", call, func(s string) (sdk.Result, error) {
			return &sdk.CallToolResult{IsError: true, Content: toolText(upstreamMark + " denied " + s)}, nil
		}, errorResult, "isError"},
		{"error message", call, func(s string) (sdk.Result, error) {
			return nil, wireError("401 for /mcp?token="+s, nil)
		}, keptCode, "jsonrpc:-32042"},
		{"error data object", call, func(s string) (sdk.Result, error) {
			return nil, wireError("refused", map[string]any{"url": "/mcp?token=" + s})
		}, keptCode, "jsonrpc:-32042"},
		{"error data string", call, func(s string) (sdk.Result, error) {
			return nil, wireError("refused", "/mcp?token="+s)
		}, keptCode, "jsonrpc:-32042"},
		{"error data array", call, func(s string) (sdk.Result, error) {
			return nil, wireError("refused", []any{"x", s})
		}, keptCode, "jsonrpc:-32042"},
		{"read text", read, func(s string) (sdk.Result, error) {
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: "file:///r", Text: upstreamMark + " " + s}}}, nil
		}, withheldCode, "ok"},
		{"read blob", read, func(s string) (sdk.Result, error) {
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
				{URI: "file:///r", Text: upstreamMark},
				{URI: "file:///b", Blob: []byte(blobPrefix + s)},
			}}, nil
		}, withheldCode, "ok"},
		{"read error", read, func(s string) (sdk.Result, error) {
			return nil, wireError("no resource "+s, nil)
		}, keptCode, "jsonrpc:-32042"},
		{"prompt text", prompt, func(s string) (sdk.Result, error) {
			return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{{Role: "user", Content: &sdk.TextContent{Text: upstreamMark + " " + s}}}}, nil
		}, withheldCode, "ok"},
		{"prompt error", prompt, func(s string) (sdk.Result, error) {
			return nil, wireError("refused", map[string]any{"env": s})
		}, keptCode, "jsonrpc:-32042"},
		{"resource list", "resources/list", func(s string) (sdk.Result, error) {
			return &sdk.ListResourcesResult{Resources: []*sdk.Resource{{URI: "file:///x", Name: upstreamMark, Description: "see " + s}}}, nil
		}, withheldCode, ""},
		{"template list", "resources/templates/list", func(s string) (sdk.Result, error) {
			return &sdk.ListResourceTemplatesResult{ResourceTemplates: []*sdk.ResourceTemplate{{URITemplate: "file:///{p}", Name: upstreamMark, Description: s}}}, nil
		}, withheldCode, ""},
		{"prompt list", "prompts/list", func(s string) (sdk.Result, error) {
			return &sdk.ListPromptsResult{Prompts: []*sdk.Prompt{{Name: upstreamMark, Description: s}}}, nil
		}, withheldCode, ""},
		{"list error", "resources/list", func(s string) (sdk.Result, error) {
			return nil, wireError("refused "+s, nil)
		}, keptCode, ""},
	}
}

// echo answers one method of the victim with what the test sets, counting
// the answers it gave.
type echo struct {
	mu     sync.Mutex
	method string
	answer func() (sdk.Result, error)
	hits   int
}

func (e *echo) set(method string, answer func() (sdk.Result, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.method, e.answer, e.hits = method, answer, 0
}

func (e *echo) served() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hits
}

func (e *echo) install(v *victim) {
	v.server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			e.mu.Lock()
			answer := e.answer
			if m != e.method || answer == nil {
				e.mu.Unlock()
				return next(ctx, m, req)
			}
			e.hits++
			e.mu.Unlock()
			return answer()
		}
	})
}

// recorder keeps every response body the agent's client read.
type recorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.TeeReader(resp.Body, r), resp.Body}
	return resp, nil
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *recorder) take() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := bytes.Clone(r.buf.Bytes())
	r.buf.Reset()
	return out
}

// recordedAgent connects an agent to an HTTP rig whose every response body
// the recorder keeps.
func recordedAgent(t *testing.T, r *rig) (*sdk.ClientSession, *recorder) {
	t.Helper()
	rec := &recorder{}
	c := sdk.NewClient(&sdk.Implementation{Name: "agent-a", Version: "0"}, nil)
	cs, err := c.Connect(ctxT(t), &sdk.StreamableClientTransport{Endpoint: r.url, HTTPClient: &http.Client{Transport: rec}}, &sdk.ClientSessionOptions{ProtocolVersion: r.version})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, rec
}

// ask sends the agent's request for method, a call to read_file for
// tools/call.
func ask(t *testing.T, agent *sdk.ClientSession, method string) (any, error) {
	t.Helper()
	ctx := ctxT(t)
	switch method {
	case "tools/call":
		return agent.CallTool(ctx, &sdk.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "/x"}})
	case "resources/read":
		return agent.ReadResource(ctx, &sdk.ReadResourceParams{URI: "file:///r"})
	case "prompts/get":
		return agent.GetPrompt(ctx, &sdk.GetPromptParams{Name: "p"})
	case "resources/list":
		return agent.ListResources(ctx, nil)
	case "resources/templates/list":
		return agent.ListResourceTemplates(ctx, nil)
	case "prompts/list":
		return agent.ListPrompts(ctx, nil)
	}
	t.Fatalf("no request for %s", method)
	return nil, nil
}

// leakRig is a rig over a victim whose answers the echo sets, scanning for
// the test's secrets.
func leakRig(t *testing.T, o rigOptions) (*rig, *echo) {
	t.Helper()
	v := newVictim()
	e := &echo{}
	e.install(v)
	o.secrets = leakSet(t)
	return newRigOver(t, v, mcp.KindStatelessHTTP, o), e
}

// TestAnAnswerQuotingASecretIsWithheld: every place of every answer an
// upstream can echo each configured secret in reaches the agent as the fixed
// answer with the marker, and nothing of the upstream's answer is in what
// the agent's client read. The closing record keeps the upstream's status
// and hash and says the answer was withheld.
func TestAnAnswerQuotingASecretIsWithheld(t *testing.T) {
	r, e := leakRig(t, rigOptions{})
	agent, rec := recordedAgent(t, r)
	for _, l := range leaks() {
		for _, p := range places() {
			t.Run(l.name+"/"+p.name, func(t *testing.T) {
				var built sdk.Result
				e.set(p.method, func() (sdk.Result, error) {
					res, err := p.answer(l.value)
					built = res
					return res, err
				})
				before := len(r.pipe.closed())
				rec.take()
				res, err := ask(t, agent, p.method)
				raw := rec.take()
				if n := e.served(); n != 1 {
					t.Fatalf("the upstream answered %d times, want once", n)
				}
				assertShown(t, p, res, err)
				decoded, _ := json.Marshal(res)
				blob := base64.StdEncoding.EncodeToString([]byte(blobPrefix + l.value))
				for _, got := range [][]byte{raw, decoded, []byte(errText(err))} {
					for _, leaked := range []string{l.value, upstreamMark, blob[:len(blob)-4]} {
						if bytes.Contains(got, []byte(leaked)) {
							t.Errorf("the agent received %q: %s", leaked, got)
						}
					}
				}
				assertRecord(t, r, p, before, "withheld:", built)
			})
		}
	}
	if got, want := r.adapter.Stats().Withheld, int64(len(leaks())*len(places())); got != want {
		t.Errorf("Stats.Withheld = %d, want %d", got, want)
	}
}

// TestACleanAnswerIsDelivered is the control: the same places holding no
// secret reach the agent as the upstream wrote them, and no record says
// withheld.
func TestACleanAnswerIsDelivered(t *testing.T) {
	r, e := leakRig(t, rigOptions{})
	agent, rec := recordedAgent(t, r)
	for _, p := range places() {
		t.Run(p.name, func(t *testing.T) {
			var built sdk.Result
			e.set(p.method, func() (sdk.Result, error) {
				res, err := p.answer("nothing-configured")
				built = res
				return res, err
			})
			before := len(r.pipe.closed())
			rec.take()
			res, err := ask(t, agent, p.method)
			raw := rec.take()
			if !bytes.Contains(raw, []byte(upstreamMark)) {
				t.Errorf("the upstream's answer did not reach the agent: %v %v %s", res, err, raw)
			}
			if bytes.Contains(raw, []byte(`"`+metaAnswer+`"`)) {
				t.Errorf("a clean answer carries the marker: %s", raw)
			}
			assertRecord(t, r, p, before, "", built)
		})
	}
	if got := r.adapter.Stats().Withheld; got != 0 {
		t.Errorf("Stats.Withheld = %d after clean answers", got)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// assertShown checks the fixed answer the agent was shown for p.
func assertShown(t *testing.T, p place, res any, err error) {
	t.Helper()
	if p.shown == keptCode || p.shown == withheldCode {
		assertWithheldError(t, p, res, err)
		return
	}
	r, ok := res.(*sdk.CallToolResult)
	if err != nil || !ok || r == nil {
		t.Fatalf("want a tool result, got %v, %v", res, err)
	}
	want, isError := textRan, false
	if p.shown == errorResult {
		want, isError = textErrored, true
	}
	assertWithheldResult(t, r, want, isError)
}

// assertWithheldError checks the fixed wire error the agent was shown for p:
// the upstream's code or CodeWithheld, a fixed message, and as data the
// marker and, for a tools/call, the trail's ids.
func assertWithheldError(t *testing.T, p place, res any, err error) {
	t.Helper()
	var werr *jsonrpc.Error
	if !errors.As(err, &werr) {
		t.Fatalf("want a JSON-RPC error, got %v, %v", res, err)
	}
	code, message := int64(upstreamCode), messageError
	if p.shown == withheldCode {
		code, message = mcp.CodeWithheld, messageAnswer
	}
	if werr.Code != code || werr.Message != message {
		t.Errorf("error %d %q, want %d %q", werr.Code, werr.Message, code, message)
	}
	want := map[string]any{metaAnswer: answerWithheld}
	if p.method == "tools/call" {
		want[metaRequestID], want[metaDecisionID] = pipelineRequestID, "dec-1"
	}
	var data map[string]any
	if json.Unmarshal(werr.Data, &data) != nil || !sameStrings(data, want) {
		t.Errorf("error data %s, want %v", werr.Data, want)
	}
}

func assertWithheldResult(t *testing.T, r *sdk.CallToolResult, text string, isError bool) {
	t.Helper()
	if r.IsError != isError {
		t.Errorf("isError %v, want %v", r.IsError, isError)
	}
	if len(r.Content) != 1 {
		t.Fatalf("content %v, want one text", r.Content)
	}
	if tc, ok := r.Content[0].(*sdk.TextContent); !ok || tc.Text != text {
		t.Errorf("content %#v, want the text %q", r.Content[0], text)
	}
	if r.StructuredContent != nil {
		t.Errorf("structured content %v, want none", r.StructuredContent)
	}
	want := map[string]any{metaAnswer: answerWithheld, metaRequestID: pipelineRequestID, metaDecisionID: "dec-1"}
	if !sameStrings(withoutProtocolKeys(r.Meta), want) {
		t.Errorf("_meta %v, want %v", r.Meta, want)
	}
}

// withoutProtocolKeys is m without the keys the protocol reserves, which the
// library writes on every result it serves.
func withoutProtocolKeys(m sdk.Meta) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if !strings.HasPrefix(k, "io.modelcontextprotocol/") {
			out[k] = v
		}
	}
	return out
}

func sameStrings(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// assertRecord checks the closing record p's call appended: none for a
// list; for a call, the status prefixed by prefix and, for a result, the
// hash of the upstream's result as the test built it.
func assertRecord(t *testing.T, r *rig, p place, before int, prefix string, built sdk.Result) {
	t.Helper()
	closes := r.pipe.closed()
	if p.status == "" {
		if len(closes) != before {
			t.Errorf("a list appended %d records", len(closes)-before)
		}
		return
	}
	if len(closes) != before+1 {
		t.Fatalf("%d records appended, want one", len(closes)-before)
	}
	got := closes[len(closes)-1].result
	if got.GetToolProtocolStatus() != prefix+p.status {
		t.Errorf("tool_protocol_status %q, want %q", got.GetToolProtocolStatus(), prefix+p.status)
	}
	status := controlv1.ResultStatus_RESULT_STATUS_SUCCESS
	if p.status != "ok" {
		status = controlv1.ResultStatus_RESULT_STATUS_FAILURE
	}
	if got.GetStatus() != status {
		t.Errorf("status %v, want %v", got.GetStatus(), status)
	}
	if p.method != "tools/call" || built == nil {
		return
	}
	raw, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if want := "sha256:" + hex.EncodeToString(sum[:]); got.GetResultHash() != want {
		t.Errorf("result_hash %s, want the upstream's %s", got.GetResultHash(), want)
	}
}

// TestAWithheldSuccessIsAnErrorOnlyAgainstAnOutputSchema: no fixed answer
// meets a declared output schema, which a client checks a success against,
// so a tool that declares one is shown an isError result with the same text;
// one that declares none, a success.
func TestAWithheldSuccessIsAnErrorOnlyAgainstAnOutputSchema(t *testing.T) {
	v := newVictim()
	tool := *v.tools["transfer"]
	tool.OutputSchema = objectSchema(map[string]any{"receipt": map[string]any{"type": "string"}})
	v.tools["transfer"] = &tool
	v.server.RemoveTools("transfer")
	v.server.AddTool(&tool, v.handler("transfer"))
	e := &echo{}
	e.install(v)
	r := newRigOver(t, v, mcp.KindStatelessHTTP, rigOptions{secrets: leakSet(t)})
	agent := r.connect(t, "agent-a")
	secret := leaks()[2].value
	e.set("tools/call", func() (sdk.Result, error) {
		return &sdk.CallToolResult{Content: toolText(secret), StructuredContent: map[string]any{"receipt": secret}}, nil
	})
	for tool, isError := range map[string]bool{"transfer": true, "read_file": false} {
		args := map[string]any{"path": "/x"}
		if tool == "transfer" {
			args = map[string]any{"amount": 5, "account": "acc-1"}
		}
		res, err := callTool(t, agent, tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		assertWithheldResult(t, res, textRan, isError)
		closes := r.pipe.closed()
		if got := closes[len(closes)-1].result.GetToolProtocolStatus(); got != "withheld:ok" {
			t.Errorf("%s: tool_protocol_status %q, want withheld:ok", tool, got)
		}
	}
}

// TestAnUpstreamCannotMarkItsAnswerWithheld: the marker an upstream puts on
// a clean answer, in a result's _meta or a wire error's data, is stripped
// like every key under the namespace.
func TestAnUpstreamCannotMarkItsAnswerWithheld(t *testing.T) {
	r, e := leakRig(t, rigOptions{})
	agent := r.connect(t, "agent-a")
	e.set("tools/call", func() (sdk.Result, error) {
		out := &sdk.CallToolResult{Content: toolText(upstreamMark)}
		out.Meta = sdk.Meta{metaAnswer: answerWithheld, "upstream-key": "kept"}
		return out, nil
	})
	res, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta[metaAnswer] != nil || res.Meta["upstream-key"] != "kept" {
		t.Errorf("_meta %v, want the upstream's key without the marker", res.Meta)
	}
	for _, method := range []string{"tools/call", "resources/read"} {
		e.set(method, func() (sdk.Result, error) {
			return nil, wireError("refused", map[string]any{metaAnswer: answerWithheld, "upstream-key": "kept"})
		})
		_, err := ask(t, agent, method)
		var werr *jsonrpc.Error
		if !errors.As(err, &werr) || werr.Code != upstreamCode {
			t.Fatalf("%s: %v, want the upstream's error", method, err)
		}
		if bytes.Contains(werr.Data, []byte(metaAnswer)) || !bytes.Contains(werr.Data, []byte(`"upstream-key"`)) {
			t.Errorf("%s: data %s, want the upstream's key without the marker", method, werr.Data)
		}
	}
	if got := r.adapter.Stats().Withheld; got != 0 {
		t.Errorf("Stats.Withheld = %d for clean answers", got)
	}
}

// TestAnAnswerIsWithheldInObserve: the scan changes no decision, so it runs
// in OBSERVE too, and the record the real pipeline appends says so.
func TestAnAnswerIsWithheldInObserve(t *testing.T) {
	r, e := leakRig(t, rigOptions{mode: modeObserve, rules: []string{allowReads}})
	agent := r.connect(t, "agent-a")
	secret := leaks()[0].value
	e.set("tools/call", func() (sdk.Result, error) {
		return &sdk.CallToolResult{Content: toolText(upstreamMark + " " + secret)}, nil
	})
	res, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Meta[metaAnswer] != answerWithheld || strings.Contains(res.Content[0].(*sdk.TextContent).Text, secret) {
		t.Errorf("OBSERVE delivered %+v", res)
	}
	events := r.events()
	closing := events[len(events)-1]
	if closing.GetKind() != controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED || closing.GetResult().GetToolProtocolStatus() != "withheld:ok" {
		t.Errorf("the closing record is %v %q, want a completion marked withheld:ok", closing.GetKind(), closing.GetResult().GetToolProtocolStatus())
	}
}

// TestNewRefusesNoSecretSetAndAnEmptyOneScansNothing: a configuration
// without a set would turn the scan off unseen; an empty set is allowed.
func TestNewRefusesNoSecretSetAndAnEmptyOneScansNothing(t *testing.T) {
	v := newVictim()
	ct, _ := sdk.NewInMemoryTransports()
	cfg := newConfig(t, v, mcp.KindStdio, ct, rigOptions{})
	cfg.Secrets = nil
	if a, err := mcp.New(cfg); a != nil || !errors.Is(err, mcp.ErrNoScanSet) {
		t.Errorf("New without a secret set = %v, %v; want nil, ErrNoScanSet", a, err)
	}
	cfg.Secrets = noSecrets(t)
	if _, err := mcp.New(cfg); err != nil {
		t.Errorf("New with an empty set: %v", err)
	}
}
