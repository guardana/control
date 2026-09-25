package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/scenario"
)

// named is a result's _meta naming a trail, plus the members given.
func named(extra map[string]any) sdk.Meta {
	m := sdk.Meta{brand.OTelNamespace + "/request_id": "r1", brand.OTelNamespace + "/decision_id": "d1"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func text(s ...string) []sdk.Content {
	out := make([]sdk.Content, len(s))
	for i, t := range s {
		out[i] = &sdk.TextContent{Text: t}
	}
	return out
}

// TestClassifyReadsTheMarkerAndTheTrail holds each kind of answer to what the
// runner reads from it.
func TestClassifyReadsTheMarkerAndTheTrail(t *testing.T) {
	marker := brand.OTelNamespace + "/answer"
	for _, c := range []struct {
		name string
		res  *sdk.CallToolResult
		err  error
		want answered
	}{
		{"a result", &sdk.CallToolResult{Meta: named(nil), Content: text("hello")},
			nil, answered{kind: scenario.AnswerResult, codes: []string{}, requestID: "r1", decisionID: "d1", output: "hello"}},
		{"a result at the output bound", &sdk.CallToolResult{Meta: named(nil), Content: text(strings.Repeat("a", 64<<10))},
			nil, answered{kind: scenario.AnswerResult, codes: []string{}, requestID: "r1", decisionID: "d1", output: strings.Repeat("a", 64<<10)}},
		{"a result past the output bound", &sdk.CallToolResult{Meta: named(nil), Content: text(strings.Repeat("a", 64<<10+1))},
			nil, answered{kind: scenario.AnswerResult, codes: []string{}, requestID: "r1", decisionID: "d1", noOutput: "the result's text is 65537 bytes, over 65536"}},
		{"a result of two items", &sdk.CallToolResult{Meta: named(nil), Content: text("a", "b")},
			nil, answered{kind: scenario.AnswerResult, codes: []string{}, requestID: "r1", decisionID: "d1", noOutput: "the result holds 2 content items, not one"}},
		{"an upstream's error result", &sdk.CallToolResult{Meta: named(nil), Content: text("no"), IsError: true},
			nil, answered{kind: scenario.AnswerResult, codes: []string{}, requestID: "r1", decisionID: "d1", noOutput: "the result is an error"}},
		{"a block", &sdk.CallToolResult{Meta: named(map[string]any{marker: "blocked"}), IsError: true, Content: text("RULE_DENY"),
			StructuredContent: map[string]any{"reason_codes": []any{"RULE_DENY", "NO_MATCHING_RULE"}}},
			nil, answered{kind: scenario.AnswerBlocked, codes: []string{"RULE_DENY", "NO_MATCHING_RULE"}, requestID: "r1", decisionID: "d1", noOutput: "the answer is blocked"}},
		{"a pending answer", &sdk.CallToolResult{Meta: named(map[string]any{marker: "pending"}), IsError: true,
			StructuredContent: map[string]any{"reason_code": "APPROVAL_PENDING", "approval_id": "a1"}},
			nil, answered{kind: scenario.AnswerPending, codes: []string{"APPROVAL_PENDING"}, requestID: "r1", decisionID: "d1", approvalID: "a1", noOutput: "the answer is pending"}},
		{"an upstream's wire error", nil, &jsonrpc.Error{Code: -32000, Message: "down",
			Data: json.RawMessage(`{"` + brand.OTelNamespace + `/request_id":"r1","` + brand.OTelNamespace + `/decision_id":"d1","why":"x"}`)},
			answered{kind: scenario.AnswerError, codes: []string{}, requestID: "r1", decisionID: "d1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := classify(c.res, c.err)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("classify = %+v, %v\nwant %+v", got, err, c.want)
			}
		})
	}
}

// TestClassifyRefusesWhatNamesNoTrail: an answer that names no trail, carries
// a marker the runner cannot read, or is no answer at all cannot be compared.
func TestClassifyRefusesWhatNamesNoTrail(t *testing.T) {
	marker := brand.OTelNamespace + "/answer"
	ids := `"` + brand.OTelNamespace + `/request_id":"r1","` + brand.OTelNamespace + `/decision_id":"d1"`
	for _, c := range []struct {
		name string
		res  *sdk.CallToolResult
		err  error
		says string
	}{
		{"no request id", &sdk.CallToolResult{Meta: sdk.Meta{brand.OTelNamespace + "/decision_id": "d1"}, Content: text("a")}, nil, "/request_id, so it names no trail"},
		{"no decision id", &sdk.CallToolResult{Meta: sdk.Meta{brand.OTelNamespace + "/request_id": "r1"}, Content: text("a")}, nil, "/decision_id, so it names no trail"},
		{"an empty id", &sdk.CallToolResult{Meta: named(map[string]any{brand.OTelNamespace + "/request_id": ""}), Content: text("a")}, nil, "names no trail"},
		{"ids in the structured content only", &sdk.CallToolResult{Content: text("a"),
			StructuredContent: map[string]any{brand.OTelNamespace + "/request_id": "r1", brand.OTelNamespace + "/decision_id": "d1"}}, nil, "names no trail"},
		{"ids in a content item only", &sdk.CallToolResult{Content: text(`{"` + brand.OTelNamespace + `/request_id":"r1"}`)}, nil, "names no trail"},
		{"an unknown marker", &sdk.CallToolResult{Meta: named(map[string]any{marker: "held"})}, nil, "does not know"},
		{"a block without its codes", &sdk.CallToolResult{Meta: named(map[string]any{marker: "blocked"}), StructuredContent: map[string]any{}}, nil, "no list of reason codes"},
		{"a block with a code that is not text", &sdk.CallToolResult{Meta: named(map[string]any{marker: "blocked"}),
			StructuredContent: map[string]any{"reason_codes": []any{1.0}}}, nil, "no list of reason codes"},
		{"a pending answer without its approval", &sdk.CallToolResult{Meta: named(map[string]any{marker: "pending"}),
			StructuredContent: map[string]any{"reason_code": "APPROVAL_PENDING"}}, nil, "no approval id"},
		{"error data that is a string", nil, &jsonrpc.Error{Code: -32000, Message: "down", Data: json.RawMessage(`"str"`)}, "not an object"},
		{"error data that is null", nil, &jsonrpc.Error{Code: -32000, Message: "down", Data: json.RawMessage(`null`)}, "not an object"},
		{"error with no data", nil, &jsonrpc.Error{Code: -32000, Message: "down"}, "names no trail"},
		{"error data without the ids", nil, &jsonrpc.Error{Code: -32000, Message: "down", Data: json.RawMessage(`{"why":"x"}`)}, "names no trail"},
		{"no answer at all", nil, fmt.Errorf("closed: %w", errors.New("EOF")), "got no answer"},
		{"a wire error with ids and a marker it cannot carry", nil, &jsonrpc.Error{Code: -32000, Message: "x",
			Data: json.RawMessage(`{` + ids + `,"` + marker + `":"shown"}`)}, "does not know"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := classify(c.res, c.err)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("classify = %+v, %v; want an error saying %q", got, err, c.says)
			}
		})
	}
}
