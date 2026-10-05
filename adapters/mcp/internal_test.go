package mcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/secretscan"
)

// emptySecrets is the set that scans every answer and finds nothing.
func emptySecrets(t *testing.T) *secretscan.Set {
	t.Helper()
	set, err := secretscan.New(nil)
	if err != nil {
		t.Fatalf("secretscan.New(nil): %v", err)
	}
	return set
}

// pages answers with n items per page, for as many pages as it is asked.
func pagesOf(perPage, lastPage int) func(context.Context, string) ([]int, string, error) {
	return func(_ context.Context, cursor string) ([]int, string, error) {
		n, _ := strconv.Atoi(cursor)
		items := make([]int, perPage)
		if lastPage > 0 && n+1 >= lastPage {
			return items, "", nil
		}
		return items, strconv.Itoa(n + 1), nil
	}
}

// TestReadAllBounds: the bounds on pages and on entries, at the input where
// removing each one changes the answer.
func TestReadAllBounds(t *testing.T) {
	ctx := context.Background()
	if got, err := readAll(ctx, pagesOf(1, maxListPages)); err != nil || len(got) != maxListPages {
		t.Errorf("a list of exactly %d pages: %d items, %v", maxListPages, len(got), err)
	}
	if _, err := readAll(ctx, pagesOf(1, maxListPages+1)); !errors.Is(err, ErrListBound) {
		t.Errorf("a list of %d pages = %v, want ErrListBound", maxListPages+1, err)
	}
	if _, err := readAll(ctx, pagesOf(1, 0)); !errors.Is(err, ErrListBound) {
		t.Errorf("an endless list = %v, want ErrListBound", err)
	}
	if got, err := readAll(ctx, pagesOf(maxListEntries, 1)); err != nil || len(got) != maxListEntries {
		t.Errorf("a page of exactly %d entries: %d items, %v", maxListEntries, len(got), err)
	}
	if _, err := readAll(ctx, pagesOf(maxListEntries+1, 1)); !errors.Is(err, ErrListBound) {
		t.Errorf("a page of %d entries = %v, want ErrListBound", maxListEntries+1, err)
	}
	// Two pages that pass the bound together are refused too.
	if _, err := readAll(ctx, pagesOf(maxListEntries/2+1, 2)); !errors.Is(err, ErrListBound) {
		t.Errorf("two pages over the bound = %v, want ErrListBound", err)
	}
}

// TestListCacheDropsAnOlderGeneration: a list shaped from a manifest older
// than the last refresh is never kept, and one shaped after it is.
func TestListCacheDropsAnOlderGeneration(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	tools := []*mcp.Tool{{Name: "t"}}
	c.put("alice", tools, "b1", 1, now)
	if _, ok := c.get("alice", "b1", 1, now); !ok {
		t.Fatal("a list shaped from the current manifest was not kept")
	}
	c.reset(2)
	if _, ok := c.get("alice", "b1", 1, now); ok {
		t.Error("a refresh left a list cached")
	}
	c.put("alice", tools, "b1", 1, now)
	if _, ok := c.get("alice", "b1", 1, now); ok {
		t.Error("a list shaped from an older manifest was kept")
	}
	c.put("alice", tools, "b1", 2, now)
	if _, ok := c.get("alice", "b1", 2, now); !ok {
		t.Error("a list shaped after the refresh was not kept")
	}
}

// TestListCacheAnswersUnderItsBundleOnly: a list kept under one bundle is no
// answer under another, and one kept under the other is.
func TestListCacheAnswersUnderItsBundleOnly(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	tools := []*mcp.Tool{{Name: "t"}}
	c.put("alice", tools, "b1", 1, now)
	if _, ok := c.get("alice", "b2", 1, now); ok {
		t.Error("a list shaped under b1 answered under b2")
	}
	c.put("alice", tools, "b2", 1, now)
	if _, ok := c.get("alice", "b2", 1, now); !ok {
		t.Error("a list shaped under b2 did not answer under b2")
	}
}

