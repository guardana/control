//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

// TestCallScriptJudgesTheHTTPStatus: call.sh prints the JSON-RPC answer of a
// 2xx reply, plain or as one server-sent event, and exits 1 on any other
// status, saying the status and the reply's body on standard error only.
func TestCallScriptJudgesTheHTTPStatus(t *testing.T) {
	t.Parallel()
	const rpc = `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        string
		code        int
		stdout      string
		stderr      string
	}{
		{name: "200 as JSON", status: 200, contentType: "application/json", body: rpc + "\n", stdout: rpc + "\n"},
		{name: "200 as an event", status: 200, contentType: "text/event-stream", body: "event: message\ndata: " + rpc + "\n\n", stdout: rpc + "\n"},
		{name: "299", status: 299, contentType: "application/json", body: rpc, stdout: rpc + "\n"},
		{name: "300", status: 300, contentType: "application/json", body: rpc, code: 1, stderr: "./call.sh: HTTP status 300\n" + rpc + "\n"},
		{name: "403 with an answer", status: 403, contentType: "application/json", body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"no"}}`,
			code: 1, stderr: "./call.sh: HTTP status 403\n" + `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"no"}}` + "\n"},
		{name: "500 empty", status: 500, code: 1, stderr: "./call.sh: HTTP status 500\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "./call.sh", srv.URL, "read_order", `{"id":"ord-1"}`) //nolint:gosec // G204: this package's own script
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			code := 0
			var exit *exec.ExitError
			switch {
			case errors.As(err, &exit):
				code = exit.ExitCode()
			case err != nil:
				t.Fatal(err)
			}
			if code != tc.code || stdout.String() != tc.stdout || stderr.String() != tc.stderr {
				t.Fatalf("exit %d, stdout %q, stderr %q; want %d, %q, %q", code, stdout.String(), stderr.String(), tc.code, tc.stdout, tc.stderr)
			}
		})
	}
}
