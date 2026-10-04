package otel

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
)

// TestScrubNamesACauseByItsTypeAlone: a transport's failure is logged by the
// operation, the endpoint as logs name it and a cause its error's type gives;
// the exporter's own error keeps its words, and no other text is printed.
func TestScrubNamesACauseByItsTypeAlone(t *testing.T) {
	const marker = "collector-echo-marker"
	e := &Exporter{where: "http://collector:4318/v1/logs"}
	at := func(cause error) error {
		return &url.Error{Op: "Post", URL: "http://u:p@collector:4318/v1/logs?" + marker, Err: cause}
	}
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4318}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"a refused dial":     {at(&net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), "dial tcp 127.0.0.1:4318: connect: connection refused"},
		"a bare errno":       {at(&net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: syscall.ECONNRESET}), "read tcp 127.0.0.1:4318: connection reset by peer"},
		"an unknown host":    {at(&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: marker, Err: marker, IsNotFound: true}}), "dial tcp: no such host"},
		"a resolver failure": {at(&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: marker, Err: marker}}), "dial tcp: " + causeSocket},
		"a remote alert":     {at(&net.OpError{Op: "remote error", Err: tls.AlertError(42)}), "remote error: TLS alert 42"},
		"an untyped answer":  {at(fmt.Errorf("transport connection broken: %w", fmt.Errorf("malformed HTTP response %q", marker))), causeWithheld},
		"a deadline":         {at(context.DeadlineExceeded), causeDeadline},
		"a timeout":          {at(os.ErrDeadlineExceeded), causeDeadline},
		"a cancel":           {at(context.Canceled), causeCanceled},
		"an early close":     {at(io.EOF), causeClosed},
		"an unknown CA":      {at(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), "the certificate is signed by an unknown authority"},
		"a wrong host":       {at(x509.HostnameError{Host: marker, Certificate: &x509.Certificate{}}), "the certificate is not valid for the host"},
		"an invalid one":     {at(x509.CertificateInvalidError{Reason: x509.Expired, Detail: marker}), "the certificate is not valid"},
		"a failed verify":    {at(&tls.CertificateVerificationError{Err: errors.New(marker)}), "the certificate did not verify"},
		"no TLS answer":      {at(tls.RecordHeaderError{Msg: marker}), "the peer did not answer in TLS"},
	} {
		want := "Post http://collector:4318/v1/logs: " + c.want
		if got := e.scrub(c.err); got != want || strings.Contains(got, marker) {
			t.Errorf("%s: logged %q, want %q", name, got, want)
		}
	}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"its own error":    {fmt.Errorf("reading: %w", errAnswerTooLong), errAnswerTooLong.Error()},
		"a body cut short": {io.ErrUnexpectedEOF, causeClosed},
		"a body's text":    {errors.New(marker), causeWithheld},
		"nothing":          {nil, ""},
	} {
		if got := e.scrub(c.err); got != c.want {
			t.Errorf("%s: logged %q, want %q", name, got, c.want)
		}
	}
}