// TestListCacheIsBounded: the number of principals whose lists are kept is
// bounded, and an expired list makes room for a new one.
func TestListCacheIsBounded(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	tools := []*mcp.Tool{{Name: "t"}}
	for i := range maxCachedLists {
		c.put(strconv.Itoa(i), tools, "b1", 1, now)
	}
	if c.len() != maxCachedLists {
		t.Fatalf("the cache holds %d lists, want %d", c.len(), maxCachedLists)
	}
	c.put("one-too-many", tools, "b1", 1, now)
	if _, ok := c.get("one-too-many", "b1", 1, now); ok || c.len() != maxCachedLists {
		t.Errorf("the cache grew past its bound: %d lists", c.len())
	}
	later := now.Add(2 * time.Minute)
	c.put("after-they-expire", tools, "b1", 1, later)
	if _, ok := c.get("after-they-expire", "b1", 1, later); !ok {
		t.Errorf("an expired list did not make room: %d lists", c.len())
	}
}

// TestAnErrorTheClientRaisesIsNoAnswer: a JSON-RPC error the upstream sent
// is its failure, but one the client library raises about an answer it could
// not parse or a call it could not complete is no answer, so nobody knows
// whether the call took effect.
func TestAnErrorTheClientRaisesIsNoAnswer(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		err    error
		status controlv1.ResultStatus
		proto  string
	}{
		{"sent by the upstream", &jsonrpc.Error{Code: -32602, Message: "invalid params"}, controlv1.ResultStatus_RESULT_STATUS_FAILURE, "jsonrpc:-32602"},
		{"rejected by the transport", fmt.Errorf("%w: write to closed stream", &jsonrpc.Error{Code: -32005, Message: "rejected by transport"}), controlv1.ResultStatus_RESULT_STATUS_UNKNOWN, "error"},
		{"an id it could not parse", fmt.Errorf("calling: %w", &jsonrpc.Error{Code: -32700, Message: "parse error"}), controlv1.ResultStatus_RESULT_STATUS_UNKNOWN, "error"},
		{"an answer with no id", fmt.Errorf("calling: %w", &jsonrpc.Error{Code: -32600, Message: "invalid request"}), controlv1.ResultStatus_RESULT_STATUS_UNKNOWN, "error"},
		{"a connection closed", errors.New("connection closed"), controlv1.ResultStatus_RESULT_STATUS_UNKNOWN, "error"},
	}
	for _, c := range cases {
		got := resultOf(gateway.Disposition{}, now, now, inspect(emptySecrets(t), nil, c.err))
		if got.GetStatus() != c.status || got.GetToolProtocolStatus() != c.proto {
			t.Errorf("%s: %v %q, want %v %q", c.name, got.GetStatus(), got.GetToolProtocolStatus(), c.status, c.proto)
		}
	}
}

// TestAResultThatCannotBeEncodedIsUnknown: an answer with no encoding has no
// hash, so nothing can say what it held; it is recorded UNKNOWN, not SUCCESS
// or FAILURE, and since it could not be scanned either, withheld. An
// encodable answer keeps its status and hash.
func TestAResultThatCannotBeEncodedIsUnknown(t *testing.T) {
	now := time.Now()
	for name, res := range map[string]any{
		"a channel":               make(chan int),
		"an infinite number":      &mcp.CallToolResult{StructuredContent: math.Inf(1)},
		"an error with no number": &mcp.CallToolResult{IsError: true, StructuredContent: math.NaN()},
	} {
		got := resultOf(gateway.Disposition{}, now, now, inspect(emptySecrets(t), res, nil))
		if got.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN || got.GetToolProtocolStatus() != "withheld:unhashable" || got.GetResultHash() != "" {
			t.Errorf("%s: %v %q %q, want UNKNOWN \"withheld:unhashable\" and no hash", name, got.GetStatus(), got.GetToolProtocolStatus(), got.GetResultHash())
		}
	}
	got := resultOf(gateway.Disposition{}, now, now, inspect(emptySecrets(t), &mcp.CallToolResult{StructuredContent: 1.5}, nil))
	if got.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_SUCCESS || got.GetToolProtocolStatus() != "ok" || !strings.HasPrefix(got.GetResultHash(), "sha256:") {
		t.Errorf("an encodable answer: %v %q %q", got.GetStatus(), got.GetToolProtocolStatus(), got.GetResultHash())
	}
}

