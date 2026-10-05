package transportcause_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/guardana/control/internal/transportcause"
)

var words = transportcause.Words{Withheld: "the caller's withheld cause", Deadline: "the caller's deadline cause"}

// TestACauseIsWordedByTheErrorsTypeAlone: every failure is worded by the
// types it wraps, and no text an error carries reaches the cause, whether it
// is the endpoint, its userinfo or query, or what the peer sent.
func TestACauseIsWordedByTheErrorsTypeAlone(t *testing.T) {
	const marker = "transport-echo-marker"
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"a refused dial":         {&net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, "dial tcp 127.0.0.1:1: connect: connection refused"},
		"a bare errno":           {&net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: syscall.ECONNRESET}, "read tcp 127.0.0.1:1: connection reset by peer"},
		"an unknown host":        {&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: marker, Err: "no such host " + marker, IsNotFound: true}}, "dial tcp: no such host"},
		"a resolver failure":     {&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: marker, Err: "server misbehaving " + marker}}, "dial tcp: failed"},
		"a socket's text":        {&net.OpError{Op: "read", Net: "tcp", Err: errors.New(marker)}, "read tcp: failed"},
		"a system call's text":   {&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errors.New(marker))}, "dial tcp: failed"},
		"a remote alert":         {&net.OpError{Op: "remote error", Err: tls.AlertError(42)}, "remote error: TLS alert 42"},
		"TLS under a socket":     {&net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: tls.RecordHeaderError{Msg: marker}}, "read tcp 127.0.0.1:1: the peer did not answer in TLS"},
		"a socket's timeout":     {&net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: os.ErrDeadlineExceeded}, "the caller's deadline cause"},
		"a deadline":             {context.DeadlineExceeded, "the caller's deadline cause"},
		"a wrapped deadline":     {fmt.Errorf("calling %q: %w", marker, context.DeadlineExceeded), "the caller's deadline cause"},
		"a timeout":              {os.ErrDeadlineExceeded, "the caller's deadline cause"},
		"a cancel":               {fmt.Errorf("%s: %w", marker, context.Canceled), "the request was canceled"},
		"an end of stream":       {io.EOF, "the connection closed before the answer ended"},
		"an early close":         {fmt.Errorf("reading %s: %w", marker, io.ErrUnexpectedEOF), "the connection closed before the answer ended"},
		"an unknown CA":          {&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "the certificate is signed by an unknown authority"},
		"a wrong host":           {x509.HostnameError{Host: marker, Certificate: &x509.Certificate{}}, "the certificate is not valid for the host"},
		"an invalid one":         {x509.CertificateInvalidError{Reason: x509.Expired, Detail: marker}, "the certificate is not valid"},
		"a failed verify":        {&tls.CertificateVerificationError{Err: errors.New(marker)}, "the certificate did not verify"},
		"no TLS answer":          {tls.RecordHeaderError{Msg: marker}, "the peer did not answer in TLS"},
		"a malformed answer":     {fmt.Errorf("net/http: HTTP/1.x transport connection broken: %w", fmt.Errorf("malformed HTTP response %q", marker)), "the caller's withheld cause"},
		"a URL with credentials": {&url.Error{Op: "Post", URL: "http://user:" + marker + "@host:1/p?token=" + marker, Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(marker)}}, "dial tcp: failed"},
		"a URL and an untyped":   {&url.Error{Op: "Post", URL: "http://host:1/p?token=" + marker, Err: errors.New(marker)}, "the caller's withheld cause"},
		//nolint:errorlint // a library flattens the transport's error with %v, which is the case under test
		"a flattened failure": {fmt.Errorf("stream failed (session ID: %v): %v", marker, &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: syscall.ECONNREFUSED}), "the caller's withheld cause"},
		"nothing":             {nil, "the caller's withheld cause"},
	} {
		if got := words.Of(c.err); got != c.want || strings.Contains(got, marker) {
			t.Errorf("%s: worded %q, want %q", name, got, c.want)
		}
	}
}

// TestThePeerNamingCausesAreTheCallers: the two causes that name a peer come
// from the caller's Words, and the rest are the same for every caller.
func TestThePeerNamingCausesAreTheCallers(t *testing.T) {
	other := transportcause.Words{Withheld: "another withheld cause", Deadline: "another deadline cause"}
	for _, c := range []struct {
		err  error
		want string
	}{
		{errors.New("untyped"), "another withheld cause"},
		{context.DeadlineExceeded, "another deadline cause"},
		{context.Canceled, "the request was canceled"},
		{io.EOF, "the connection closed before the answer ended"},
	} {
		if got := other.Of(c.err); got != c.want {
			t.Errorf("%v: worded %q, want %q", c.err, got, c.want)
		}
	}
	for got, want := range map[string]string{
		transportcause.Canceled: "the request was canceled",
		transportcause.Closed:   "the connection closed before the answer ended",
		transportcause.Socket:   "failed",
	} {
		if got != want {
			t.Errorf("constant %q, want %q", got, want)
		}
	}
}
