package mcp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/guardana/control/internal/secretscan"
)

// TestAWireErrorIsScannedByMessageAndData: an error is withheld when its
// message or its data quotes a secret, or its data cannot be read, the found
// key named before a scan that could not run; a failure that is no wire
// error carries nothing of the upstream's and is never withheld.
func TestAWireErrorIsScannedByMessageAndData(t *testing.T) {
	value := "echoed" + "-query-" + "value"
	set, err := secretscan.New([]secretscan.Secret{{Key: "q", Value: value, Kind: secretscan.MaybeCredential}})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		err      error
		withheld bool
		state    secretscan.State
	}{
		"clean":                   {&jsonrpc.Error{Code: -1, Message: "no", Data: json.RawMessage(`{"a":1}`)}, false, secretscan.Clean},
		"in the message":          {&jsonrpc.Error{Code: -1, Message: "no " + value}, true, secretscan.Found},
		"in the data":             {&jsonrpc.Error{Code: -1, Message: "no", Data: json.RawMessage(`["` + value + `"]`)}, true, secretscan.Found},
		"unreadable data":         {&jsonrpc.Error{Code: -1, Message: "no", Data: json.RawMessage(`{"a":`)}, true, secretscan.Unscanned},
		"found beside unreadable": {&jsonrpc.Error{Code: -1, Message: value, Data: json.RawMessage(`{"a":`)}, true, secretscan.Found},
		"wrapped":                 {errors.Join(errors.New("calling"), &jsonrpc.Error{Code: -1, Message: value}), true, secretscan.Found},
		"no wire error":           {errors.New("connection reset " + value), false, secretscan.Unscanned},
	} {
		got := inspect(set, nil, c.err)
		if got.withheld != c.withheld || got.verdict.State != c.state {
			t.Errorf("%s: withheld %v, state %v; want %v, %v", name, got.withheld, got.verdict.State, c.withheld, c.state)
		}
		if c.state == secretscan.Found && got.verdict.Key != "q" {
			t.Errorf("%s: key %q, want q", name, got.verdict.Key)
		}
	}
}

// TestAResultIsScannedByTheBytesItIsHashedBy: the encoding kept for the
// record's hash is json.Marshal's, escapes and all, and the scan finds the
// secret in it; a result that does not encode is withheld unscanned.
func TestAResultIsScannedByTheBytesItIsHashedBy(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{{Key: "p", Value: "<pw>&", Kind: secretscan.Credential}})
	if err != nil {
		t.Fatal(err)
	}
	got := inspect(set, map[string]any{"t": "x <pw>& y"}, nil)
	if !got.withheld || got.verdict.Key != "p" || !got.encoded || string(got.encoding) != `{"t":"x \u003cpw\u003e\u0026 y"}` {
		t.Errorf("inspect = %+v, %s", got, got.encoding)
	}
	got = inspect(set, make(chan int), nil)
	if !got.withheld || got.verdict.State != secretscan.Unscanned || got.encoded {
		t.Errorf("an unencodable result: %+v", got)
	}
}

// TestAnAnswerThatCouldNotBeScannedSaysSo: the log line of an answer withheld
// because the scan did not run names no secret and says it was not scanned;
// one withheld for a found secret does not.
func TestAnAnswerThatCouldNotBeScannedSaysSo(t *testing.T) {
	var logs strings.Builder
	a := &Adapter{logger: slog.New(slog.NewTextHandler(&logs, nil))}
	a.withheldAnswer("req-1", methodCallTool, "victim", secretscan.Verdict{})
	a.withheldAnswer("req-2", methodCallTool, "victim", secretscan.Verdict{State: secretscan.Found, Key: "k", Spelling: "raw"})
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d log line(s), want 2:\n%s", len(lines), logs.String())
	}
	if !strings.Contains(lines[0], `scan="not scanned"`) {
		t.Errorf("an unscanned answer's line does not say so: %s", lines[0])
	}
	if strings.Contains(lines[1], "scan=") {
		t.Errorf("a found secret's line says it was not scanned: %s", lines[1])
	}
	if got := definitionWithheld(secretscan.Verdict{}); !strings.Contains(got, "could not be scanned") {
		t.Errorf("an unscanned definition's reason is %q", got)
	}
}