// scriptedStdio is an upstream on a pipe that answers initialize properly and
// every tools/call with answer, as written.
func scriptedStdio(t *testing.T, answer string) *mcp.ClientSession {
	t.Helper()
	fromServer, toClient := io.Pipe()
	fromClient, toServer := io.Pipe()
	go func() {
		lines := bufio.NewScanner(fromClient)
		for lines.Scan() {
			var m struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"params"`
			}
			if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil {
				continue
			}
			reply := `{"jsonrpc":"2.0","id":` + string(m.ID) + `,"error":{"code":-32601,"message":"no"}}`
			switch m.Method {
			case "initialize":
				reply = `{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{"protocolVersion":"` + m.Params.ProtocolVersion +
					`","capabilities":{"tools":{}},"serverInfo":{"name":"scripted","version":"1"}}}`
			case "tools/call":
				reply = answer
			}
			if _, err := io.WriteString(toClient, reply+"\n"); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: fromServer, Writer: toServer}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = toClient.Close()
		_ = fromClient.Close()
	})
	return cs
}

// TestAnAnswerTheClientCannotReadIsNoAnswer: the SDK's own errors for an
// answer it cannot read are recorded as no answer, through the SDK itself,
// so a change in the codes it raises fails here.
func TestAnAnswerTheClientCannotReadIsNoAnswer(t *testing.T) {
	for name, answer := range map[string]string{
		"a boolean id": `{"jsonrpc":"2.0","id":true,"result":{"content":[]}}`,
		"no id":        `{"jsonrpc":"2.0","result":{"content":[]}}`,
		"a null id":    `{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"x"}}`,
		"not JSON":     `garbage`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := scriptedStdio(t, answer).CallTool(ctx, &mcp.CallToolParams{Name: "x"})
			if err == nil {
				t.Fatal("the call succeeded")
			}
			got := resultOf(gateway.Disposition{}, time.Now(), time.Now(), inspect(emptySecrets(t), nil, err))
			if got.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN {
				t.Errorf("%v -> %v %q, want UNKNOWN", err, got.GetStatus(), got.GetToolProtocolStatus())
			}
		})
	}
}

