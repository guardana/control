package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// echoingUpstream answers every request with a status line that is not one,
// spelled from the request line and every header it was sent, and
// returns its host:port.
func echoingUpstream(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close() //nolint:errcheck // the answer is written; nothing reads the close
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				var echo strings.Builder
				echo.WriteString(req.Method + " " + req.RequestURI + " " + req.Proto + " ")
				_ = req.Header.Write(&echo)
				_, _ = io.WriteString(conn, strings.NewReplacer(" ", "_", "\r\n", "_").Replace(echo.String())+"\r\n\r\n")
			}()
		}
	}()
	return ln.Addr().String()
}

// TestAnAnswerEchoingTheRequestIsNotLogged: an upstream that answers with a
// malformed status line quoting the request line and the Authorization header
// gets net/http to quote them back in its error; the logged text names the
// operation and a fixed cause, and neither the query's credential nor the
// userinfo, raw or as the header encodes it.
func TestAnAnswerEchoingTheRequestIsNotLogged(t *testing.T) {
	const user, password, token = "probe-user", "userinfo-marker", "query-token-marker"
	transport := &mcp.StreamableClientTransport{
		Endpoint: "http://" + user + ":" + password + "@" + echoingUpstream(t) + "/mcp?token=" + token,
		HTTPClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil).Connect(ctx, transport, nil)
	if err == nil {
		t.Fatal("Connect succeeded over an upstream that answers no status line")
	}
	got := logText(err)
	basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	for _, secret := range []string{token, password, basic, "Authorization"} {
		if strings.Contains(got, secret) {
			t.Errorf("logged %q, which holds %q", got, secret)
		}
	}
	if want := "Post: " + causeWithheld; got != want {
		t.Errorf("logged %q, want %q", got, want)
	}
}
