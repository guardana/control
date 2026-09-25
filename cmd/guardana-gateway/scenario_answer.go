package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/scenario"
)

// maxOutputBytes bounds the text of a result a later call's arguments may
// take. A longer one is never cut: a step that takes it cannot run.
const maxOutputBytes = 64 << 10

// The keys under the plane's namespace a tools/call answer carries: the
// marker of an answer the plane made itself, and the ids naming its trail.
var (
	keyAnswerMarker = brand.OTelNamespace + "/answer"
	keyRequestID    = brand.OTelNamespace + "/request_id"
	keyDecisionID   = brand.OTelNamespace + "/decision_id"
)

// answered is what one tools/call answer says: its kind, its codes, the trail
// it names, the approval a pending answer waits on, and the text a later
// call may take from a result, or why it has none.
type answered struct {
	kind       scenario.AnswerKind
	codes      []string
	requestID  string
	decisionID string
	approvalID string
	output     string
	noOutput   string
}

// classify reads one tools/call answer. Its kind comes from the plane's
// marker alone, and its ids only from the result's _meta or the error's data,
// which are the two places the plane strips its namespace from what an
// upstream sends. An answer that names no trail, or that is no answer at all,
// cannot be compared and is an error.
func classify(res *sdk.CallToolResult, callErr error) (answered, error) {
	a, fields, body, err := unmarked(res, callErr)
	if err != nil {
		return answered{}, err
	}
	for _, id := range []struct {
		key string
		to  *string
	}{{keyRequestID, &a.requestID}, {keyDecisionID, &a.decisionID}} {
		var ok bool
		if *id.to, ok = textAt(fields, id.key); !ok {
			return answered{}, fmt.Errorf("the answer carries no %s, so it names no trail", id.key)
		}
	}
	marker, present := fields[keyAnswerMarker]
	if !present {
		return a, nil
	}
	return marked(a, marker, body)
}

// unmarked reads an answer as though the plane had not made it: a result
// with its text, or an upstream's wire error. It returns the members the ids
// and the marker are read from, and the body a marked answer's codes are.
func unmarked(res *sdk.CallToolResult, callErr error) (answered, map[string]any, map[string]any, error) {
	a := answered{codes: []string{}}
	if callErr == nil {
		if res == nil {
			return answered{}, nil, nil, errors.New("the call got no answer")
		}
		a.kind = scenario.AnswerResult
		a.output, a.noOutput = outputOf(res)
		body, _ := res.StructuredContent.(map[string]any)
		return a, map[string]any(res.Meta), body, nil
	}
	var werr *jsonrpc.Error
	if !errors.As(callErr, &werr) {
		return answered{}, nil, nil, fmt.Errorf("the call got no answer: %w", callErr)
	}
	var data map[string]any
	if len(werr.Data) > 0 {
		if err := json.Unmarshal(werr.Data, &data); err != nil || data == nil {
			return answered{}, nil, nil, errors.New("the error's data is not an object, so the answer names no trail")
		}
	}
	a.kind = scenario.AnswerError
	return a, data, data, nil
}

// marked reads an answer the plane made itself, by its marker.
func marked(a answered, marker any, body map[string]any) (answered, error) {
	a.output, a.noOutput = "", fmt.Sprintf("the answer is %v", marker)
	switch marker {
	case "blocked":
		codes, ok := stringsAt(body, "reason_codes")
		if !ok {
			return answered{}, errors.New("the blocked answer carries no list of reason codes")
		}
		a.kind, a.codes = scenario.AnswerBlocked, codes
	case "pending":
		code, okCode := textAt(body, "reason_code")
		approval, okApproval := textAt(body, "approval_id")
		if !okCode || !okApproval {
			return answered{}, errors.New("the pending answer carries no reason code or no approval id")
		}
		a.kind, a.codes, a.approvalID = scenario.AnswerPending, []string{code}, approval
	default:
		return answered{}, fmt.Errorf("the answer's marker %s is %v, which this runner does not know", keyAnswerMarker, marker)
	}
	return a, nil
}

// outputOf is the text a later call may take from a result: exactly one text
// item of at most maxOutputBytes, from a result that is not an error.
func outputOf(res *sdk.CallToolResult) (string, string) {
	if res.IsError {
		return "", "the result is an error"
	}
	if len(res.Content) != 1 {
		return "", fmt.Sprintf("the result holds %d content items, not one", len(res.Content))
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	switch {
	case !ok:
		return "", "the result's one item is not text"
	case len(text.Text) > maxOutputBytes:
		return "", fmt.Sprintf("the result's text is %d bytes, over %d", len(text.Text), maxOutputBytes)
	}
	return text.Text, ""
}

// textAt is a non-empty string member.
func textAt(fields map[string]any, key string) (string, bool) {
	v, ok := fields[key].(string)
	return v, ok && v != ""
}

// stringsAt is a list of strings, which may be empty but must be there.
func stringsAt(fields map[string]any, key string) ([]string, bool) {
	items, ok := fields[key].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(items))
	for i, item := range items {
		if out[i], ok = item.(string); !ok {
			return nil, false
		}
	}
	return out, true
}
