package mcp

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
)

// TestAnUntypedTransportFailureIsLoggedByAFixedCause: the library words some
// failures with %v, so the endpoint it dialled and the session id the
// upstream chose reach the text with no error type to find. Such a text is
// never logged; the adapter's own errors and a deadline keep their words.
func TestAnUntypedTransportFailureIsLoggedByAFixedCause(t *testing.T) {
	const marker = "untyped-credential-marker"
	dialled := &url.Error{Op: "Get", URL: "http://" + marker + ":***@127.0.0.1:1/mcp?token=" + marker, Err: io.EOF}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		//nolint:errorlint // the library flattens the transport's error with %v, which is the case under test
		"a stream that failed": {fmt.Errorf("standalone SSE request failed (session ID: %v): %v", marker, fmt.Errorf("connection failed after 5 attempts: %w", dialled)), causeWithheld},
		"a reflected text":     {fmt.Errorf("broken session: %s", marker), causeWithheld},
		"a deadline":           {fmt.Errorf("calling %q: %w", marker, context.DeadlineExceeded), causeDeadline},
		"its own wrapped":      {fmt.Errorf("%w: %s", ErrListBound, marker), ErrListBound.Error()},
	} {
		got := logText(c.err)
		if got != c.want || strings.Contains(got, marker) {
			t.Errorf("%s: logged %q, want %q", name, got, c.want)
		}
	}
}