// TestUnderBrandFoldsTheNamespaceOnly: a key is under the namespace when its
// first runes fold to the namespace and its slash, whatever the case and
// whatever follows; a key one rune short of it, or with anything before it,
// is not.
func TestUnderBrandFoldsTheNamespaceOnly(t *testing.T) {
	short := strings.TrimSuffix(brandMeta, "/")
	for key, want := range map[string]bool{
		brandMeta:                        true,
		brandMeta + "answer":             true,
		strings.ToUpper(brandMeta) + "x": true,
		strings.ToUpper(brandMeta[:1]) + brandMeta[1:] + "answer": true,
		short:              false,
		short + "x/answer": false,
		"x" + brandMeta:    false,
		"":                 false,
		brandMeta[:2]:      false,
	} {
		if got := underBrand(key); got != want {
			t.Errorf("underBrand(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestUnderBrandFoldsRuneByRune: the namespace's own letters have no fold
// partner outside ASCII, so the fold is pinned under a prefix whose letters
// do. A Kelvin sign folds to k and a long s to s, each taking more bytes than
// the letter it stands for, so the prefix is measured in runes and folded as
// Unicode folds, not lower-cased and not compared byte for byte.
func TestUnderBrandFoldsRuneByRune(t *testing.T) {
	saved := brandMeta
	brandMeta = "kiosk.ns/"
	t.Cleanup(func() { brandMeta = saved })
	for key, want := range map[string]bool{
		"kiosk.ns/x": true,
		"KIOSK.NS/x": true,
		"Kiosk.ns/x": true,
		"Kiosk.ns/":  true,
		"kioſk.ns/x": true,
		"KioſK.ns/":  true,
		"Kiosk.ns":   false,
		"Kiosk.né/x": false,
		"xKiosk.ns/": false,
	} {
		if got := underBrand(key); got != want {
			t.Errorf("underBrand(%q) under %q = %v, want %v", key, brandMeta, got, want)
		}
	}
}

// stuckWriter accepts nothing and reports no error, and takes a deadline.
type stuckWriter struct{ header http.Header }

func (s *stuckWriter) Header() http.Header              { return s.header }
func (s *stuckWriter) Write([]byte) (int, error)        { return 0, nil }
func (s *stuckWriter) WriteHeader(int)                  {}
func (s *stuckWriter) SetWriteDeadline(time.Time) error { return nil }

// TestABoundedWriteThatMovesNothingFails: an inner writer that takes no byte
// and reports no error ends the write with io.ErrShortWrite instead of
// looping on it.
func TestABoundedWriteThatMovesNothingFails(t *testing.T) {
	w := &stuckWriter{header: http.Header{}}
	b := &boundedWriter{ResponseWriter: w, rc: http.NewResponseController(w), bound: time.Second}
	done := make(chan error, 1)
	go func() {
		_, err := b.Write([]byte("answer"))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrShortWrite) {
			t.Errorf("a write that moved nothing returned %v, want io.ErrShortWrite", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a write that moved nothing never returned")
	}
}

// TestASessionCapLeftUnsetIsTheDefault: a listener that names no cap is
// capped at 1024 sessions, and one that names a cap at that cap.
func TestASessionCapLeftUnsetIsTheDefault(t *testing.T) {
	for set, want := range map[int]int{0: 1024, 1: 1, 7: 7} {
		a := &Adapter{cfg: Config{Listener: Listener{MaxSessions: set}}}
		if got := a.sessionLimit(); got != want {
			t.Errorf("MaxSessions %d caps at %d, want %d", set, got, want)
		}
	}
}

// TestTheResultHashIsTheHashOfItsMarshalledBytes: the hash Close records is
// sha256 over what json.Marshal writes for the result, computed here from
// Marshal itself, for results whose encoding escapes, nests, holds raw JSON
// and runs past one buffer.
func TestTheResultHashIsTheHashOfItsMarshalledBytes(t *testing.T) {
	now := time.Now()
	for name, res := range map[string]mcp.Result{
		"escaped characters": &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "<a href=\"x\">&amp;</a>  \x01"}}},
		"nested structure":   &mcp.CallToolResult{StructuredContent: map[string]any{"b": []any{1, "two", map[string]any{"c": nil}}, "a": true}},
		"raw arguments":      &mcp.CallToolResult{StructuredContent: json.RawMessage(`{ "z" : 1, "a" : [ 2 ] }`)},
		"a long text":        &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("long ", 1<<16)}}},
		"a resource":         &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "file:///r", Text: "r"}}},
	} {
		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		want := "sha256:" + hex.EncodeToString(sum[:])
		if got := resultOf(gateway.Disposition{}, now, now, inspect(emptySecrets(t), res, nil)).GetResultHash(); got != want {
			t.Errorf("%s: result hash %s, want %s", name, got, want)
		}
	}
}

