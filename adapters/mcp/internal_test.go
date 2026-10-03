package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gateway"
)

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
	if _, ok := c.get("alice", "b1", now); !ok {
		t.Fatal("a list shaped from the current manifest was not kept")
	}
	c.reset(2)
	if _, ok := c.get("alice", "b1", now); ok {
		t.Error("a refresh left a list cached")
	}
	c.put("alice", tools, "b1", 1, now)
	if _, ok := c.get("alice", "b1", now); ok {
		t.Error("a list shaped from an older manifest was kept")
	}
	c.put("alice", tools, "b1", 2, now)
	if _, ok := c.get("alice", "b1", now); !ok {
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
	if _, ok := c.get("alice", "b2", now); ok {
		t.Error("a list shaped under b1 answered under b2")
	}
	c.put("alice", tools, "b2", 1, now)
	if _, ok := c.get("alice", "b2", now); !ok {
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
	if _, ok := c.get("one-too-many", "b1", now); ok || c.len() != maxCachedLists {
		t.Errorf("the cache grew past its bound: %d lists", c.len())
	}
	later := now.Add(2 * time.Minute)
	c.put("after-they-expire", tools, "b1", 1, later)
	if _, ok := c.get("after-they-expire", "b1", later); !ok {
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
		got := resultOf(gateway.Disposition{}, now, now, nil, c.err)
		if got.GetStatus() != c.status || got.GetToolProtocolStatus() != c.proto {
			t.Errorf("%s: %v %q, want %v %q", c.name, got.GetStatus(), got.GetToolProtocolStatus(), c.status, c.proto)
		}
	}
}

// TestAResultThatCannotBeEncodedIsUnknown: an answer with no encoding has no
// hash, so nothing can say what it held; it is recorded UNKNOWN, not SUCCESS
// or FAILURE, while an encodable answer keeps its status and hash.
func TestAResultThatCannotBeEncodedIsUnknown(t *testing.T) {
	now := time.Now()
	for name, res := range map[string]any{
		"a channel":               make(chan int),
		"an infinite number":      &mcp.CallToolResult{StructuredContent: math.Inf(1)},
		"an error with no number": &mcp.CallToolResult{IsError: true, StructuredContent: math.NaN()},
	} {
		got := resultOf(gateway.Disposition{}, now, now, res, nil)
		if got.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN || got.GetToolProtocolStatus() != "unhashable" || got.GetResultHash() != "" {
			t.Errorf("%s: %v %q %q, want UNKNOWN \"unhashable\" and no hash", name, got.GetStatus(), got.GetToolProtocolStatus(), got.GetResultHash())
		}
	}
	got := resultOf(gateway.Disposition{}, now, now, &mcp.CallToolResult{StructuredContent: 1.5}, nil)
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
			got := resultOf(gateway.Disposition{}, time.Now(), time.Now(), nil, err)
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
