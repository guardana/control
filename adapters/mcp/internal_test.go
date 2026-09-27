package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
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
	c.put("alice", tools, 1, now)
	if _, ok := c.get("alice", now); !ok {
		t.Fatal("a list shaped from the current manifest was not kept")
	}
	c.reset(2)
	if _, ok := c.get("alice", now); ok {
		t.Error("a refresh left a list cached")
	}
	c.put("alice", tools, 1, now)
	if _, ok := c.get("alice", now); ok {
		t.Error("a list shaped from an older manifest was kept")
	}
	c.put("alice", tools, 2, now)
	if _, ok := c.get("alice", now); !ok {
		t.Error("a list shaped after the refresh was not kept")
	}
}

// TestListCacheIsBounded: the number of principals whose lists are kept is
// bounded, and an expired list makes room for a new one.
func TestListCacheIsBounded(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	tools := []*mcp.Tool{{Name: "t"}}
	for i := range maxCachedLists {
		c.put(strconv.Itoa(i), tools, 1, now)
	}
	if c.len() != maxCachedLists {
		t.Fatalf("the cache holds %d lists, want %d", c.len(), maxCachedLists)
	}
	c.put("one-too-many", tools, 1, now)
	if _, ok := c.get("one-too-many", now); ok || c.len() != maxCachedLists {
		t.Errorf("the cache grew past its bound: %d lists", c.len())
	}
	later := now.Add(2 * time.Minute)
	c.put("after-they-expire", tools, 1, later)
	if _, ok := c.get("after-they-expire", later); !ok {
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