// TestTheRecordedHashIsUnchanged pins the recorded hash of four results byte
// for byte: HTML escaping, a float, a blob and a prompt.
func TestTheRecordedHashIsUnchanged(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		res  mcp.Result
		want string
	}{
		{&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "<a href=\"x\">&amp;</a> \xe2\x80\xa8 \x01"}}}, "sha256:7dd82498506581b82f6a0be0467c2b5521687debefd0c706851723e426e4ffae"},
		{&mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"b": []any{1, "two"}, "a": 1.5}}, "sha256:aec0150ff0a763041fee1d91f10194d26887c51924f2c233326de97b7f18a10a"},
		{&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "file:///r", Blob: []byte("blob\x00bytes")}}}, "sha256:48f0c8df637ec28af2ba17f18969659c9392634ef34c5d15cfb911c0ed7dec51"},
		{&mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "p"}}}}, "sha256:53f5a8ab5bf0eab3e45d05b5a88689ebee1906258db03402689526b7154677ba"},
	} {
		if got := resultOf(gateway.Disposition{}, now, now, inspect(emptySecrets(t), c.res, nil)).GetResultHash(); got != c.want {
			t.Errorf("%T: result hash %s, want %s", c.res, got, c.want)
		}
	}
}

// TestALoggedFailureNamesItsCauseAndNotItsURL: a transport's failure is
// logged by the operation and a cause its error's type gives, never by the URL
// it quotes or by its text; a wire error by its code, never by the upstream's
// message.
func TestALoggedFailureNamesItsCauseAndNotItsURL(t *testing.T) {
	const marker = "query-credential-marker"
	at := func(cause error) error {
		return &url.Error{Op: "Post", URL: "http://127.0.0.1:1/mcp?token=" + marker, Err: cause}
	}
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	refused := &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	rejected := &jsonrpc.Error{Code: -32005, Message: "rejected by transport"}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"a refused dial":     {fmt.Errorf("mcp: tools/list from up: %w", fmt.Errorf("sending %q: %w: %w", "tools/list", rejected, at(refused))), "Post: dial tcp 127.0.0.1:1: connect: connection refused"},
		"a bare errno":       {at(&net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: syscall.ECONNRESET}), "Post: read tcp 127.0.0.1:1: connection reset by peer"},
		"an unknown host":    {at(&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: marker, Err: "no such host " + marker, IsNotFound: true}}), "Post: dial tcp: no such host"},
		"a resolver failure": {at(&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: marker, Err: "server misbehaving " + marker}}), "Post: dial tcp: " + causeSocket},
		"a socket's text":    {at(&net.OpError{Op: "read", Net: "tcp", Err: errors.New(marker)}), "Post: read tcp: " + causeSocket},
		"a remote alert":     {at(&net.OpError{Op: "remote error", Err: tls.AlertError(42)}), "Post: remote error: TLS alert 42"},
		"an untyped answer":  {at(fmt.Errorf("net/http: HTTP/1.x transport connection broken: %w", fmt.Errorf("malformed HTTP response %q", marker))), "Post: " + causeWithheld},
		"a deadline":         {at(context.DeadlineExceeded), "Post: " + causeDeadline},
		"a timeout":          {at(os.ErrDeadlineExceeded), "Post: " + causeDeadline},
		"a cancel":           {at(context.Canceled), "Post: " + causeCanceled},
		"an early close":     {at(io.ErrUnexpectedEOF), "Post: " + causeClosed},
		"an unknown CA":      {at(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), "Post: the certificate is signed by an unknown authority"},
		"a wrong host":       {at(x509.HostnameError{Host: marker, Certificate: &x509.Certificate{}}), "Post: the certificate is not valid for the host"},
		"an invalid one":     {at(x509.CertificateInvalidError{Reason: x509.Expired, Detail: marker}), "Post: the certificate is not valid"},
		"a failed verify":    {at(&tls.CertificateVerificationError{Err: errors.New(marker)}), "Post: the certificate did not verify"},
		"no TLS answer":      {at(tls.RecordHeaderError{Msg: marker}), "Post: the peer did not answer in TLS"},
		"a wire error":       {fmt.Errorf("mcp: tools/list from up: %w", &jsonrpc.Error{Code: -32042, Message: "reflected " + marker}), "JSON-RPC error -32042"},
		"its own error":      {ErrListBound, ErrListBound.Error()},
	} {
		if got := logText(c.err); got != c.want || strings.Contains(got, marker) {
			t.Errorf("%s: logged %q, want %q", name, got, c.want)
		}
	}
}
